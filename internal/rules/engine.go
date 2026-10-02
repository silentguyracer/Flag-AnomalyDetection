package rules

import (
	"time"
)

// Engine evaluates transactions against registered rules based on configuration.
type Engine struct {
	cfg     *Config
	rules   []Rule
	version string
}

// NewEngine constructs an evaluation engine with rules configured per Config.
func NewEngine(cfg *Config) *Engine {
	e := &Engine{
		cfg:     cfg,
		version: cfg.Version,
	}

	for name, s := range cfg.Rules {
		if !s.Enabled {
			continue
		}

		switch name {
		case "velocity":
			th := s.Threshold
			if th == 0 {
				th = 5
			}
			e.rules = append(e.rules, VelocityRule{
				Threshold:           th,
				Sum1hThresholdMinor: s.Sum1hThresholdMinor,
			})

		case "card_testing":
			smallUnder := s.SmallUnderMinor
			if smallUnder == 0 {
				smallUnder = 200 // £2.00
			}
			minSmall := s.MinSmall
			if minSmall == 0 {
				minSmall = 3
			}
			largeOver := s.LargeOverMinor
			if largeOver == 0 {
				largeOver = 5000 // £50.00
			}
			e.rules = append(e.rules, CardTestingRule{
				SmallUnderMinor: smallUnder,
				MinSmall:        minSmall,
				LargeOverMinor:  largeOver,
			})

		case "amount_outlier":
			z := s.Z
			if z == 0 {
				z = 3.5
			}
			minHist := s.MinHistory
			if minHist == 0 {
				minHist = 10
			}
			minAmt := s.MinAmountMinor
			if minAmt == 0 {
				minAmt = 5000
			}
			e.rules = append(e.rules, AmountOutlierRule{
				ZThreshold:     z,
				MinHistory:     minHist,
				MinAmountMinor: minAmt,
			})

		case "new_country":
			ageDays := s.MinAccountAgeDays
			if ageDays == 0 {
				ageDays = 14
			}
			e.rules = append(e.rules, NewCountryRule{
				MinAccountAgeDays: ageDays,
			})

		case "impossible_travel":
			win := s.Window
			if win == 0 {
				win = 2 * time.Hour
			}
			maxSpeed := s.MaxSpeedKmh
			if maxSpeed == 0 {
				maxSpeed = 850.0
			}
			e.rules = append(e.rules, ImpossibleTravelRule{
				Window:      win,
				MaxSpeedKmh: maxSpeed,
			})

		case "new_merchant_high_value":
			mult := s.MinMultiplier
			if mult == 0 {
				mult = 5.0
			}
			minAmt := s.MinAmountMinor
			if minAmt == 0 {
				minAmt = 5000
			}
			e.rules = append(e.rules, NewMerchantHighValueRule{
				MinMultiplier:  mult,
				MinAmountMinor: minAmt,
			})
		}
	}

	return e
}

// Version returns the configuration version string.
func (e *Engine) Version() string {
	return e.version
}

// Config returns the active engine configuration.
func (e *Engine) Config() *Config {
	return e.cfg
}

// FlagThreshold returns the minimum combined score required to trigger a fraud flag.
func (e *Engine) FlagThreshold() float64 {
	return e.cfg.FlagThreshold
}

// Run executes all active rules against the given features and returns triggered signals.
func (e *Engine) Run(f *Features) []Signal {
	var signals []Signal
	for _, r := range e.rules {
		if sig := r.Evaluate(f); sig != nil {
			signals = append(signals, *sig)
		}
	}
	return signals
}
