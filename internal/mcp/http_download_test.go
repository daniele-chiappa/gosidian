package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
)

// download issues GET /mcp/download?path=<rel> against the handler directly,
// with an optional bearer token.
func download(t *testing.T, s *Server, token, rel string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/mcp/download?path="+rel, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.handleHTTPDownload(rec, req)
	return rec
}

func TestHTTPDownload_Bearer(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead})
	body := []byte("---\ntitle: Report\n---\n\n# Report\n\nbody\n")
	if err := s.vault.Save("proj/report.md", body); err != nil {
		t.Fatal(err)
	}
	note, err := s.vault.Load("proj/report.md")
	if err != nil {
		t.Fatal(err)
	}

	rec := download(t, s, token, "proj/report.md")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.Bytes(); string(got) != string(body) {
		t.Errorf("body mismatch:\n%s", got)
	}
	h := rec.Header()
	if ct := h.Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if et := h.Get("ETag"); et != `"`+note.ETag()+`"` {
		t.Errorf("ETag = %q, want quoted %q", et, note.ETag())
	}
	// Inert like /vault-files/ (ADR-021): sandbox CSP, nosniff, and always a
	// download so an .html note never renders in the app origin.
	if !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("CSP = %q", h.Get("Content-Security-Policy"))
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("nosniff missing: %v", h)
	}
	if cd := h.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, "report.md") {
		t.Errorf("Content-Disposition = %q", cd)
	}
}

func TestHTTPDownload_HTMLNote(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead})
	s.vault.SetHTMLNotes(true)
	body := []byte("<!--\ntitle: Dash\n-->\n<html><body><script>1</script></body></html>\n")
	if err := s.vault.Save("proj/dash.html", body); err != nil {
		t.Fatal(err)
	}
	rec := download(t, s, token, "proj/dash.html")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec.Body.String() != string(body) {
		t.Errorf("body mismatch:\n%s", rec.Body.String())
	}

	// Flag off: an .html file is not a note on this instance (IMP-080).
	s.vault.SetHTMLNotes(false)
	if rec := download(t, s, token, "proj/dash.html"); rec.Code != http.StatusBadRequest {
		t.Errorf("html notes off: status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPDownload_RejectsNoToken(t *testing.T) {
	s, _ := serverWithToken(t, "", []string{auth.ScopeRead})
	rec := download(t, s, "", "proj/x.md")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("WWW-Authenticate missing")
	}
}

func TestHTTPDownload_RejectsWriteOnlyToken(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeWrite})
	if err := s.vault.Save("proj/x.md", []byte("# x\n")); err != nil {
		t.Fatal(err)
	}
	if rec := download(t, s, token, "proj/x.md"); rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (no read scope)", rec.Code)
	}
}

func TestHTTPDownload_ScopedTokenOutsideProjectIs404(t *testing.T) {
	s, token := serverWithToken(t, "mine", []string{auth.ScopeRead})
	if err := s.vault.Save("other/secret.md", []byte("# secret\n")); err != nil {
		t.Fatal(err)
	}
	rec := download(t, s, token, "other/secret.md")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (must not reveal existence)", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("body leaks content: %s", rec.Body.String())
	}
}

func TestHTTPDownload_MissingNoteIs404(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead})
	if rec := download(t, s, token, "proj/nope.md"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHTTPDownload_AttachmentPathPointsToVaultFiles(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead})
	dir := filepath.Join(s.vault.Root, "proj", "attachments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := download(t, s, token, "proj/attachments/x.png")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "/vault-files/proj/attachments/x.png") {
		t.Errorf("error should teach the /vault-files/ channel: %s", rec.Body.String())
	}
}

func TestHTTPDownload_RejectsBadPaths(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead})
	for _, p := range []string{"", "../etc/passwd", "proj/../../x.md", ".git/config", "proj/.gosidian/config.toml"} {
		rec := download(t, s, token, p)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("path %q: status = %d, want 400", p, rec.Code)
		}
	}
}

func TestHTTPDownload_MethodNotAllowed(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead})
	req := httptest.NewRequest(http.MethodPost, "/mcp/download?path=proj/x.md", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.handleHTTPDownload(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

// The quoted ETag header is meant to go straight back as if_match on the
// write that follows the local edit — no unquoting on the agent's side.
func TestHTTPDownload_ETagRoundTripsAsIfMatch(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	if err := s.vault.Save("proj/big.md", []byte("# v1\n")); err != nil {
		t.Fatal(err)
	}
	rec := download(t, s, token, "proj/big.md")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if !strings.HasPrefix(etag, `"`) {
		t.Fatalf("ETag %q should be quoted (RFC 7232)", etag)
	}

	tok, err := s.tokens.Validate(token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), tokenCtxKey, tok)
	r, _ := s.handleUpdate(ctx, call(map[string]any{
		"path": "proj/big.md", "content": "# v2\n", "if_match": etag,
	}))
	if r.IsError {
		t.Fatalf("update with quoted if_match: %s", expectError(t, r))
	}
	// A stale stamp still fails: the tolerance is about quotes, not CAS.
	r, _ = s.handleUpdate(ctx, call(map[string]any{
		"path": "proj/big.md", "content": "# v3\n", "if_match": etag,
	}))
	if !r.IsError {
		t.Fatalf("stale quoted if_match should be rejected")
	}
}

// The endpoint is reachable through the mounted single-port handler next to
// /upload, sharing the transport base path.
func TestHTTPDownload_MountedOnHandler(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead})
	if err := s.vault.Save("proj/m.md", []byte("# mounted\n")); err != nil {
		t.Fatal(err)
	}
	h := s.Handler("/mcp")
	req := httptest.NewRequest(http.MethodGet, "/mcp/download?path=proj/m.md", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "# mounted\n" {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
}
