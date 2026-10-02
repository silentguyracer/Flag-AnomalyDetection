package rules

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// VelocityRule flags rapid bursts of payments within short time windows.
type VelocityRule struct {
	Threshold           int   // e.g. 5 payments in 1 minute
	Sum1hThresholdMinor int64 // e.g. £1,000 in 1 hour
}

func (r VelocityRule) Name() string { return "velocity" }

func (r VelocityRule) Evaluate(f *Features) *Signal {
	if f.Count1m >= r.Threshold {
		score := math.Min(1.0, 0.4+0.1*float64(f.Count1m-r.Threshold))
		if r.Sum1hThresholdMinor > 0 && f.Sum1h >= r.Sum1hThresholdMinor {
			score = math.Min(1.0, score+0.15)
		}
		return &Signal{
			Rule:  r.Name(),
			Score: score,
			Reason: fmt.Sprintf("%d payments in the last minute (threshold %d)",
				f.Count1m, r.Threshold),
			Evidence: map[string]any{
				"count_1m":  f.Count1m,
				"sum_1h":    f.Sum1h,
				"threshold": r.Threshold,
			},
		}
	}

	if r.Sum1hThresholdMinor > 0 && f.Sum1h >= r.Sum1hThresholdMinor {
		return &Signal{
			Rule:  r.Name(),
			Score: 0.65,
			Reason: fmt.Sprintf("High hourly volume: %d minor in 1 hour (threshold %d)",
				f.Sum1h, r.Sum1hThresholdMinor),
			Evidence: map[string]any{
				"count_1m":  f.Count1m,
				"sum_1h":    f.Sum1h,
				"threshold": r.Sum1hThresholdMinor,
			},
		}
	}

	return nil
}

// CardTestingRule detects micro-charges followed by a large transaction.
type CardTestingRule struct {
	SmallUnderMinor int64 // e.g. 200 (£2.00)
	MinSmall        int   // e.g. 3 small transactions
	LargeOverMinor  int64 // e.g. 5000 (£50.00)
}

func (r CardTestingRule) Name() string { return "card_testing" }

func (r CardTestingRule) Evaluate(f *Features) *Signal {
	if f.Txn.AmountMinor >= r.LargeOverMinor && f.RecentSmallCount10m >= r.MinSmall {
		return &Signal{
			Rule:  r.Name(),
			Score: 0.85,
			Reason: fmt.Sprintf("Card testing pattern: %d charges under £%.2f followed by £%.2f charge within 10m",
				f.RecentSmallCount10m,
				float64(r.SmallUnderMinor)/100.0,
				float64(f.Txn.AmountMinor)/100.0,
			),
			Evidence: map[string]any{
				"small_count_10m": f.RecentSmallCount10m,
				"amount_minor":    f.Txn.AmountMinor,
				"window":          "10m",
			},
		}
	}
	return nil
}

// AmountOutlierRule detects statistically anomalous transaction amounts using log-scale Welford baselines.
type AmountOutlierRule struct {
	ZThreshold     float64 // e.g. 3.5 standard deviations
	MinHistory     int64   // e.g. 10 transactions required (cold start mitigation)
	MinAmountMinor int64   // e.g. 5000 (£50) absolute floor to avoid £3 -> £12 flagging
}

func (r AmountOutlierRule) Name() string { return "amount_outlier" }

func (r AmountOutlierRule) Evaluate(f *Features) *Signal {
	// Cold start: do not evaluate on insufficient baseline
	if f.ProfileN < r.MinHistory {
		return nil
	}

	// Floor check: ignore small absolute sums
	if f.Txn.AmountMinor < r.MinAmountMinor {
		return nil
	}

	x := math.Log(float64(f.Txn.AmountMinor))
	std := math.Max(f.StdLog, 0.3) // Floor prevents divide-by-zero for habitual spenders
	z := (x - f.MeanLog) / std

	if z > r.ZThreshold {
		score := math.Min(0.98, 0.50+0.12*(z-r.ZThreshold))
		return &Signal{
			Rule:  r.Name(),
			Score: score,
			Reason: fmt.Sprintf("Amount £%.2f is a %.2f-sigma outlier (baseline mean_log=%.2f, std=%.2f, n=%d)",
				float64(f.Txn.AmountMinor)/100.0,
				z, f.MeanLog, f.StdLog, f.ProfileN,
			),
			Evidence: map[string]any{
				"z_score":      z,
				"amount_minor": f.Txn.AmountMinor,
				"mean_log":     f.MeanLog,
				"std_log":      f.StdLog,
				"profile_n":    f.ProfileN,
			},
		}
	}

	return nil
}

// NewCountryRule flags transactions in countries not previously seen in account history.
type NewCountryRule struct {
	MinAccountAgeDays int // e.g. 14 days (cold start mitigation)
}

func (r NewCountryRule) Name() string { return "new_country" }

func (r NewCountryRule) Evaluate(f *Features) *Signal {
	// Missing country in schema evolution is skipped, never an error
	country := strings.ToUpper(strings.TrimSpace(f.Txn.Country))
	if country == "" {
		return nil
	}

	// Cold start: allow new accounts grace period to establish profile
	minAge := time.Duration(r.MinAccountAgeDays) * 24 * time.Hour
	if f.AccountAge < minAge {
		return nil
	}

	if f.KnownCountries != nil {
		if _, exists := f.KnownCountries[country]; exists {
			return nil
		}
	}

	// Base confidence for new country
	score := 0.45

	// Boost score if channel is online without recognized device
	if strings.ToLower(f.Txn.Channel) == "online" && f.Txn.DeviceID == "" {
		score += 0.25
	}

	// Boost score for high value payment
	if f.Txn.AmountMinor >= 20000 { // >= £200
		score += 0.15
	}
	score = math.Min(0.95, score)

	return &Signal{
		Rule:  r.Name(),
		Score: score,
		Reason: fmt.Sprintf("First-ever payment in %s (account age: %d days, known countries: %d)",
			country, int(f.AccountAge.Hours()/24), len(f.KnownCountries)),
		Evidence: map[string]any{
			"new_country":     country,
			"known_countries": len(f.KnownCountries),
			"account_age_hrs": f.AccountAge.Hours(),
			"channel":         f.Txn.Channel,
		},
	}
}

// ImpossibleTravelRule flags transactions across geographic locations at impossible velocities.
type ImpossibleTravelRule struct {
	Window      time.Duration // e.g. 2 hours
	MaxSpeedKmh float64       // e.g. 850 km/h (cruising jet speed)
}

func (r ImpossibleTravelRule) Name() string { return "impossible_travel" }

func (r ImpossibleTravelRule) Evaluate(f *Features) *Signal {
	currentCountry := strings.ToUpper(strings.TrimSpace(f.Txn.Country))
	lastCountry := strings.ToUpper(strings.TrimSpace(f.LastCountry))

	// Missing either country skips evaluation
	if currentCountry == "" || lastCountry == "" || currentCountry == lastCountry {
		return nil
	}

	if f.LastTxnAt.IsZero() {
		return nil
	}

	gap := f.At.Sub(f.LastTxnAt)
	if gap < 0 {
		// Handled gracefully in out-of-order replays
		return nil
	}

	distKm := DistanceKm(lastCountry, currentCountry)
	if distKm <= 50.0 { // Border vicinity or negligible distance
		return nil
	}

	gapHours := gap.Hours()
	if gapHours <= 0.001 {
		// Instantaneous jump across countries
		return &Signal{
			Rule:  r.Name(),
			Score: 0.98,
			Reason: fmt.Sprintf("Simultaneous transactions in %s and %s (gap: %s)",
				lastCountry, currentCountry, gap.Round(time.Second)),
			Evidence: map[string]any{
				"last_country":    lastCountry,
				"current_country": currentCountry,
				"distance_km":     distKm,
				"gap":             gap.String(),
			},
		}
	}

	speedKmh := distKm / gapHours
	if speedKmh > r.MaxSpeedKmh || (gap < r.Window && distKm > 1000.0) {
		score := math.Min(0.98, 0.70+0.25*math.Min(1.0, speedKmh/(r.MaxSpeedKmh*2)))
		return &Signal{
			Rule:  r.Name(),
			Score: score,
			Reason: fmt.Sprintf("Impossible travel: from %s to %s (%.0f km in %s, ~%.0f km/h)",
				lastCountry, currentCountry, distKm, gap.Round(time.Minute), speedKmh),
			Evidence: map[string]any{
				"last_country":    lastCountry,
				"current_country": currentCountry,
				"distance_km":     distKm,
				"gap_minutes":     gap.Minutes(),
				"speed_kmh":       speedKmh,
			},
		}
	}

	return nil
}

// NewMerchantHighValueRule flags large payments at previously unseen merchants.
type NewMerchantHighValueRule struct {
	MinMultiplier  float64 // e.g. 5x typical spend
	MinAmountMinor int64   // e.g. £50
}

func (r NewMerchantHighValueRule) Name() string { return "new_merchant_high_value" }

func (r NewMerchantHighValueRule) Evaluate(f *Features) *Signal {
	if f.MerchantSeen {
		return nil
	}
	if f.ProfileN < 5 { // cold start check
		return nil
	}
	if f.Txn.AmountMinor < r.MinAmountMinor {
		return nil
	}

	typicalSpend := math.Exp(f.MeanLog)
	if typicalSpend <= 0 {
		return nil
	}

	ratio := float64(f.Txn.AmountMinor) / typicalSpend
	if ratio >= r.MinMultiplier {
		return &Signal{
			Rule:  r.Name(),
			Score: 0.40, // Weak on its own, combines with others via noisy-OR
			Reason: fmt.Sprintf("First payment at %q is %.1fx typical spend (£%.2f vs avg £%.2f)",
				f.Txn.Merchant, ratio, float64(f.Txn.AmountMinor)/100.0, typicalSpend/100.0),
			Evidence: map[string]any{
				"merchant":      f.Txn.Merchant,
				"amount_minor":  f.Txn.AmountMinor,
				"typical_spend": typicalSpend,
				"ratio":         ratio,
			},
		}
	}

	return nil
}
