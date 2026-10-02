package features

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"fraud-service/internal/events"
	"fraud-service/internal/rules"
)

// FeatureLoader defines the interface for building evaluation features before recording a transaction.
type FeatureLoader interface {
	Load(ctx context.Context, tx pgx.Tx, txn events.TransactionCreated, occurredAt time.Time) (*rules.Features, error)
}

// PGFeatureLoader loads features from PostgreSQL within the ongoing transaction.
type PGFeatureLoader struct{}

// NewPGFeatureLoader constructs a new PostgreSQL feature loader.
func NewPGFeatureLoader() *PGFeatureLoader {
	return &PGFeatureLoader{}
}

// Load queries history and profile BEFORE inserting this transaction.
func (l *PGFeatureLoader) Load(ctx context.Context, tx pgx.Tx, txn events.TransactionCreated, occurredAt time.Time) (*rules.Features, error) {
	f := &rules.Features{
		Txn:            txn,
		At:             occurredAt,
		KnownCountries: make(map[string]time.Time),
	}

	// 1. Load account profile
	var n int64
	var meanLog, m2Log float64
	var countriesJSON []byte
	var lastCountry *string
	var lastTxnAt *time.Time
	var firstSeenAt time.Time

	err := tx.QueryRow(ctx, `
		SELECT n, mean_log, m2_log, countries, last_country, last_txn_at, first_seen_at
		FROM account_profile
		WHERE account_id = $1
	`, txn.AccountID).Scan(&n, &meanLog, &m2Log, &countriesJSON, &lastCountry, &lastTxnAt, &firstSeenAt)

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to query account profile: %w", err)
	}

	if err == nil {
		f.ProfileN = n
		f.MeanLog = meanLog
		w := WelfordProfile{N: n, MeanLog: meanLog, M2Log: m2Log}
		f.StdLog = w.StdLog()

		if len(countriesJSON) > 0 {
			var rawCountries map[string]string
			if err := json.Unmarshal(countriesJSON, &rawCountries); err == nil {
				for code, tStr := range rawCountries {
					if parsed, err := time.Parse(time.RFC3339, tStr); err == nil {
						f.KnownCountries[code] = parsed
					} else {
						f.KnownCountries[code] = occurredAt
					}
				}
			}
		}

		if lastCountry != nil {
			f.LastCountry = *lastCountry
		}
		if lastTxnAt != nil {
			f.LastTxnAt = *lastTxnAt
		}
		if !firstSeenAt.IsZero() {
			if occurredAt.After(firstSeenAt) {
				f.AccountAge = occurredAt.Sub(firstSeenAt)
			}
		}
	}

	// 2. Query event-time window statistics from txn_history
	// Velocity 1m window
	oneMinAgo := occurredAt.Add(-1 * time.Minute)
	err = tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM txn_history
		WHERE account_id = $1 AND occurred_at >= $2 AND occurred_at <= $3
	`, txn.AccountID, oneMinAgo, occurredAt).Scan(&f.Count1m)
	if err != nil {
		return nil, fmt.Errorf("failed to query 1m velocity: %w", err)
	}

	// Velocity 10m window and small charges count (for card testing)
	tenMinAgo := occurredAt.Add(-10 * time.Minute)
	err = tx.QueryRow(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE amount_minor < 200)
		FROM txn_history
		WHERE account_id = $1 AND occurred_at >= $2 AND occurred_at <= $3
	`, txn.AccountID, tenMinAgo, occurredAt).Scan(&f.Count10m, &f.RecentSmallCount10m)
	if err != nil {
		return nil, fmt.Errorf("failed to query 10m count and small charges: %w", err)
	}

	// Volume 1h sum
	oneHourAgo := occurredAt.Add(-1 * time.Hour)
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_minor), 0)
		FROM txn_history
		WHERE account_id = $1 AND occurred_at >= $2 AND occurred_at <= $3
	`, txn.AccountID, oneHourAgo, occurredAt).Scan(&f.Sum1h)
	if err != nil {
		return nil, fmt.Errorf("failed to query 1h sum: %w", err)
	}

	// Merchant familiarity check
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM txn_history
			WHERE account_id = $1 AND merchant = $2
		)
	`, txn.AccountID, txn.Merchant).Scan(&f.MerchantSeen)
	if err != nil {
		return nil, fmt.Errorf("failed to query merchant history: %w", err)
	}

	return f, nil
}
