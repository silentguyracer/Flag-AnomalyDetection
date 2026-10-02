package bandit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBanditOptimizer_SelectionAndLearning(t *testing.T) {
	b := NewBanditOptimizer()
	require.NotEmpty(t, b.arms)

	// Simulate selecting an arm
	arm := b.SelectArm()
	require.NotNil(t, arm)
	assert.Greater(t, arm.PullCount, int64(0))

	// Provide positive feedback to "balanced" arm (caught £500 fraud)
	initialAlpha := arm.Alpha
	b.RecordFeedback(arm.ID, true, 50000)

	summaries := b.GetSummaries()
	for _, s := range summaries {
		if s.ArmID == arm.ID {
			assert.Equal(t, initialAlpha+1.0, s.Alpha)
			assert.Greater(t, s.NetRewardGBP, 0.0)
		}
	}

	// Repeated rewards should increase win rate probability
	for i := 0; i < 50; i++ {
		b.RecordFeedback("balanced", true, 10000)
	}

	updatedSummaries := b.GetSummaries()
	for _, s := range updatedSummaries {
		if s.ArmID == "balanced" {
			assert.Greater(t, s.ExpectedWinPct, 80.0)
		}
	}
}
