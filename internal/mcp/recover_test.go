package mcp

import (
	"context"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// A panic in a tool answers that call with an error result, through the
// real JSON-RPC dispatch, and the server keeps serving (IMP-086).
func TestRecoverMiddleware_PanicBecomesToolError(t *testing.T) {
	s, _, _ := newTestServer(t)
	s.impl.AddTool(mcplib.NewTool("test_panic"), func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		var m map[string]int
		m["boom"] = 1 // a runtime panic: assignment to a nil map
		return nil, nil
	})
	ctx := context.Background()
	out := rpcMessage(t, s, ctx, "tools/call", `{"name":"test_panic","arguments":{}}`)
	if !strings.Contains(out, `"isError":true`) || !strings.Contains(out, "internal error in test_panic") {
		t.Fatalf("panic result = %s", out)
	}
	out = rpcMessage(t, s, ctx, "tools/call", `{"name":"memory_list_projects","arguments":{}}`)
	if strings.Contains(out, `"isError":true`) || strings.Contains(out, `"error":`) {
		t.Fatalf("the next call = %s", out)
	}
}
