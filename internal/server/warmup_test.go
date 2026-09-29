package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSwitch_StartingThenReady(t *testing.T) {
	sw := NewSwitch()
	do := func(method, path, accept string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		rec := httptest.NewRecorder()
		sw.ServeHTTP(rec, req)
		return rec
	}

	cases := []struct {
		name, method, path, accept, ctype, body string
	}{
		{"healthz", http.MethodGet, "/healthz", "", "application/json", `"status":"starting"`},
		{"browser", http.MethodGet, "/notes/x", "text/html,application/xhtml+xml", "text/html", `http-equiv="refresh"`},
		{"api", http.MethodGet, "/api/v1/tree", "application/json", "application/json", `"code":"server.unavailable"`},
		{"mcp", http.MethodPost, "/mcp", "application/json, text/event-stream", "application/json", `"code":"server.unavailable"`},
	}
	for _, c := range cases {
		rec := do(c.method, c.path, c.accept)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status %d, want 503", c.name, rec.Code)
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Errorf("%s: no Retry-After", c.name)
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), c.ctype) {
			t.Errorf("%s: Content-Type %q, want %s", c.name, rec.Header().Get("Content-Type"), c.ctype)
		}
		if !strings.Contains(rec.Body.String(), c.body) {
			t.Errorf("%s: body %q lacks %s", c.name, rec.Body.String(), c.body)
		}
	}

	sw.Ready(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	for _, c := range cases {
		if rec := do(c.method, c.path, c.accept); rec.Code != http.StatusTeapot || rec.Header().Get("Retry-After") != "" {
			t.Errorf("%s after Ready: status %d, Retry-After %q; want the real handler", c.name, rec.Code, rec.Header().Get("Retry-After"))
		}
	}
}

func eventStreamRequest(ctx context.Context) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/mcp/sse", nil).WithContext(ctx)
	req.Header.Set("Accept", "text/event-stream")
	return req
}

// An EventSource gives up on any answer but a 200, so during the scan its
// GET waits for the real handler instead of getting the 503.
func TestSwitch_HoldsEventStreamUntilReady(t *testing.T) {
	sw := NewSwitch()
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		sw.ServeHTTP(rec, eventStreamRequest(context.Background()))
		close(done)
	}()

	select {
	case <-done:
		t.Fatalf("event stream answered before Ready: status %d", rec.Code)
	case <-time.After(50 * time.Millisecond):
	}
	sw.Ready(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("held event stream not released by Ready")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status %d, want the real handler's 418", rec.Code)
	}
}

func TestSwitch_EventStreamHoldRunsOut(t *testing.T) {
	sw := NewSwitch()
	sw.hold = 10 * time.Millisecond
	rec := httptest.NewRecorder()
	sw.ServeHTTP(rec, eventStreamRequest(context.Background()))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("status %d, Retry-After %q; want 503 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestSwitch_EventStreamClientLeaves(t *testing.T) {
	sw := NewSwitch()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		sw.ServeHTTP(httptest.NewRecorder(), eventStreamRequest(ctx))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("held event stream outlived its client")
	}
}
