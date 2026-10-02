package events

import (
	"time"

	"github.com/google/uuid"
)

const (
	EventTypeTransactionCreated = "transactions.created"
	EventTypeFraudFlagged       = "fraud.flags"
	CurrentVersion              = 1
)

// TransactionCreated is the payload for transactions.created events.
// Extended backwards-compatibly with optional Country, Channel, and DeviceID.
type TransactionCreated struct {
	TransactionID uuid.UUID `json:"transaction_id"`
	AccountID     uuid.UUID `json:"account_id"`
	AmountMinor   int64     `json:"amount_minor"` // in minor units (pence / cents)
	Currency      string    `json:"currency"`
	Merchant      string    `json:"merchant"`
	MCC           string    `json:"mcc"`

	// Schema evolution additions (optional, tolerates absence)
	Country  string `json:"country,omitempty"`   // ISO 3166-1 alpha-2, e.g. "GB", "US"
	Channel  string `json:"channel,omitempty"`   // contactless | chip | online | atm
	DeviceID string `json:"device_id,omitempty"` // online payments identifier
}

// Validate checks business validity of TransactionCreated.
// Notice: missing country/channel/device_id is valid (skipped, not an error).
func (t TransactionCreated) Validate() error {
	if t.TransactionID == uuid.Nil {
		return NewPermanentError("transaction_id cannot be nil")
	}
	if t.AccountID == uuid.Nil {
		return NewPermanentError("account_id cannot be nil")
	}
	if t.AmountMinor <= 0 {
		return NewPermanentError("amount_minor must be positive, got %d", t.AmountMinor)
	}
	if t.Currency == "" {
		return NewPermanentError("currency cannot be empty")
	}
	if t.Merchant == "" {
		return NewPermanentError("merchant cannot be empty")
	}
	return nil
}

// SignalSummary contains the human-readable explanation and rule details for downstream services.
type SignalSummary struct {
	Rule     string         `json:"rule"`
	Score    float64        `json:"score"`
	Reason   string         `json:"reason"`
	Evidence map[string]any `json:"evidence,omitempty"`
}

// FraudFlagged is the payload emitted to fraud.flags for notification and case management.
type FraudFlagged struct {
	FlagID        uuid.UUID       `json:"flag_id"`
	TransactionID uuid.UUID       `json:"transaction_id"`
	AccountID     uuid.UUID       `json:"account_id"`
	AmountMinor   int64           `json:"amount_minor"`
	Currency      string          `json:"currency"`
	Merchant      string          `json:"merchant"`
	Country       string          `json:"country,omitempty"`
	Channel       string          `json:"channel,omitempty"`
	Score         float64         `json:"score"`
	Severity      string          `json:"severity"` // low | medium | high
	Signals       []SignalSummary `json:"signals"`
	RulesVersion  string          `json:"rules_version"`
	ModelVersion  string          `json:"model_version,omitempty"`
	OccurredAt    time.Time       `json:"occurred_at"`
}
