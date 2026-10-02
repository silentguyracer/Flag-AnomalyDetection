package xai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"fraud-service/internal/rules"
)

func TestExplain_AdverseActionNotice(t *testing.T) {
	signals := []rules.Signal{
		{
			Rule:   "amount_outlier",
			Score:  0.95,
			Reason: "Amount £500.00 is a 9.5-sigma outlier",
		},
		{
			Rule:   "impossible_travel",
			Score:  0.80,
			Reason: "Impossible travel from GB to JP (9200 km in 15m)",
		},
	}

	notice := Explain("DECLINE", 0.99, signals)
	require.Equal(t, "DECLINE", notice.Decision)
	require.Equal(t, 0.99, notice.FinalScore)
	assert.Equal(t, "Amount £500.00 is a 9.5-sigma outlier", notice.PrimaryFactor)

	// Reason codes
	assert.Contains(t, notice.ReasonCodes, "EXCEEDS_HISTORICAL_AMOUNT_PROFILE")
	assert.Contains(t, notice.ReasonCodes, "SUPERHUMAN_GEOGRAPHIC_VELOCITY")

	// Feature attributions sum to approx 100%
	var totalPct float64
	for _, attr := range notice.FeatureAttributions {
		totalPct += attr.WeightPct
	}
	assert.InDelta(t, 100.0, totalPct, 1.0)

	// Counterfactuals
	require.NotEmpty(t, notice.CounterfactualGuidance)
	memo := notice.FormatMemorandum()
	assert.Contains(t, memo, "REGULATORY ADVERSE ACTION")
}
