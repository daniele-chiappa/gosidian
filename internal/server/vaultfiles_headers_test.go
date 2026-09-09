package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BUG-030 / ADR-021: /vault-files/ serves inert bytes. An SVG carrying a
// <script> must not be able to run in the app origin when opened directly,
// and non-image attachments are downloads, not documents.
func TestVaultFiles_SecurityHeaders(t *testing.T) {
	s := newTestServer(t)
	s.SetVaultFileAuthorizer(func(*http.Request, string) int { return 0 })
	root := s.vault.Root
	write := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("proj/attachments/x.svg", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	write("proj/attachments/d.pdf", "%PDF-1.4\n%test\n")

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	svg := get("/vault-files/proj/attachments/x.svg")
	if svg.Code != http.StatusOK {
		t.Fatalf("svg status=%d", svg.Code)
	}
	csp := svg.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "script-src 'none'") {
		t.Errorf("svg CSP missing sandbox/script-src 'none': %q", csp)
	}
	if svg.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("svg missing nosniff")
	}
	if strings.HasPrefix(svg.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("images must stay embeddable inline, got Content-Disposition=%q", svg.Header().Get("Content-Disposition"))
	}
	pdf := get("/vault-files/proj/attachments/d.pdf")
	if !strings.HasPrefix(pdf.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("non-image attachment should be a download, got %q", pdf.Header().Get("Content-Disposition"))
	}
	if pdf.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("pdf missing CSP")
	}
	inline := get("/vault-files/proj/attachments/x.svg?inline")
	if inline.Code != http.StatusOK || inline.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("?inline branch: status=%d csp=%q", inline.Code, inline.Header().Get("Content-Security-Policy"))
	}
}
