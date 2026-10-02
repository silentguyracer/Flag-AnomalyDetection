package events

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Envelope is the standard wrapper for all events in the pipeline.
type Envelope struct {
	EventID    uuid.UUID       `json:"event_id"`
	EventType  string          `json:"event_type"`
	Version    int             `json:"version"`
	OccurredAt time.Time       `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

// NewEnvelope wraps payload data into a standardized envelope.
func NewEnvelope(eventType string, version int, payload any) (Envelope, error) {
	dataBytes, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("failed to marshal payload for envelope: %w", err)
	}

	return Envelope{
		EventID:    uuid.New(),
		EventType:  eventType,
		Version:    version,
		OccurredAt: time.Now().UTC(),
		Data:       dataBytes,
	}, nil
}

// NewDeterministicEnvelope wraps payload with a deterministic UUID (e.g. SHA-1 hash) and specific timestamp.
func NewDeterministicEnvelope(eventID uuid.UUID, eventType string, version int, occurredAt time.Time, payload any) (Envelope, error) {
	dataBytes, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("failed to marshal payload for envelope: %w", err)
	}

	return Envelope{
		EventID:    eventID,
		EventType:  eventType,
		Version:    version,
		OccurredAt: occurredAt,
		Data:       dataBytes,
	}, nil
}

// UnmarshalData extracts the inner payload into target struct.
func (e Envelope) UnmarshalData(target any) error {
	if len(e.Data) == 0 {
		return NewPermanentError("empty event data in envelope")
	}
	if err := json.Unmarshal(e.Data, target); err != nil {
		return NewPermanentError("failed to unmarshal event payload: %v", err)
	}
	return nil
}
