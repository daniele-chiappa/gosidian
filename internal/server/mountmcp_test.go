package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/vault"
)

// A Streamable HTTP client POSTs to the exact /mcp path; with only the
// /mcp/ subtree registered, http.ServeMux would answer 301 → /mcp/, which
// MCP clients do not follow. Both patterns must reach the handler untouched.
func TestMountMCP_ExactPathAndSubtree(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "idx.db"))
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	s := New(vault.New(dir), idx)
	s.MountMCP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Path", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/mcp"},
		{http.MethodGet, "/mcp/sse"},
		{http.MethodPost, "/mcp/message?sessionId=x"},
		{http.MethodPost, "/mcp/upload"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Errorf("%s %s: status = %d, want 204 (no redirect, no SPA fallback)", tc.method, tc.path, rec.Code)
		}
		if got := rec.Header().Get("X-Seen-Path"); got != req.URL.Path {
			t.Errorf("%s %s: handler saw path %q (prefix must not be stripped)", tc.method, tc.path, got)
		}
	}
}
