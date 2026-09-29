package mcp

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
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
}
