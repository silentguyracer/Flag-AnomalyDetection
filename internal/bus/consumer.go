package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
	"fraud-service/internal/events"
)

// Handler processes an event within an atomic database transaction.
type Handler func(ctx context.Context, tx pgx.Tx, env events.Envelope) error

// Config holds configuration for the shared consumer runner.
type Config struct {
	Name          string          // e.g. "fraud"
	Brokers       []string        // Kafka broker addresses
	GroupID       string          // Consumer group ID
	MainTopic     string          // e.g. "transactions.created"
	RetryTiers    []time.Duration // e.g. [5s, 30s, 5m]
	Handler       Handler         // Business logic handler
	DB            *pgxpool.Pool   // Database connection pool
	Logger        *slog.Logger    // Structured logger
	JitterPercent float64         // Jitter percentage, default 0.20 (+/- 20%)
}

// Consumer implements an idempotent, reliable Kafka consumer runner with multi-tier retry and DLQ routing.
type Consumer struct {
	cfg      Config
	cl       *kgo.Client
	producer *Producer
	db       *pgxpool.Pool
	log      *slog.Logger
}

// NewConsumer initializes the consumer runner and its internal producer for retries/DLQ.
func NewConsumer(cfg Config) (*Consumer, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.GroupID == "" {
		cfg.GroupID = cfg.Name + "-group"
	}
	if cfg.JitterPercent == 0 {
		cfg.JitterPercent = 0.20
	}

	topics := []string{cfg.MainTopic}
	for _, tier := range cfg.RetryTiers {
		topics = append(topics, fmt.Sprintf("%s.retry.%s", cfg.Name, FormatDuration(tier)))
	}
	topics = append(topics, fmt.Sprintf("%s.replay", cfg.Name))

	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ConsumerGroup(cfg.GroupID),
		kgo.ConsumeTopics(topics...),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
	}

	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka consumer client: %w", err)
	}

	prod, err := NewProducer(cfg.Brokers)
	if err != nil {
		cl.Close()
		return nil, fmt.Errorf("failed to create internal producer: %w", err)
	}

	return &Consumer{
		cfg:      cfg,
		cl:       cl,
		producer: prod,
		db:       cfg.DB,
		log:      cfg.Logger,
	}, nil
}

// FormatDuration formats durations to string like "5s", "30s", "5m".
func FormatDuration(d time.Duration) string {
	if d%time.Hour == 0 && d >= time.Hour {
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	if d%time.Minute == 0 && d >= time.Minute {
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return d.String()
}

// Run starts the polling loop, blocking until context cancellation.
func (c *Consumer) Run(ctx context.Context) error {
	c.log.Info("starting consumer runner",
		"consumer", c.cfg.Name,
		"group", c.cfg.GroupID,
		"main_topic", c.cfg.MainTopic,
	)

	for {
		if ctx.Err() != nil {
			c.log.Info("context cancelled, shutting down consumer", "consumer", c.cfg.Name)
			return nil
		}

		fetches := c.cl.PollFetches(ctx)
		if ctx.Err() != nil {
			return nil
		}

		fetches.EachError(func(t string, p int32, err error) {
			c.log.Error("kafka fetch error", "topic", t, "partition", p, "error", err)
		})

		var iterErr error
		fetches.EachRecord(func(rec *kgo.Record) {
			if iterErr != nil {
				return
			}
			iterErr = c.handle(ctx, rec)
		})

		if iterErr != nil {
			c.log.Error("error handling record, sleeping briefly before next poll", "error", iterErr)
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return nil
			}
		}
	}
}

// Close gracefully closes the consumer and internal producer.
func (c *Consumer) Close() {
	if c.cl != nil {
		c.cl.Close()
	}
	if c.producer != nil {
		c.producer.Close()
	}
}

// handle handles waiting (for retry topics), processing with idempotency, retry/dlq routing, and offset committing.
func (c *Consumer) handle(ctx context.Context, rec *kgo.Record) error {
	c.waitUntil(ctx, rec)

	err := c.processOnce(ctx, rec)
	if err != nil {
		c.log.Warn("processing failed, routing event",
			"consumer", c.cfg.Name,
			"topic", rec.Topic,
			"event_id", HeaderString(rec, HeaderEventID),
			"error", err.Error(),
		)
		if rerr := c.route(ctx, rec, err); rerr != nil {
			c.log.Error("failed to route to retry/dlq topic", "error", rerr)
			return rerr // couldn't hand off -> do NOT commit; will be redelivered
		}
	}

	return c.cl.CommitRecords(ctx, rec)
}

// waitUntil checks if the record has an x-not-before header and delays processing until that timestamp.
func (c *Consumer) waitUntil(ctx context.Context, rec *kgo.Record) {
	notBefore := HeaderInt64(rec, HeaderNotBefore)
	if notBefore == 0 {
		return
	}

	targetTime := time.UnixMilli(notBefore)
	diff := time.Until(targetTime)
	if diff > 0 {
		select {
		case <-time.After(diff):
		case <-ctx.Done():
		}
	}
}

// processOnce executes the business handler in a database transaction with idempotency guarantees.
func (c *Consumer) processOnce(ctx context.Context, rec *kgo.Record) error {
	var env events.Envelope
	if err := json.Unmarshal(rec.Value, &env); err != nil {
		return events.NewPermanentError("bad envelope JSON: %w", err)
	}

	tx, err := c.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin db transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`INSERT INTO processed_events (consumer, event_id) VALUES ($1, $2)
		 ON CONFLICT DO NOTHING`, c.cfg.Name, env.EventID)
	if err != nil {
		return fmt.Errorf("failed to check idempotency in processed_events: %w", err)
	}

	if tag.RowsAffected() == 0 {
		DuplicatesSkippedTotal.WithLabelValues(c.cfg.Name).Inc()
		c.log.Info("duplicate event skipped",
			"consumer", c.cfg.Name,
			"event_id", env.EventID.String(),
			"topic", rec.Topic,
		)
		return nil // already processed: skip, offset will be committed
	}

	start := time.Now()
	if err := c.cfg.Handler(ctx, tx, env); err != nil {
		return err // rollback un-inserts the processed_events row
	}

	ProcessingDuration.WithLabelValues(c.cfg.Name).Observe(time.Since(start).Seconds())
	EventsProcessedTotal.WithLabelValues(c.cfg.Name).Inc()

	return tx.Commit(ctx)
}

// route routes a failed message to the next retry tier or directly to the DLQ.
func (c *Consumer) route(ctx context.Context, rec *kgo.Record, procErr error) error {
	attempt := HeaderInt(rec, HeaderAttempt) // 0 for first delivery

	var perm events.PermanentError
	if errors.As(procErr, &perm) || attempt >= len(c.cfg.RetryTiers) {
		return c.publishDLQ(ctx, rec, procErr, attempt)
	}

	baseDelay := c.cfg.RetryTiers[attempt]
	tierName := FormatDuration(baseDelay)

	// Apply jitter: +/- JitterPercent (e.g. +/- 20%)
	jitterFactor := 1.0 + (rand.Float64()*2*c.cfg.JitterPercent - c.cfg.JitterPercent)
	delay := time.Duration(float64(baseDelay) * jitterFactor)
	if delay < 0 {
		delay = baseDelay
	}

	nextTopic := fmt.Sprintf("%s.retry.%s", c.cfg.Name, tierName)
	notBefore := time.Now().Add(delay).UnixMilli()

	RetriesTotal.WithLabelValues(c.cfg.Name, tierName).Inc()

	next := &kgo.Record{
		Topic: nextTopic,
		Key:   rec.Key,
		Value: rec.Value,
		Headers: CopyHeaders(rec, map[string]string{
			HeaderAttempt:           strconv.Itoa(attempt + 1),
			HeaderNotBefore:         strconv.FormatInt(notBefore, 10),
			HeaderLastError:         Truncate(procErr.Error(), 500),
			HeaderOriginalTopic:     OriginalTopic(rec),
			HeaderOriginalPartition: OriginalPartition(rec),
			HeaderOriginalOffset:    OriginalOffset(rec),
		}),
	}

	c.log.Info("routing event to retry tier",
		"consumer", c.cfg.Name,
		"topic", nextTopic,
		"attempt", attempt+1,
		"delay", delay.String(),
	)

	return c.producer.ProduceSync(ctx, next)
}

// publishDLQ writes the original payload and failure diagnostic headers to the service DLQ.
func (c *Consumer) publishDLQ(ctx context.Context, rec *kgo.Record, cause error, attempt int) error {
	var perm events.PermanentError
	kind := "retries_exhausted"
	if errors.As(cause, &perm) {
		kind = "permanent"
	}

	DLQTotal.WithLabelValues(c.cfg.Name, kind).Inc()

	dlqTopic := fmt.Sprintf("%s.dlq", c.cfg.Name)

	c.log.Error("routing event to DLQ",
		"consumer", c.cfg.Name,
		"topic", dlqTopic,
		"error_kind", kind,
		"attempt", attempt,
		"cause", cause.Error(),
	)

	return c.producer.ProduceSync(ctx, &kgo.Record{
		Topic: dlqTopic,
		Key:   rec.Key,
		Value: rec.Value, // untouched original payload
		Headers: CopyHeaders(rec, map[string]string{
			HeaderError:             Truncate(cause.Error(), 1000),
			HeaderErrorKind:         kind,
			HeaderAttempts:          strconv.Itoa(attempt),
			HeaderFailedAt:          time.Now().UTC().Format(time.RFC3339),
			HeaderConsumer:          c.cfg.Name,
			HeaderOriginalTopic:     OriginalTopic(rec),
			HeaderOriginalPartition: OriginalPartition(rec),
			HeaderOriginalOffset:    OriginalOffset(rec),
		}),
	})
}
