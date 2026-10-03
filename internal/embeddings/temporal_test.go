package embeddings

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemporalGraph_EmbeddingAndSimilarity(t *testing.T) {
	tg := NewTemporalGraph()

	now := time.Now().UTC()
	syndicateRoot := uuid.New()
	muleA := uuid.New()
	muleB := uuid.New()
	muleC := uuid.New()

	// High-frequency temporal burst: Root -> MuleA (t=0), MuleA -> MuleB (t+1m), MuleB -> MuleC (t+2m)
	tg.AddEdge(syndicateRoot, muleA, 500000, now)
	tg.AddEdge(muleA, muleB, 480000, now.Add(1*time.Minute))
	tg.AddEdge(muleB, muleC, 460000, now.Add(2*time.Minute))

	// Benign account with isolated payment
	benignAccount := uuid.New()
	merchant := uuid.New()
	tg.AddEdge(benignAccount, merchant, 1500, now.Add(24*time.Hour))

	embRoot := tg.ComputeEmbedding(syndicateRoot, 20, 3, 42)
	embMuleA := tg.ComputeEmbedding(muleA, 20, 3, 42)
	embBenign := tg.ComputeEmbedding(benignAccount, 20, 3, 42)

	require.Equal(t, EmbeddingDim, len(embRoot.Vector))
	assert.InDelta(t, 1.0, embRoot.Norm, 0.01)

	// Temporal neighbor (Root and MuleA) should have high cosine similarity
	simSyndicate := CosineSimilarity(embRoot, embMuleA)
	simBenign := CosineSimilarity(embRoot, embBenign)

	assert.Greater(t, simSyndicate, 0.50, "Direct temporal counterparties in syndicate should share latent vector space")
	assert.Less(t, simBenign, simSyndicate, "Unrelated benign account must have lower cosine similarity than syndicate peer")
}
