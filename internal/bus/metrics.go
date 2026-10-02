package bus

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	EventsProcessedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "pipeline",
			Subsystem: "consumer",
			Name:      "events_processed_total",
			Help:      "Total number of events successfully processed.",
		},
		[]string{"consumer"},
	)

	DuplicatesSkippedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "pipeline",
			Subsystem: "consumer",
			Name:      "duplicates_skipped_total",
			Help:      "Total number of duplicate events skipped due to idempotency check.",
		},
		[]string{"consumer"},
	)

	RetriesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "pipeline",
			Subsystem: "consumer",
			Name:      "retries_total",
			Help:      "Total number of retries scheduled to retry topics.",
		},
		[]string{"consumer", "tier"},
	)

	DLQTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "pipeline",
			Subsystem: "consumer",
			Name:      "dlq_total",
			Help:      "Total number of events forwarded to DLQ.",
		},
		[]string{"consumer", "error_kind"},
	)

	ProcessingDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "pipeline",
			Subsystem: "consumer",
			Name:      "processing_seconds",
			Help:      "Time taken to process an event handler.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"consumer"},
	)
)
