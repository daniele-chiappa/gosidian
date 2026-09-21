package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/server/events"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// fakeSession stands in for the transport session mcp-go attaches to the
// context before invoking the context func.
type fakeSession struct{ id string }

func (f fakeSession) Initialize()                                            {}
func (f fakeSession) Initialized() bool                                      { return true }
func (f fakeSession) NotificationChannel() chan<- mcplib.JSONRPCNotification { return nil }
func (f fakeSession) SessionID() string                                      { return f.id }

// Regression for BUG-053: the context func runs once per JSON-RPC message
// on both transports, so a random id minted there never tagged a session.
func TestHTTPContext_CorrelationFollowsSession(t *testing.T) {
	s, _, _ := newTestServer(t)
	fn := s.httpContext("/mcp")
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	withSession := func(id string) context.Context {
		return s.impl.WithContext(context.Background(), fakeSession{id: id})
	}

	a1 := correlationIDFromContext(fn(withSession("sess-a"), req))
	a2 := correlationIDFromContext(fn(withSession("sess-a"), req))
	b := correlationIDFromContext(fn(withSession("sess-b"), req))
	if a1 != a2 {
		t.Errorf("same session, different correlation ids: %q vs %q", a1, a2)
	}
	if a1 == b {
		t.Errorf("distinct sessions share correlation id %q", a1)
	}
	if len(a1) != 8 || strings.Contains(a1, "sess") {
		t.Errorf("correlation id %q should be 8 hex chars derived from, not equal to, the session id", a1)
	}

	// No session (protocol 2026-07-28 messages, tests): fresh id per message.
	n1 := correlationIDFromContext(fn(context.Background(), req))
	n2 := correlationIDFromContext(fn(context.Background(), req))
	if n1 == n2 || len(n1) != 8 {
		t.Errorf("sessionless messages should get distinct 8-char ids, got %q and %q", n1, n2)
	}

	req.Header.Set("Accept-Language", "it")
	ctx := fn(withSession("x"), req)
	if basePathFromContext(ctx) != "/mcp" || LangFromContext(ctx) != "it" {
		t.Errorf("basePath/lang not threaded: %q %q", basePathFromContext(ctx), LangFromContext(ctx))
	}
}

// The one-waiter guard of memory_wait_changes keys on the correlation id: two
// messages of the same session, each with its own context, must collide.
func TestMCP_WaitChangesGuardKeyedOnSession(t *testing.T) {
	s, _, _ := newTestServer(t)
	s.SetEvents(events.New(events.HubOptions{}))
	fn := s.httpContext("/mcp")
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	message := func() context.Context {
		return fn(s.impl.WithContext(context.Background(), fakeSession{id: "same-session"}), req)
	}

	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(started)
		_, _ = s.handleWaitChanges(message(), call(map[string]any{"timeout_s": 2}))
	}()
	<-started
	time.Sleep(100 * time.Millisecond) // same readiness window as TestMCP_WaitChangesOneWaiterPerSession

	res, _ := s.handleWaitChanges(message(), call(map[string]any{"timeout_s": 1}))
	if msg := expectError(t, res); !strings.Contains(msg, "already in flight") {
		t.Fatalf("second message of the same session was not refused: %q", msg)
	}
	<-done
}
