package drift

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComputePSI(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	// 1. Stable scenario: Two samples from the exact same normal distribution
	base := make([]float64, 1000)
	serv := make([]float64, 1000)
	for i := 0; i < 1000; i++ {
		base[i] = rng.NormFloat64()
		serv[i] = rng.NormFloat64()
	}

	report := ComputePSI("test_feature", base, serv, 10)
	assert.Equal(t, "STABLE", report.Status)
	assert.True(t, report.PSI < 0.10, "identical distributions must yield PSI < 0.10")

	// 2. Significant drift scenario: Mean shifted significantly from 0 to 2.5
	drifted := make([]float64, 1000)
	for i := 0; i < 1000; i++ {
		drifted[i] = rng.NormFloat64() + 2.5
	}

	driftReport := ComputePSI("test_feature", base, drifted, 10)
	assert.Equal(t, "SIGNIFICANT_DRIFT", driftReport.Status)
	assert.True(t, driftReport.PSI >= 0.25, "heavily shifted distribution must yield PSI >= 0.25")
}
