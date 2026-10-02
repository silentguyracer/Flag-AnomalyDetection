package graph

import (
	"fmt"

	"github.com/google/uuid"
)

// CircularMuleRing represents a detected circular money laundering loop.
// Pattern: Account A -> Account B -> Account C -> Account A (Layering topology)
type CircularMuleRing struct {
	RingID        string      `json:"ring_id"`
	HopCount      int         `json:"hop_count"`       // e.g. 3 hops: A -> B -> C -> A
	Accounts      []uuid.UUID `json:"accounts"`        // ordered sequence in cycle
	TotalVolume   int64       `json:"total_volume"`    // total minor units transferred
	Confidence    float64     `json:"confidence"`      // 0..1 confidence score
	Description   string      `json:"description"`
}

// DetectCycles finds all directed cycles up to maxDepth starting from all accounts in the graph.
// Implements depth-limited DFS with visited path backtracking.
func (g *MemoryGraph) DetectCycles(maxDepth int) []CircularMuleRing {
	if maxDepth <= 1 {
		maxDepth = 5
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	var detectedRings []CircularMuleRing
	seenRings := make(map[string]bool)

	for startNode := range g.accountLinks {
		visited := make(map[uuid.UUID]bool)
		path := []uuid.UUID{startNode}
		visited[startNode] = true

		g.dfsCycle(startNode, startNode, visited, path, 1, maxDepth, &detectedRings, seenRings)
	}

	return detectedRings
}

func (g *MemoryGraph) dfsCycle(
	startNode, current uuid.UUID,
	visited map[uuid.UUID]bool,
	path []uuid.UUID,
	depth, maxDepth int,
	rings *[]CircularMuleRing,
	seenRings map[string]bool,
) {
	if depth > maxDepth {
		return
	}

	neighbors := g.accountLinks[current]
	if neighbors == nil {
		return
	}

	for next, volume := range neighbors {
		if next == startNode && depth >= 3 {
			// Found circular cycle back to startNode (minimum 3 hops to be a true ring)
			ringKey := canonicalRingKey(path)
			if !seenRings[ringKey] {
				seenRings[ringKey] = true
				accountsCopy := make([]uuid.UUID, len(path))
				copy(accountsCopy, path)

				confidence := 0.85 + 0.03*float64(len(path))
				if confidence > 0.99 {
					confidence = 0.99
				}

				*rings = append(*rings, CircularMuleRing{
					RingID:      fmt.Sprintf("ring_%x", ringKey[:8]),
					HopCount:    len(path),
					Accounts:    accountsCopy,
					TotalVolume: volume,
					Confidence:  confidence,
					Description: fmt.Sprintf("Circular Layering Ring: %d accounts in closed laundering loop (%s...)", len(path), path[0].String()[:8]),
				})
			}
			continue
		}

		if !visited[next] && depth < maxDepth {
			visited[next] = true
			path = append(path, next)

			g.dfsCycle(startNode, next, visited, path, depth+1, maxDepth, rings, seenRings)

			// Backtrack
			visited[next] = false
			path = path[:len(path)-1]
		}
	}
}

// canonicalRingKey creates a rotation-invariant string key for a cycle
func canonicalRingKey(path []uuid.UUID) string {
	if len(path) == 0 {
		return ""
	}
	minIdx := 0
	for i := 1; i < len(path); i++ {
		if path[i].String() < path[minIdx].String() {
			minIdx = i
		}
	}

	var key string
	for i := 0; i < len(path); i++ {
		idx := (minIdx + i) % len(path)
		key += path[idx].String() + "->"
	}
	return key
}
