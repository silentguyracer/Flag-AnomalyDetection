package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"fraud-service/internal/db"
	"fraud-service/internal/events"
	"fraud-service/internal/features"
	"fraud-service/internal/rules"
	"fraud-service/internal/scorer"
)

// Config defines the runtime configuration for the fraud evaluation service.
type Config struct {
	RulesConfigPath string
	FlagThreshold   float64
	Version         string
}

// Service orchestrates transaction feature loading, rule evaluation, ML scoring, and flagging.
type Service struct {
	cfg      *rules.Config
	engine   *rules.Engine
	features features.FeatureLoader
	store    db.Store
	scorer   scorer.Scorer
	log      *slog.Logger
}

// NewService creates a new Service instance.
func NewService(cfg *rules.Config, features features.FeatureLoader, store db.Store, sc scorer.Scorer, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	engine := rules.NewEngine(cfg)

	return &Service{
		cfg:      cfg,
		engine:   engine,
		features: features,
		store:    store,
		scorer:   sc,
		log:      log,
	}
}

// Handle processes a transaction.created event within an atomic database transaction.
func (s *Service) Handle(ctx context.Context, tx pgx.Tx, env events.Envelope) error {
	var t events.TransactionCreated
	if err := json.Unmarshal(env.Data, &t); err != nil {
		return events.NewPermanentError("malformed transaction payload JSON: %w", err)
	}

	if err := t.Validate(); err != nil {
		return err // PermanentError returned by Validate() -> routes to DLQ immediately
	}

	// 1. Load features from database BEFORE inserting this transaction (avoids self-pollution)
	f, err := s.features.Load(ctx, tx, t, env.OccurredAt)
	if err != nil {
		return fmt.Errorf("transient failure loading features: %w", err) // triggers retry tiers
	}

	// 2. Run deterministic event-time rules engine
	signals := s.engine.Run(f)
	ruleScore := rules.Combine(signals)

	// 3. Optional ML scorer call with 150ms timeout and circuit breaker
	var mlScore *float64
	var modelVersion string
	if s.scorer != nil {
		if sc, err := s.scorer.Score(ctx, f); err == nil {
			mlScore = &sc.Score
			modelVersion = sc.ModelVersion
		} else {
			// ML failure is non-fatal: degrade gracefully to rules-only
			s.log.Warn("ML scorer unavailable or timed out; proceeding with rules-only",
				"transaction_id", t.TransactionID.String(),
				"error", err.Error(),
			)
		}
	}

	// 4. Combine signals and ML score
	final := s.decide(ruleScore, mlScore, modelVersion, t.TransactionID)

	// 5. Insert txn_history, update Welford account_profile baseline, and log feature vector
	if err := s.store.Record(ctx, tx, t, f, signals, ruleScore, mlScore, ""); err != nil {
		return fmt.Errorf("failed to record transaction and features: %w", err)
	}

	// 6. If score exceeds threshold: insert flag and transactional outbox row
	if final.Score >= s.cfg.FlagThreshold {
		s.log.Info("transaction flagged for fraud review",
			"transaction_id", t.TransactionID.String(),
			"account_id", t.AccountID.String(),
			"score", final.Score,
			"severity", final.Severity,
			"signals_count", len(signals),
		)
		if err := s.store.Flag(ctx, tx, t, signals, final, s.engine.Version(), env.OccurredAt); err != nil {
			return fmt.Errorf("failed to persist flag and outbox row: %w", err)
		}
	}

	return nil
}

// decide computes the final decision, severity, and deterministic flag ID.
func (s *Service) decide(ruleScore float64, mlScore *float64, modelVersion string, txnID uuid.UUID) db.Decision {
	finalScore := ruleScore

	// If ML is available and NOT in shadow mode, combine scores (e.g. max)
	if mlScore != nil && s.scorer != nil && !s.scorer.IsShadowMode() {
		finalScore = math.Max(ruleScore, *mlScore)
	}

	flagID := db.DeterministicFlagID(txnID, s.engine.Version())
	severity := rules.SeverityBand(finalScore)

	return db.Decision{
		FlagID:       flagID,
		Score:        finalScore,
		Severity:     severity,
		ModelVersion: modelVersion,
	}
}
