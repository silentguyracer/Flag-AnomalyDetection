package biometrics

import (
	"fmt"
	"math"
)

// BiometricProfile represents client-side human interaction dynamics captured during checkout.
type BiometricProfile struct {
	InterKeyDelaysMs []float64 `json:"inter_key_delays_ms"` // Delays between sequential keystrokes
	KeyHoldTimesMs   []float64 `json:"key_hold_times_ms"`   // Duration a physical key is depressed
	MouseTrajectory  []Point   `json:"mouse_trajectory"`   // Coordinates and timestamps of cursor movement
	PasteEventsCount int       `json:"paste_events_count"`  // Copy-paste occurrences into payment fields
	FormCompletionMs float64   `json:"form_completion_ms"`  // Total milliseconds to complete checkout
}

type Point struct {
	X int64 `json:"x"`
	Y int64 `json:"y"`
	T int64 `json:"t"` // Milliseconds offset
}

// BiometricVerdict represents the assessment outcome.
type BiometricVerdict struct {
	IsRobotic       bool     `json:"is_robotic"`
	AnomalyScore    float64  `json:"anomaly_score"` // 0..1 confidence of bot / headless script
	RiskSignals     []string `json:"risk_signals"`
	CadenceStdDevMs float64  `json:"cadence_std_dev_ms"`
	TrajectoryJitter float64 `json:"trajectory_jitter"`
}

// Analyze evaluates the human neuromuscular dynamics against bot benchmarks.
func Analyze(p *BiometricProfile) BiometricVerdict {
	v := BiometricVerdict{}
	if p == nil {
		return v
	}

	var signals []string
	var anomalyMass float64

	// 1. Keystroke Dynamics Analysis (Inter-Key Delay Variance)
	if len(p.InterKeyDelaysMs) >= 4 {
		meanIKD, stdIKD := calculateStats(p.InterKeyDelaysMs)
		v.CadenceStdDevMs = math.Round(stdIKD*100) / 100

		// Bot check A: Uniform delay (zero human neuromuscular jitter, e.g. std < 3.0ms)
		if stdIKD < 4.0 {
			signals = append(signals, fmt.Sprintf("Synthetic Keystroke Cadence: Near-zero neuromuscular jitter (std=%.2fms)", stdIKD))
			anomalyMass += 0.85
		}
		// Bot check B: Instantaneous typing (superhuman speed, e.g. mean IKD < 15ms)
		if meanIKD < 20.0 {
			signals = append(signals, fmt.Sprintf("Superhuman Typing Speed: Mean inter-key delay is %.1fms (human minimum ~60ms)", meanIKD))
			anomalyMass += 0.90
		}
	}

	// 2. Mouse Flight Path & Micro-tremor Physics
	if len(p.MouseTrajectory) >= 3 {
		jitter := calculateTrajectoryJitter(p.MouseTrajectory)
		v.TrajectoryJitter = math.Round(jitter*1000) / 1000

		// Bot check C: Robotic straight line / teleportation (jitter near 0.0)
		if jitter < 0.01 {
			signals = append(signals, fmt.Sprintf("Robotic Cursor Vector: Linear unjittered flight path (curvature entropy=%.3f)", jitter))
			anomalyMass += 0.75
		}
	}

	// 3. Automated Form Completion Check
	if p.FormCompletionMs > 0 && p.FormCompletionMs < 350.0 {
		signals = append(signals, fmt.Sprintf("Sub-second Form Filling: Completed complex checkout in %.0fms (headless script indicator)", p.FormCompletionMs))
		anomalyMass += 0.80
	}

	if anomalyMass > 0.98 {
		anomalyMass = 0.98
	}

	v.RiskSignals = signals
	v.AnomalyScore = math.Round(anomalyMass*100) / 100
	v.IsRobotic = anomalyMass >= 0.70

	return v
}

func calculateStats(values []float64) (mean, std float64) {
	if len(values) == 0 {
		return 0, 0
	}
	var sum float64
	for _, val := range values {
		sum += val
	}
	mean = sum / float64(len(values))

	var sumSq float64
	for _, val := range values {
		d := val - mean
		sumSq += d * d
	}
	std = math.Sqrt(sumSq / float64(len(values)))
	return mean, std
}

func calculateTrajectoryJitter(points []Point) float64 {
	if len(points) < 3 {
		return 1.0 // Assume human if insufficient points
	}

	var curvatureSum float64
	for i := 1; i < len(points)-1; i++ {
		p0, p1, p2 := points[i-1], points[i], points[i+1]
		dx1 := float64(p1.X - p0.X)
		dy1 := float64(p1.Y - p0.Y)
		dx2 := float64(p2.X - p1.X)
		dy2 := float64(p2.Y - p1.Y)

		angle1 := math.Atan2(dy1, dx1)
		angle2 := math.Atan2(dy2, dx2)
		diff := math.Abs(angle2 - angle1)
		curvatureSum += diff
	}

	return curvatureSum / float64(len(points)-2)
}
