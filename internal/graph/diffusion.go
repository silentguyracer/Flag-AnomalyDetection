package graph

import (
	"math"

	"github.com/google/uuid"
)

// ContaminatedNode represents an account that received diffuse risk from confirmed fraud nodes.
type ContaminatedNode struct {
	AccountID uuid.UUID `json:"account_id"`
	RiskScore float64   `json:"risk_score"` // 0..1 diffused contamination score
	IsSeed    bool      `json:"is_seed"`    // true if this was an original confirmed fraud account
	HopsFromSeed int    `json:"hops_from_seed"`
}

// ComputeRiskDiffusion executes a Personalized PageRank / Random Walk with Restart algorithm.
// seedFraudAccounts: accounts confirmed as fraudulent (teleport vector source)
// alpha: probability of following transaction links (typically 0.85)
// maxIterations: power iteration limit (typically 20-30)
func (g *MemoryGraph) ComputeRiskDiffusion(
	seedFraudAccounts []uuid.UUID,
	alpha float64,
	maxIterations int,
) map[uuid.UUID]float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if len(seedFraudAccounts) == 0 {
		return nil
	}
	if alpha <= 0 || alpha >= 1.0 {
		alpha = 0.85
	}
	if maxIterations <= 0 {
		maxIterations = 25
	}

	// 1. Collect all active account nodes in the transfer graph
	nodesMap := make(map[uuid.UUID]bool)
	for _, seed := range seedFraudAccounts {
		nodesMap[seed] = true
	}
	for src, neighbors := range g.accountLinks {
		nodesMap[src] = true
		for dst := range neighbors {
			nodesMap[dst] = true
		}
	}

	numNodes := len(nodesMap)
	if numNodes == 0 {
		return nil
	}

	// 2. Build seed / teleport vector s
	s := make(map[uuid.UUID]float64)
	seedWeight := 1.0 / float64(len(seedFraudAccounts))
	for _, seed := range seedFraudAccounts {
		s[seed] = seedWeight
	}

	// 3. Initialize rank vector r = s
	r := make(map[uuid.UUID]float64)
	for node := range nodesMap {
		r[node] = s[node]
	}

	// 4. Precompute total outgoing volume per source for weighted transition probabilities
	outDegree := make(map[uuid.UUID]int64)
	for src, neighbors := range g.accountLinks {
		var tot int64
		for _, vol := range neighbors {
			tot += vol
		}
		outDegree[src] = tot
	}

	// 5. Power Iteration: r_{t+1} = alpha * (P^T * r_t) + (1 - alpha) * s
	for iter := 0; iter < maxIterations; iter++ {
		nextR := make(map[uuid.UUID]float64)

		// Base teleport contribution: (1 - alpha) * s
		for node := range nodesMap {
			nextR[node] = (1.0 - alpha) * s[node]
		}

		// Graph walk transition: alpha * sum( r_u * P(u -> v) )
		for u, neighbors := range g.accountLinks {
			tot := outDegree[u]
			if tot <= 0 || r[u] <= 0 {
				continue
			}

			probU := r[u]
			for v, vol := range neighbors {
				transitionWeight := float64(vol) / float64(tot)
				nextR[v] += alpha * probU * transitionWeight
			}
		}

		// Handle dangling nodes (nodes with outDegree == 0): redistribute mass to seed set
		var danglingMass float64
		for node := range nodesMap {
			if outDegree[node] == 0 {
				danglingMass += r[node]
			}
		}
		if danglingMass > 0 {
			for _, seed := range seedFraudAccounts {
				nextR[seed] += alpha * danglingMass * seedWeight
			}
		}

		// Check L1 convergence: ||nextR - r||_1 < 1e-6
		var diff float64
		for node := range nodesMap {
			diff += math.Abs(nextR[node] - r[node])
		}

		r = nextR
		if diff < 1e-6 {
			break
		}
	}

	// Normalize scores relative to maximum non-seed or calibrated scale [0, 1]
	maxScore := 0.0
	for _, score := range r {
		if score > maxScore {
			maxScore = score
		}
	}

	normalized := make(map[uuid.UUID]float64)
	if maxScore > 0 {
		for node, score := range r {
			normScore := score / maxScore
			if normScore > 1.0 {
				normScore = 1.0
			}
			normalized[node] = normScore
		}
	}

	return normalized
}

// GetContaminatedAccounts finds all accounts whose diffused risk score exceeds minRisk
func (g *MemoryGraph) GetContaminatedAccounts(
	seedFraudAccounts []uuid.UUID,
	minRisk float64,
) []ContaminatedNode {
	scores := g.ComputeRiskDiffusion(seedFraudAccounts, 0.85, 25)
	if scores == nil {
		return nil
	}

	seedSet := make(map[uuid.UUID]bool)
	for _, s := range seedFraudAccounts {
		seedSet[s] = true
	}

	var results []ContaminatedNode
	for node, score := range scores {
		if score >= minRisk {
			results = append(results, ContaminatedNode{
				AccountID:    node,
				RiskScore:    math.Round(score*1000) / 1000,
				IsSeed:       seedSet[node],
				HopsFromSeed: 1, // proximate neighbor
			})
		}
	}
	return results
}
