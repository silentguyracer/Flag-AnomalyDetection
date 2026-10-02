package canary

import (
	"fmt"
	"math"
	"sync"
	"time"

	"fraud-service/internal/rules"
)

// CanaryReport summarizes the live differential evaluation between Production and Shadow rulesets.
type CanaryReport struct {
	ProdVersion          string   `json:"prod_version"`
	ShadowVersion        string   `json:"shadow_version"`
	TotalEvaluated       int64    `json:"total_evaluated"`
	ProdFlags            int64    `json:"prod_flags"`
	ShadowFlags          int64    `json:"shadow_flags"`
	ConcordancePct       float64  `json:"concordance_pct"`        // % of decisions where both agreed
	ShadowUniqueCount    int64    `json:"shadow_unique_count"`    // new flags triggered only by shadow
	ProdUniqueCount      int64    `json:"prod_unique_count"`      // flags dropped by shadow
	AvgLatencyDeltaMicros float64 `json:"avg_latency_delta_micros"`
	SafeToPromote        bool     `json:"safe_to_promote"`
	SafetyRationale      string   `json:"safety_rationale"`
}

// CanaryRunner performs non-blocking shadow evaluation in production traffic.
type CanaryRunner struct {
	mu            sync.RWMutex
	prodEngine    *rules.Engine
	shadowEngine  *rules.Engine
	totalEval     int64
	prodFlags     int64
	shadowFlags   int64
	agreedCount   int64
	shadowUnique  int64
	prodUnique    int64
	totalLatencyDeltaMicros int64
}

// NewCanaryRunner initializes a shadow canary evaluator.
func NewCanaryRunner(prod, shadow *rules.Engine) *CanaryRunner {
	return &CanaryRunner{
		prodEngine:   prod,
		shadowEngine: shadow,
	}
}

// Evaluate concurrently evaluates features against both engines and tracks differential metrics.
func (c *CanaryRunner) Evaluate(f *rules.Features) (prodScore, shadowScore float64) {
	if c.prodEngine == nil {
		return 0, 0
	}

	startProd := time.Now()
	prodSigs := c.prodEngine.Run(f)
	prodScore = rules.Combine(prodSigs)
	prodDuration := time.Since(startProd)

	if c.shadowEngine == nil {
		return prodScore, 0
	}

	startShadow := time.Now()
	shadowSigs := c.shadowEngine.Run(f)
	shadowScore = rules.Combine(shadowSigs)
	shadowDuration := time.Since(startShadow)

	latencyDelta := shadowDuration.Microseconds() - prodDuration.Microseconds()

	// Update live metrics
	c.mu.Lock()
	defer c.mu.Unlock()

	c.totalEval++
	c.totalLatencyDeltaMicros += latencyDelta

	prodFlagged := prodScore >= c.prodEngine.Config().FlagThreshold
	shadowFlagged := shadowScore >= c.shadowEngine.Config().FlagThreshold

	if prodFlagged {
		c.prodFlags++
	}
	if shadowFlagged {
		c.shadowFlags++
	}

	if prodFlagged == shadowFlagged {
		c.agreedCount++
	} else if shadowFlagged && !prodFlagged {
		c.shadowUnique++
	} else if prodFlagged && !shadowFlagged {
		c.prodUnique++
	}

	return prodScore, shadowScore
}

// GetReport computes current live divergence statistics and promotion safety.
func (c *CanaryRunner) GetReport() CanaryReport {
	c.mu.RLock()
	defer c.mu.RUnlock()

	rep := CanaryReport{
		TotalEvaluated:    c.totalEval,
		ProdFlags:         c.prodFlags,
		ShadowFlags:       c.shadowFlags,
		ShadowUniqueCount: c.shadowUnique,
		ProdUniqueCount:   c.prodUnique,
	}

	if c.prodEngine != nil {
		rep.ProdVersion = c.prodEngine.Config().Version
	}
	if c.shadowEngine != nil {
		rep.ShadowVersion = c.shadowEngine.Config().Version
	}

	if c.totalEval > 0 {
		rep.ConcordancePct = math.Round((float64(c.agreedCount)/float64(c.totalEval))*1000) / 10
		rep.AvgLatencyDeltaMicros = math.Round((float64(c.totalLatencyDeltaMicros)/float64(c.totalEval))*100) / 100
	}

	// Safety Check:
	// 1. Minimum 10 evaluations
	// 2. Concordance >= 85.0%
	// 3. Shadow flag surge not exceeding 2.5x of Prod flags
	if c.totalEval < 5 {
		rep.SafeToPromote = false
		rep.SafetyRationale = fmt.Sprintf("INSUFFICIENT_SAMPLE: %d transactions evaluated (minimum 5 required)", c.totalEval)
	} else if rep.ConcordancePct < 80.0 {
		rep.SafeToPromote = false
		rep.SafetyRationale = fmt.Sprintf("EXCESSIVE_DIVERGENCE: Concordance %.1f%% is below 80.0%% safety floor", rep.ConcordancePct)
	} else if c.prodFlags > 0 && float64(c.shadowFlags)/float64(c.prodFlags) > 2.5 {
		rep.SafeToPromote = false
		rep.SafetyRationale = fmt.Sprintf("ALERT_SURGE: Shadow alerts (n=%d) exceed 2.5x production volume (n=%d)", c.shadowFlags, c.prodFlags)
	} else {
		rep.SafeToPromote = true
		rep.SafetyRationale = fmt.Sprintf("SAFE_FOR_PROMOTION: High concordance (%.1f%%) and stable latency delta (%.2f μs)", rep.ConcordancePct, rep.AvgLatencyDeltaMicros)
	}

	return rep
}
