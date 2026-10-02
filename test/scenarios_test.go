package test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"fraud-service/internal/db"
	"fraud-service/internal/events"
	"fraud-service/internal/rules"
	"fraud-service/internal/scorer"
	"fraud-service/internal/service"
)

func newTestRulesConfig() *rules.Config {
	return &rules.Config{
		Version:       "2026-09-v3",
		FlagThreshold: 0.50,
		Rules: map[string]rules.RuleSetting{
			"velocity":                {Enabled: true, Threshold: 5, Sum1hThresholdMinor: 100000},
			"card_testing":            {Enabled: true, SmallUnderMinor: 200, MinSmall: 3, LargeOverMinor: 5000},
			"amount_outlier":          {Enabled: true, Z: 3.5, MinHistory: 10, MinAmountMinor: 5000},
			"new_country":             {Enabled: true, MinAccountAgeDays: 14},
			"impossible_travel":       {Enabled: true, Window: 2 * time.Hour, MaxSpeedKmh: 850.0},
			"new_merchant_high_value": {Enabled: true, MinMultiplier: 5.0, MinAmountMinor: 5000},
		},
	}
}

// 1. Duplicate Event: Idempotency prevents phantom velocity flags
func TestScenario1_DuplicateEvent(t *testing.T) {
	mockStore := NewMockStore()
	cfg := newTestRulesConfig()
	svc := service.NewService(cfg, mockStore, mockStore, nil, nil)

	accountID := uuid.New()
	eventID := uuid.New()
	txnID := uuid.New()
	now := time.Now().UTC()

	payload := events.TransactionCreated{
		TransactionID: txnID,
		AccountID:     accountID,
		AmountMinor:   2500,
		Currency:      "GBP",
		Merchant:      "Tesco Metro",
		Country:       "GB",
		Channel:       "contactless",
	}

	env, err := events.NewDeterministicEnvelope(eventID, events.EventTypeTransactionCreated, events.CurrentVersion, now, payload)
	require.NoError(t, err)

	// Simulate consumer loop with idempotency check
	processWithIdempotency := func() error {
		if !mockStore.CheckProcessed("fraud", env.EventID) {
			return nil // duplicate skipped
		}
		return svc.Handle(context.Background(), nil, env)
	}

	// First execution succeeds
	err = processWithIdempotency()
	require.NoError(t, err)

	// Publish same event_id 5 times
	for i := 0; i < 5; i++ {
		err = processWithIdempotency()
		require.NoError(t, err)
	}

	// Verify profile baseline and velocity are unchanged
	mockStore.mu.RLock()
	defer mockStore.mu.RUnlock()

	history := mockStore.History[accountID]
	assert.Len(t, history, 1, "exactly 1 record in history; no duplicates")

	profile := mockStore.Profiles[accountID]
	require.NotNil(t, profile)
	assert.Equal(t, int64(1), profile.N, "Welford profile N must be 1, not 6")

	assert.Len(t, mockStore.Flags, 0, "no phantom velocity flag triggered")
}

// 2. Burst: 8 transactions in 40 seconds; velocity fires at 5th; one flag per txn
func TestScenario2_Burst(t *testing.T) {
	mockStore := NewMockStore()
	cfg := newTestRulesConfig()
	svc := service.NewService(cfg, mockStore, mockStore, nil, nil)

	accountID := uuid.New()
	t0 := time.Now().UTC()

	var flagsEmitted int
	for i := 0; i < 8; i++ {
		txnTime := t0.Add(time.Duration(i*5) * time.Second) // 5s apart (40s total)
		txnID := uuid.New()
		payload := events.TransactionCreated{
			TransactionID: txnID,
			AccountID:     accountID,
			AmountMinor:   3000,
			Currency:      "GBP",
			Merchant:      "CoffeeHouse",
			Country:       "GB",
			Channel:       "contactless",
		}
		env, _ := events.NewDeterministicEnvelope(uuid.New(), events.EventTypeTransactionCreated, events.CurrentVersion, txnTime, payload)
		err := svc.Handle(context.Background(), nil, env)
		require.NoError(t, err)

		mockStore.mu.RLock()
		if _, flagged := mockStore.Flags[txnID]; flagged {
			flagsEmitted++
			// Txn 1 to 4 should not be flagged (count1m < 5)
			// Txn 5 should flag (count1m = 4 before recording; 6th txn has count1m=5)
			assert.True(t, i >= 4, "flags must only fire after threshold is reached")
		}
		mockStore.mu.RUnlock()
	}

	assert.True(t, flagsEmitted >= 2, "burst transactions exceeding threshold must be flagged")
}

// 3. Card testing: 4 small charges (< £2) then £90 cashout within 10m
func TestScenario3_CardTesting(t *testing.T) {
	mockStore := NewMockStore()
	cfg := newTestRulesConfig()
	svc := service.NewService(cfg, mockStore, mockStore, nil, nil)

	accountID := uuid.New()
	t0 := time.Now().UTC()

	// 4 small charges under £2 (e.g. £1.20 = 120 minor)
	for i := 0; i < 4; i++ {
		payload := events.TransactionCreated{
			TransactionID: uuid.New(),
			AccountID:     accountID,
			AmountMinor:   120,
			Currency:      "GBP",
			Merchant:      "MicroAuth-Test",
			Country:       "GB",
			Channel:       "online",
		}
		env, _ := events.NewDeterministicEnvelope(uuid.New(), events.EventTypeTransactionCreated, events.CurrentVersion, t0.Add(time.Duration(i*30)*time.Second), payload)
		err := svc.Handle(context.Background(), nil, env)
		require.NoError(t, err)
	}

	// Large transaction £90 (9000 minor)
	cashoutID := uuid.New()
	cashoutPayload := events.TransactionCreated{
		TransactionID: cashoutID,
		AccountID:     accountID,
		AmountMinor:   9000,
		Currency:      "GBP",
		Merchant:      "ElectronicsSuperstore",
		Country:       "GB",
		Channel:       "online",
	}
	env, _ := events.NewDeterministicEnvelope(uuid.New(), events.EventTypeTransactionCreated, events.CurrentVersion, t0.Add(3*time.Minute), cashoutPayload)
	err := svc.Handle(context.Background(), nil, env)
	require.NoError(t, err)

	mockStore.mu.RLock()
	defer mockStore.mu.RUnlock()

	flag, ok := mockStore.Flags[cashoutID]
	require.True(t, ok, "card testing cashout transaction must be flagged")
	assert.Equal(t, "high", flag.Severity)

	hasCardTestingSignal := false
	for _, s := range flag.Signals {
		if s.Rule == "card_testing" {
			hasCardTestingSignal = true
			assert.True(t, s.Score >= 0.80)
		}
	}
	assert.True(t, hasCardTestingSignal)
}

// 4. Outlier: Account averaging £12, then £400 flags; same £400 on £300 avg does NOT flag
func TestScenario4_Outlier(t *testing.T) {
	mockStore := NewMockStore()
	cfg := newTestRulesConfig()
	svc := service.NewService(cfg, mockStore, mockStore, nil, nil)

	// Account A: average £12.00 (1200 minor), 20 transactions history
	acctA := uuid.New()
	mockStore.SeedMatureProfile(acctA, 20, 1200, "GB", 60)

	// Account B: average £300.00 (30000 minor), 20 transactions history
	acctB := uuid.New()
	mockStore.SeedMatureProfile(acctB, 20, 30000, "GB", 60)

	now := time.Now().UTC()

	// £400 payment on Account A -> should flag as outlier
	txnAID := uuid.New()
	payloadA := events.TransactionCreated{
		TransactionID: txnAID,
		AccountID:     acctA,
		AmountMinor:   40000, // £400.00
		Currency:      "GBP",
		Merchant:      "Luxury Boutique",
		Country:       "GB",
		Channel:       "chip",
	}
	envA, _ := events.NewDeterministicEnvelope(uuid.New(), events.EventTypeTransactionCreated, events.CurrentVersion, now, payloadA)
	err := svc.Handle(context.Background(), nil, envA)
	require.NoError(t, err)

	// £400 payment on Account B -> should NOT flag
	txnBID := uuid.New()
	payloadB := events.TransactionCreated{
		TransactionID: txnBID,
		AccountID:     acctB,
		AmountMinor:   40000, // £400.00
		Currency:      "GBP",
		Merchant:      "Luxury Boutique",
		Country:       "GB",
		Channel:       "chip",
	}
	envB, _ := events.NewDeterministicEnvelope(uuid.New(), events.EventTypeTransactionCreated, events.CurrentVersion, now, payloadB)
	err = svc.Handle(context.Background(), nil, envB)
	require.NoError(t, err)

	mockStore.mu.RLock()
	defer mockStore.mu.RUnlock()

	_, flaggedA := mockStore.Flags[txnAID]
	assert.True(t, flaggedA, "£400 on £12 average account must trigger outlier flag")

	_, flaggedB := mockStore.Flags[txnBID]
	assert.False(t, flaggedB, "£400 on £300 average account must NOT flag")
}

// 5. Cold start: Brand-new account, £400 payment -> statistical rules stay silent
func TestScenario5_ColdStart(t *testing.T) {
	mockStore := NewMockStore()
	cfg := newTestRulesConfig()
	svc := service.NewService(cfg, mockStore, mockStore, nil, nil)

	newAcct := uuid.New()
	txnID := uuid.New()
	now := time.Now().UTC()

	payload := events.TransactionCreated{
		TransactionID: txnID,
		AccountID:     newAcct,
		AmountMinor:   40000, // £400.00 on Day 1
		Currency:      "GBP",
		Merchant:      "FirstPurchaseStore",
		Country:       "GB",
		Channel:       "chip",
	}
	env, _ := events.NewDeterministicEnvelope(uuid.New(), events.EventTypeTransactionCreated, events.CurrentVersion, now, payload)
	err := svc.Handle(context.Background(), nil, env)
	require.NoError(t, err)

	mockStore.mu.RLock()
	defer mockStore.mu.RUnlock()

	_, flagged := mockStore.Flags[txnID]
	assert.False(t, flagged, "cold start must not flag due to lack of baseline")
}

// 6. New Country: GB history then first-ever JP online payment flags
func TestScenario6_NewCountry(t *testing.T) {
	mockStore := NewMockStore()
	cfg := newTestRulesConfig()
	svc := service.NewService(cfg, mockStore, mockStore, nil, nil)

	acct := uuid.New()
	mockStore.SeedMatureProfile(acct, 30, 2500, "GB", 90)

	txnID := uuid.New()
	now := time.Now().UTC()

	// High value online payment in JP
	payload := events.TransactionCreated{
		TransactionID: txnID,
		AccountID:     acct,
		AmountMinor:   28000, // £280.00
		Currency:      "JPY",
		Merchant:      "Tokyo Electronics",
		Country:       "JP",
		Channel:       "online",
	}
	env, _ := events.NewDeterministicEnvelope(uuid.New(), events.EventTypeTransactionCreated, events.CurrentVersion, now, payload)
	err := svc.Handle(context.Background(), nil, env)
	require.NoError(t, err)

	mockStore.mu.RLock()
	defer mockStore.mu.RUnlock()

	flag, ok := mockStore.Flags[txnID]
	require.True(t, ok, "first-ever payment in new country must trigger flag")

	foundNewCountrySignal := false
	for _, s := range flag.Signals {
		if s.Rule == "new_country" {
			foundNewCountrySignal = true
			assert.Contains(t, s.Reason, "JP")
		}
	}
	assert.True(t, foundNewCountrySignal)
}

// 7. Impossible Travel: GB then US 20 minutes later
func TestScenario7_ImpossibleTravel(t *testing.T) {
	mockStore := NewMockStore()
	cfg := newTestRulesConfig()
	svc := service.NewService(cfg, mockStore, mockStore, nil, nil)

	acct := uuid.New()
	mockStore.SeedMatureProfile(acct, 25, 2000, "GB", 60)

	t0 := time.Now().UTC()

	// 1. Payment in London, GB
	londonTxn := events.TransactionCreated{
		TransactionID: uuid.New(),
		AccountID:     acct,
		AmountMinor:   1500,
		Currency:      "GBP",
		Merchant:      "London Cafe",
		Country:       "GB",
		Channel:       "contactless",
	}
	env1, _ := events.NewDeterministicEnvelope(uuid.New(), events.EventTypeTransactionCreated, events.CurrentVersion, t0, londonTxn)
	err := svc.Handle(context.Background(), nil, env1)
	require.NoError(t, err)

	// 2. Payment in New York, US only 20 minutes later
	usTxnID := uuid.New()
	nyTxn := events.TransactionCreated{
		TransactionID: usTxnID,
		AccountID:     acct,
		AmountMinor:   8500,
		Currency:      "USD",
		Merchant:      "NY Department Store",
		Country:       "US",
		Channel:       "chip",
	}
	env2, _ := events.NewDeterministicEnvelope(uuid.New(), events.EventTypeTransactionCreated, events.CurrentVersion, t0.Add(20*time.Minute), nyTxn)
	err = svc.Handle(context.Background(), nil, env2)
	require.NoError(t, err)

	mockStore.mu.RLock()
	defer mockStore.mu.RUnlock()

	flag, ok := mockStore.Flags[usTxnID]
	require.True(t, ok, "impossible travel across Atlantic in 20m must trigger flag")

	foundTravelSignal := false
	for _, s := range flag.Signals {
		if s.Rule == "impossible_travel" {
			foundTravelSignal = true
			assert.True(t, s.Score >= 0.70)
		}
	}
	assert.True(t, foundTravelSignal)
}

// 8. Missing Fields: Missing country is skipped, not an error
func TestScenario8_MissingFields(t *testing.T) {
	mockStore := NewMockStore()
	cfg := newTestRulesConfig()
	svc := service.NewService(cfg, mockStore, mockStore, nil, nil)

	acct := uuid.New()
	txnID := uuid.New()

	payload := events.TransactionCreated{
		TransactionID: txnID,
		AccountID:     acct,
		AmountMinor:   2500,
		Currency:      "GBP",
		Merchant:      "CornerShop",
		Country:       "", // omitted country
		Channel:       "", // omitted channel
	}
	env, _ := events.NewDeterministicEnvelope(uuid.New(), events.EventTypeTransactionCreated, events.CurrentVersion, time.Now().UTC(), payload)
	err := svc.Handle(context.Background(), nil, env)
	assert.NoError(t, err, "event with missing optional fields must be processed successfully")
}

// 9. Poison message: Malformed JSON or invalid schema routes immediately to DLQ
func TestScenario9_PoisonMessage(t *testing.T) {
	// Case A: Corrupted JSON
	corrupted := events.Envelope{
		EventID: uuid.New(),
		Data:    []byte(`{not valid json`),
	}
	var target events.TransactionCreated
	err := corrupted.UnmarshalData(&target)
	assert.Error(t, err)
	var permErr events.PermanentError
	assert.True(t, errors.As(err, &permErr), "unmarshal error must be PermanentError to route to DLQ")

	// Case B: Business validation failure (AmountMinor <= 0)
	invalidTxn := events.TransactionCreated{
		TransactionID: uuid.New(),
		AccountID:     uuid.New(),
		AmountMinor:   -50,
		Currency:      "GBP",
		Merchant:      "Test",
	}
	valErr := invalidTxn.Validate()
	assert.Error(t, valErr)
	assert.True(t, errors.As(valErr, &permErr), "negative amount must yield PermanentError")
}

// 10. ML Down: Scorer failure degrades gracefully to rules-only without crashing
type FailingMockScorer struct{}

func (f *FailingMockScorer) Score(ctx context.Context, feat *rules.Features) (*scorer.ScoreResponse, error) {
	return nil, errors.New("connection refused: 127.0.0.1:8000")
}
func (f *FailingMockScorer) IsShadowMode() bool { return false }

func TestScenario10_MLDown(t *testing.T) {
	mockStore := NewMockStore()
	cfg := newTestRulesConfig()
	failingScorer := &FailingMockScorer{}
	svc := service.NewService(cfg, mockStore, mockStore, failingScorer, nil)

	acct := uuid.New()
	mockStore.SeedMatureProfile(acct, 30, 1500, "GB", 60)

	txnID := uuid.New()
	payload := events.TransactionCreated{
		TransactionID: txnID,
		AccountID:     acct,
		AmountMinor:   50000, // £500 outlier
		Currency:      "GBP",
		Merchant:      "HighEndGoods",
		Country:       "GB",
		Channel:       "chip",
	}
	env, _ := events.NewDeterministicEnvelope(uuid.New(), events.EventTypeTransactionCreated, events.CurrentVersion, time.Now().UTC(), payload)

	// Must complete successfully without error even when ML scorer is down
	err := svc.Handle(context.Background(), nil, env)
	assert.NoError(t, err, "handler must succeed in rules-only degradation mode")

	mockStore.mu.RLock()
	defer mockStore.mu.RUnlock()

	flag, ok := mockStore.Flags[txnID]
	assert.True(t, ok, "rules must still trigger flag even when ML is offline")
	assert.True(t, flag.Score >= 0.50)
}

// 11. Reprocess Determinism: Replaying same events generates identical deterministic flag UUIDs
func TestScenario11_ReprocessDeterminism(t *testing.T) {
	txnID := uuid.New()
	rulesVersion := "2026-09-v3"

	flagID1 := db.DeterministicFlagID(txnID, rulesVersion)
	flagID2 := db.DeterministicFlagID(txnID, rulesVersion)

	assert.Equal(t, flagID1, flagID2, "flag IDs must be completely deterministic")
	assert.Equal(t, uuid.Version(5), flagID1.Version(), "deterministic flag ID must be UUID v5 (SHA-1)")
}

// 12. Event-time correctness: Late arriving event is evaluated at occurred_at
func TestScenario12_EventTimeCorrectness(t *testing.T) {
	mockStore := NewMockStore()
	cfg := newTestRulesConfig()
	svc := service.NewService(cfg, mockStore, mockStore, nil, nil)

	acct := uuid.New()
	baseTime := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

	// 5 payments at baseTime
	for i := 0; i < 5; i++ {
		payload := events.TransactionCreated{
			TransactionID: uuid.New(),
			AccountID:     acct,
			AmountMinor:   1000,
			Currency:      "GBP",
			Merchant:      "Store",
			Country:       "GB",
			Channel:       "contactless",
		}
		env, _ := events.NewDeterministicEnvelope(uuid.New(), events.EventTypeTransactionCreated, events.CurrentVersion, baseTime.Add(time.Duration(i*5)*time.Second), payload)
		err := svc.Handle(context.Background(), nil, env)
		require.NoError(t, err)
	}

	// Now deliver a late event: occurred 10 minutes AFTER baseTime
	lateTxnID := uuid.New()
	lateTime := baseTime.Add(10 * time.Minute)
	latePayload := events.TransactionCreated{
		TransactionID: lateTxnID,
		AccountID:     acct,
		AmountMinor:   1000,
		Currency:      "GBP",
		Merchant:      "Store",
		Country:       "GB",
		Channel:       "contactless",
	}
	lateEnv, _ := events.NewDeterministicEnvelope(uuid.New(), events.EventTypeTransactionCreated, events.CurrentVersion, lateTime, latePayload)
	err := svc.Handle(context.Background(), nil, lateEnv)
	require.NoError(t, err)

	mockStore.mu.RLock()
	defer mockStore.mu.RUnlock()

	// The late transaction occurred 10 minutes later, so count1m should be 0, meaning it must NOT flag on velocity!
	_, flagged := mockStore.Flags[lateTxnID]
	assert.False(t, flagged, "late event evaluated on occurred_at must have count1m=0 and not flag")
}
