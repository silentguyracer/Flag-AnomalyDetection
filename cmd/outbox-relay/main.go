package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/twmb/franz-go/pkg/kgo"
	"fraud-service/internal/bus"
	"fraud-service/internal/db"
)

var (
	outboxPublishedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "pipeline",
		Subsystem: "outbox",
		Name:      "published_total",
		Help:      "Total number of outbox messages published to Kafka",
	})

	outboxPublishErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "pipeline",
		Subsystem: "outbox",
		Name:      "publish_errors_total",
		Help:      "Total number of failed outbox publish attempts",
	})
)

type OutboxRow struct {
	ID      uuid.UUID
	Topic   string
	Key     string
	Payload []byte
	Headers map[string]string
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:dev@localhost:5432/postgres?sslmode=disable"
	}

	brokersStr := os.Getenv("KAFKA_BROKERS")
	if brokersStr == "" {
		brokersStr = "localhost:9092"
	}
	brokers := strings.Split(brokersStr, ",")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8084"
	}

	pollMs := 200
	if val := os.Getenv("POLL_INTERVAL_MS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			pollMs = n
		}
	}
	pollInterval := time.Duration(pollMs) * time.Millisecond

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger.Info("starting fraud outbox relay", "db_url", dbURL, "brokers", brokers, "poll_interval", pollInterval)

	pool, err := db.ConnectPool(ctx, dbURL)
	if err != nil {
		logger.Error("failed to connect to postgres pool", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	producer, err := bus.NewProducer(brokers)
	if err != nil {
		logger.Error("failed to initialize kafka producer", "error", err)
		os.Exit(1)
	}
	defer producer.Close()

	// Start HTTP health and metrics server
	router := chi.NewRouter()
	router.Handle("/metrics", promhttp.Handler())
	router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"fraud-outbox-relay"}`))
	})

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	go func() {
		logger.Info("outbox relay HTTP server listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server error", "error", err)
		}
	}()

	// Polling loop
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutting down outbox relay gracefully")
			shutdownCtx, sCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer sCancel()
			srv.Shutdown(shutdownCtx)
			return
		case <-ticker.C:
			if err := processOutboxBatch(ctx, pool, producer, logger); err != nil {
				logger.Error("error processing outbox batch", "error", err)
			}
		}
	}
}

func processOutboxBatch(ctx context.Context, pool *pgxpool.Pool, producer *bus.Producer, logger *slog.Logger) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id, topic, key, payload, headers
		FROM outbox
		WHERE published = false
		ORDER BY created_at ASC
		LIMIT 100
		FOR UPDATE SKIP LOCKED
	`)
	if err != nil {
		return fmt.Errorf("failed to query outbox: %w", err)
	}
	defer rows.Close()

	var batch []OutboxRow
	for rows.Next() {
		var r OutboxRow
		var hJSON []byte
		if err := rows.Scan(&r.ID, &r.Topic, &r.Key, &r.Payload, &hJSON); err != nil {
			return fmt.Errorf("failed to scan outbox row: %w", err)
		}
		if len(hJSON) > 0 {
			_ = rows.Scan() // no-op
		}
		batch = append(batch, r)
	}

	if len(batch) == 0 {
		return nil
	}

	var publishedIDs []uuid.UUID
	var records []*kgo.Record

	for _, item := range batch {
		rec := &kgo.Record{
			Topic: item.Topic,
			Key:   []byte(item.Key),
			Value: item.Payload,
			Headers: []kgo.RecordHeader{
				{Key: bus.HeaderEventID, Value: []byte(item.ID.String())},
				{Key: "source", Value: []byte("fraud-outbox-relay")},
			},
		}
		records = append(records, rec)
		publishedIDs = append(publishedIDs, item.ID)
	}

	if err := producer.ProduceSync(ctx, records...); err != nil {
		outboxPublishErrorsTotal.Add(float64(len(records)))
		return fmt.Errorf("failed to produce to kafka: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE outbox
		SET published = true, published_at = now()
		WHERE id = ANY($1)
	`, publishedIDs)
	if err != nil {
		return fmt.Errorf("failed to mark outbox as published: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit outbox update: %w", err)
	}

	outboxPublishedTotal.Add(float64(len(records)))
	logger.Info("relayed outbox flags to Kafka", "count", len(records))
	return nil
}
