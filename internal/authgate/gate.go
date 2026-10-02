package authgate

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"fraud-service/internal/events"
	"fraud-service/internal/graph"
	"fraud-service/internal/rules"
	"fraud-service/internal/scorer"
)

// Decision defines the authorization policy outcome.
type Decision string

const (
	DecisionApprove      Decision = "APPROVE"
	DecisionChallenge3DS Decision = "CHALLENGE_3DS"
	DecisionDecline      Decision = "DECLINE"
)

// AuthRequest represents an inbound synchronous payment authorization request.
type AuthRequest struct {
	TransactionID uuid.UUID `json:"transaction_id"`
	AccountID     uuid.UUID `json:"account_id"`
	AmountMinor   int64     `json:"amount_minor"`
	Currency      string    `json:"currency"`
	Merchant      string    `json:"merchant"`
	MCC           string    `json:"mcc"`
	Country       string    `json:"country"`
	Channel       string    `json:"channel"`
	DeviceID      string    `json:"device_id"`
}

// AuthResponse represents the synchronous response returned to the payment gateway within the latency SLA budget.
type AuthResponse struct {
	Decision      Decision       `json:"decision"`
	RiskScore     float64        `json:"risk_score"`
	Severity      string         `json:"severity"`
	EvaluationMs  float64        `json:"evaluation_ms"`
	Reasons       []string       `json:"reasons"`
	Signals       []rules.Signal `json:"signals"`
	GraphAnomaly  *graph.GraphAnomaly `json:"graph_anomaly,omitempty"`
	FailOpen      bool           `json:"fail_open,omitempty"`
}

// Gate evaluates authorization transactions within a strict latency SLA (e.g. < 25ms).
type Gate struct {
	engine     *rules.Engine
	graph      *graph.MemoryGraph
	scorer     scorer.Scorer
	slaBudget  time.Duration
	failOpen   bool
}

// NewGate constructs a synchronous authorization risk evaluator.
func NewGate(engine *rules.Engine, memGraph *graph.MemoryGraph, sc scorer.Scorer, slaBudget time.Duration, failOpen bool) *Gate {
	if slaBudget <= 0 {
		slaBudget = 25 * time.Millisecond
	}
	return &Gate{
		engine:    engine,
		graph:     memGraph,
		scorer:    sc,
		slaBudget: slaBudget,
		failOpen:  failOpen,
	}
}

// Evaluate synchronously decides whether to APPROVE, CHALLENGE_3DS, or DECLINE a payment.
func (g *Gate) Evaluate(ctx context.Context, req AuthRequest, f *rules.Features) *AuthResponse {
	start := time.Now()

	// 1. Create timeout context strictly enforcing the authorization SLA budget
	evalCtx, cancel := context.WithTimeout(ctx, g.slaBudget)
	defer cancel()

	if f == nil {
		// Minimum baseline fallback
		f = &rules.Features{
			Txn: events.TransactionCreated{
				TransactionID: req.TransactionID,
				AccountID:     req.AccountID,
				AmountMinor:   req.AmountMinor,
				Currency:      req.Currency,
				Merchant:      req.Merchant,
				Country:       req.Country,
				Channel:       req.Channel,
				DeviceID:      req.DeviceID,
			},
			At: time.Now().UTC(),
		}
	}

	// 2. Evaluate Rule Signals
	signals := g.engine.Run(f)

	// 3. Inspect In-Memory Graph for Device Syndicates & Mule Rings
	var graphAnomaly *graph.GraphAnomaly
	if g.graph != nil {
		graphAnomaly = g.graph.AnalyzeAccount(req.AccountID, req.DeviceID)
		if graphAnomaly != nil {
			if sig := graphAnomaly.AsRuleSignal(); sig != nil {
				signals = append(signals, *sig)
			}
		}
	}

	// 4. Combine deterministic signals via Noisy-OR
	combinedScore := rules.Combine(signals)

	// 5. ML Fast-Path Scoring (if available and within remaining latency budget)
	if g.scorer != nil && !g.scorer.IsShadowMode() {
		select {
		case <-evalCtx.Done():
			// Latency budget exhausted; degrade to rules score immediately
		default:
			if resp, err := g.scorer.Score(evalCtx, f); err == nil && resp != nil {
				if resp.Score > combinedScore {
					combinedScore = resp.Score
				}
			}
		}
	}

	elapsed := time.Since(start)
	elapsedMs := float64(elapsed.Microseconds()) / 1000.0

	// 6. Map combined score to Gate Decision Policy
	var decision Decision
	var reasons []string

	for _, s := range signals {
		reasons = append(reasons, fmt.Sprintf("[%s] %s", s.Rule, s.Reason))
	}

	switch {
	case combinedScore >= 0.75:
		decision = DecisionDecline
	case combinedScore >= 0.40:
		decision = DecisionChallenge3DS
	default:
		decision = DecisionApprove
	}

	// If SLA expired and fail-open is enabled
	isFailOpen := false
	if evalCtx.Err() == context.DeadlineExceeded && g.failOpen && decision == DecisionDecline {
		decision = DecisionChallenge3DS
		isFailOpen = true
		reasons = append(reasons, "SLA deadline exceeded: fail-open policy applied (challenge 3DS)")
	}

	return &AuthResponse{
		Decision:     decision,
		RiskScore:    combinedScore,
		Severity:     rules.SeverityBand(combinedScore),
		EvaluationMs: elapsedMs,
		Reasons:      reasons,
		Signals:      signals,
		GraphAnomaly: graphAnomaly,
		FailOpen:     isFailOpen,
	}
}
