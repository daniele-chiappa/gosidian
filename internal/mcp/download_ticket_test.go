package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
)

// mintDownload calls memory_get transfer:"http" and returns the ticket payload.
func mintDownload(t *testing.T, s *Server, ctx context.Context, path string) map[string]any {
	t.Helper()
	res, err := s.handleGet(ctx, call(map[string]any{"path": path, "transfer": "http"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("mint: %s", resultText(t, res))
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	return out
}

func getTicket(h http.Handler, endpoint string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, endpoint, nil))
	return rec
}

// memory_get transfer:"http" gives a single-use URL that serves the note's
// bytes with no bearer, ETag included; the body never enters the result
// (IMP-142).
func TestDownloadTicket_RoundTrip(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead})
	body := "---\ntitle: Big\n---\n\n# Big\n\n" + strings.Repeat("line\n", 100)
	if err := s.vault.Save("proj/big.md", []byte(body)); err != nil {
		t.Fatal(err)
	}
	out := mintDownload(t, s, ctx, "proj/big.md")
	endpoint, _ := out["endpoint"].(string)
	if !strings.HasPrefix(endpoint, "/download/") || out["method"] != "GET" || out["single_use"] != true || out["size"] != float64(len(body)) {
		t.Fatalf("ticket = %v", out)
	}
	if _, ok := out["content"]; ok {
		t.Error("the body must not be in the result")
	}
	h := s.Handler("")
	rec := getTicket(h, endpoint)
	if rec.Code != http.StatusOK || rec.Body.String() != body {
		t.Fatalf("redeem: %d %q", rec.Code, rec.Body.String())
	}
	if et := rec.Header().Get("ETag"); et != `"`+out["etag"].(string)+`"` {
		t.Errorf("ETag = %q, ticket etag %v", et, out["etag"])
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("not inert: %v", rec.Header())
	}
	// Single-use.
	if rec := getTicket(h, endpoint); rec.Code != http.StatusNotFound {
		t.Errorf("second redeem: %d", rec.Code)
	}
	// Mounted under the transport's base path too.
	out = mintDownload(t, s, context.WithValue(ctx, basePathCtxKey, "/mcp"), "proj/big.md")
	if rec := getTicket(s.Handler("/mcp"), out["endpoint"].(string)); rec.Code != http.StatusOK {
		t.Errorf("redeem under /mcp: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDownloadTicket_Refusals(t *testing.T) {
	s, ctx := newScopedServer(t, "proj", []string{auth.ScopeRead})
	if err := s.vault.Save("proj/a.md", []byte("# A\n")); err != nil {
		t.Fatal(err)
	}
	if err := s.vault.Save("other/b.md", []byte("# B\n")); err != nil {
		t.Fatal(err)
	}
	h := s.Handler("")
	// Outside the token's scope, or missing: no ticket.
	for _, p := range []string{"other/b.md", "proj/missing.md"} {
		if res, _ := s.handleGet(ctx, call(map[string]any{"path": p, "transfer": "http"})); !res.IsError {
			t.Errorf("%s: want an error, got %s", p, resultText(t, res))
		}
	}
	// Expired.
	out := mintDownload(t, s, ctx, "proj/a.md")
	s.downloadTicketsMu.Lock()
	s.downloadTickets[out["ticket"].(string)].Expires = time.Now().Add(-time.Second)
	s.downloadTicketsMu.Unlock()
	if rec := getTicket(h, out["endpoint"].(string)); rec.Code != http.StatusGone {
		t.Errorf("expired: %d", rec.Code)
	}
	// The minting token revoked before the redemption. Another token stays,
	// or the empty store would put the server in open mode.
	out = mintDownload(t, s, ctx, "proj/a.md")
	minter := s.tokens.List()[0].ID
	if _, _, err := s.tokens.Create("other", nil, []string{auth.ScopeRead}, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.tokens.Revoke(minter); err != nil {
		t.Fatal(err)
	}
	if rec := getTicket(h, out["endpoint"].(string)); rec.Code != http.StatusForbidden {
		t.Errorf("revoked token: %d %s", rec.Code, rec.Body.String())
	}
	// Unknown id, and the wrong method.
	if rec := getTicket(h, "/download/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown: %d", rec.Code)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/download/x", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
}

// A token may hold only a bounded number of unredeemed download tickets.
func TestDownloadTicket_CapPerToken(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead})
	if err := s.vault.Save("proj/a.md", []byte("# A\n")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxDownloadTicketsPerToken; i++ {
		mintDownload(t, s, ctx, "proj/a.md")
	}
	res, _ := s.handleGet(ctx, call(map[string]any{"path": "proj/a.md", "transfer": "http"}))
	if msg := expectError(t, res); !strings.Contains(msg, "too many pending download tickets") {
		t.Errorf("cap: %s", msg)
	}
}

// A ticket is redeemed with its token narrowed to the owner's access now:
// an account that lost the project, or was disabled, after the mint reads
// nothing with it (BUG-113, S3-4).
func TestDownloadTicket_FollowsTheOwner(t *testing.T) {
	f := newAccessFixture(t)
	f.visibility(t, "proj", "private")
	f.grant(t, "proj", "m1", "read")
	if err := f.s.vault.Save("proj/n.md", []byte("# n")); err != nil {
		t.Fatal(err)
	}
	_, tok := f.token(t, "m1", nil, []string{auth.ScopeRead})
	ctx := context.WithValue(context.Background(), tokenCtxKey, f.s.effectiveToken(tok))
	h := f.s.Handler("")

	first := mintDownload(t, f.s, ctx, "proj/n.md")
	second := mintDownload(t, f.s, ctx, "proj/n.md")
	if err := f.projects.RemoveMember("proj", "m1"); err != nil {
		t.Fatal(err)
	}
	if rec := getTicket(h, first["endpoint"].(string)); rec.Code == http.StatusOK {
		t.Errorf("redeemed after the grant went: %d %q", rec.Code, rec.Body.String())
	}
	f.grant(t, "proj", "m1", "read")
	delete(f.roles, "m1") // disabled or gone
	if rec := getTicket(h, second["endpoint"].(string)); rec.Code == http.StatusOK {
		t.Errorf("redeemed after the owner went: %d %q", rec.Code, rec.Body.String())
	}
}
