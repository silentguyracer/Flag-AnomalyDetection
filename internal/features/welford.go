package features

import (
	"math"
)

// WelfordProfile represents the online running baseline for an account.
type WelfordProfile struct {
	N       int64   `json:"n"`
	MeanLog float64 `json:"mean_log"`
	M2Log   float64 `json:"m2_log"`
}

// StdLog returns the sample standard deviation of log-amounts.
// Returns 0 if N < 2.
func (w WelfordProfile) StdLog() float64 {
	if w.N < 2 {
		return 0.0
	}
	variance := w.M2Log / float64(w.N-1)
	if variance < 0.0 {
		return 0.0
	}
	return math.Sqrt(variance)
}

// Update computes the next Welford state by incorporating a new amount (in minor units).
// Always executed AFTER evaluation to prevent an outlier from polluting its own baseline.
func (w *WelfordProfile) Update(amountMinor int64) {
	if amountMinor <= 0 {
		return
	}
	x := math.Log(float64(amountMinor))
	w.N++
	d := x - w.MeanLog
	w.MeanLog += d / float64(w.N)
	w.M2Log += d * (x - w.MeanLog)
}
