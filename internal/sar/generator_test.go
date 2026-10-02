package sar

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fraud-service/internal/events"
	"fraud-service/internal/rules"
)

func TestGenerateDraft_SAR(t *testing.T) {
	acctID := uuid.New()
	flagID := uuid.New()

	history := []events.TransactionCreated{
		{TransactionID: uuid.New(), AccountID: acctID, AmountMinor: 500000},  // £5000
		{TransactionID: uuid.New(), AccountID: acctID, AmountMinor: 650000},  // £6500
	}

	signals := []rules.Signal{
		{
			Rule:   "impossible_travel",
			Score:  0.95,
			Reason: "Impossible travel from GB to JP (9200 km in 15m)",
		},
		{
			Rule:   "amount_outlier",
			Score:  0.98,
			Reason: "Amount £6500.00 is a 14-sigma outlier",
		},
	}

	draft := GenerateDraft(acctID, flagID, history, signals, "")
	require.NotNil(t, draft)

	assert.Equal(t, "CROSS_BORDER_ACCOUNT_TAKEOVER", draft.PrimaryTypology)
	assert.Equal(t, 11500.0, draft.TotalSuspiciousGBP)
	assert.Equal(t, 2, draft.ItemizedTxnsCount)
	assert.Contains(t, draft.Narrative, "SUSPICIOUS ACTIVITY REPORT")
	assert.Contains(t, draft.Narrative, "Impossible travel")

	jsonStr, err := draft.ToJSON()
	require.NoError(t, err)
	assert.Contains(t, jsonStr, draft.ReportID)
}
