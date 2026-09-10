package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/server/events"
)

type waitResult struct {
	Events []waitEvent `json:"events"`
	Cursor uint64      `json:"cursor"`
	Resync bool        `json:"resync"`
	Timed  bool        `json:"timed_out"`
}

func waitCall(t *testing.T, s *Server, ctx context.Context, args map[string]any) waitResult {
	t.Helper()
	res, _ := s.handleWaitChanges(ctx, call(args))
	var out waitResult
	decodeResult(t, res, &out)
	return out
}

func TestMCP_WaitChangesWakesOnWrite(t *testing.T) {
	s, _, _ := newTestServer(t)
	hub := events.New(events.HubOptions{})
	s.SetEvents(hub)
	ctx := context.Background()

	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = s.handleCreate(ctx, call(map[string]any{"path": "p/x.md", "content": "# X"}))
	}()

	start := time.Now()
	out := waitCall(t, s, ctx, map[string]any{"timeout_s": 5})
	if len(out.Events) == 0 || out.Events[0].Path != "p/x.md" || out.Events[0].Action != "create" {
		t.Fatalf("wait result = %+v", out)
	}
	if out.Resync || out.Timed || out.Cursor == 0 {
		t.Fatalf("wait flags = %+v", out)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("wait did not wake promptly on write")
	}
}

func TestMCP_WaitChangesReplaysHandoffGap(t *testing.T) {
	s, _, _ := newTestServer(t)
	hub := events.New(events.HubOptions{})
	s.SetEvents(hub)
	ctx := context.Background()

	cursor := int(hub.Seq()) // int: the JSON layer delivers numbers, not uint64
	path := createTestHandoff(t, s, ctx)

	// The handoff was created after our cursor: no blocking, immediate replay.
	out := waitCall(t, s, ctx, map[string]any{"cursor": cursor, "timeout_s": 30})
	if len(out.Events) == 0 || !strings.Contains(out.Events[0].Path, "/handoffs/") || out.Events[0].Path != path {
		t.Fatalf("replay result = %+v", out)
	}
	if out.Resync || out.Cursor <= uint64(cursor) {
		t.Fatalf("replay flags = %+v", out)
	}

	// Resuming from the returned cursor with nothing new: clean timeout.
	out2 := waitCall(t, s, ctx, map[string]any{"cursor": int(out.Cursor), "timeout_s": 1})
	if len(out2.Events) != 0 || !out2.Timed || out2.Resync {
		t.Fatalf("idle wait = %+v", out2)
	}
}

func TestMCP_WaitChangesScopeFilter(t *testing.T) {
	s, _, _ := newTestServer(t)
	hub := events.New(events.HubOptions{})
	s.SetEvents(hub)
	admin := context.Background()

	other := &auth.Token{ID: "qqqq1111", Name: "agent-q", Project: "q", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}}
	cursor := int(hub.Seq())
	_, _ = s.handleCreate(admin, call(map[string]any{"path": "p/secret.md", "content": "# P"}))

	// The p/ write is outside agent-q's scope: filtered out, clean timeout.
	out := waitCall(t, s, ctxWithToken(other), map[string]any{"cursor": cursor, "timeout_s": 1})
	if len(out.Events) != 0 || !out.Timed {
		t.Fatalf("scoped wait leaked events: %+v", out)
	}

	// Explicit project outside scope is rejected up front.
	res, _ := s.handleWaitChanges(ctxWithToken(other), call(map[string]any{"project": "p"}))
	expectError(t, res)
}

func TestMCP_WaitChangesResyncWhenCursorTooOld(t *testing.T) {
	s, _, _ := newTestServer(t)
	hub := events.New(events.HubOptions{RingLen: 2})
	s.SetEvents(hub)
	ctx := context.Background()

	for _, p := range []string{"p/a.md", "p/b.md", "p/c.md"} {
		_, _ = s.handleCreate(ctx, call(map[string]any{"path": p, "content": "# n"}))
	}
	// 6 events published (note+tree each), ring keeps 2: cursor 1 is long gone.
	out := waitCall(t, s, ctx, map[string]any{"cursor": 1, "timeout_s": 5})
	if !out.Resync {
		t.Fatalf("expected resync, got %+v", out)
	}
	if out.Cursor != hub.Seq() {
		t.Fatalf("resync cursor = %d, want %d", out.Cursor, hub.Seq())
	}
}

func TestMCP_WaitChangesOneWaiterPerSession(t *testing.T) {
	s, _, _ := newTestServer(t)
	hub := events.New(events.HubOptions{})
	s.SetEvents(hub)
	ctx := context.Background()

	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(started)
		_, _ = s.handleWaitChanges(ctx, call(map[string]any{"timeout_s": 2}))
	}()
	<-started
	// Readiness sleep (BUG-032 survey): the first waiter has no observable
	// "in flight" state other than the error the second call gets; the
	// window only makes the negative assertion more likely, never a false
	// failure of a positive one.
	time.Sleep(100 * time.Millisecond)

	res, _ := s.handleWaitChanges(ctx, call(map[string]any{"timeout_s": 1}))
	if msg := expectError(t, res); !strings.Contains(msg, "already in flight") {
		t.Fatalf("second waiter error = %q", msg)
	}
	<-done
}
