// Package mcp — instrumentation middleware.
//
// instrumentMiddleware wraps every tool handler to emit Prometheus metrics and
// a single structured slog line per invocation. Registered once in New() via
// server.WithToolHandlerMiddleware so handlers themselves stay clean.
//
// Metrics:
//   - gosidian_mcp_tool_calls_total{tool, outcome}
//   - gosidian_mcp_tool_duration_seconds{tool, outcome}
//   - gosidian_mcp_tool_payload_bytes{tool, direction}
//
// Outcome is "success" or "error". Rate-limited rejects are counted
// separately by gosidian_mcp_rate_limit_hits_total (via checkWriteLimits), so
// outcome="error" conflates them with real errors — deliberately, to keep the
// label cardinality low.
package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/metrics"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// instrumentMiddleware is the single middleware attached to the MCP server.
// It deliberately computes payload sizes via json.Marshal approximation — the
// wire format passes through the mark3labs SDK internals and isn't directly
// observable here. The approximation is within a few percent for all tool
// shapes in this codebase and is fine for histogram buckets.
func instrumentMiddleware(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		toolName := req.Params.Name
		started := time.Now()

		// Input payload size: marshal the arguments map. Missing/empty args
		// yields "null" (4 bytes) — still a useful baseline in the histogram.
		var payloadIn int
		if buf, err := json.Marshal(req.Params.Arguments); err == nil {
			payloadIn = len(buf)
		}

		result, handlerErr := next(ctx, req)

		outcome := "success"
		errMsg := ""
		if handlerErr != nil {
			outcome = "error"
			errMsg = handlerErr.Error()
		} else if result != nil && result.IsError {
			outcome = "error"
			errMsg = firstTextContent(result)
		}

		// Output payload size: marshal whatever result we have. Nil result
		// (rare, only when next returned error without a result) stays at 0.
		var payloadOut int
		if result != nil {
			if buf, err := json.Marshal(result); err == nil {
				payloadOut = len(buf)
			}
		}

		dur := time.Since(started)

		metrics.MCPToolCalls.WithLabelValues(toolName, outcome).Inc()
		metrics.MCPToolLatency.WithLabelValues(toolName, outcome).Observe(dur.Seconds())
		metrics.MCPToolPayloadBytes.WithLabelValues(toolName, "in").Observe(float64(payloadIn))
		metrics.MCPToolPayloadBytes.WithLabelValues(toolName, "out").Observe(float64(payloadOut))

		attrs := []any{
			"tool", toolName,
			"outcome", outcome,
			"latency_ms", dur.Milliseconds(),
			"payload_in", payloadIn,
			"payload_out", payloadOut,
		}
		if tid := tokenIDFromContext(ctx); tid != "" {
			attrs = append(attrs, "token_id", tid)
		}
		if cid := correlationIDFromContext(ctx); cid != "" {
			attrs = append(attrs, "correlation_id", cid)
		}
		if errMsg != "" {
			attrs = append(attrs, "error", errMsg)
		}
		slog.Info("mcp.call", attrs...)

		return result, handlerErr
	}
}

// tokenIDFromContext returns the 8-hex id of the authenticated token if any.
// Empty when the call arrived on an unauthenticated path (tests).
func tokenIDFromContext(ctx context.Context) string {
	if tok, ok := ctx.Value(tokenCtxKey).(*auth.Token); ok && tok != nil {
		return tok.ID
	}
	return ""
}

// firstTextContent extracts the first TextContent string from a tool result.
// Used to surface the error message into the structured log when IsError=true.
func firstTextContent(r *mcp.CallToolResult) string {
	for _, c := range r.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
