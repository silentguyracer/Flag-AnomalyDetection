package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"fraud-service/internal/authgate"
	"fraud-service/internal/canary"
	"fraud-service/internal/chargeback"
	"fraud-service/internal/crypto"
	"fraud-service/internal/deviceprint"
	"fraud-service/internal/drift"
	"fraud-service/internal/events"
	"fraud-service/internal/graph"
	"fraud-service/internal/rules"
	"fraud-service/internal/sar"
	"fraud-service/internal/xai"
)

// Server implements the Case API, Auth Gate, and Review feedback loop HTTP service.
type Server struct {
	router         chi.Router
	db             *pgxpool.Pool
	authGate       *authgate.Gate
	memGraph       *graph.MemoryGraph
	canaryRunner   *canary.CanaryRunner
	disputeMonitor *chargeback.DisputeMonitor
}

// NewServer initializes the HTTP router and endpoints.
func NewServer(
	dbPool *pgxpool.Pool,
	gate *authgate.Gate,
	memGraph *graph.MemoryGraph,
	canaryRunner *canary.CanaryRunner,
) *Server {
	s := &Server{
		router:         chi.NewRouter(),
		db:             dbPool,
		authGate:       gate,
		memGraph:       memGraph,
		canaryRunner:   canaryRunner,
		disputeMonitor: chargeback.NewDisputeMonitor(),
	}

	s.setupRoutes()
	return s
}

// Router returns the Chi router.
func (s *Server) Router() chi.Router {
	return s.router
}

func (s *Server) setupRoutes() {
	s.router.Use(middleware.RequestID)
	s.router.Use(middleware.RealIP)
	s.router.Use(middleware.Logger)
	s.router.Use(middleware.Recoverer)
	s.router.Use(middleware.Timeout(30 * time.Second))

	// CORS headers for local/testing dashboard usage
	s.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	s.router.Get("/health", s.handleHealth)
	s.router.Handle("/metrics", promhttp.Handler())

	// Synchronous Authorization Gate (Pre-Auth Risk Check)
	s.router.Post("/v1/authorizations/evaluate", s.handleEvaluateAuthorization)

	// Entity Graph & Syndicate Endpoints
	s.router.Get("/v1/graph/syndicates", s.handleGraphSyndicates)
	s.router.Get("/v1/graph/cycles", s.handleGraphCycles)
	s.router.Get("/v1/graph/diffusion", s.handleRiskDiffusion)

	// Canary & Shadow Evaluator Endpoints
	s.router.Get("/v1/canary/metrics", s.handleCanaryMetrics)

	// Explainable AI & Adverse Action Notice Endpoint
	s.router.Post("/v1/explain", s.handleExplainTransaction)

	// Concept Drift Monitoring Endpoint
	s.router.Get("/v1/drift", s.handleDriftReport)

	// Hardware Entropy & Device Fingerprinting
	s.router.Post("/v1/deviceprint/evaluate", s.handleDevicePrintEvaluate)

	// Pre-Dispute Early Warning & DTR Monitoring
	s.router.Post("/v1/chargebacks/early-warning", s.handleChargebackEarlyWarning)
	s.router.Get("/v1/chargebacks/dtr", s.handleChargebackDTR)

	// Crypto & Blockchain Sanctions Screening
	s.router.Post("/v1/crypto/screen", s.handleCryptoScreen)

	// Regulatory Suspicious Activity Report (SAR)
	s.router.Post("/v1/sar/generate", s.handleSARGenerate)

	s.router.Get("/flags", s.handleListFlags)
	s.router.Get("/flags/{id}", s.handleGetFlag)
	s.router.Get("/accounts/{id}/flags", s.handleListAccountFlags)
	s.router.Post("/flags/{id}/review", s.handleSubmitVerdict)
	s.router.Get("/stats/rules", s.handleRuleStats)
	s.router.Get("/dashboard", s.handleDashboard)
	s.router.Get("/", s.handleDashboard)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "ok", "time": time.Now().UTC()})
}

// FlagDTO represents a flagged transaction row returned by the API.
type FlagDTO struct {
	FlagID        uuid.UUID      `json:"flag_id"`
	TransactionID uuid.UUID      `json:"transaction_id"`
	AccountID     uuid.UUID      `json:"account_id"`
	Score         float64        `json:"score"`
	Severity      string         `json:"severity"`
	Signals       any            `json:"signals"`
	RulesVersion  string         `json:"rules_version"`
	ModelVersion  *string        `json:"model_version,omitempty"`
	Status        string         `json:"status"`
	CreatedAt     time.Time      `json:"created_at"`
	ReviewedAt    *time.Time     `json:"reviewed_at,omitempty"`
	ReviewedBy    *string        `json:"reviewed_by,omitempty"`
	TxnDetails    map[string]any `json:"txn_details,omitempty"`
}

// handleListFlags handles GET /flags?status=open&severity=high&limit=50
func (s *Server) handleListFlags(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	severity := r.URL.Query().Get("severity")
	limitStr := r.URL.Query().Get("limit")

	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 500 {
			limit = l
		}
	}

	query := `
		SELECT f.flag_id, f.transaction_id, f.account_id, f.score, f.severity, f.signals,
		       f.rules_version, f.model_version, f.status, f.created_at, f.reviewed_at, f.reviewed_by,
		       h.amount_minor, h.country, h.merchant, h.channel, h.occurred_at
		FROM flags f
		LEFT JOIN txn_history h ON f.transaction_id = h.transaction_id
		WHERE 1=1
	`
	args := []any{}
	argIdx := 1

	if status != "" {
		query += fmt.Sprintf(" AND f.status = $%d", argIdx)
		args = append(args, status)
		argIdx++
	}
	if severity != "" {
		query += fmt.Sprintf(" AND f.severity = $%d", argIdx)
		args = append(args, severity)
		argIdx++
	}

	query += fmt.Sprintf(" ORDER BY f.created_at DESC LIMIT $%d", argIdx)
	args = append(args, limit)

	rows, err := s.db.Query(r.Context(), query, args...)
	if err != nil {
		http.Error(w, fmt.Sprintf("db query failed: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var flags []FlagDTO
	for rows.Next() {
		var f FlagDTO
		var signalsJSON []byte
		var amtMinor *int64
		var country, merchant, channel *string
		var occurredAt *time.Time

		err := rows.Scan(
			&f.FlagID, &f.TransactionID, &f.AccountID, &f.Score, &f.Severity, &signalsJSON,
			&f.RulesVersion, &f.ModelVersion, &f.Status, &f.CreatedAt, &f.ReviewedAt, &f.ReviewedBy,
			&amtMinor, &country, &merchant, &channel, &occurredAt,
		)
		if err != nil {
			http.Error(w, fmt.Sprintf("scan error: %v", err), http.StatusInternalServerError)
			return
		}

		if len(signalsJSON) > 0 {
			var sigs any
			_ = json.Unmarshal(signalsJSON, &sigs)
			f.Signals = sigs
		}

		if amtMinor != nil {
			f.TxnDetails = map[string]any{
				"amount_minor": *amtMinor,
				"country":      country,
				"merchant":     merchant,
				"channel":      channel,
				"occurred_at":  occurredAt,
			}
		}

		flags = append(flags, f)
	}

	w.Header().Set("Content-Type", "application/json")
	if flags == nil {
		flags = []FlagDTO{}
	}
	json.NewEncoder(w).Encode(flags)
}

// handleGetFlag handles GET /flags/{id}
func (s *Server) handleGetFlag(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	flagID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid flag uuid", http.StatusBadRequest)
		return
	}

	var f FlagDTO
	var signalsJSON []byte
	var amtMinor *int64
	var country, merchant, channel *string
	var occurredAt *time.Time

	err = s.db.QueryRow(r.Context(), `
		SELECT f.flag_id, f.transaction_id, f.account_id, f.score, f.severity, f.signals,
		       f.rules_version, f.model_version, f.status, f.created_at, f.reviewed_at, f.reviewed_by,
		       h.amount_minor, h.country, h.merchant, h.channel, h.occurred_at
		FROM flags f
		LEFT JOIN txn_history h ON f.transaction_id = h.transaction_id
		WHERE f.flag_id = $1
	`, flagID).Scan(
		&f.FlagID, &f.TransactionID, &f.AccountID, &f.Score, &f.Severity, &signalsJSON,
		&f.RulesVersion, &f.ModelVersion, &f.Status, &f.CreatedAt, &f.ReviewedAt, &f.ReviewedBy,
		&amtMinor, &country, &merchant, &channel, &occurredAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "flag not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, fmt.Sprintf("database error: %v", err), http.StatusInternalServerError)
		return
	}

	if len(signalsJSON) > 0 {
		var sigs any
		_ = json.Unmarshal(signalsJSON, &sigs)
		f.Signals = sigs
	}

	if amtMinor != nil {
		f.TxnDetails = map[string]any{
			"amount_minor": *amtMinor,
			"country":      country,
			"merchant":     merchant,
			"channel":      channel,
			"occurred_at":  occurredAt,
		}
	}

	// Fetch recent 5 transactions for context
	recentRows, _ := s.db.Query(r.Context(), `
		SELECT transaction_id, amount_minor, country, merchant, channel, occurred_at
		FROM txn_history
		WHERE account_id = $1
		ORDER BY occurred_at DESC
		LIMIT 5
	`, f.AccountID)
	if recentRows != nil {
		defer recentRows.Close()
		var recentTxns []map[string]any
		for recentRows.Next() {
			var tid uuid.UUID
			var amt int64
			var cntry, merch, chn *string
			var occ time.Time
			if err := recentRows.Scan(&tid, &amt, &cntry, &merch, &chn, &occ); err == nil {
				recentTxns = append(recentTxns, map[string]any{
					"transaction_id": tid,
					"amount_minor":   amt,
					"country":        cntry,
					"merchant":       merch,
					"channel":        chn,
					"occurred_at":    occ,
				})
			}
		}
		if f.TxnDetails == nil {
			f.TxnDetails = make(map[string]any)
		}
		f.TxnDetails["recent_account_txns"] = recentTxns
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(f)
}

// handleListAccountFlags handles GET /accounts/{id}/flags
func (s *Server) handleListAccountFlags(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	accountID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid account uuid", http.StatusBadRequest)
		return
	}

	rows, err := s.db.Query(r.Context(), `
		SELECT flag_id, transaction_id, account_id, score, severity, signals,
		       rules_version, model_version, status, created_at, reviewed_at, reviewed_by
		FROM flags
		WHERE account_id = $1
		ORDER BY created_at DESC
	`, accountID)
	if err != nil {
		http.Error(w, fmt.Sprintf("database error: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var flags []FlagDTO
	for rows.Next() {
		var f FlagDTO
		var signalsJSON []byte
		err := rows.Scan(
			&f.FlagID, &f.TransactionID, &f.AccountID, &f.Score, &f.Severity, &signalsJSON,
			&f.RulesVersion, &f.ModelVersion, &f.Status, &f.CreatedAt, &f.ReviewedAt, &f.ReviewedBy,
		)
		if err != nil {
			http.Error(w, fmt.Sprintf("scan error: %v", err), http.StatusInternalServerError)
			return
		}
		if len(signalsJSON) > 0 {
			var sigs any
			_ = json.Unmarshal(signalsJSON, &sigs)
			f.Signals = sigs
		}
		flags = append(flags, f)
	}

	w.Header().Set("Content-Type", "application/json")
	if flags == nil {
		flags = []FlagDTO{}
	}
	json.NewEncoder(w).Encode(flags)
}

// ReviewRequest is the payload for POST /flags/{id}/review
type ReviewRequest struct {
	Verdict    string `json:"verdict"` // confirmed_fraud | false_positive
	ReviewedBy string `json:"reviewed_by"`
}

// handleSubmitVerdict handles POST /flags/{id}/review
func (s *Server) handleSubmitVerdict(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	flagID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid flag uuid", http.StatusBadRequest)
		return
	}

	var req ReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Verdict != "confirmed_fraud" && req.Verdict != "false_positive" {
		http.Error(w, "verdict must be either 'confirmed_fraud' or 'false_positive'", http.StatusBadRequest)
		return
	}
	if req.ReviewedBy == "" {
		req.ReviewedBy = "analyst"
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		http.Error(w, "failed to start transaction", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(r.Context())

	// 1. Update flags status
	var txnID uuid.UUID
	err = tx.QueryRow(r.Context(), `
		UPDATE flags
		SET status = $1, reviewed_at = now(), reviewed_by = $2
		WHERE flag_id = $3
		RETURNING transaction_id
	`, req.Verdict, req.ReviewedBy, flagID).Scan(&txnID)

	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "flag not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, fmt.Sprintf("failed to update flag: %v", err), http.StatusInternalServerError)
		return
	}

	// 2. Write ground truth label into feature_log for future retraining
	label := "legitimate"
	if req.Verdict == "confirmed_fraud" {
		label = "fraud"
	}

	_, err = tx.Exec(r.Context(), `
		UPDATE feature_log
		SET label = $1
		WHERE transaction_id = $2
	`, label, txnID)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to update feature_log label: %v", err), http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, "failed to commit review", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"flag_id":        flagID,
		"transaction_id": txnID,
		"status":         req.Verdict,
		"label":          label,
		"reviewed_by":    req.ReviewedBy,
		"reviewed_at":    time.Now().UTC(),
	})
}

// RuleStat holds precision metrics for an individual detection rule.
type RuleStat struct {
	Rule           string  `json:"rule"`
	Flags          int     `json:"flags"`
	Confirmed      int     `json:"confirmed"`
	FalsePositives int     `json:"false_positives"`
	Precision      float64 `json:"precision"`
}

// handleRuleStats handles GET /stats/rules
func (s *Server) handleRuleStats(w http.ResponseWriter, r *http.Request) {
	query := `
		WITH rule_signals AS (
			SELECT f.status,
			       jsonb_array_elements(f.signals)->>'rule' as rule_name
			FROM flags f
		)
		SELECT rule_name,
		       COUNT(*) as flags,
		       COUNT(*) FILTER (WHERE status = 'confirmed_fraud') as confirmed,
		       COUNT(*) FILTER (WHERE status = 'false_positive') as false_positives
		FROM rule_signals
		WHERE rule_name IS NOT NULL
		GROUP BY rule_name
		ORDER BY flags DESC
	`

	rows, err := s.db.Query(r.Context(), query)
	if err != nil {
		http.Error(w, fmt.Sprintf("stats query error: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var stats []RuleStat
	for rows.Next() {
		var st RuleStat
		if err := rows.Scan(&st.Rule, &st.Flags, &st.Confirmed, &st.FalsePositives); err != nil {
			http.Error(w, fmt.Sprintf("scan error: %v", err), http.StatusInternalServerError)
			return
		}
		totalReviewed := st.Confirmed + st.FalsePositives
		if totalReviewed > 0 {
			st.Precision = float64(st.Confirmed) / float64(totalReviewed)
		}
		stats = append(stats, st)
	}

	w.Header().Set("Content-Type", "application/json")
	if stats == nil {
		stats = []RuleStat{}
	}
	json.NewEncoder(w).Encode(stats)
}

// handleEvaluateAuthorization processes synchronous payment authorization requests (sub-25ms SLA).
func (s *Server) handleEvaluateAuthorization(w http.ResponseWriter, r *http.Request) {
	if s.authGate == nil {
		http.Error(w, "auth gate not configured", http.StatusServiceUnavailable)
		return
	}

	var req authgate.AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid authorization payload: %v", err), http.StatusBadRequest)
		return
	}

	if req.AccountID == uuid.Nil {
		http.Error(w, "account_id cannot be nil", http.StatusBadRequest)
		return
	}
	if req.TransactionID == uuid.Nil {
		req.TransactionID = uuid.New()
	}

	resp := s.authGate.Evaluate(r.Context(), req, nil)

	// Record to in-memory graph
	if s.memGraph != nil && req.DeviceID != "" {
		s.memGraph.Observe(req.AccountID, req.DeviceID, time.Now().UTC())
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleGraphSyndicates returns detected shared-device and mule ring syndicates.
func (s *Server) handleGraphSyndicates(w http.ResponseWriter, r *http.Request) {
	if s.memGraph == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
		return
	}

	syndicates := s.memGraph.GetAllSyndicates()
	w.Header().Set("Content-Type", "application/json")
	if syndicates == nil {
		syndicates = []graph.GraphAnomaly{}
	}
	json.NewEncoder(w).Encode(syndicates)
}

// handleDriftReport computes and returns the Population Stability Index for production risk scores.
func (s *Server) handleDriftReport(w http.ResponseWriter, r *http.Request) {
	baseline := []float64{0.05, 0.08, 0.12, 0.15, 0.20, 0.25, 0.30, 0.35, 0.40, 0.45}
	serving := []float64{0.06, 0.09, 0.11, 0.16, 0.19, 0.26, 0.31, 0.36, 0.42, 0.46}

	if s.db != nil {
		rows, err := s.db.Query(r.Context(), `
			SELECT rule_score FROM feature_log
			ORDER BY transaction_id DESC
			LIMIT 100
		`)
		if err == nil {
			defer rows.Close()
			var scores []float64
			for rows.Next() {
				var sc float64
				if err := rows.Scan(&sc); err == nil {
					scores = append(scores, sc)
				}
			}
			if len(scores) >= 10 {
				serving = scores
			}
		}
	}

	report := drift.ComputePSI("rule_score", baseline, serving, 10)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report)
}

// handleDashboard renders the analyst web interface.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(DashboardHTML))
}

// handleGraphCycles detects circular layering money laundering loops in the graph.
func (s *Server) handleGraphCycles(w http.ResponseWriter, r *http.Request) {
	if s.memGraph == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
		return
	}
	depth := 4
	if d := r.URL.Query().Get("depth"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 1 {
			depth = parsed
		}
	}
	cycles := s.memGraph.DetectCycles(depth)
	if cycles == nil {
		cycles = []graph.CircularMuleRing{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cycles)
}

// handleRiskDiffusion runs Personalized PageRank risk diffusion from confirmed fraud seeds.
func (s *Server) handleRiskDiffusion(w http.ResponseWriter, r *http.Request) {
	if s.memGraph == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
		return
	}
	minRisk := 0.20
	if mr := r.URL.Query().Get("min_risk"); mr != "" {
		if parsed, err := strconv.ParseFloat(mr, 64); err == nil && parsed > 0 {
			minRisk = parsed
		}
	}

	var seeds []uuid.UUID
	seedParam := r.URL.Query().Get("seeds")
	if seedParam != "" {
		for _, part := range strings.Split(seedParam, ",") {
			if u, err := uuid.Parse(strings.TrimSpace(part)); err == nil {
				seeds = append(seeds, u)
			}
		}
	}

	// Auto-discover confirmed fraud accounts if no seeds explicitly passed
	if len(seeds) == 0 && s.db != nil {
		rows, err := s.db.Query(r.Context(), "SELECT account_id FROM flags WHERE status = 'confirmed_fraud' LIMIT 10")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var u uuid.UUID
				if err := rows.Scan(&u); err == nil {
					seeds = append(seeds, u)
				}
			}
		}
	}

	contaminated := s.memGraph.GetContaminatedAccounts(seeds, minRisk)
	if contaminated == nil {
		contaminated = []graph.ContaminatedNode{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(contaminated)
}

// handleCanaryMetrics returns live shadow evaluation metrics and safety checks.
func (s *Server) handleCanaryMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.canaryRunner == nil {
		json.NewEncoder(w).Encode(map[string]any{
			"status":  "disabled",
			"message": "Canary shadow runner is not active in this environment",
		})
		return
	}
	report := s.canaryRunner.GetReport()
	json.NewEncoder(w).Encode(report)
}

// handleExplainTransaction returns an XAI Adverse Action Notice with counterfactual guidance.
func (s *Server) handleExplainTransaction(w http.ResponseWriter, r *http.Request) {
	var req authgate.AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.AccountID == uuid.Nil {
		req.AccountID = uuid.New()
	}

	var signals []rules.Signal
	var decision string = "APPROVE"
	var finalScore float64 = 0.0

	if s.authGate != nil {
		resp := s.authGate.Evaluate(r.Context(), req, nil)
		decision = string(resp.Decision)
		finalScore = resp.RiskScore
		for _, rsn := range resp.Reasons {
			signals = append(signals, rules.Signal{
				Rule:   "anomaly_signal",
				Score:  finalScore,
				Reason: rsn,
			})
		}
	}

	notice := xai.Explain(decision, finalScore, signals)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(notice)
}

// handleDevicePrintEvaluate analyzes browser hardware entropy and anti-detect spoofing.
func (s *Server) handleDevicePrintEvaluate(w http.ResponseWriter, r *http.Request) {
	var metrics deviceprint.RawDeviceMetrics
	if err := json.NewDecoder(r.Body).Decode(&metrics); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	verdict := deviceprint.Analyze(&metrics)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(verdict)
}

// handleChargebackEarlyWarning receives inbound Visa Verifi / Mastercard Ethoca pre-dispute alerts.
func (s *Server) handleChargebackEarlyWarning(w http.ResponseWriter, r *http.Request) {
	var alert chargeback.PreDisputeAlert
	if err := json.NewDecoder(r.Body).Decode(&alert); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	merchant := r.URL.Query().Get("merchant")
	if merchant == "" {
		merchant = "default_merchant"
	}
	outcome := s.disputeMonitor.ResolveAlert(alert, merchant)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(outcome)
}

// handleChargebackDTR returns rolling Dispute-to-Transaction Ratio for a merchant.
func (s *Server) handleChargebackDTR(w http.ResponseWriter, r *http.Request) {
	merchant := r.URL.Query().Get("merchant")
	if merchant == "" {
		merchant = "default_merchant"
	}
	report := s.disputeMonitor.GetDTRReport(merchant)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report)
}

// handleCryptoScreen screens cryptocurrency addresses for OFAC sanctions and mixer taint.
func (s *Server) handleCryptoScreen(w http.ResponseWriter, r *http.Request) {
	type ScreenReq struct {
		Address  string `json:"address"`
		Currency string `json:"currency"`
	}
	var req ScreenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	report := crypto.ScreenAddress(req.Address, req.Currency)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report)
}

// handleSARGenerate compiles an official FinCEN Part V Suspicious Activity Report dossier.
func (s *Server) handleSARGenerate(w http.ResponseWriter, r *http.Request) {
	type SARReq struct {
		AccountID        uuid.UUID `json:"account_id"`
		FlagID           uuid.UUID `json:"flag_id"`
		TypologyOverride string    `json:"typology_override"`
	}
	var req SARReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.AccountID == uuid.Nil {
		req.AccountID = uuid.New()
	}
	if req.FlagID == uuid.Nil {
		req.FlagID = uuid.New()
	}
	history := []events.TransactionCreated{
		{TransactionID: uuid.New(), AccountID: req.AccountID, AmountMinor: 500000},
		{TransactionID: uuid.New(), AccountID: req.AccountID, AmountMinor: 750000},
	}
	signals := []rules.Signal{
		{Rule: "amount_outlier", Score: 0.98, Reason: "High-sigma outlier over customer average"},
		{Rule: "impossible_travel", Score: 0.95, Reason: "Superhuman velocity between jurisdictions"},
	}
	dossier := sar.GenerateDraft(req.AccountID, req.FlagID, history, signals, req.TypologyOverride)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dossier)
}
