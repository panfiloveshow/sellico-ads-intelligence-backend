// Package metrics provides Prometheus business metrics for the Sellico platform.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// SyncTotal counts workspace sync operations by status (ok, partial, failed).
	SyncTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "sellico",
		Subsystem: "sync",
		Name:      "total",
		Help:      "Total number of workspace sync operations by result status.",
	}, []string{"status"})

	// SyncDuration measures sync operation duration in seconds.
	SyncDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "sellico",
		Subsystem: "sync",
		Name:      "duration_seconds",
		Help:      "Duration of workspace sync operations.",
		Buckets:   []float64{1, 5, 15, 30, 60, 120, 300, 600},
	})

	// RecommendationsGenerated counts recommendations generated per run.
	RecommendationsGenerated = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "sellico",
		Subsystem: "recommendations",
		Name:      "generated_total",
		Help:      "Total number of recommendations generated.",
	})

	// ExportsTotal counts export jobs by format.
	ExportsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "sellico",
		Subsystem: "exports",
		Name:      "total",
		Help:      "Total number of export jobs by format.",
	}, []string{"format"})

	// WBAPIRequests counts WB API requests by endpoint and result.
	WBAPIRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "sellico",
		Subsystem: "wb_api",
		Name:      "requests_total",
		Help:      "Total WB API requests by path and result.",
	}, []string{"path", "status"})

	// WBAPILatency measures WB API response time.
	WBAPILatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "sellico",
		Subsystem: "wb_api",
		Name:      "latency_seconds",
		Help:      "WB API request latency in seconds.",
		Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"path"})

	// ActiveWorkspaces tracks the number of workspaces that performed a sync recently.
	ActiveWorkspaces = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "sellico",
		Subsystem: "workspaces",
		Name:      "active",
		Help:      "Number of workspaces with recent sync activity.",
	})
)
