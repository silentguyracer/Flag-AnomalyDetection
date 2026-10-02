package features

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWelfordProfile(t *testing.T) {
	p := &WelfordProfile{}
	assert.Equal(t, int64(0), p.N)
	assert.Equal(t, 0.0, p.StdLog())

	// Incorporate 1st value: £10.00 (1000 minor)
	p.Update(1000)
	assert.Equal(t, int64(1), p.N)
	assert.InDelta(t, math.Log(1000), p.MeanLog, 0.0001)
	assert.Equal(t, 0.0, p.StdLog())

	// Incorporate more values
	amounts := []int64{1200, 1100, 950, 1050, 1000, 1300, 1150, 1250, 1000}
	for _, a := range amounts {
		p.Update(a)
	}

	assert.Equal(t, int64(10), p.N)
	assert.True(t, p.MeanLog > 0)
	assert.True(t, p.StdLog() > 0)

	// Compare with naive computation
	var sum float64
	for _, a := range append([]int64{1000}, amounts...) {
		sum += math.Log(float64(a))
	}
	expectedMean := sum / 10.0
	assert.InDelta(t, expectedMean, p.MeanLog, 0.0001)
}
