package biometrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAnalyze_SyntheticBotProfile(t *testing.T) {
	// Robotic bot: perfectly uniform 10.0ms delays and straight line cursor
	bot := &BiometricProfile{
		InterKeyDelaysMs: []float64{10.0, 10.0, 10.0, 10.0, 10.0},
		MouseTrajectory: []Point{
			{X: 10, Y: 10, T: 10},
			{X: 20, Y: 20, T: 20},
			{X: 30, Y: 30, T: 30},
		},
		FormCompletionMs: 150.0, // Sub-second automation
	}

	verdict := Analyze(bot)
	assert.True(t, verdict.IsRobotic, "Uniform IKD and straight mouse vector should be classified as robotic")
	assert.GreaterOrEqual(t, verdict.AnomalyScore, 0.85)
	assert.NotEmpty(t, verdict.RiskSignals)
}

func TestAnalyze_HumanProfile(t *testing.T) {
	// Human user: irregular neuromuscular jitter delays and curved mouse path
	human := &BiometricProfile{
		InterKeyDelaysMs: []float64{120.0, 85.0, 210.0, 65.0, 140.0, 95.0},
		MouseTrajectory: []Point{
			{X: 12, Y: 15, T: 50},
			{X: 35, Y: 88, T: 120},
			{X: 110, Y: 94, T: 250},
			{X: 205, Y: 180, T: 410},
		},
		FormCompletionMs: 6500.0, // 6.5s normal human reading & entry
	}

	verdict := Analyze(human)
	assert.False(t, verdict.IsRobotic, "Natural jitter and cadence must pass human verification")
	assert.Equal(t, 0.0, verdict.AnomalyScore)
	assert.Empty(t, verdict.RiskSignals)
}
