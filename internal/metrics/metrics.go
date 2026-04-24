// Package metrics defines the Prometheus collectors gosidian exposes on
// /metrics. Counters/gauges are package-level so handlers can use them
// without dependency injection. The /metrics handler itself is wired in
// the server package.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gosidian_http_requests_total",
			Help: "HTTP requests handled by the web server.",
		},
		[]string{"method", "route", "status"},
	)
	HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gosidian_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)
	MCPToolCalls = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gosidian_mcp_tool_calls_total",
			Help: "MCP tool invocations grouped by tool name and outcome.",
		},
		[]string{"tool", "outcome"},
	)
	MCPRateLimitHits = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gosidian_mcp_rate_limit_hits_total",
			Help: "Times an MCP write was rejected by the per-token rate limiter.",
		},
	)
	GitSyncCommits = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gosidian_git_sync_commits_total",
			Help: "Auto commits performed by the git sync, by outcome.",
		},
		[]string{"outcome"},
	)
	// GitSyncStatus exposes the current gitsync health as a tri-state gauge.
	// 0 = disabled, 1 = healthy, 2 = degraded. Read by /healthz and scraped by
	// Prometheus so operators can alert on prolonged degradation.
	GitSyncStatus = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gosidian_gitsync_status",
			Help: "Git sync subsystem status: 0=disabled, 1=healthy, 2=degraded.",
		},
	)
	// MCPToolLatency measures per-call duration for every MCP tool invocation,
	// tagged by tool name and outcome (success/error/rate_limited). Default
	// buckets already cover sub-second ops well.
	MCPToolLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gosidian_mcp_tool_duration_seconds",
			Help:    "MCP tool call duration in seconds, by tool and outcome.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"tool", "outcome"},
	)
	// MCPToolPayloadBytes measures input and output payload sizes per MCP tool.
	// Buckets target typical note sizes (100 B to 1 MiB). Direction is "in" for
	// request params and "out" for the tool result JSON.
	MCPToolPayloadBytes = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gosidian_mcp_tool_payload_bytes",
			Help:    "MCP tool payload size in bytes, by tool and direction (in/out).",
			Buckets: []float64{100, 1_000, 10_000, 100_000, 1_000_000, 10_000_000},
		},
		[]string{"tool", "direction"},
	)
	NotesGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gosidian_notes_total",
			Help: "Number of notes in the index.",
		},
	)
)

// Register installs all collectors on the default registry. Idempotent for
// tests via prometheus.Registerer interface guard.
func Register() {
	for _, c := range []prometheus.Collector{
		HTTPRequestsTotal, HTTPRequestDuration,
		MCPToolCalls, MCPRateLimitHits,
		MCPToolLatency, MCPToolPayloadBytes,
		GitSyncCommits, GitSyncStatus, NotesGauge,
	} {
		_ = prometheus.Register(c)
	}
}

// Handler returns the http.Handler serving /metrics.
func Handler() http.Handler {
	return promhttp.Handler()
}

// ObserveHTTP wraps a single request: records duration and status. Use as a
// middleware-style helper in a handler that already knows its route name.
func ObserveHTTP(method, route string, status int, started time.Time) {
	HTTPRequestsTotal.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	HTTPRequestDuration.WithLabelValues(method, route).Observe(time.Since(started).Seconds())
}
