package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	gserver "github.com/gosidian/gosidian/internal/server"
)

// An open HTTP+SSE stream ends when the server closes its streams, so a
// graceful shutdown does not wait for it (BUG-069).
func TestCloseStreams_EndsSSESessions(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead})
	ts := httptest.NewServer(s.Handler("/mcp"))
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/mcp/sse", nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	r := bufio.NewReader(resp.Body)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended before the endpoint event: %v", err)
		}
		if strings.HasPrefix(line, "event: endpoint") {
			break
		}
	}

	done := make(chan struct{})
	go func() {
		for {
			if _, err := r.ReadString('\n'); err != nil {
				close(done)
				return
			}
		}
	}()
	s.CloseStreams()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE stream still open after CloseStreams")
	}

	// A client reconnecting at once gets no new stream to hold the shutdown.
	again, err := http.DefaultClient.Do(req.Clone(req.Context()))
	if err != nil {
		t.Fatal(err)
	}
	again.Body.Close()
	if again.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("reconnect after CloseStreams: status %d, want 503", again.StatusCode)
	}
}

// modernPost sends a protocol 2026-07-28 JSON-RPC request to the Streamable
// HTTP endpoint and returns the response, body unread.
func modernPost(t *testing.T, url, token, method, params string) *http.Response {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":{` + params +
		`"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
		`"io.modelcontextprotocol/clientCapabilities":{}}}}`
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", method)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("%s: status %d: %s", method, resp.StatusCode, b)
	}
	return resp
}

const listenParams = `"notifications":{"toolsListChanged":true},`

// openListen opens a subscriptions/listen stream and reads it up to the
// acknowledgement, after which mcp-go holds it open.
func openListen(t *testing.T, url, token string) *bufio.Reader {
	t.Helper()
	resp := modernPost(t, url, token, "subscriptions/listen", listenParams)
	t.Cleanup(func() { resp.Body.Close() })
	r := bufio.NewReader(resp.Body)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended before the acknowledgement: %v", err)
		}
		if strings.Contains(line, "notifications/subscriptions/acknowledged") {
			return r
		}
	}
}

// ended closes the returned channel once the stream behind r ends.
func ended(r *bufio.Reader) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		for {
			if _, err := r.ReadString('\n'); err != nil {
				close(done)
				return
			}
		}
	}()
	return done
}

// A subscriptions/listen stream (protocol 2026-07-28) is a POST that mcp-go
// holds open until the request ends; it too ends when the server closes its
// streams, and one opened afterwards ends at once (BUG-072).
func TestCloseStreams_EndsListenStreams(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead})
	ts := httptest.NewServer(s.Handler("/mcp"))
	defer ts.Close()

	done := ended(openListen(t, ts.URL+"/mcp", token))
	select {
	case <-done:
		t.Fatal("listen stream ended before CloseStreams")
	case <-time.After(200 * time.Millisecond):
	}
	s.CloseStreams()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("listen stream still open after CloseStreams")
	}

	again := modernPost(t, ts.URL+"/mcp", token, "subscriptions/listen", listenParams)
	defer again.Body.Close()
	select {
	case <-ended(bufio.NewReader(again.Body)):
	case <-time.After(2 * time.Second):
		t.Fatal("listen opened after CloseStreams still open")
	}

	// Other requests are neither cut short nor altered by the method check.
	list := modernPost(t, ts.URL+"/mcp", token, "tools/list", "")
	defer list.Body.Close()
	var out struct {
		Result struct {
			Tools []struct{ Name string } `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(list.Body).Decode(&out); err != nil || len(out.Result.Tools) == 0 {
		t.Fatalf("tools/list after CloseStreams: %d tools, err %v", len(out.Result.Tools), err)
	}
}

// The whole shutdown with a listen open: it takes nowhere near its timeout
// and finds nothing still busy (BUG-072).
func TestShutdown_WithListenOpen(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	s, token := serverWithToken(t, "", []string{auth.ScopeRead})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	inflight := gserver.NewInflight()
	srv := &http.Server{Handler: inflight.Wrap(s.Handler("/mcp"))}
	go func() { _ = srv.Serve(ln) }()
	done := ended(openListen(t, "http://"+ln.Addr().String()+"/mcp", token))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	gserver.Shutdown(ctx, s.CloseStreams, inflight, srv)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("shutdown took %s, want well under its 5 s timeout", d)
	}
	if strings.Contains(logs.String(), "still busy") {
		t.Errorf("shutdown still busy: %s", logs.String())
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("listen stream still open after shutdown")
	}
}
