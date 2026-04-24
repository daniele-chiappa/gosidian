package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestWriteLimiter_Allow(t *testing.T) {
	l := newWriteLimiter(3)
	for i := 0; i < 3; i++ {
		if !l.Allow("tok1") {
			t.Errorf("attempt %d should be allowed", i)
		}
	}
	if l.Allow("tok1") {
		t.Errorf("4th attempt should be blocked")
	}
	// Different token has its own bucket
	if !l.Allow("tok2") {
		t.Errorf("tok2 should be allowed independently")
	}
}

func TestWriteLimiter_Disabled(t *testing.T) {
	l := newWriteLimiter(0)
	for i := 0; i < 1000; i++ {
		if !l.Allow("x") {
			t.Errorf("disabled limiter should always allow")
			break
		}
	}
}

func TestMCP_WriteLimit(t *testing.T) {
	s, _, _ := newTestServer(t)
	s.SetWriteLimits(2, 0)
	ctx := context.Background()

	// Two creates allowed
	if r, _ := s.handleCreate(ctx, call(map[string]any{"path": "a.md", "content": "x"})); r.IsError {
		t.Fatalf("first create should pass")
	}
	if r, _ := s.handleCreate(ctx, call(map[string]any{"path": "b.md", "content": "x"})); r.IsError {
		t.Fatalf("second create should pass")
	}
	// Third blocked
	r, _ := s.handleCreate(ctx, call(map[string]any{"path": "c.md", "content": "x"}))
	msg := expectError(t, r)
	if !strings.Contains(msg, "rate limit") {
		t.Errorf("expected rate limit error, got: %s", msg)
	}
}

func TestMCP_SizeLimit(t *testing.T) {
	s, _, _ := newTestServer(t)
	s.SetWriteLimits(0, 16) // 16 bytes max
	ctx := context.Background()
	big := strings.Repeat("X", 50)
	r, _ := s.handleCreate(ctx, call(map[string]any{"path": "big.md", "content": big}))
	msg := expectError(t, r)
	if !strings.Contains(msg, "exceeds limit") {
		t.Errorf("expected size limit error, got: %s", msg)
	}
}
