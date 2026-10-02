package scorer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sony/gobreaker"
	"fraud-service/internal/rules"
)

var (
	MLUnavailableTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "fraud_ml_unavailable_total",
		Help: "Total count of ML sidecar call failures or circuit breaker trips, causing graceful degradation to rules-only.",
	})

	MLLatencyHistogram = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "fraud_ml_latency_seconds",
		Help:    "Latency histogram of ML sidecar scoring calls in seconds.",
		Buckets: []float64{0.005, 0.010, 0.025, 0.050, 0.100, 0.150, 0.250, 0.500},
	})
)

// FeatureVector maps raw features into the normalized vector expected by the ML model.
type FeatureVector struct {
	AmountMinor       int64   `json:"amount_minor"`
	AmountToMean      float64 `json:"amount_to_mean"`
	ZScore            float64 `json:"z_score"`
	Count1m           int     `json:"count_1m"`
	Count10m          int     `json:"count_10m"`
	Sum1h             int64   `json:"sum_1h"`
	IsNewCountry      int     `json:"is_new_country"`
	IsNewMerchant     int     `json:"is_new_merchant"`
	HourSin           float64 `json:"hour_sin"`
	HourCos           float64 `json:"hour_cos"`
	Channel           string  `json:"channel"`
	SecsSinceLastTxn  float64 `json:"secs_since_last_txn"`
	AccountAgeDays    float64 `json:"account_age_days"`
}

// ScoreRequest is the payload sent to the FastAPI ML sidecar.
type ScoreRequest struct {
	Features FeatureVector `json:"features"`
}

// ScoreResponse is the response returned by the ML sidecar.
type ScoreResponse struct {
	Score        float64 `json:"score"`
	ModelVersion string  `json:"model_version"`
}

// Scorer defines the interface for ML scoring.
type Scorer interface {
	Score(ctx context.Context, f *rules.Features) (*ScoreResponse, error)
	IsShadowMode() bool
}

// HTTPScorer calls the external FastAPI scorer sidecar with circuit breaking and tight latency bounds.
type HTTPScorer struct {
	endpoint   string
	client     *http.Client
	breaker    *gobreaker.CircuitBreaker
	shadowMode bool
	timeout    time.Duration
}

// NewHTTPScorer constructs an HTTP-based ML scorer client with 150ms default timeout and circuit breaking.
func NewHTTPScorer(endpoint string, shadowMode bool, timeout time.Duration) *HTTPScorer {
	if timeout <= 0 {
		timeout = 150 * time.Millisecond
	}

	cbSettings := gobreaker.Settings{
		Name:        "fraud-ml-scorer",
		MaxRequests: 3,
		Interval:    10 * time.Second,
		Timeout:     5 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
			return counts.Requests >= 5 && failureRatio >= 0.5
		},
	}

	return &HTTPScorer{
		endpoint:   endpoint,
		shadowMode: shadowMode,
		timeout:    timeout,
		client: &http.Client{
			Timeout: timeout,
		},
		breaker: gobreaker.NewCircuitBreaker(cbSettings),
	}
}

func (s *HTTPScorer) IsShadowMode() bool {
	return s.shadowMode
}

// BuildFeatureVector converts rules.Features into a tabular feature vector.
func BuildFeatureVector(f *rules.Features) FeatureVector {
	typicalSpend := math.Exp(f.MeanLog)
	amountToMean := 1.0
	if typicalSpend > 0 {
		amountToMean = float64(f.Txn.AmountMinor) / typicalSpend
	}

	std := math.Max(f.StdLog, 0.3)
	zScore := 0.0
	if f.ProfileN >= 2 {
		zScore = (math.Log(float64(f.Txn.AmountMinor)) - f.MeanLog) / std
	}

	isNewCountry := 0
	if f.Txn.Country != "" && f.KnownCountries != nil {
		if _, exists := f.KnownCountries[f.Txn.Country]; !exists && len(f.KnownCountries) > 0 {
			isNewCountry = 1
		}
	}

	isNewMerchant := 1
	if f.MerchantSeen {
		isNewMerchant = 0
	}

	hour := float64(f.At.Hour())
	hourSin := math.Sin(2.0 * math.Pi * hour / 24.0)
	hourCos := math.Cos(2.0 * math.Pi * hour / 24.0)

	secsSinceLast := -1.0
	if !f.LastTxnAt.IsZero() {
		diff := f.At.Sub(f.LastTxnAt).Seconds()
		if diff >= 0 {
			secsSinceLast = diff
		}
	}

	return FeatureVector{
		AmountMinor:      f.Txn.AmountMinor,
		AmountToMean:     amountToMean,
		ZScore:           zScore,
		Count1m:          f.Count1m,
		Count10m:         f.Count10m,
		Sum1h:            f.Sum1h,
		IsNewCountry:     isNewCountry,
		IsNewMerchant:    isNewMerchant,
		HourSin:          hourSin,
		HourCos:          hourCos,
		Channel:          f.Txn.Channel,
		SecsSinceLastTxn: secsSinceLast,
		AccountAgeDays:   f.AccountAge.Hours() / 24.0,
	}
}

// Score executes the HTTP call within a bounded context and circuit breaker.
func (s *HTTPScorer) Score(ctx context.Context, f *rules.Features) (*ScoreResponse, error) {
	vec := BuildFeatureVector(f)
	reqBody, err := json.Marshal(ScoreRequest{Features: vec})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal score request: %w", err)
	}

	start := time.Now()
	val, err := s.breaker.Execute(func() (any, error) {
		reqCtx, cancel := context.WithTimeout(ctx, s.timeout)
		defer cancel()

		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, s.endpoint+"/score", bytes.NewReader(reqBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := s.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("ml scorer returned HTTP %d", resp.StatusCode)
		}

		var res ScoreResponse
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			return nil, err
		}
		return &res, nil
	})

	if err != nil {
		MLUnavailableTotal.Inc()
		return nil, err
	}

	MLLatencyHistogram.Observe(time.Since(start).Seconds())
	res, ok := val.(*ScoreResponse)
	if !ok || res == nil {
		MLUnavailableTotal.Inc()
		return nil, errors.New("invalid response from scorer")
	}

	return res, nil
}
