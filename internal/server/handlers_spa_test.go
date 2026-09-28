package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandleSPA_NonceInjected locks the BUG-019 contract: the shell ships a
// per-request CSP nonce both in the script-src header and in a <meta> the SPA
// reads, the two values match, and the nonce rotates per request.
func TestHandleSPA_NonceInjected(t *testing.T) {
	s := newTestServer(t)

	get := func() (csp, body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		s.handleSPA(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		return rec.Header().Get("Content-Security-Policy"), rec.Body.String()
	}

	csp, body := get()

	const marker = "script-src 'self' 'nonce-"
	i := strings.Index(csp, marker)
	if i < 0 {
		t.Fatalf("CSP missing nonce'd script-src: %q", csp)
	}
	rest := csp[i+len(marker):]
	end := strings.IndexByte(rest, '\'')
	if end <= 0 {
		t.Fatalf("malformed nonce in CSP: %q", csp)
	}
	nonce := rest[:end]

	// The same nonce must reach the SPA via <meta> so it can stamp note scripts.
	wantMeta := `<meta name="csp-nonce" content="` + nonce + `">`
	if !strings.Contains(body, wantMeta) {
		t.Errorf("meta nonce missing/mismatched; want %q in shell body", wantMeta)
	}

	// Per-request rotation: a static nonce would defeat the purpose.
	if csp2, _ := get(); csp2 == csp {
		t.Errorf("nonce did not rotate across requests: %q", csp2)
	}
}

// TestHandleSPA_WellKnownNotFound: discovery paths never get the HTML shell.
// With OAuth off the oauth-* documents are not mounted and must 404, not
// 200 text/html; SPA routes keep the shell.
func TestHandleSPA_WellKnownNotFound(t *testing.T) {
	s := newTestServer(t)
	cases := []struct {
		path string
		want int
	}{
		{"/.well-known/oauth-authorization-server", http.StatusNotFound},
		{"/.well-known/oauth-protected-resource", http.StatusNotFound},
		{"/.well-known/oauth-protected-resource/mcp", http.StatusNotFound},
		{"/.well-known/security.txt", http.StatusNotFound},
		{"/oauth/consent", http.StatusOK},
		{"/notes/foo", http.StatusOK},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s: status=%d, want %d", c.path, rec.Code, c.want)
		}
		if c.want == http.StatusNotFound && strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s: got HTML shell on a discovery path", c.path)
		}
	}
}
