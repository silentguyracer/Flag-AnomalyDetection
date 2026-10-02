package mcc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMCCRiskTiers(t *testing.T) {
	cryptoProf := GetProfile("6051")
	assert.Equal(t, TierCritical, cryptoProf.Tier)
	assert.Equal(t, 1.80, cryptoProf.RiskMultiplier)
	assert.Equal(t, 2, cryptoProf.MaxVelocityPerMin)

	groceryProf := GetProfile("5411")
	assert.Equal(t, TierLow, groceryProf.Tier)
	assert.Equal(t, 0.80, groceryProf.RiskMultiplier)
	assert.Equal(t, 8, groceryProf.MaxVelocityPerMin)

	unknownProf := GetProfile("9999")
	assert.Equal(t, TierStandard, unknownProf.Tier)
	assert.Equal(t, 1.00, unknownProf.RiskMultiplier)
}

func TestAdjustScore(t *testing.T) {
	// Raw score 0.40 on crypto MCC 6051 (multiplier 1.80) -> 0.72 (crosses into elevated risk)
	adjusted, prof := AdjustScore(0.40, "6051")
	assert.InDelta(t, 0.72, adjusted, 0.0001)
	assert.Equal(t, "6051", prof.Code)

	// Raw score 0.80 capped at 1.0
	adjustedCapped, _ := AdjustScore(0.80, "6051")
	assert.Equal(t, 1.0, adjustedCapped)
}
