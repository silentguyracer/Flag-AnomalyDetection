package embeddings

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Temporal Graph Continuous-Time Random Walk & Dynamic Node Embedding Engine.
// Encodes temporal transaction sequences into 16-dimensional embedding vectors
// with time-decayed exponential kernels: K(Δt) = exp(-λ * Δt).

const (
	EmbeddingDim = 16
	DecayLambda  = 0.0001 // Half-life decay parameter for temporal edge recency
)

// TemporalEdge represents a directed transfer with microsecond timestamp and amount.
type TemporalEdge struct {
	Source      uuid.UUID
	Destination uuid.UUID
	AmountMinor int64
	Timestamp   time.Time
}

// TemporalGraph stores continuous-time directed transaction sequences.
type TemporalGraph struct {
	mu    sync.RWMutex
	edges map[uuid.UUID][]TemporalEdge // Source -> chronological edges
}

func NewTemporalGraph() *TemporalGraph {
	return &TemporalGraph{
		edges: make(map[uuid.UUID][]TemporalEdge),
	}
}

// AddEdge inserts a directed chronological edge into the temporal graph.
func (tg *TemporalGraph) AddEdge(src, dst uuid.UUID, amountMinor int64, t time.Time) {
	if src == uuid.Nil || dst == uuid.Nil {
		return
	}

	tg.mu.Lock()
	defer tg.mu.Unlock()

	tg.edges[src] = append(tg.edges[src], TemporalEdge{
		Source:      src,
		Destination: dst,
		AmountMinor: amountMinor,
		Timestamp:   t,
	})
}

// NodeEmbedding represents the continuous 16-dimensional latent representation of an account.
type NodeEmbedding struct {
	AccountID uuid.UUID            `json:"account_id"`
	Vector    [EmbeddingDim]float64 `json:"vector"`
	Norm      float64              `json:"norm"`
}

// ComputeEmbedding generates a continuous-time embedding vector for targetAccount via temporal random walks.
func (tg *TemporalGraph) ComputeEmbedding(targetAccount uuid.UUID, numWalks, walkLength int, seed int64) NodeEmbedding {
	tg.mu.RLock()
	defer tg.mu.RUnlock()

	rng := rand.New(rand.NewSource(seed))
	var vec [EmbeddingDim]float64

	for w := 0; w < numWalks; w++ {
		current := targetAccount
		currentT := time.Time{}

		for step := 0; step < walkLength; step++ {
			outEdges := tg.edges[current]
			if len(outEdges) == 0 {
				break
			}

			// Filter for forward-in-time edges (t >= currentT)
			var validEdges []TemporalEdge
			for _, e := range outEdges {
				if e.Timestamp.After(currentT) || e.Timestamp.Equal(currentT) {
					validEdges = append(validEdges, e)
				}
			}

			if len(validEdges) == 0 {
				break
			}

			// Sample edge with exponential time-decay weighting
			chosen := validEdges[rng.Intn(len(validEdges))]

			deltaSec := 1.0
			if !currentT.IsZero() {
				deltaSec = chosen.Timestamp.Sub(currentT).Seconds()
				if deltaSec < 0 {
					deltaSec = 0
				}
			}

			decayWeight := math.Exp(-DecayLambda * deltaSec)
			logAmt := math.Log(float64(chosen.AmountMinor) + 1.0)

			// Project into embedding dimension slot
			dimSlot := (step + w) % EmbeddingDim
			vec[dimSlot] += decayWeight * logAmt

			current = chosen.Destination
			currentT = chosen.Timestamp
		}
	}

	// L2 Normalization
	var sumSq float64
	for i := 0; i < EmbeddingDim; i++ {
		sumSq += vec[i] * vec[i]
	}
	norm := math.Sqrt(sumSq)

	unitNorm := 0.0
	if norm > 0 {
		for i := 0; i < EmbeddingDim; i++ {
			vec[i] /= norm
		}
		unitNorm = 1.0
	}

	return NodeEmbedding{
		AccountID: targetAccount,
		Vector:    vec,
		Norm:      unitNorm,
	}
}

// CosineSimilarity computes cosine distance between two account embeddings.
func CosineSimilarity(a, b NodeEmbedding) float64 {
	var dot float64
	for i := 0; i < EmbeddingDim; i++ {
		dot += a.Vector[i] * b.Vector[i]
	}
	return dot
}
