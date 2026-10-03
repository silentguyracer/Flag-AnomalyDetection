package chargeback

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDisputeMonitor_ResolutionAndDTR(t *testing.T) {
	dm := NewDisputeMonitor()
	merchant := "Global-Luxury-Store"

	// Record 1000 normal transactions
	for i := 0; i < 1000; i++ {
		dm.RecordTransaction(merchant)
	}

	repClean := dm.GetDTRReport(merchant)
	assert.Equal(t, TierClean, repClean.Tier)
	assert.Equal(t, 0.0, repClean.DisputeRatioPct)

	// Inbound Visa Verifi RDR Pre-Dispute Alert
	alert := PreDisputeAlert{
		AlertID:        "RDR-VISA-20261003-9912",
		Network:        NetworkVisa,
		TransactionID:  uuid.New(),
		ARN:            "74512938491029384758192",
		CardLast4:      "4242",
		AmountMinor:    8500, // £85.00
		Currency:       "GBP",
		ReasonCode:     "10.4 Other Fraud (Card-Absent)",
		IssuerBank:     "Barclays Bank UK",
		AlertTimestamp: time.Now().UTC(),
	}

	outcome := dm.ResolveAlert(alert, merchant)
	require.True(t, outcome.DisputeAvoided)
	assert.Equal(t, "RESOLVED_REFUNDED", outcome.Status)
	assert.Equal(t, 20.00, outcome.FeeSavedGBP)

	// Simulate 7 more disputes to reach 8 total out of 1000 (0.80% -> TierEarlyWarning)
	for i := 0; i < 7; i++ {
		dm.ResolveAlert(alert, merchant)
	}

	repWarning := dm.GetDTRReport(merchant)
	assert.Equal(t, TierEarlyWarning, repWarning.Tier)
	assert.Equal(t, 0.80, repWarning.DisputeRatioPct)
}
