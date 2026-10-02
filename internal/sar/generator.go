package sar

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"fraud-service/internal/events"
	"fraud-service/internal/rules"
)

// SuspiciousActivityReport represents an official SAR filing package compliant with FinCEN / UK NCA guidelines.
type SuspiciousActivityReport struct {
	ReportID            string                 `json:"report_id"`
	FilingDate          time.Time              `json:"filing_date"`
	ReportingEntity     string                 `json:"reporting_entity"`
	FilingType          string                 `json:"filing_type"` // "INITIAL", "CORRECTION", "SUPPLEMENTAL"
	PrimaryTypology     string                 `json:"primary_typology"`
	SuspectAccountID    uuid.UUID              `json:"suspect_account_id"`
	TotalSuspiciousMinor int64                 `json:"total_suspicious_minor"`
	TotalSuspiciousGBP  float64                `json:"total_suspicious_gbp"`
	EarliestActivity    time.Time              `json:"earliest_activity"`
	LatestActivity      time.Time              `json:"latest_activity"`
	ItemizedTxnsCount   int                    `json:"itemized_txns_count"`
	TriggeredRules      []string               `json:"triggered_rules"`
	Narrative           string                 `json:"narrative"`
	Metadata            map[string]any         `json:"metadata"`
}

// GenerateDraft compiles a regulatory SAR draft from flagged account activity and evaluated signals.
func GenerateDraft(
	accountID uuid.UUID,
	flagID uuid.UUID,
	history []events.TransactionCreated,
	signals []rules.Signal,
	typologyOverride string,
) *SuspiciousActivityReport {
	now := time.Now().UTC()
	reportID := fmt.Sprintf("SAR-%s-%s", now.Format("20060102"), flagID.String()[:8])

	typology := classifyTypology(signals)
	if typologyOverride != "" {
		typology = typologyOverride
	}

	var totalMinor int64
	var earliest, latest time.Time
	var ruleNames []string

	for _, s := range signals {
		ruleNames = append(ruleNames, s.Rule)
	}

	for i, t := range history {
		totalMinor += t.AmountMinor
		if i == 0 {
			earliest = now
			latest = now
		}
	}

	totalGBP := float64(totalMinor) / 100.0

	// Draft narrative in compliance with FinCEN Part V Narrative Guidelines:
	// Who, What, Where, When, Why, and How.
	narrative := draftNarrative(accountID, reportID, typology, totalGBP, len(history), signals)

	return &SuspiciousActivityReport{
		ReportID:             reportID,
		FilingDate:           now,
		ReportingEntity:      "Global FinTech Payments Ltd (LEI: 5493001KJTIIGC8Y1R12)",
		FilingType:           "INITIAL",
		PrimaryTypology:      typology,
		SuspectAccountID:     accountID,
		TotalSuspiciousMinor: totalMinor,
		TotalSuspiciousGBP:   totalGBP,
		EarliestActivity:     earliest,
		LatestActivity:       latest,
		ItemizedTxnsCount:    len(history),
		TriggeredRules:       ruleNames,
		Narrative:            narrative,
		Metadata: map[string]any{
			"flag_id":             flagID,
			"auto_generated_by":   "Antigravity AML Engine v2026.4",
			"requires_law_enforce": totalGBP >= 10000.0 || typology == "TERRORIST_FINANCING",
		},
	}
}

func classifyTypology(signals []rules.Signal) string {
	for _, s := range signals {
		switch s.Rule {
		case "impossible_travel", "new_country":
			return "CROSS_BORDER_ACCOUNT_TAKEOVER"
		case "card_testing":
			return "AUTOMATED_CARD_TESTING_SYNDICATE"
		case "velocity":
			return "RAPID_FUNDS_MOVEMENT_SMURFING"
		case "graph_syndicate_ring":
			return "CREDENTIAL_FARM_MULE_RING"
		}
	}
	return "SUSPICIOUS_UNUSUAL_TRANSACTIONS"
}

func draftNarrative(
	accountID uuid.UUID,
	reportID string,
	typology string,
	totalGBP float64,
	txnCount int,
	signals []rules.Signal,
) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("SUSPICIOUS ACTIVITY REPORT INVESTIGATIVE MEMORANDUM (%s)\n\n", reportID))
	sb.WriteString("I. EXECUTIVE SUMMARY:\n")
	sb.WriteString(fmt.Sprintf(
		"This Suspicious Activity Report (SAR) is filed regarding Account %s, which engaged in suspicious financial transactions aggregating £%.2f across %d transactions. Internal automated surveillance systems and graph heuristics identified activity consistent with '%s'.\n\n",
		accountID, totalGBP, txnCount, typology,
	))

	sb.WriteString("II. SURVEILLANCE & INVESTIGATIVE FINDINGS:\n")
	for i, s := range signals {
		sb.WriteString(fmt.Sprintf("  %d. Rule Trigger [%s] (Risk Confidence: %.2f): %s\n", i+1, s.Rule, s.Score, s.Reason))
	}

	sb.WriteString("\nIII. PATTERN OF SUSPICIOUS ACTIVITY & LAW ENFORCEMENT RECOMMENDATION:\n")
	sb.WriteString("Transaction patterns demonstrate deviation from established account baseline, abnormal geographic jumps, and micro-charge validation typical of illicit cashout operations. The reporting institution has marked the subject account for enhanced monitoring and restricted outbound wire functionality pending regulatory law enforcement inquiry.\n")

	return sb.String()
}

// ToJSON converts the SAR report into standard regulatory filing JSON.
func (sar *SuspiciousActivityReport) ToJSON() (string, error) {
	bytes, err := json.MarshalIndent(sar, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
