package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"fraud-service/internal/events"
	"fraud-service/internal/features"
	"fraud-service/internal/rules"
)

// FlagNamespace is the fixed UUID namespace used to deterministically generate flag IDs.
var FlagNamespace = uuid.MustParse("e0f878f4-2f08-4e5c-9c94-bce38e788bc1")

// DeterministicFlagID computes a reproducible UUID based on transaction ID and rules version.
// A reprocessed event then emits the exact same event_id, guaranteeing downstream deduplication.
func DeterministicFlagID(transactionID uuid.UUID, rulesVersion string) uuid.UUID {
	data := fmt.Sprintf("%s:%s", transactionID.String(), rulesVersion)
	return uuid.NewSHA1(FlagNamespace, []byte(data))
}

// Decision represents the combined outcome of rules and ML scoring.
type Decision struct {
	FlagID       uuid.UUID
	Score        float64
	Severity     string
	ModelVersion string
}

// Store defines the database persistence interface for fraud detection transactions.
type Store interface {
	Record(ctx context.Context, tx pgx.Tx, txn events.TransactionCreated, f *rules.Features, signals []rules.Signal, ruleScore float64, mlScore *float64, label string) error
	Flag(ctx context.Context, tx pgx.Tx, txn events.TransactionCreated, signals []rules.Signal, final Decision, rulesVersion string, occurredAt time.Time) error
}

// PGStore implements Store backed by PostgreSQL.
type PGStore struct{}

// NewPGStore constructs a PostgreSQL store instance.
func NewPGStore() *PGStore {
	return &PGStore{}
}

// Record persists the transaction history, updates Welford profile baseline, and logs features.
// Evaluated BEFORE recording so that this transaction is never part of its own baseline.
func (s *PGStore) Record(ctx context.Context, tx pgx.Tx, txn events.TransactionCreated, f *rules.Features, signals []rules.Signal, ruleScore float64, mlScore *float64, label string) error {
	// 1. Insert into txn_history
	_, err := tx.Exec(ctx, `
		INSERT INTO txn_history (
			transaction_id, account_id, amount_minor, country, merchant, channel, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (transaction_id) DO NOTHING
	`, txn.TransactionID, txn.AccountID, txn.AmountMinor, txn.Country, txn.Merchant, txn.Channel, f.At)
	if err != nil {
		return fmt.Errorf("failed to insert txn_history: %w", err)
	}

	// 2. Compute updated Welford baseline statistics
	w := features.WelfordProfile{
		N:       f.ProfileN,
		MeanLog: f.MeanLog,
		M2Log:   f.StdLog * f.StdLog * float64(mathMax(f.ProfileN-1, 1)),
	}
	w.Update(txn.AmountMinor)

	// Update known countries dictionary
	known := make(map[string]string)
	for c, t := range f.KnownCountries {
		known[c] = t.Format(time.RFC3339)
	}
	if txn.Country != "" {
		known[txn.Country] = f.At.Format(time.RFC3339)
	}
	countriesJSON, err := json.Marshal(known)
	if err != nil {
		return fmt.Errorf("failed to marshal countries: %w", err)
	}

	lastCountry := txn.Country
	if lastCountry == "" {
		lastCountry = f.LastCountry
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO account_profile (
			account_id, n, mean_log, m2_log, countries, last_country, last_txn_at, first_seen_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		ON CONFLICT (account_id) DO UPDATE SET
			n = EXCLUDED.n,
			mean_log = EXCLUDED.mean_log,
			m2_log = EXCLUDED.m2_log,
			countries = EXCLUDED.countries,
			last_country = CASE WHEN EXCLUDED.last_country <> '' THEN EXCLUDED.last_country ELSE account_profile.last_country END,
			last_txn_at = EXCLUDED.last_txn_at,
			first_seen_at = LEAST(account_profile.first_seen_at, EXCLUDED.first_seen_at)
	`, txn.AccountID, w.N, w.MeanLog, w.M2Log, countriesJSON, lastCountry, f.At)
	if err != nil {
		return fmt.Errorf("failed to update account_profile: %w", err)
	}

	// 3. Insert feature_log (the training set logged at serving time)
	featuresJSON, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("failed to marshal features: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO feature_log (
			transaction_id, features, rule_score, ml_score, label
		) VALUES ($1, $2, $3, $4, NULLIF($5, ''))
		ON CONFLICT (transaction_id) DO UPDATE SET
			features = EXCLUDED.features,
			rule_score = EXCLUDED.rule_score,
			ml_score = EXCLUDED.ml_score,
			label = COALESCE(feature_log.label, EXCLUDED.label)
	`, txn.TransactionID, featuresJSON, ruleScore, mlScore, label)
	if err != nil {
		return fmt.Errorf("failed to insert feature_log: %w", err)
	}

	return nil
}

// Flag records an entry in the flags table and queues an event in the outbox.
func (s *PGStore) Flag(ctx context.Context, tx pgx.Tx, txn events.TransactionCreated, signals []rules.Signal, final Decision, rulesVersion string, occurredAt time.Time) error {
	signalsJSON, err := json.Marshal(signals)
	if err != nil {
		return fmt.Errorf("failed to marshal signals for flag: %w", err)
	}

	var modelVersion *string
	if final.ModelVersion != "" {
		modelVersion = &final.ModelVersion
	}

	// 1. Insert flag
	_, err = tx.Exec(ctx, `
		INSERT INTO flags (
			flag_id, transaction_id, account_id, score, severity, signals,
			rules_version, model_version, status, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'open', $9)
		ON CONFLICT (transaction_id) DO NOTHING
	`, final.FlagID, txn.TransactionID, txn.AccountID, final.Score, final.Severity, signalsJSON, rulesVersion, modelVersion, occurredAt)
	if err != nil {
		return fmt.Errorf("failed to insert flag: %w", err)
	}

	// 2. Prepare FraudFlagged payload
	var sigSummaries []events.SignalSummary
	for _, sig := range signals {
		sigSummaries = append(sigSummaries, events.SignalSummary{
			Rule:     sig.Rule,
			Score:    sig.Score,
			Reason:   sig.Reason,
			Evidence: sig.Evidence,
		})
	}

	flaggedPayload := events.FraudFlagged{
		FlagID:        final.FlagID,
		TransactionID: txn.TransactionID,
		AccountID:     txn.AccountID,
		AmountMinor:   txn.AmountMinor,
		Currency:      txn.Currency,
		Merchant:      txn.Merchant,
		Country:       txn.Country,
		Channel:       txn.Channel,
		Score:         final.Score,
		Severity:      final.Severity,
		Signals:       sigSummaries,
		RulesVersion:  rulesVersion,
		ModelVersion:  final.ModelVersion,
		OccurredAt:    occurredAt,
	}

	envelope, err := events.NewDeterministicEnvelope(
		final.FlagID,
		events.EventTypeFraudFlagged,
		events.CurrentVersion,
		occurredAt,
		flaggedPayload,
	)
	if err != nil {
		return fmt.Errorf("failed to wrap fraud flag envelope: %w", err)
	}

	envJSON, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal outbox envelope: %w", err)
	}

	// 3. Insert outbox row
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox (
			id, topic, key, payload, headers, published, created_at
		) VALUES ($1, 'fraud.flags', $2, $3, '{"source": "fraud-service"}'::jsonb, false, $4)
		ON CONFLICT (id) DO NOTHING
	`, final.FlagID, txn.AccountID.String(), envJSON, occurredAt)
	if err != nil {
		return fmt.Errorf("failed to insert outbox row: %w", err)
	}

	return nil
}

func mathMax(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
