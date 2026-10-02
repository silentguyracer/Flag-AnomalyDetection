package rules

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config defines the YAML schema for rules engine parameters.
type Config struct {
	Version       string                 `yaml:"version"`
	FlagThreshold float64                `yaml:"flag_threshold"`
	Rules         map[string]RuleSetting `yaml:"rules"`
}

// RuleSetting captures individual rule toggles and arbitrary numeric/duration parameters.
type RuleSetting struct {
	Enabled             bool          `yaml:"enabled"`
	Threshold           int           `yaml:"threshold,omitempty"`
	Sum1hThresholdMinor int64         `yaml:"sum_1h_threshold_minor,omitempty"`
	SmallUnderMinor     int64         `yaml:"small_under_minor,omitempty"`
	MinSmall            int           `yaml:"min_small,omitempty"`
	LargeOverMinor      int64         `yaml:"large_over_minor,omitempty"`
	Z                   float64       `yaml:"z,omitempty"`
	MinHistory          int64         `yaml:"min_history,omitempty"`
	MinAmountMinor      int64         `yaml:"min_amount_minor,omitempty"`
	MinAccountAgeDays   int           `yaml:"min_account_age_days,omitempty"`
	Window              time.Duration `yaml:"window,omitempty"`
	MaxSpeedKmh         float64       `yaml:"max_speed_kmh,omitempty"`
	MinMultiplier       float64       `yaml:"min_multiplier,omitempty"`
}

// LoadConfig parses a YAML configuration file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read rules config file %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal rules config: %w", err)
	}

	if cfg.FlagThreshold <= 0 {
		cfg.FlagThreshold = 0.50
	}
	if cfg.Version == "" {
		cfg.Version = "default"
	}

	return &cfg, nil
}
