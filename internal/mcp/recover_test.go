package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

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

// A panic under a path lock releases it: the next write of the path does
// not wait for good (IMP-161, S3-13).
func TestUnderLock_ReleasesOnPanic(t *testing.T) {
	s, _, _ := newTestServer(t)
	func() {
		defer func() { _ = recover() }()
		_ = s.underLock("p/n.md", func() error { panic("boom") })
	}()
	done := make(chan struct{})
	go func() {
		s.vault.LockPath("p/n.md")()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the path lock stayed taken after a panic")
	}
}
