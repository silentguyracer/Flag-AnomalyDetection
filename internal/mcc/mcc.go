package mcc

import (
	"math"
	"strings"
)

// RiskTier defines the baseline risk category for a Merchant Category Code.
type RiskTier string

const (
	TierCritical RiskTier = "CRITICAL" // e.g. Crypto, Wire Transfer, Gambling
	TierElevated RiskTier = "ELEVATED" // e.g. Luxury jewelry, electronics, pawn shops
	TierStandard RiskTier = "STANDARD" // e.g. General retail, digital goods
	TierLow      RiskTier = "LOW"      // e.g. Groceries, public transit, utilities
)

// MCCProfile holds regulatory risk parameters for an ISO 18245 merchant category.
type MCCProfile struct {
	Code              string   `json:"code"`
	CategoryName      string   `json:"category_name"`
	Tier              RiskTier `json:"tier"`
	RiskMultiplier    float64  `json:"risk_multiplier"`    // Multiplies rule scores (e.g. 1.8x for Crypto)
	MaxVelocityPerMin int      `json:"max_velocity_per_min"` // Allowed burst count before flagging
	Requires3DSFloor  int64    `json:"requires_3ds_floor"` // Minor amount threshold requiring 3DS challenge
}

// Registry maps known MCC codes to risk profiles.
var Registry = map[string]MCCProfile{
	// Crypto & Quasi-Cash
	"6051": {
		Code:              "6051",
		CategoryName:      "Quasi-Cash / Cryptocurrency Exchanges",
		Tier:              TierCritical,
		RiskMultiplier:    1.80,
		MaxVelocityPerMin: 2,
		Requires3DSFloor:  5000, // £50.00
	},
	"4829": {
		Code:              "4829",
		CategoryName:      "Money Orders / Wire Transfer",
		Tier:              TierCritical,
		RiskMultiplier:    1.75,
		MaxVelocityPerMin: 2,
		Requires3DSFloor:  5000,
	},
	// Gambling & Gaming
	"7995": {
		Code:              "7995",
		CategoryName:      "Betting, Casino & Gambling Transactions",
		Tier:              TierCritical,
		RiskMultiplier:    1.90,
		MaxVelocityPerMin: 2,
		Requires3DSFloor:  3000, // £30.00
	},
	// High-Risk Goods
	"5944": {
		Code:              "5944",
		CategoryName:      "Jewelry, Watches & Precious Stones",
		Tier:              TierElevated,
		RiskMultiplier:    1.40,
		MaxVelocityPerMin: 3,
		Requires3DSFloor:  15000, // £150.00
	},
	"5732": {
		Code:              "5732",
		CategoryName:      "Consumer Electronics",
		Tier:              TierElevated,
		RiskMultiplier:    1.25,
		MaxVelocityPerMin: 4,
		Requires3DSFloor:  20000,
	},
	// Standard Retail
	"5311": {
		Code:              "5311",
		CategoryName:      "Department Stores",
		Tier:              TierStandard,
		RiskMultiplier:    1.00,
		MaxVelocityPerMin: 5,
		Requires3DSFloor:  25000,
	},
	// Low-Risk Everyday Spend
	"5411": {
		Code:              "5411",
		CategoryName:      "Grocery Stores & Supermarkets",
		Tier:              TierLow,
		RiskMultiplier:    0.80,
		MaxVelocityPerMin: 8,
		Requires3DSFloor:  50000,
	},
	"4111": {
		Code:              "4111",
		CategoryName:      "Local & Suburban Commuter Transit",
		Tier:              TierLow,
		RiskMultiplier:    0.70,
		MaxVelocityPerMin: 10,
		Requires3DSFloor:  100000,
	},
}

// DefaultProfile returns standard baseline parameters for unmapped MCCs.
var DefaultProfile = MCCProfile{
	Code:              "0000",
	CategoryName:      "General Retail / Unclassified",
	Tier:              TierStandard,
	RiskMultiplier:    1.00,
	MaxVelocityPerMin: 5,
	Requires3DSFloor:  25000,
}

// GetProfile retrieves the MCC profile, trimming leading/trailing spaces.
func GetProfile(mccCode string) MCCProfile {
	cleaned := strings.TrimSpace(mccCode)
	if p, ok := Registry[cleaned]; ok {
		return p
	}
	return DefaultProfile
}

// AdjustScore applies the MCC risk multiplier to raw rule score, capped at 1.0.
func AdjustScore(rawScore float64, mccCode string) (float64, MCCProfile) {
	p := GetProfile(mccCode)
	adjusted := rawScore * p.RiskMultiplier
	if adjusted > 1.0 {
		adjusted = 1.0
	}
	return math.Round(adjusted*10000) / 10000, p
}
