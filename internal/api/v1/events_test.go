package v1

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/server/events"
)

// startEventsServer spins up a real httptest.Server so EventSource-
// style streaming can be exercised end-to-end. The router needs a
// proper HTTP server (not just ResponseRecorder) because the SSE
// handler relies on http.Flusher and Hijack-style streaming, which
// the recorder doesn't simulate.
func startEventsServer(t *testing.T) (*httptest.Server, *adminFixture) {
	t.Helper()
	f := newAdminFixture(t)
	srv := httptest.NewServer(f.router)
	t.Cleanup(srv.Close)
	return srv, f
}

func TestEvents_RequiresToken(t *testing.T) {
	srv, _ := startEventsServer(t)
	res, err := http.Get(srv.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", res.StatusCode)
	}
}

func TestEvents_RejectsInvalidToken(t *testing.T) {
	srv, _ := startEventsServer(t)
	res, err := http.Get(srv.URL + "/api/v1/events?token=gsp_bogus")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", res.StatusCode)
	}
}

func TestEvents_StreamsPublishedEvent(t *testing.T) {
	srv, f := startEventsServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL+"/api/v1/events?token="+f.bearer+"&topics=tree", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("Content-Type=%q", got)
	}

	reader := bufio.NewReader(res.Body)

	// Drop the initial ": connected" comment frame.
	if _, _, err := reader.ReadLine(); err != nil {
		t.Fatal(err)
	}
	// Empty line that terminates the comment frame.
	if _, _, err := reader.ReadLine(); err != nil {
		t.Fatal(err)
	}

	// Publish from another goroutine once the subscription is registered:
	// the HTTP roundtrip + handler entry + Hub.Subscribe are asynchronous
	// from here, so wait for the hub to see the subscriber instead of
	// sleeping a fixed amount (BUG-032).
	waitSubscribers(t, f.router.deps.Events, 1)
	go func() {
		f.router.deps.Events.Publish(events.TopicTree, map[string]string{"action": "test", "path": "x.md"})
	}()

	// Read frames with a hard deadline so the test doesn't wedge if
	// something goes wrong.
	deadline := time.Now().Add(2 * time.Second)
	frame := strings.Builder{}
	for time.Now().Before(deadline) {
		line, _, err := reader.ReadLine()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		frame.WriteString(string(line))
		frame.WriteByte('\n')
		if len(line) == 0 {
			break // frame terminator (CRLF after data)
		}
	}
	body := frame.String()
	if !strings.Contains(body, "event: tree") {
		t.Errorf("expected event:tree in frame, got %q", body)
	}
	if !strings.Contains(body, `"action":"test"`) {
		t.Errorf("expected payload data, got %q", body)
	}
}

func TestEvents_TopicFilter(t *testing.T) {
	srv, f := startEventsServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Subscribe to `note` only — a tree publish must NOT appear.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL+"/api/v1/events?token="+f.bearer+"&topics=note", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	reader := bufio.NewReader(res.Body)
	// Drain initial comment.
	_, _, _ = reader.ReadLine()
	_, _, _ = reader.ReadLine()
	waitSubscribers(t, f.router.deps.Events, 1)

	// Publish a tree event the subscriber should NOT receive.
	f.router.deps.Events.Publish(events.TopicTree, map[string]string{"action": "tree-only"})

	// http.Response.Body has no SetReadDeadline, so we rely on the
	// request context to bound the wait. Cancel after 200ms; if the
	// reader saw the tree-only payload before that, the filter
	// leaked.
	done := make(chan string, 1)
	go func() {
		buf := strings.Builder{}
		for {
			line, _, err := reader.ReadLine()
			if err != nil {
				done <- buf.String()
				return
			}
			buf.WriteString(string(line))
			buf.WriteByte('\n')
		}
	}()

	select {
	case body := <-done:
		// Connection closed during read — body should not contain
		// the tree-only payload.
		if strings.Contains(body, "tree-only") {
			t.Errorf("topic filter leaked tree event: %q", body)
		}
	case <-time.After(200 * time.Millisecond):
		// No data within 200ms confirms the filter held. Cancel the
		// request to wind down the goroutine.
		cancel()
	}
}

func TestEvents_RejectsNonGet(t *testing.T) {
	srv, f := startEventsServer(t)
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/api/v1/events?token="+f.bearer, strings.NewReader(""))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status=%d, want 405", res.StatusCode)
	}
}

// waitSubscribers blocks until the hub reports at least n subscriptions or
// the deadline passes. Readiness must be observed, never assumed from a
// sleep (BUG-032).
func waitSubscribers(t *testing.T, hub *events.Hub, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for hub.SubCount() < n {
		if time.Now().After(deadline) {
			t.Fatalf("subscriber did not register: have %d, want %d", hub.SubCount(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
