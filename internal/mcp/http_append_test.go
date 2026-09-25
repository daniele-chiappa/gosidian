package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
)

// appendReq issues POST /mcp/append?path=<rel> with the body and an
// optional bearer / If-Match, against the handler directly.
func appendReq(t *testing.T, s *Server, token, rel, body, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp/append?path="+rel, strings.NewReader(body))
	req.Header.Set("Content-Type", "text/markdown")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rec := httptest.NewRecorder()
	s.handleHTTPAppend(rec, req)
	return rec
}

func TestHTTPAppend_CreatesThenAppends(t *testing.T) {
	s, token := serverWithToken(t, "proj", []string{auth.ScopeRead, auth.ScopeWrite})

	// First append onto a missing note creates it.
	rec := appendReq(t, s, token, "proj/log.md", "## entry one\n", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"created":true`) {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body.String())
	}
	etag := strings.Trim(rec.Header().Get("ETag"), `"`)
	if etag == "" {
		t.Fatal("ETag header missing")
	}
	// Second append with the fresh ETag as precondition.
	rec = appendReq(t, s, token, "proj/log.md", "## entry two\n", `"`+etag+`"`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"created":false`) {
		t.Fatalf("append = %d (%s)", rec.Code, rec.Body.String())
	}
	note, err := s.vault.Load("proj/log.md")
	if err != nil {
		t.Fatal(err)
	}
	// Same separator rule as memory_append: one blank line between entries.
	if got := string(note.Content); got != "## entry one\n\n## entry two\n" {
		t.Errorf("content = %q", got)
	}
	// A stale precondition is refused.
	if rec := appendReq(t, s, token, "proj/log.md", "late\n", `"`+etag+`"`); rec.Code != http.StatusPreconditionFailed {
		t.Errorf("stale If-Match = %d want 412 (%s)", rec.Code, rec.Body.String())
	}
	// If-Match on a note that does not exist is a mismatch too.
	if rec := appendReq(t, s, token, "proj/new.md", "x\n", `"deadbeef-1"`); rec.Code != http.StatusPreconditionFailed {
		t.Errorf("If-Match on missing = %d want 412", rec.Code)
	}
}

func TestHTTPAppend_Guards(t *testing.T) {
	s, token := serverWithToken(t, "proj", []string{auth.ScopeRead, auth.ScopeWrite})

	if rec := appendReq(t, s, "", "proj/log.md", "x\n", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no bearer = %d want 401", rec.Code)
	}
	if rec := appendReq(t, s, token, "other/log.md", "x\n", ""); rec.Code != http.StatusNotFound {
		t.Errorf("outside scope = %d want 404 (%s)", rec.Code, rec.Body.String())
	}
	if rec := appendReq(t, s, token, "proj/notes.txt", "x\n", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("non-note path = %d want 400", rec.Code)
	}
	if rec := appendReq(t, s, token, "proj/log.md", "   \n", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("empty body = %d want 400", rec.Code)
	}
	if rec := appendReq(t, s, token, "", "x\n", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("missing path = %d want 400", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/mcp/append?path=proj/log.md", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.handleHTTPAppend(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d want 405", rec.Code)
	}

	// Read-only token cannot append.
	ro, roTok := serverWithToken(t, "proj", []string{auth.ScopeRead})
	if rec := appendReq(t, ro, roTok, "proj/log.md", "x\n", ""); rec.Code != http.StatusForbidden {
		t.Errorf("read scope = %d want 403", rec.Code)
	}
}

// The size cap applies to the merged note, like memory_append.
func TestHTTPAppend_SizeLimit(t *testing.T) {
	s, token := serverWithToken(t, "proj", []string{auth.ScopeRead, auth.ScopeWrite})
	s.SetWriteLimits(60, 64)
	if rec := appendReq(t, s, token, "proj/log.md", strings.Repeat("a", 40)+"\n", ""); rec.Code != http.StatusOK {
		t.Fatalf("first = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := appendReq(t, s, token, "proj/log.md", strings.Repeat("b", 40)+"\n", ""); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("over the cap = %d want 413 (%s)", rec.Code, rec.Body.String())
	}
}
