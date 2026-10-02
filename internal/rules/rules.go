package rules

import (
	"math"
	"time"

	"fraud-service/internal/events"
)

// Features holds all behavioural and historical context for evaluating a transaction.
// All evaluation is performed strictly at EVENT time (At = occurred_at), never time.Now().
type Features struct {
	Txn events.TransactionCreated
	At  time.Time // EVENT time (occurred_at), ensures determinism in replays & backtests

	Count1m      int
	Count10m     int
	Sum1h        int64
	ProfileN     int64
	MeanLog      float64
	StdLog       float64
	KnownCountries map[string]time.Time
	LastCountry  string
	LastTxnAt    time.Time
	AccountAge   time.Duration
	MerchantSeen bool

	// Supporting evidence for multi-transaction rules (e.g. card testing)
	RecentSmallCount10m int   // payments < £2 in last 10m
	HasRecentLargeIn10m bool  // whether there's already a large payment in window
}

// Signal represents a single rule evaluation outcome.
type Signal struct {
	Rule     string         `json:"rule"`
	Score    float64        `json:"score"`    // 0..1 confidence
	Reason   string         `json:"reason"`   // Human-readable rationale for analysts/auditors
	Evidence map[string]any `json:"evidence"` // Structured data for drill-down
}

// Rule defines the interface for all pluggable fraud detection rules.
type Rule interface {
	Name() string
	Evaluate(f *Features) *Signal // nil = rule does not trigger
}

// Combine implements the noisy-OR probabilistic combination formula:
// P(fraud) = 1 - \prod (1 - P_i)
// Independent weak signals accumulate gracefully within [0, 1].
func Combine(sigs []Signal) float64 {
	if len(sigs) == 0 {
		return 0.0
	}
	p := 1.0
	for _, s := range sigs {
		score := math.Max(0.0, math.Min(1.0, s.Score))
		p *= (1.0 - score)
	}
	combined := 1.0 - p
	if combined < 0.0 {
		return 0.0
	}
	if combined > 1.0 {
		return 1.0
	}
	return combined
}

// SeverityBand maps a score to low, medium, or high severity.
// Score < 0.5: no flag (or "low" if under threshold)
// 0.5 <= Score < 0.75: "medium"
// Score >= 0.75: "high"
func SeverityBand(score float64) string {
	switch {
	case score >= 0.75:
		return "high"
	case score >= 0.50:
		return "medium"
	default:
		return "low"
	}
}
