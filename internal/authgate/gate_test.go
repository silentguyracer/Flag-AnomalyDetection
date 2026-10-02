package authgate

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"fraud-service/internal/events"
	"fraud-service/internal/graph"
	"fraud-service/internal/rules"
)

func newTestRulesConfig() *rules.Config {
	return &rules.Config{
		Version:       "2026-09-v3",
		FlagThreshold: 0.50,
		Rules: map[string]rules.RuleSetting{
			"velocity":          {Enabled: true, Threshold: 5},
			"amount_outlier":    {Enabled: true, Z: 3.5, MinHistory: 10, MinAmountMinor: 5000},
			"new_country":       {Enabled: true, MinAccountAgeDays: 14},
			"impossible_travel": {Enabled: true, Window: 2 * time.Hour, MaxSpeedKmh: 850.0},
		},
	}
}

func TestAuthGate_SynchronousDecisions(t *testing.T) {
	cfg := newTestRulesConfig()
	engine := rules.NewEngine(cfg)
	memGraph := graph.NewMemoryGraph()

	gate := NewGate(engine, memGraph, nil, 25*time.Millisecond, true)

	accountID := uuid.New()
	firstSeen := time.Now().Add(-30 * 24 * time.Hour)
	knownCountries := map[string]time.Time{"GB": firstSeen}

	// 1. Test APPROVE: Normal low-risk payment
	normalReq := AuthRequest{
		TransactionID: uuid.New(),
		AccountID:     accountID,
		AmountMinor:   1500, // £15.00
		Currency:      "GBP",
		Merchant:      "Tesco Metro",
		Country:       "GB",
		Channel:       "contactless",
	}
	fNormal := &rules.Features{
		Txn: events.TransactionCreated{
			TransactionID: normalReq.TransactionID,
			AccountID:     accountID,
			AmountMinor:   1500,
			Country:       "GB",
			Merchant:      "Tesco Metro",
		},
		At:             time.Now().UTC(),
		ProfileN:       25,
		MeanLog:        math.Log(1500),
		StdLog:         0.35,
		KnownCountries: knownCountries,
		AccountAge:     30 * 24 * time.Hour,
	}

	resApprove := gate.Evaluate(context.Background(), normalReq, fNormal)
	assert.Equal(t, DecisionApprove, resApprove.Decision)
	assert.True(t, resApprove.RiskScore < 0.40)
	assert.True(t, resApprove.EvaluationMs < 25.0, "must evaluate well within 25ms SLA")

	// 2. Test CHALLENGE_3DS: First-time online payment in unfamiliar country (medium risk)
	challengeReq := AuthRequest{
		TransactionID: uuid.New(),
		AccountID:     accountID,
		AmountMinor:   4500, // £45.00
		Currency:      "EUR",
		Merchant:      "Parisian Boutique",
		Country:       "FR",
		Channel:       "online",
		DeviceID:      "", // unknown device
	}
	fChallenge := &rules.Features{
		Txn: events.TransactionCreated{
			TransactionID: challengeReq.TransactionID,
			AccountID:     accountID,
			AmountMinor:   4500,
			Country:       "FR",
			Merchant:      "Parisian Boutique",
			Channel:       "online",
		},
		At:             time.Now().UTC(),
		ProfileN:       25,
		MeanLog:        math.Log(1500),
		StdLog:         0.35,
		KnownCountries: knownCountries,
		LastCountry:    "GB",
		LastTxnAt:      time.Now().Add(-5 * time.Hour), // normal 5h gap (not impossible travel)
		AccountAge:     30 * 24 * time.Hour,
	}

	resChallenge := gate.Evaluate(context.Background(), challengeReq, fChallenge)
	assert.Equal(t, DecisionChallenge3DS, resChallenge.Decision)
	assert.True(t, resChallenge.RiskScore >= 0.40 && resChallenge.RiskScore < 0.75)

	// 3. Test DECLINE: Impossible Travel + Sudden Outlier (£450 in US only 10m after London)
	declineReq := AuthRequest{
		TransactionID: uuid.New(),
		AccountID:     accountID,
		AmountMinor:   45000, // £450.00
		Currency:      "USD",
		Merchant:      "Miami Electronics",
		Country:       "US",
		Channel:       "chip",
	}
	fDecline := &rules.Features{
		Txn: events.TransactionCreated{
			TransactionID: declineReq.TransactionID,
			AccountID:     accountID,
			AmountMinor:   45000,
			Country:       "US",
			Merchant:      "Miami Electronics",
		},
		At:             time.Now().UTC(),
		ProfileN:       25,
		MeanLog:        math.Log(1500),
		StdLog:         0.35,
		KnownCountries: knownCountries,
		LastCountry:    "GB",
		LastTxnAt:      time.Now().Add(-10 * time.Minute), // London to Miami in 10m!
		AccountAge:     30 * 24 * time.Hour,
	}

	resDecline := gate.Evaluate(context.Background(), declineReq, fDecline)
	assert.Equal(t, DecisionDecline, resDecline.Decision)
	assert.True(t, resDecline.RiskScore >= 0.75)

	hasTravelReason := false
	for _, r := range resDecline.Reasons {
		if strings.Contains(r, "impossible_travel") {
			hasTravelReason = true
			break
		}
	}
	assert.True(t, hasTravelReason, "reasons must include impossible_travel")

	// 4. Test Graph Syndicate Integration: 3 accounts on same device triggers DECLINE
	syndicateDevice := "shared_farm_phone_99"
	memGraph.Observe(uuid.New(), syndicateDevice, time.Now())
	memGraph.Observe(uuid.New(), syndicateDevice, time.Now())
	memGraph.Observe(uuid.New(), syndicateDevice, time.Now()) // 3 accounts on same device

	syndicateReq := AuthRequest{
		TransactionID: uuid.New(),
		AccountID:     accountID,
		AmountMinor:   2500,
		Currency:      "GBP",
		Merchant:      "Online Store",
		Country:       "GB",
		Channel:       "online",
		DeviceID:      syndicateDevice,
	}
	resSyndicate := gate.Evaluate(context.Background(), syndicateReq, nil)
	require.NotNil(t, resSyndicate.GraphAnomaly)
	assert.Equal(t, "shared_device_syndicate", resSyndicate.GraphAnomaly.Type)
	assert.Equal(t, DecisionChallenge3DS, resSyndicate.Decision)
}
