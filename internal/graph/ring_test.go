package graph

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryGraph_SyndicateAndMuleRing(t *testing.T) {
	g := NewMemoryGraph()
	now := time.Now().UTC()

	devID := "fingerprint_syndicate_x88"
	acct1 := uuid.New()
	acct2 := uuid.New()
	acct3 := uuid.New()
	acct4 := uuid.New()

	// 1. Observe 1st and 2nd accounts: no syndicate yet
	g.Observe(acct1, devID, now)
	g.Observe(acct2, devID, now.Add(1*time.Minute))

	anomaly := g.AnalyzeAccount(acct1, devID)
	assert.Nil(t, anomaly, "2 accounts on same device should not trigger syndicate")

	// 2. Observe 3rd account on same device: triggers shared_device_syndicate
	g.Observe(acct3, devID, now.Add(2*time.Minute))
	anomaly = g.AnalyzeAccount(acct3, devID)
	require.NotNil(t, anomaly, "3 accounts on same device must trigger syndicate anomaly")
	assert.Equal(t, "shared_device_syndicate", anomaly.Type)
	assert.Equal(t, 3, anomaly.ClusterSize)
	assert.True(t, anomaly.Score >= 0.70)
	assert.Equal(t, devID, anomaly.SharedEntity)

	// 3. Add 4th account: cluster size increases and score escalates
	g.Observe(acct4, devID, now.Add(3*time.Minute))
	anomaly4 := g.AnalyzeAccount(acct4, devID)
	require.NotNil(t, anomaly4)
	assert.Equal(t, 4, anomaly4.ClusterSize)
	assert.True(t, anomaly4.Score > anomaly.Score)

	// Verify conversion to rules.Signal
	sig := anomaly4.AsRuleSignal()
	require.NotNil(t, sig)
	assert.Equal(t, "graph_syndicate_ring", sig.Rule)
	assert.True(t, sig.Score >= 0.75)

	// 4. Test Mule Fan-Out Smurfing (source account distributes to 5 mules)
	hubAcct := uuid.New()
	for i := 0; i < 5; i++ {
		muleAcct := uuid.New()
		g.RecordTransfer(hubAcct, muleAcct, 4500)
	}

	muleAnomaly := g.AnalyzeAccount(hubAcct, "")
	require.NotNil(t, muleAnomaly)
	assert.Equal(t, "mule_fan_out", muleAnomaly.Type)
	assert.Equal(t, 5, muleAnomaly.ClusterSize)
	assert.True(t, muleAnomaly.Score >= 0.80)
}
