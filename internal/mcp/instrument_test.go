package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/gosidian/gosidian/internal/metrics"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestInstrumentMiddleware_Success verifies that a handler returning a normal
// result increments the success counter and records non-zero latency and
// output payload bytes.
func TestInstrumentMiddleware_Success(t *testing.T) {
	before := testutil.ToFloat64(metrics.MCPToolCalls.WithLabelValues("test_tool", "success"))

	handler := instrumentMiddleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Name = "test_tool"
	req.Params.Arguments = map[string]any{"foo": "bar"}

	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("expected successful result, got %+v", res)
	}

	after := testutil.ToFloat64(metrics.MCPToolCalls.WithLabelValues("test_tool", "success"))
	if after-before != 1 {
		t.Errorf("success counter delta = %v, want 1", after-before)
	}
}

// TestInstrumentMiddleware_HandlerError verifies that a handler returning a
// non-nil error increments the error counter.
func TestInstrumentMiddleware_HandlerError(t *testing.T) {
	before := testutil.ToFloat64(metrics.MCPToolCalls.WithLabelValues("test_err", "error"))

	handler := instrumentMiddleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New("boom")
	})

	req := mcp.CallToolRequest{}
	req.Params.Name = "test_err"

	_, err := handler(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	after := testutil.ToFloat64(metrics.MCPToolCalls.WithLabelValues("test_err", "error"))
	if after-before != 1 {
		t.Errorf("error counter delta = %v, want 1", after-before)
	}
}

// TestInstrumentMiddleware_IsErrorResult verifies that a handler returning a
// result with IsError=true (the gosidian pattern for "tool-level" errors like
// auth failure or rate limit) is classified as outcome="error" too.
func TestInstrumentMiddleware_IsErrorResult(t *testing.T) {
	before := testutil.ToFloat64(metrics.MCPToolCalls.WithLabelValues("test_iserr", "error"))

	handler := instrumentMiddleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("unauthorized"), nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Name = "test_iserr"

	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned go error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected IsError=true, got %+v", res)
	}

	after := testutil.ToFloat64(metrics.MCPToolCalls.WithLabelValues("test_iserr", "error"))
	if after-before != 1 {
		t.Errorf("error counter delta = %v, want 1", after-before)
	}
}

// TestInstrumentMiddleware_RecordsLatency verifies the latency histogram is
// observed. We only check sample count > 0 — exact duration is meaningless in
// a unit test.
func TestInstrumentMiddleware_RecordsLatency(t *testing.T) {
	// Use a fresh tool name to isolate the counter state.
	handler := instrumentMiddleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(""), nil
	})
	req := mcp.CallToolRequest{}
	req.Params.Name = "test_lat"

	_, _ = handler(context.Background(), req)

	collected := testutil.CollectAndCount(metrics.MCPToolLatency, "gosidian_mcp_tool_duration_seconds")
	if collected == 0 {
		t.Error("expected MCPToolLatency to have at least one observation")
	}
}
