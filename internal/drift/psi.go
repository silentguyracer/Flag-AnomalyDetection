package drift

import (
	"fmt"
	"math"
	"sort"
)

// DriftReport encapsulates the statistical distribution stability metrics between baseline and production.
type DriftReport struct {
	FeatureName string  `json:"feature_name"`
	PSI         float64 `json:"psi"`          // Population Stability Index
	Status      string  `json:"status"`       // "STABLE" (< 0.10) | "MODERATE_DRIFT" (0.10 - 0.25) | "SIGNIFICANT_DRIFT" (>= 0.25)
	BaselineN   int     `json:"baseline_n"`
	ServingN    int     `json:"serving_n"`
	Message     string  `json:"message"`
}

// ComputePSI calculates the Population Stability Index between a baseline reference sample and a serving sample.
// Uses B quantile-based bins (e.g. 10 deciles).
func ComputePSI(featureName string, baseline, serving []float64, numBins int) DriftReport {
	if len(baseline) == 0 || len(serving) == 0 {
		return DriftReport{
			FeatureName: featureName,
			PSI:         0.0,
			Status:      "INSUFFICIENT_DATA",
			Message:     "Insufficient observations to compute PSI",
		}
	}

	if numBins <= 0 {
		numBins = 10
	}

	// 1. Sort baseline to determine bin boundary quantiles
	sortedBase := make([]float64, len(baseline))
	copy(sortedBase, baseline)
	sort.Float64s(sortedBase)

	cutoffs := make([]float64, numBins-1)
	for i := 1; i < numBins; i++ {
		idx := int(float64(i) * float64(len(sortedBase)) / float64(numBins))
		if idx >= len(sortedBase) {
			idx = len(sortedBase) - 1
		}
		cutoffs[i-1] = sortedBase[idx]
	}

	// 2. Count frequencies in baseline and serving
	baseCounts := countBins(baseline, cutoffs)
	servCounts := countBins(serving, cutoffs)

	baseTotal := float64(len(baseline))
	servTotal := float64(len(serving))

	// 3. Compute PSI with epsilon smoothing to prevent division by zero
	const eps = 1e-4
	psi := 0.0

	for i := 0; i < numBins; i++ {
		p := (float64(baseCounts[i]) + eps) / (baseTotal + float64(numBins)*eps)
		q := (float64(servCounts[i]) + eps) / (servTotal + float64(numBins)*eps)

		delta := q - p
		ratio := q / p
		psi += delta * math.Log(ratio)
	}

	var status, msg string
	switch {
	case psi >= 0.25:
		status = "SIGNIFICANT_DRIFT"
		msg = fmt.Sprintf("CRITICAL: Significant concept drift detected (PSI=%.4f >= 0.25). Immediate retraining required.", psi)
	case psi >= 0.10:
		status = "MODERATE_DRIFT"
		msg = fmt.Sprintf("WARNING: Moderate distribution drift detected (PSI=%.4f). Monitor performance closely.", psi)
	default:
		status = "STABLE"
		msg = fmt.Sprintf("STABLE: Feature distribution matches baseline (PSI=%.4f < 0.10).", psi)
	}

	return DriftReport{
		FeatureName: featureName,
		PSI:         psi,
		Status:      status,
		BaselineN:   len(baseline),
		ServingN:    len(serving),
		Message:     msg,
	}
}

func countBins(data []float64, cutoffs []float64) []int {
	bins := make([]int, len(cutoffs)+1)
	for _, val := range data {
		placed := false
		for i, c := range cutoffs {
			if val <= c {
				bins[i]++
				placed = true
				break
			}
		}
		if !placed {
			bins[len(cutoffs)]++
		}
	}
	return bins
}
