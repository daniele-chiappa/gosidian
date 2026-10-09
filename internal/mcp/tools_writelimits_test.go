package mcp

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
)

// The limiter of every MCP tool that writes is checked by the write
// conformance suite (write_conformance_test.go), which replaced the table
// that lived here (BUG-035); the HTTP surfaces are checked below.

func TestHTTPUpload_HonoursWriteLimiter(t *testing.T) {
	s, plaintext := serverWithToken(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	s.SetWriteLimits(1, 0)
	post := func() *httptest.ResponseRecorder {
		body, ct := multipartPNG(t)
		r := httptest.NewRequest(http.MethodPost, "/upload?project=proj", body)
		r.Header.Set("Authorization", "Bearer "+plaintext)
		r.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		s.handleHTTPUpload(w, r)
		return w
	}
	if w := post(); w.Code != http.StatusOK {
		t.Fatalf("first upload: %d %s", w.Code, w.Body.String())
	}
	w := post()
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second upload must hit the limiter: %d %s", w.Code, w.Body.String())
	}
	// The refusal says when a place frees up (IMP-141).
	if ra, err := strconv.Atoi(w.Header().Get("Retry-After")); err != nil || ra < 1 || ra > 60 {
		t.Errorf("Retry-After = %q, want 1..60 seconds", w.Header().Get("Retry-After"))
	}
}

// A token may hold only a bounded number of unredeemed tickets: minting in
// a loop must not grow the ticket map without limit.
func TestMintIngestTicket_CapPerToken(t *testing.T) {
	s, ctx := newScopedServer(t, "proj", []string{auth.ScopeRead, auth.ScopeWrite})
	s.SetWriteLimits(1000, 0)
	for i := 0; i < maxIngestTicketsPerToken; i++ {
		res, _ := s.handleIngest(ctx, call(map[string]any{"project": "proj", "transfer": "http"}))
		if res.IsError {
			t.Fatalf("mint %d: %s", i+1, expectError(t, res))
		}
	}
	res, _ := s.handleIngest(ctx, call(map[string]any{"project": "proj", "transfer": "http"}))
	if msg := expectError(t, res); !strings.Contains(msg, "pending ingest tickets") {
		t.Fatalf("expected the per-token ticket cap, got: %s", msg)
	}
}

// An ingestion takes one place in the write rate: a CSV that becomes an
// attachment and its table note, and a ticket from its mint to the note
// it writes. With one write a minute both go through whole, and the next
// write is refused (IMP-161, S3-7: the ticket counted three times and lost
// the bytes at the third).
func TestIngest_TakesOnePlaceInTheWriteRate(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	s.vault.SetTableNotes(true)
	s.SetWriteLimits(1, 0)
	res, _ := s.handleIngest(ctx, call(map[string]any{
		"project": "audit", "data": b64(sampleCSV), "filename": "log.csv", "title": "Direct",
	}))
	if out := resultText(t, res); !strings.Contains(out, `"kind":"table"`) {
		t.Fatalf("direct ingest under a limit of one: %s", out)
	}
	res, _ = s.handleIngest(ctx, call(map[string]any{"project": "audit", "data": b64(sampleCSV), "filename": "log.csv"}))
	if msg := expectError(t, res); !strings.Contains(msg, "write rate limit") {
		t.Fatalf("a second ingestion in the minute: %s", msg)
	}

	s, ctx = newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	s.vault.SetTableNotes(true)
	s.SetWriteLimits(1, 0)
	out := ingestOut(t, s, ctx, map[string]any{"project": "audit", "transfer": "http", "title": "Remote"})
	code, body := postTicket(t, s.Handler(""), out["endpoint"].(string), "log.csv", []byte(sampleCSV))
	if code != http.StatusOK || !strings.Contains(body, `"kind":"table"`) {
		t.Fatalf("ticket under a limit of one: HTTP %d %s", code, body)
	}
	if _, err := s.vault.Load("audit/remote.md"); err != nil {
		t.Errorf("the ticket's table note: %v", err)
	}
}
