package xai

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"fraud-service/internal/rules"
)

// AdverseActionNotice represents a legally compliant explainability payload
// fulfilling EU AI Act transparency and US FCRA / ECOA Adverse Action requirements.
type AdverseActionNotice struct {
	Decision              string             `json:"decision"`                // "DECLINE", "CHALLENGE_3DS", "FLAG_REVIEW"
	FinalScore            float64            `json:"final_score"`
	PrimaryFactor         string             `json:"primary_factor"`          // Single highest contributing factor
	ReasonCodes           []string           `json:"reason_codes"`            // Standardized adverse action codes
	FeatureAttributions   []FeatureWeight    `json:"feature_attributions"`    // Normalized percentage impact (sum to 100%)
	CounterfactualGuidance []string          `json:"counterfactual_guidance"` // Actionable conditions for approval
}

// FeatureWeight represents the relative contribution of a single signal to the anomaly score.
type FeatureWeight struct {
	FeatureName string  `json:"feature_name"`
	WeightPct   float64 `json:"weight_pct"` // 0..100%
	Description string  `json:"description"`
}

// Explain produces an AdverseActionNotice from a set of evaluated rule signals and score.
func Explain(decision string, finalScore float64, signals []rules.Signal) AdverseActionNotice {
	notice := AdverseActionNotice{
		Decision:   decision,
		FinalScore: math.Round(finalScore*100) / 100,
	}

	if len(signals) == 0 {
		notice.PrimaryFactor = "No adverse risk factors identified"
		notice.ReasonCodes = []string{"APPROVED_STANDARD"}
		return notice
	}

	// 1. Calculate Shapley-style raw marginal weights
	type signalWeight struct {
		rule   string
		score  float64
		reason string
	}
	var weights []signalWeight
	var totalRawScore float64

	for _, s := range signals {
		if s.Score > 0 {
			weights = append(weights, signalWeight{
				rule:   s.Rule,
				score:  s.Score,
				reason: s.Reason,
			})
			totalRawScore += s.Score
		}
	}

	// Sort signals descending by score
	sort.Slice(weights, func(i, j int) bool {
		return weights[i].score > weights[j].score
	})

	if len(weights) > 0 {
		notice.PrimaryFactor = weights[0].reason
	}

	// 2. Generate normalized attribution percentages and reason codes
	for _, w := range weights {
		pct := 0.0
		if totalRawScore > 0 {
			pct = math.Round((w.score/totalRawScore)*1000) / 10
		}
		notice.FeatureAttributions = append(notice.FeatureAttributions, FeatureWeight{
			FeatureName: w.rule,
			WeightPct:   pct,
			Description: w.reason,
		})
		notice.ReasonCodes = append(notice.ReasonCodes, mapReasonCode(w.rule))
	}

	// 3. Generate Counterfactual Guidance
	notice.CounterfactualGuidance = generateCounterfactuals(signals)

	return notice
}

func mapReasonCode(ruleName string) string {
	switch ruleName {
	case "amount_outlier":
		return "EXCEEDS_HISTORICAL_AMOUNT_PROFILE"
	case "velocity":
		return "HIGH_FREQUENCY_VELOCITY_EXCEEDED"
	case "card_testing":
		return "SUSPICIOUS_MICROCHARGE_PATTERN"
	case "new_country":
		return "UNRECOGNIZED_GEOGRAPHIC_REGION"
	case "impossible_travel":
		return "SUPERHUMAN_GEOGRAPHIC_VELOCITY"
	case "new_merchant_high_value":
		return "UNVERIFIED_MERCHANT_DISPROPORTIONATE_SPEND"
	case "graph_syndicate_ring":
		return "DEVICE_FINGERPRINT_LINKED_TO_SUSPICIOUS_CLUSTER"
	default:
		return strings.ToUpper("ANOMALY_" + ruleName)
	}
}

func generateCounterfactuals(signals []rules.Signal) []string {
	var cf []string
	for _, s := range signals {
		switch s.Rule {
		case "amount_outlier":
			cf = append(cf, "Splitting payment below typical spend threshold or supplying verified source of wealth.")
		case "impossible_travel":
			cf = append(cf, "Verifying device location services or authenticating via Strong Customer Authentication (SCA/3DS).")
		case "new_country":
			cf = append(cf, "Pre-authorizing international travel itinerary in banking app.")
		case "velocity":
			cf = append(cf, "Waiting 60 seconds before initiating subsequent transactions.")
		case "card_testing":
			cf = append(cf, "Completing chip & PIN or biometric 3DS authentication for higher-value purchase.")
		}
	}

	if len(cf) == 0 {
		cf = append(cf, "Authenticate through biometric 3DS challenge.")
	}
	return cf
}

// FormatAdverseNotice generates a human-readable regulatory compliance memorandum.
func (n *AdverseActionNotice) FormatMemorandum() string {
	var sb strings.Builder
	sb.WriteString("=======================================================\n")
	sb.WriteString("     REGULATORY ADVERSE ACTION EXPLAINABILITY NOTICE   \n")
	sb.WriteString("=======================================================\n")
	sb.WriteString(fmt.Sprintf("Decision:          [%s]\n", n.Decision))
	sb.WriteString(fmt.Sprintf("Composite Score:   %.2f / 1.00\n", n.FinalScore))
	sb.WriteString(fmt.Sprintf("Primary Factor:    %s\n", n.PrimaryFactor))
	sb.WriteString(fmt.Sprintf("Reason Codes:      %s\n\n", strings.Join(n.ReasonCodes, ", ")))

	sb.WriteString("Feature Attribution Breakdown (Shapley Proxy):\n")
	for _, attr := range n.FeatureAttributions {
		sb.WriteString(fmt.Sprintf("  • %-25s %5.1f%% : %s\n", attr.FeatureName, attr.WeightPct, attr.Description))
	}

	sb.WriteString("\nActionable Counterfactual Requirements (What alters decision):\n")
	for _, c := range n.CounterfactualGuidance {
		sb.WriteString(fmt.Sprintf("  -> %s\n", c))
	}
	sb.WriteString("=======================================================\n")
	return sb.String()
}
