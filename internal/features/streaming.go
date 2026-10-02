package features

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"fraud-service/internal/events"
	"fraud-service/internal/rules"
)

// WindowTxn represents a lightweight cached transaction in the streaming window.
type WindowTxn struct {
	OccurredAt  time.Time
	AmountMinor int64
	Merchant    string
	Country     string
}

// StreamingFeatureStore maintains real-time in-memory sliding windows per account.
// Allows sub-millisecond retrieval of Count1m, Count10m, Sum1h, and Merchant familiarity.
type StreamingFeatureStore struct {
	mu       sync.RWMutex
	windows  map[uuid.UUID][]WindowTxn
	maxAge   time.Duration
}

// NewStreamingFeatureStore constructs a new in-memory streaming window store.
func NewStreamingFeatureStore(maxAge time.Duration) *StreamingFeatureStore {
	if maxAge <= 0 {
		maxAge = 1 * time.Hour
	}
	return &StreamingFeatureStore{
		windows: make(map[uuid.UUID][]WindowTxn),
		maxAge:  maxAge,
	}
}

// Record inserts a new transaction into the account's sliding window and prunes expired items.
func (s *StreamingFeatureStore) Record(accountID uuid.UUID, txn events.TransactionCreated, occurredAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := occurredAt.Add(-s.maxAge)
	existing := s.windows[accountID]

	// Prune expired records
	var retained []WindowTxn
	for _, item := range existing {
		if item.OccurredAt.After(cutoff) {
			retained = append(retained, item)
		}
	}

	// Append current transaction
	retained = append(retained, WindowTxn{
		OccurredAt:  occurredAt,
		AmountMinor: txn.AmountMinor,
		Merchant:    txn.Merchant,
		Country:     txn.Country,
	})

	s.windows[accountID] = retained
}

// PopulateFeatures extracts real-time velocity metrics at event time without hitting PostgreSQL.
func (s *StreamingFeatureStore) PopulateFeatures(f *rules.Features, accountID uuid.UUID, at time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	txns, ok := s.windows[accountID]
	if !ok || len(txns) == 0 {
		return
	}

	oneMinAgo := at.Add(-1 * time.Minute)
	tenMinAgo := at.Add(-10 * time.Minute)
	oneHourAgo := at.Add(-1 * time.Hour)

	for _, item := range txns {
		if !item.OccurredAt.Before(oneMinAgo) && !item.OccurredAt.After(at) {
			f.Count1m++
		}
		if !item.OccurredAt.Before(tenMinAgo) && !item.OccurredAt.After(at) {
			f.Count10m++
			if item.AmountMinor < 200 {
				f.RecentSmallCount10m++
			}
		}
		if !item.OccurredAt.Before(oneHourAgo) && !item.OccurredAt.After(at) {
			f.Sum1h += item.AmountMinor
		}
		if item.Merchant == f.Txn.Merchant {
			f.MerchantSeen = true
		}
	}
}
