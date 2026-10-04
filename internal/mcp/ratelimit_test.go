package mcp

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWriteLimiter_Allow(t *testing.T) {
	l := newWriteLimiter(3)
	for i := 0; i < 3; i++ {
		if ok, _, _ := l.Allow("tok1", ""); !ok {
			t.Errorf("attempt %d should be allowed", i)
		}
	}
	ok, wait, tokenLevel := l.Allow("tok1", "")
	if ok || tokenLevel || wait <= 0 || wait > time.Minute {
		t.Errorf("4th attempt: ok=%v wait=%v tokenLevel=%v, want a session refusal with a wait under a minute", ok, wait, tokenLevel)
	}
	// Different token has its own bucket
	if ok, _, _ := l.Allow("tok2", ""); !ok {
		t.Errorf("tok2 should be allowed independently")
	}
}

// Sessions of one token count apart, up to the token's shared cap
// (IMP-141): two agents on one token no longer take each other's budget,
// and many sessions together still stop at tokenLimitFactor times one.
func TestWriteLimiter_Sessions(t *testing.T) {
	l := newWriteLimiter(2)
	for _, sess := range []string{"a", "a", "b", "b"} {
		if ok, _, _ := l.Allow("tok", sess); !ok {
			t.Fatalf("session %s refused within its own cap", sess)
		}
	}
	if ok, _, tokenLevel := l.Allow("tok", "a"); ok || tokenLevel {
		t.Errorf("session a over its cap: ok=%v tokenLevel=%v", ok, tokenLevel)
	}
	// Fill the token's cap (2×5 = 10) with more sessions.
	for _, sess := range []string{"c", "c", "d", "d", "e", "e"} {
		if ok, _, _ := l.Allow("tok", sess); !ok {
			t.Fatalf("session %s refused before the token's cap", sess)
		}
	}
	ok, wait, tokenLevel := l.Allow("tok", "f")
	if ok || !tokenLevel || wait <= 0 {
		t.Errorf("a fresh session past the token's cap: ok=%v tokenLevel=%v wait=%v", ok, tokenLevel, wait)
	}
	st := l.Stats("tok", "f")
	if st.MaxPerMinute != 2 || st.Used != 0 || st.Remaining != 0 || st.TokenMaxPerMinute != 10 || st.TokenUsed != 10 {
		t.Errorf("stats = %+v", st)
	}
	if st := l.Stats("tok", "a"); st.Used != 2 || st.OldestUnixS == 0 {
		t.Errorf("session stats = %+v", st)
	}
}

// A bucket whose writes all left the window is dropped by the sweep, so the
// map does not keep every session that ever wrote.
func TestWriteLimiter_Sweep(t *testing.T) {
	l := newWriteLimiter(5)
	l.Allow("tok", "old")
	old := time.Now().Add(-2 * time.Minute)
	l.hits[sessionBucket("tok", "old")] = []time.Time{old}
	l.hits[tokenBucket("tok")] = []time.Time{old}
	l.lastSweep = old
	l.Allow("tok", "new")
	if _, ok := l.hits[sessionBucket("tok", "old")]; ok {
		t.Error("the idle session's bucket survived the sweep")
	}
	if n := len(l.hits[tokenBucket("tok")]); n != 1 {
		t.Errorf("token bucket has %d hits, want the new one only", n)
	}
}

func TestWriteLimiter_Disabled(t *testing.T) {
	l := newWriteLimiter(0)
	for i := 0; i < 1000; i++ {
		if ok, _, _ := l.Allow("x", ""); !ok {
			t.Errorf("disabled limiter should always allow")
			break
		}
	}
}

func TestRetrySeconds(t *testing.T) {
	for d, want := range map[time.Duration]int{0: 1, 300 * time.Millisecond: 1, 1500 * time.Millisecond: 2, 59 * time.Second: 59} {
		if got := retrySeconds(d); got != want {
			t.Errorf("retrySeconds(%v) = %d, want %d", d, got, want)
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
	// Third blocked, and the agent is told when to retry.
	r, _ := s.handleCreate(ctx, call(map[string]any{"path": "c.md", "content": "x"}))
	msg := expectError(t, r)
	for _, want := range []string{"rate limit exceeded for this session", "at most 2 writes per minute", "the token allows 10", "Nothing was written", "Retry in "} {
		if !strings.Contains(msg, want) {
			t.Errorf("rate limit error lacks %q: %s", want, msg)
		}
	}
	// Another MCP session of the same token has its own budget.
	other := context.WithValue(ctx, sessionCtxKey, "other")
	if r, _ := s.handleCreate(other, call(map[string]any{"path": "c.md", "content": "x"})); r.IsError {
		t.Errorf("another session should pass: %s", resultText(t, r))
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
