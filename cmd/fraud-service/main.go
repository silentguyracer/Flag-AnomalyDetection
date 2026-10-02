package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"fraud-service/internal/api"
	"fraud-service/internal/authgate"
	"fraud-service/internal/bus"
	"fraud-service/internal/canary"
	"fraud-service/internal/db"
	"fraud-service/internal/features"
	"fraud-service/internal/graph"
	"fraud-service/internal/rules"
	"fraud-service/internal/scorer"
	"fraud-service/internal/service"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	rulesConfigPath := os.Getenv("RULES_CONFIG_PATH")
	if rulesConfigPath == "" {
		rulesConfigPath = "configs/rules.yaml"
	}

	rulesCfg, err := rules.LoadConfig(rulesConfigPath)
	if err != nil {
		logger.Error("failed to load rules configuration", "path", rulesConfigPath, "error", err)
		os.Exit(1)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:dev@localhost:5432/postgres?sslmode=disable"
	}

	brokersStr := os.Getenv("KAFKA_BROKERS")
	if brokersStr == "" {
		brokersStr = "localhost:9092"
	}
	brokers := strings.Split(brokersStr, ",")

	mlEndpoint := os.Getenv("ML_SCORER_URL")
	if mlEndpoint == "" {
		mlEndpoint = "http://localhost:8000"
	}

	shadowMode := false
	if val := os.Getenv("ML_SHADOW_MODE"); val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			shadowMode = b
		}
	}

	apiPort := os.Getenv("PORT")
	if apiPort == "" {
		apiPort = "8085"
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger.Info("starting fraud detection service",
		"rules_version", rulesCfg.Version,
		"threshold", rulesCfg.FlagThreshold,
		"ml_endpoint", mlEndpoint,
		"shadow_mode", shadowMode,
		"api_port", apiPort,
	)

	// 1. Database Connection Pool
	pool, err := db.ConnectPool(ctx, dbURL)
	if err != nil {
		logger.Error("failed to connect to postgres pool", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// 2. Setup Scorer, Graph, Auth Gate & Service components
	mlScorer := scorer.NewHTTPScorer(mlEndpoint, shadowMode, 150*time.Millisecond)
	featureLoader := features.NewPGFeatureLoader()
	store := db.NewPGStore()
	memGraph := graph.NewMemoryGraph()
	engine := rules.NewEngine(rulesCfg)
	authGate := authgate.NewGate(engine, memGraph, mlScorer, 25*time.Millisecond, true)

	var canaryRunner *canary.CanaryRunner
	if shadowCfg, err := rules.LoadConfig("configs/rules_v4.yaml"); err == nil {
		shadowEngine := rules.NewEngine(shadowCfg)
		canaryRunner = canary.NewCanaryRunner(engine, shadowEngine)
		logger.Info("initialized shadow canary evaluator", "shadow_version", shadowCfg.Version)
	}

	fraudService := service.NewService(rulesCfg, featureLoader, store, mlScorer, logger)

	// 3. Start Case Review HTTP API Server
	apiServer := api.NewServer(pool, authGate, memGraph, canaryRunner)
	httpSrv := &http.Server{
		Addr:    ":" + apiPort,
		Handler: apiServer.Router(),
	}

	go func() {
		logger.Info("fraud Case API listening", "port", apiPort)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP Case API server error", "error", err)
		}
	}()

	// 4. Initialize Kafka Consumer with Idempotency, Retry Tiers, and DLQ
	consumerCfg := bus.Config{
		Name:      "fraud",
		Brokers:   brokers,
		GroupID:   "fraud-group",
		MainTopic: "transactions.created",
		RetryTiers: []time.Duration{
			5 * time.Second,
			30 * time.Second,
			5 * time.Minute,
		},
		Handler: fraudService.Handle,
		DB:      pool,
		Logger:  logger,
	}

	consumer, err := bus.NewConsumer(consumerCfg)
	if err != nil {
		logger.Error("failed to initialize kafka consumer", "error", err)
		os.Exit(1)
	}
	defer consumer.Close()

	// Run consumer in background goroutine
	go func() {
		if err := consumer.Run(ctx); err != nil {
			logger.Error("consumer run error", "error", err)
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	logger.Info("shutting down fraud detection service gracefully")

	shutdownCtx, sCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer sCancel()
	httpSrv.Shutdown(shutdownCtx)
	logger.Info("shutdown complete")
}
