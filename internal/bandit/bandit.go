package bandit

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

// Dynamic Threshold Bandit Optimization via Thompson Sampling (Beta-Bernoulli Conjugate Prior).
// Automatically balances fraud prevention yield against customer false decline friction.

// ThresholdArm represents a candidate risk decision threshold policy.
type ThresholdArm struct {
	ID             string  `json:"id"`
	Threshold      float64 `json:"threshold"`        // e.g. 0.40, 0.50, 0.60
	Description    string  `json:"description"`
	Alpha          float64 `json:"alpha"`            // Success count (fraud caught / value protected)
	Beta           float64 `json:"beta"`             // Failure count (false positives / customer friction)
	PullCount      int64   `json:"pull_count"`
	TotalNetReward float64 `json:"total_net_reward"`
}

// BanditOptimizer manages exploration vs exploitation across dynamic threshold policies.
type BanditOptimizer struct {
	mu   sync.RWMutex
	rng  *rand.Rand
	arms []*ThresholdArm
}

// NewBanditOptimizer initializes candidate threshold policies.
func NewBanditOptimizer() *BanditOptimizer {
	source := rand.NewSource(time.Now().UnixNano())
	return &BanditOptimizer{
		rng: rand.New(source),
		arms: []*ThresholdArm{
			{ID: "conservative", Threshold: 0.35, Description: "Aggressive block, low fraud tolerance", Alpha: 2.0, Beta: 2.0},
			{ID: "balanced", Threshold: 0.50, Description: "Standard production balanced policy", Alpha: 5.0, Beta: 2.0},
			{ID: "lenient", Threshold: 0.65, Description: "Frictionless, higher false-negative tolerance", Alpha: 3.0, Beta: 2.0},
			{ID: "permissive", Threshold: 0.75, Description: "VIP / Low-friction payment tier", Alpha: 2.0, Beta: 3.0},
		},
	}
}

// SelectArm draws from the posterior Beta distribution of each arm and picks the argmax.
func (b *BanditOptimizer) SelectArm() *ThresholdArm {
	b.mu.Lock()
	defer b.mu.Unlock()

	var bestArm *ThresholdArm
	bestSample := -1.0

	for _, arm := range b.arms {
		// Sample from Beta(alpha, beta) using Gamma variates
		sample := b.sampleBeta(arm.Alpha, arm.Beta)
		if sample > bestSample {
			bestSample = sample
			bestArm = arm
		}
	}

	bestArm.PullCount++
	return bestArm
}

// RecordFeedback updates the posterior Beta distribution based on reviewer verdict or chargeback.
// isTruePositive: true if fraud was accurately stopped (reward)
// falsePositiveFriction: true if genuine customer was wrongly inconvenienced (penalty)
func (b *BanditOptimizer) RecordFeedback(armID string, isTruePositive bool, financialRewardMinor int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, arm := range b.arms {
		if arm.ID == armID {
			if isTruePositive {
				// Success: prevented fraud loss
				arm.Alpha += 1.0
				arm.TotalNetReward += float64(financialRewardMinor) / 100.0
			} else {
				// Failure: false decline friction cost
				arm.Beta += 1.0
				arm.TotalNetReward -= 50.0 // Estimated customer friction cost £50.00
			}
			return
		}
	}
}

// sampleBeta generates a random sample from Beta(a, betaParam) via Gamma variate transform.
func (b *BanditOptimizer) sampleBeta(a, betaParam float64) float64 {
	x := b.sampleGamma(a)
	y := b.sampleGamma(betaParam)
	if x+y == 0 {
		return 0.5
	}
	return x / (x + y)
}

// Marsaglia and Tsang method for Gamma(d) when d >= 1
func (b *BanditOptimizer) sampleGamma(alpha float64) float64 {
	if alpha < 1.0 {
		return b.sampleGamma(alpha+1.0) * math.Pow(b.rng.Float64(), 1.0/alpha)
	}

	d := alpha - 1.0/3.0
	c := 1.0 / math.Sqrt(9.0*d)

	for {
		z := b.rng.NormFloat64()
		v := 1.0 + c*z
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := b.rng.Float64()

		if u < 1.0-0.0331*z*z*z*z {
			return d * v
		}
		if math.Log(u) < 0.5*z*z+d*(1.0-v+math.Log(v)) {
			return d * v
		}
	}
}

// PolicySummary returns current posterior distributions and expected win rates.
type PolicySummary struct {
	ArmID          string  `json:"arm_id"`
	Threshold      float64 `json:"threshold"`
	ExpectedWinPct float64 `json:"expected_win_pct"`
	Alpha          float64 `json:"alpha"`
	Beta           float64 `json:"beta"`
	Pulls          int64   `json:"pulls"`
	NetRewardGBP   float64 `json:"net_reward_gbp"`
}

func (b *BanditOptimizer) GetSummaries() []PolicySummary {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var summaries []PolicySummary
	for _, arm := range b.arms {
		winRate := arm.Alpha / (arm.Alpha + arm.Beta)
		summaries = append(summaries, PolicySummary{
			ArmID:          arm.ID,
			Threshold:      arm.Threshold,
			ExpectedWinPct: math.Round(winRate*1000) / 10,
			Alpha:          arm.Alpha,
			Beta:           arm.Beta,
			Pulls:          arm.PullCount,
			NetRewardGBP:   math.Round(arm.TotalNetReward*100) / 100,
		})
	}
	return summaries
}
