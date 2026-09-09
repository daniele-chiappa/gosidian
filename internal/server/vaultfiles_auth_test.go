package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// ADR-022 / BUG-031: /vault-files/ is gated by the injected authorizer and
// fails closed without one.
func TestVaultFiles_AuthorizerGate(t *testing.T) {
	s := newTestServer(t)
	abs := filepath.Join(s.vault.Root, "proj", "attachments", "a.png")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	const p = "/vault-files/proj/attachments/a.png"

	// No authorizer installed: fail closed.
	if rec := get(p); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("no authorizer: status=%d www-auth=%q (want 401 Bearer)", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	s.SetVaultFileAuthorizer(func(*http.Request, string) int { return http.StatusUnauthorized })
	if rec := get(p); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated: status=%d", rec.Code)
	}
	if rec := get(p + "?inline"); rec.Code != http.StatusUnauthorized {
		t.Errorf("?inline unauthenticated: status=%d", rec.Code)
	}
	s.SetVaultFileAuthorizer(func(*http.Request, string) int { return http.StatusNotFound })
	if rec := get(p); rec.Code != http.StatusNotFound {
		t.Errorf("forbidden: status=%d (want 404)", rec.Code)
	}
	var seen string
	s.SetVaultFileAuthorizer(func(_ *http.Request, rel string) int { seen = rel; return 0 })
	rec := get(p)
	if rec.Code != http.StatusOK {
		t.Errorf("allowed: status=%d", rec.Code)
	}
	if seen != "proj/attachments/a.png" {
		t.Errorf("authorizer got rel=%q", seen)
	}
	if cc := rec.Header().Get("Cache-Control"); cc == "" || cc[:7] != "private" {
		t.Errorf("authenticated responses must not be publicly cacheable: %q", cc)
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("security headers must survive the gate")
	}
}
