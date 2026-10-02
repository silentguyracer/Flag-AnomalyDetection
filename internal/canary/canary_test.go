package canary

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"fraud-service/internal/events"
	"fraud-service/internal/rules"
)

func TestCanaryRunner_DifferentialEvaluation(t *testing.T) {
	prodCfg := &rules.Config{
		Version:       "2026-09-v3",
		FlagThreshold: 0.50,
		Rules: map[string]rules.RuleSetting{
			"velocity": {Enabled: true, Threshold: 5},
		},
	}
	shadowCfg := &rules.Config{
		Version:       "2026-09-v4",
		FlagThreshold: 0.50,
		Rules: map[string]rules.RuleSetting{
			"velocity": {Enabled: true, Threshold: 3}, // More aggressive
		},
	}

	prodEng := rules.NewEngine(prodCfg)
	shadowEng := rules.NewEngine(shadowCfg)

	runner := NewCanaryRunner(prodEng, shadowEng)

	now := time.Now().UTC()
	// Test 1: Count1m = 1 (both pass)
	f1 := &rules.Features{
		Txn:     events.TransactionCreated{TransactionID: uuid.New(), AccountID: uuid.New(), AmountMinor: 1000},
		At:      now,
		Count1m: 1,
	}
	runner.Evaluate(f1)

	// Test 2: Count1m = 4 (Prod passes, Shadow flags)
	f2 := &rules.Features{
		Txn:     events.TransactionCreated{TransactionID: uuid.New(), AccountID: uuid.New(), AmountMinor: 1000},
		At:      now,
		Count1m: 4,
	}
	runner.Evaluate(f2)

	// Test 3: Count1m = 6 (both flag)
	f3 := &rules.Features{
		Txn:     events.TransactionCreated{TransactionID: uuid.New(), AccountID: uuid.New(), AmountMinor: 1000},
		At:      now,
		Count1m: 6,
	}
	runner.Evaluate(f3)

	rep := runner.GetReport()
	assert.Equal(t, int64(3), rep.TotalEvaluated)
	assert.Equal(t, int64(1), rep.ProdFlags)
	assert.Equal(t, int64(2), rep.ShadowFlags)
	assert.Equal(t, int64(1), rep.ShadowUniqueCount)
	assert.InDelta(t, 66.7, rep.ConcordancePct, 1.0)
	assert.False(t, rep.SafeToPromote, "Concordance below 80% should not be safe to promote")
}
