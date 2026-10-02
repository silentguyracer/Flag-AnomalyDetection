package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/twmb/franz-go/pkg/kgo"
	"fraud-service/internal/events"
)

// Producer wraps a franz-go Kafka producer with production defaults.
type Producer struct {
	client *kgo.Client
}

// NewProducer creates a new idempotent producer client.
func NewProducer(brokers []string) (*Producer, error) {
	opts := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka producer client: %w", err)
	}

	return &Producer{client: client}, nil
}

// Client returns the underlying franz-go client if needed.
func (p *Producer) Client() *kgo.Client {
	return p.client
}

// ProduceSync produces one or more records synchronously, returning the first error encountered.
func (p *Producer) ProduceSync(ctx context.Context, recs ...*kgo.Record) error {
	results := p.client.ProduceSync(ctx, recs...)
	return results.FirstErr()
}

// PublishEnvelope marshals an Envelope and publishes it with standard metadata headers.
func (p *Producer) PublishEnvelope(ctx context.Context, topic string, key string, env events.Envelope, traceID string) error {
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}

	if traceID == "" {
		traceID = env.EventID.String()
	}

	rec := &kgo.Record{
		Topic: topic,
		Key:   []byte(key),
		Value: payload,
		Headers: []kgo.RecordHeader{
			{Key: HeaderEventID, Value: []byte(env.EventID.String())},
			{Key: HeaderEventType, Value: []byte(env.EventType)},
			{Key: HeaderSchemaVersion, Value: []byte(strconv.Itoa(env.Version))},
			{Key: HeaderTraceID, Value: []byte(traceID)},
		},
	}

	return p.ProduceSync(ctx, rec)
}

// Close gracefully closes the producer client.
func (p *Producer) Close() {
	if p.client != nil {
		p.client.Close()
	}
}
