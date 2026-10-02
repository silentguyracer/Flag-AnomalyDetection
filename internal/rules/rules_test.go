package rules

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"fraud-service/internal/events"
)

func TestCombineNoisyOR(t *testing.T) {
	// Empty signals -> 0.0
	assert.Equal(t, 0.0, Combine(nil))

	// Single signal
	s1 := []Signal{{Score: 0.40}}
	assert.InDelta(t, 0.40, Combine(s1), 0.001)

	// Two 0.40 signals: 1 - (1 - 0.4)*(1 - 0.4) = 1 - 0.36 = 0.64
	s2 := []Signal{{Score: 0.40}, {Score: 0.40}}
	assert.InDelta(t, 0.64, Combine(s2), 0.001)

	// A strong signal + weak signal
	s3 := []Signal{{Score: 0.80}, {Score: 0.50}}
	assert.InDelta(t, 0.90, Combine(s3), 0.001)
}

func TestSeverityBands(t *testing.T) {
	assert.Equal(t, "low", SeverityBand(0.25))
	assert.Equal(t, "low", SeverityBand(0.49))
	assert.Equal(t, "medium", SeverityBand(0.50))
	assert.Equal(t, "medium", SeverityBand(0.74))
	assert.Equal(t, "high", SeverityBand(0.75))
	assert.Equal(t, "high", SeverityBand(0.99))
}

func TestVelocityRule(t *testing.T) {
	rule := VelocityRule{Threshold: 5, Sum1hThresholdMinor: 100000}

	now := time.Now().UTC()
	f := &Features{
		Txn:      events.TransactionCreated{AmountMinor: 500},
		At:       now,
		Count1m:  4,
		Sum1h:    2000,
	}

	// Below threshold -> nil
	assert.Nil(t, rule.Evaluate(f))

	// Equal to threshold 5 -> 0.40
	f.Count1m = 5
	sig := rule.Evaluate(f)
	require.NotNil(t, sig)
	assert.Equal(t, "velocity", sig.Rule)
	assert.InDelta(t, 0.40, sig.Score, 0.001)

	// Above threshold 8 -> 0.4 + 0.1*(8 - 5) = 0.70
	f.Count1m = 8
	sig = rule.Evaluate(f)
	require.NotNil(t, sig)
	assert.InDelta(t, 0.70, sig.Score, 0.001)
}

func TestCardTestingRule(t *testing.T) {
	rule := CardTestingRule{SmallUnderMinor: 200, MinSmall: 3, LargeOverMinor: 5000}
	now := time.Now().UTC()

	// 2 small payments then large (£90 = 9000 minor) -> insufficient small charges
	f := &Features{
		Txn:                 events.TransactionCreated{AmountMinor: 9000},
		At:                  now,
		RecentSmallCount10m: 2,
	}
	assert.Nil(t, rule.Evaluate(f))

	// 4 small payments then small payment (£1 = 100 minor) -> large transaction not reached
	f.Txn.AmountMinor = 100
	f.RecentSmallCount10m = 4
	assert.Nil(t, rule.Evaluate(f))

	// 4 small payments then £90 large payment -> triggers
	f.Txn.AmountMinor = 9000
	f.RecentSmallCount10m = 4
	sig := rule.Evaluate(f)
	require.NotNil(t, sig)
	assert.Equal(t, "card_testing", sig.Rule)
	assert.True(t, sig.Score >= 0.80)
}

func TestAmountOutlierRule(t *testing.T) {
	rule := AmountOutlierRule{ZThreshold: 3.5, MinHistory: 10, MinAmountMinor: 5000}
	now := time.Now().UTC()

	// Cold start: ProfileN < 10 stays silent even on huge amount
	f := &Features{
		Txn:      events.TransactionCreated{AmountMinor: 500000}, // £5,000
		At:       now,
		ProfileN: 5,
		MeanLog:  math.Log(1200), // typical £12
		StdLog:   0.4,
	}
	assert.Nil(t, rule.Evaluate(f), "cold start must not flag")

	// Mature account (ProfileN = 25): typical spend £12 (1200 minor), sudden £400 (40000 minor)
	f.ProfileN = 25
	f.Txn.AmountMinor = 40000
	// ln(40000) ~ 10.597, ln(1200) ~ 7.090, diff ~ 3.506 / 0.4 ~ 8.76 sigma
	sig := rule.Evaluate(f)
	require.NotNil(t, sig)
	assert.Equal(t, "amount_outlier", sig.Rule)
	assert.True(t, sig.Score >= 0.50)

	// Same £400 on account with typical spend £300 (30000 minor)
	f.MeanLog = math.Log(30000)
	f.StdLog = 0.5
	assert.Nil(t, rule.Evaluate(f), "£400 on £300 average account must not flag")
}

func TestNewCountryRule(t *testing.T) {
	rule := NewCountryRule{MinAccountAgeDays: 14}
	now := time.Now().UTC()

	// Missing country must be tolerated without error or flag
	f := &Features{
		Txn:        events.TransactionCreated{AmountMinor: 10000, Country: ""},
		At:         now,
		AccountAge: 30 * 24 * time.Hour,
	}
	assert.Nil(t, rule.Evaluate(f))

	// Cold start: account age < 14 days
	f.Txn.Country = "JP"
	f.AccountAge = 5 * 24 * time.Hour
	assert.Nil(t, rule.Evaluate(f), "young account must stay silent")

	// Mature account with GB history, first-ever JP payment
	f.AccountAge = 40 * 24 * time.Hour
	f.KnownCountries = map[string]time.Time{"GB": now.Add(-100 * time.Hour)}
	sig := rule.Evaluate(f)
	require.NotNil(t, sig)
	assert.Equal(t, "new_country", sig.Rule)
	assert.True(t, sig.Score >= 0.45)

	// Seen country JP does not flag
	f.KnownCountries["JP"] = now.Add(-10 * time.Hour)
	assert.Nil(t, rule.Evaluate(f))
}

func TestImpossibleTravelRule(t *testing.T) {
	rule := ImpossibleTravelRule{Window: 2 * time.Hour, MaxSpeedKmh: 850.0}
	now := time.Now().UTC()

	// Missing fields or identical country
	f := &Features{
		Txn:         events.TransactionCreated{Country: "GB"},
		LastCountry: "GB",
		At:          now,
		LastTxnAt:   now.Add(-10 * time.Minute),
	}
	assert.Nil(t, rule.Evaluate(f))

	// GB then US 20 minutes later (distance > 5,000 km, implied speed > 15,000 km/h)
	f.Txn.Country = "US"
	f.LastCountry = "GB"
	sig := rule.Evaluate(f)
	require.NotNil(t, sig)
	assert.Equal(t, "impossible_travel", sig.Rule)
	assert.True(t, sig.Score >= 0.70)

	// GB then FR 5 hours later -> normal travel
	f.Txn.Country = "FR"
	f.LastCountry = "GB"
	f.LastTxnAt = now.Add(-5 * time.Hour)
	assert.Nil(t, rule.Evaluate(f))
}

func TestNewMerchantHighValueRule(t *testing.T) {
	rule := NewMerchantHighValueRule{MinMultiplier: 5.0, MinAmountMinor: 5000}
	now := time.Now().UTC()

	// Known merchant -> no signal
	f := &Features{
		Txn:          events.TransactionCreated{AmountMinor: 50000, Merchant: "Tesco"},
		At:           now,
		ProfileN:     20,
		MeanLog:      math.Log(1000), // £10
		MerchantSeen: true,
	}
	assert.Nil(t, rule.Evaluate(f))

	// New merchant with 10x typical spend (£100 vs £10 avg)
	f.MerchantSeen = false
	f.Txn.Merchant = "Luxury Jeweller"
	f.Txn.AmountMinor = 10000 // £100
	sig := rule.Evaluate(f)
	require.NotNil(t, sig)
	assert.Equal(t, "new_merchant_high_value", sig.Rule)
	assert.InDelta(t, 0.40, sig.Score, 0.01)
}

func TestEngineEvaluation(t *testing.T) {
	cfg := &Config{
		Version:       "test-v1",
		FlagThreshold: 0.50,
		Rules: map[string]RuleSetting{
			"velocity":       {Enabled: true, Threshold: 5},
			"amount_outlier": {Enabled: true, Z: 3.5, MinHistory: 10, MinAmountMinor: 5000},
			"new_country":    {Enabled: true, MinAccountAgeDays: 14},
		},
	}
	engine := NewEngine(cfg)
	assert.Equal(t, "test-v1", engine.Version())

	now := time.Now().UTC()
	// Combined scenario: New country (0.45) + Outlier (0.60)
	f := &Features{
		Txn: events.TransactionCreated{
			TransactionID: uuid.New(),
			AccountID:     uuid.New(),
			AmountMinor:   50000, // £500
			Country:       "JP",
			Merchant:      "Tokyo Electronics",
		},
		At:             now,
		ProfileN:       20,
		MeanLog:        math.Log(1500),
		StdLog:         0.4,
		AccountAge:     30 * 24 * time.Hour,
		KnownCountries: map[string]time.Time{"GB": now.Add(-100 * time.Hour)},
	}

	signals := engine.Run(f)
	assert.Len(t, signals, 2) // new_country and amount_outlier
	score := Combine(signals)
	assert.True(t, score >= engine.FlagThreshold())
	assert.Equal(t, "high", SeverityBand(score))
}
