package test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"fraud-service/internal/db"
	"fraud-service/internal/events"
	"fraud-service/internal/features"
	"fraud-service/internal/rules"
)

// MockStore implements an in-memory db.Store and features.FeatureLoader for deterministic unit and scenario testing.
type MockStore struct {
	mu              sync.RWMutex
	ProcessedEvents map[string]bool
	History         map[uuid.UUID][]MockHistoryItem
	Profiles        map[uuid.UUID]*MockProfile
	Flags           map[uuid.UUID]MockFlag
	Outbox          []MockOutboxItem
	FeatureLogs     map[uuid.UUID]MockFeatureLog
}

type MockHistoryItem struct {
	TxnID       uuid.UUID
	AccountID   uuid.UUID
	AmountMinor int64
	Country     string
	Merchant    string
	Channel     string
	OccurredAt  time.Time
}

type MockProfile struct {
	N             int64
	MeanLog       float64
	M2Log         float64
	Countries     map[string]time.Time
	LastCountry   string
	LastTxnAt     time.Time
	FirstSeenAt   time.Time
}

type MockFlag struct {
	FlagID       uuid.UUID
	TxnID        uuid.UUID
	AccountID    uuid.UUID
	Score        float64
	Severity     string
	Signals      []rules.Signal
	RulesVersion string
	Status       string
	CreatedAt    time.Time
}

type MockOutboxItem struct {
	ID        uuid.UUID
	Topic     string
	Key       string
	Payload   []byte
	CreatedAt time.Time
}

type MockFeatureLog struct {
	Features  rules.Features
	RuleScore float64
	MLScore   *float64
	Label     string
}

func NewMockStore() *MockStore {
	return &MockStore{
		ProcessedEvents: make(map[string]bool),
		History:         make(map[uuid.UUID][]MockHistoryItem),
		Profiles:        make(map[uuid.UUID]*MockProfile),
		Flags:           make(map[uuid.UUID]MockFlag),
		FeatureLogs:     make(map[uuid.UUID]MockFeatureLog),
	}
}

// CheckProcessed checks and records event_id for idempotency.
func (m *MockStore) CheckProcessed(consumer string, eventID uuid.UUID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", consumer, eventID.String())
	if m.ProcessedEvents[key] {
		return false // duplicate
	}
	m.ProcessedEvents[key] = true
	return true
}

func (m *MockStore) Load(ctx context.Context, tx pgx.Tx, txn events.TransactionCreated, occurredAt time.Time) (*rules.Features, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	f := &rules.Features{
		Txn:            txn,
		At:             occurredAt,
		KnownCountries: make(map[string]time.Time),
	}

	// 1. Profile
	if prof, ok := m.Profiles[txn.AccountID]; ok {
		f.ProfileN = prof.N
		f.MeanLog = prof.MeanLog
		w := features.WelfordProfile{N: prof.N, MeanLog: prof.MeanLog, M2Log: prof.M2Log}
		f.StdLog = w.StdLog()
		for c, t := range prof.Countries {
			f.KnownCountries[c] = t
		}
		f.LastCountry = prof.LastCountry
		f.LastTxnAt = prof.LastTxnAt
		if !prof.FirstSeenAt.IsZero() && occurredAt.After(prof.FirstSeenAt) {
			f.AccountAge = occurredAt.Sub(prof.FirstSeenAt)
		}
	}

	// 2. Window statistics from history
	if items, ok := m.History[txn.AccountID]; ok {
		oneMinAgo := occurredAt.Add(-1 * time.Minute)
		tenMinAgo := occurredAt.Add(-10 * time.Minute)
		oneHourAgo := occurredAt.Add(-1 * time.Hour)

		for _, item := range items {
			if !item.OccurredAt.Before(oneMinAgo) && !item.OccurredAt.After(occurredAt) {
				f.Count1m++
			}
			if !item.OccurredAt.Before(tenMinAgo) && !item.OccurredAt.After(occurredAt) {
				f.Count10m++
				if item.AmountMinor < 200 {
					f.RecentSmallCount10m++
				}
			}
			if !item.OccurredAt.Before(oneHourAgo) && !item.OccurredAt.After(occurredAt) {
				f.Sum1h += item.AmountMinor
			}
			if item.Merchant == txn.Merchant {
				f.MerchantSeen = true
			}
		}
	}

	return f, nil
}

func (m *MockStore) Record(ctx context.Context, tx pgx.Tx, txn events.TransactionCreated, f *rules.Features, signals []rules.Signal, ruleScore float64, mlScore *float64, label string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. Add to history
	m.History[txn.AccountID] = append(m.History[txn.AccountID], MockHistoryItem{
		TxnID:       txn.TransactionID,
		AccountID:   txn.AccountID,
		AmountMinor: txn.AmountMinor,
		Country:     txn.Country,
		Merchant:    txn.Merchant,
		Channel:     txn.Channel,
		OccurredAt:  f.At,
	})

	// 2. Update profile baseline
	prof, ok := m.Profiles[txn.AccountID]
	if !ok {
		prof = &MockProfile{
			Countries:   make(map[string]time.Time),
			FirstSeenAt: f.At,
		}
		m.Profiles[txn.AccountID] = prof
	}

	w := features.WelfordProfile{
		N:       prof.N,
		MeanLog: prof.MeanLog,
		M2Log:   prof.M2Log,
	}
	w.Update(txn.AmountMinor)
	prof.N = w.N
	prof.MeanLog = w.MeanLog
	prof.M2Log = w.M2Log

	if txn.Country != "" {
		prof.Countries[txn.Country] = f.At
		prof.LastCountry = txn.Country
	}
	prof.LastTxnAt = f.At

	// 3. Log features
	m.FeatureLogs[txn.TransactionID] = MockFeatureLog{
		Features:  *f,
		RuleScore: ruleScore,
		MLScore:   mlScore,
		Label:     label,
	}

	return nil
}

func (m *MockStore) Flag(ctx context.Context, tx pgx.Tx, txn events.TransactionCreated, signals []rules.Signal, final db.Decision, rulesVersion string, occurredAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Flags[txn.TransactionID] = MockFlag{
		FlagID:       final.FlagID,
		TxnID:        txn.TransactionID,
		AccountID:    txn.AccountID,
		Score:        final.Score,
		Severity:     final.Severity,
		Signals:      signals,
		RulesVersion: rulesVersion,
		Status:       "open",
		CreatedAt:    occurredAt,
	}

	var sigSummaries []events.SignalSummary
	for _, s := range signals {
		sigSummaries = append(sigSummaries, events.SignalSummary{
			Rule:     s.Rule,
			Score:    s.Score,
			Reason:   s.Reason,
			Evidence: s.Evidence,
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

	env, _ := events.NewDeterministicEnvelope(final.FlagID, events.EventTypeFraudFlagged, events.CurrentVersion, occurredAt, flaggedPayload)
	raw, _ := json.Marshal(env)

	m.Outbox = append(m.Outbox, MockOutboxItem{
		ID:        final.FlagID,
		Topic:     "fraud.flags",
		Key:       txn.AccountID.String(),
		Payload:   raw,
		CreatedAt: occurredAt,
	})

	return nil
}

// SeedMatureProfile initializes an account with N transactions and given mean log-amount.
func (m *MockStore) SeedMatureProfile(accountID uuid.UUID, n int64, avgMinor int64, country string, ageDays int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	meanLog := math.Log(float64(avgMinor))
	firstSeen := time.Now().Add(-time.Duration(ageDays) * 24 * time.Hour)
	countries := make(map[string]time.Time)
	if country != "" {
		countries[country] = firstSeen
	}

	m.Profiles[accountID] = &MockProfile{
		N:           n,
		MeanLog:     meanLog,
		M2Log:       0.35 * 0.35 * float64(n-1),
		Countries:   countries,
		LastCountry: country,
		LastTxnAt:   time.Now().Add(-1 * time.Hour),
		FirstSeenAt: firstSeen,
	}
}
