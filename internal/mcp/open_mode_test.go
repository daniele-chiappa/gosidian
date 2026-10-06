package mcp

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/vault"
)

// emptyStoreServer is a server with a token store that holds no token yet,
// as a fresh install has.
func emptyStoreServer(t *testing.T) (*Server, *auth.Store) {
	t.Helper()
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	store, err := auth.Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	return New(vault.New(t.TempDir()), idx, store), store
}

func tokenlessRequest(h http.Handler, method, path, body string) int {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if method == http.MethodPost && path == "/mcp" {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// An empty token store no longer opens MCP: every surface wants a token
// (IMP-146).
func TestOpenMode_EmptyStoreIsClosed(t *testing.T) {
	s, _ := emptyStoreServer(t)
	h := s.Handler("/mcp")
	cases := []struct{ method, path, body string }{
		{http.MethodPost, "/mcp", initializeBody},
		{http.MethodGet, "/mcp/sse", ""},
		{http.MethodPost, "/mcp/upload?project=p", ""},
		{http.MethodGet, "/mcp/download?path=p/a.md", ""},
		{http.MethodPost, "/mcp/append?path=p/a.md", "x"},
		{http.MethodGet, "/mcp/manifest?project=p", ""},
	}
	for _, c := range cases {
		if got := tokenlessRequest(h, c.method, c.path, c.body); got != http.StatusUnauthorized {
			t.Errorf("%s %s without a token = %d, want 401", c.method, c.path, got)
		}
	}
	if tok := s.tokenFromContext(t.Context()); tok != nil {
		t.Errorf("a tool call without a token runs as %+v", tok)
	}
}

// With the operator's opt-in MCP answers without a token while the store
// is empty; the first token closes it, at the transport too.
func TestOpenMode_OptInUntilTheFirstToken(t *testing.T) {
	s, store := emptyStoreServer(t)
	s.SetOpenWhenEmpty(true)
	h := s.Handler("/mcp")
	if got := tokenlessRequest(h, http.MethodPost, "/mcp", initializeBody); got != http.StatusOK {
		t.Fatalf("opt-in, empty store: initialize = %d, want 200", got)
	}
	if tok := s.tokenFromContext(t.Context()); tok == nil || !tok.IsAdmin() {
		t.Errorf("opt-in, empty store: tool caller = %+v, want admin", tok)
	}

	plaintext, _, err := store.Create("first", nil, []string{auth.ScopeRead}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := tokenlessRequest(h, http.MethodPost, "/mcp", initializeBody); got != http.StatusUnauthorized {
		t.Errorf("after the first token: initialize without one = %d, want 401", got)
	}
	if rec := postMCP(t, h, plaintext, "", initializeBody); rec.Code != http.StatusOK {
		t.Errorf("after the first token: initialize with it = %d", rec.Code)
	}
}

// A server started without tokens, which gets one later, refuses a
// token-less request at the transport: the guard is decided per request,
// not when the handler was built.
func TestOpenMode_TokenCreatedAfterStartClosesTheTransport(t *testing.T) {
	s, store := emptyStoreServer(t)
	h := s.Handler("/mcp")
	if _, _, err := store.Create("later", nil, []string{auth.ScopeRead}, 0, ""); err != nil {
		t.Fatal(err)
	}
	if got := tokenlessRequest(h, http.MethodPost, "/mcp", initializeBody); got != http.StatusUnauthorized {
		t.Errorf("initialize without a token = %d, want 401", got)
	}
}
