package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func serveOn(t *testing.T, h http.Handler) (*http.Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	return srv, ln.Addr().String()
}

// The streams end only once the listener is closed, so a client that comes
// straight back finds no server here, and the shutdown does not wait on it
// (BUG-069).
func TestShutdown_EndsStreamsAfterClosingListeners(t *testing.T) {
	stop := make(chan struct{})
	inflight := NewInflight()
	srv, addr := serveOn(t, inflight.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-stop:
		case <-r.Context().Done():
		}
	})))
	resp, err := http.Get("http://" + addr + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	redial := make(chan error, 1)
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	Shutdown(ctx, func() {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c.Close()
		}
		redial <- err
		close(stop)
	}, inflight, srv, nil)

	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("shutdown took %s, want well under its 5 s timeout", d)
	}
	if err := <-redial; err == nil {
		t.Error("the listener still accepted connections when the streams were ended")
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Errorf("stream did not end cleanly: %v", err)
	}
	if got := inflight.String(); got != "none" {
		t.Errorf("inflight after shutdown = %q", got)
	}
}

// A request that ignores everything is cut when the timeout ends, and the
// log names it.
func TestShutdown_ClosesWhatIsStillBusy(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	inflight := NewInflight()
	srv, addr := serveOn(t, inflight.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	})))
	errc := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/busy")
		if err == nil {
			resp.Body.Close()
		}
		errc <- err
	}()
	for i := 0; inflight.String() == "none"; i++ {
		if i > 200 {
			t.Fatal("request never reached the handler")
		}
		time.Sleep(5 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	Shutdown(ctx, func() {}, inflight, srv)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("shutdown took %s past its timeout", d)
	}
	if err := <-errc; err == nil {
		t.Error("the busy request was not cut")
	}
	if !strings.Contains(logs.String(), "GET /busy from 127.0.0.1:") {
		t.Errorf("log does not name the busy request: %s", logs.String())
	}
}
