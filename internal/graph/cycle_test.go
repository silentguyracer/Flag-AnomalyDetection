package graph

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectCycles_CircularMuleLoop(t *testing.T) {
	g := NewMemoryGraph()

	acctA := uuid.New()
	acctB := uuid.New()
	acctC := uuid.New()
	acctD := uuid.New()

	// Circular 3-hop loop: A -> B -> C -> A
	g.RecordTransfer(acctA, acctB, 50000)
	g.RecordTransfer(acctB, acctC, 49000)
	g.RecordTransfer(acctC, acctA, 48000)

	// Benign linear transfer: D -> A (no loop)
	g.RecordTransfer(acctD, acctA, 10000)

	rings := g.DetectCycles(4)
	require.NotEmpty(t, rings, "should detect circular laundering ring")
	assert.Equal(t, 1, len(rings))
	assert.Equal(t, 3, rings[0].HopCount)
	assert.GreaterOrEqual(t, rings[0].Confidence, 0.85)

	// Verify members in the cycle
	ringMembers := make(map[uuid.UUID]bool)
	for _, acct := range rings[0].Accounts {
		ringMembers[acct] = true
	}
	assert.True(t, ringMembers[acctA])
	assert.True(t, ringMembers[acctB])
	assert.True(t, ringMembers[acctC])
	assert.False(t, ringMembers[acctD], "acctD should not be in the circular ring")
}

func TestRiskDiffusion_Contamination(t *testing.T) {
	g := NewMemoryGraph()

	seedFraud := uuid.New()
	mule1 := uuid.New()
	mule2 := uuid.New()
	innocentMerchant := uuid.New()

	// Seed sends 90% of funds to mule1, 10% to innocentMerchant
	g.RecordTransfer(seedFraud, mule1, 90000)
	g.RecordTransfer(seedFraud, innocentMerchant, 10000)

	// Mule1 sends all funds to mule2
	g.RecordTransfer(mule1, mule2, 85000)

	contaminated := g.GetContaminatedAccounts([]uuid.UUID{seedFraud}, 0.20)
	require.NotEmpty(t, contaminated)

	scores := make(map[uuid.UUID]float64)
	for _, c := range contaminated {
		scores[c.AccountID] = c.RiskScore
	}

	assert.Contains(t, scores, seedFraud)
	assert.Contains(t, scores, mule1)
	assert.Contains(t, scores, mule2)

	// Mule1 should have higher diffused risk than distant or low-volume innocentMerchant
	assert.Greater(t, scores[mule1], scores[innocentMerchant])
}
