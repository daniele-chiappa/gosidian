package v1

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ---- Security headers ----

func TestSecurityHeaders_AppliedToAPIResponses(t *testing.T) {
	f := newAuthFixture(t)
	w := f.request(http.MethodGet, "/api/v1/health", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	for header, want := range SecurityHeaders {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestSecurityHeaders_AppliedOnErrorPath(t *testing.T) {
	// 401 short-circuits in middleware; defense-in-depth headers
	// must still land or an attacker crafting a 401 would get a
	// header-less response.
	f := newAuthFixture(t)
	w := f.request(http.MethodGet, "/api/v1/notes/anything", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", w.Code)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff missing on 401: got %q", got)
	}
}

func TestCSPHeader_NotOnAPIResponses(t *testing.T) {
	// CSP belongs on the SPA shell, not on JSON. Confirm the API
	// path doesn't accidentally ship a CSP that breaks tooling.
	f := newAuthFixture(t)
	w := f.request(http.MethodGet, "/api/v1/health", "", nil)
	if got := w.Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("CSP should not be set on JSON responses, got %q", got)
	}
}

func TestCSPHeader_StrictScriptSrc(t *testing.T) {
	// Sanity check on the constant: 'unsafe-inline' or 'unsafe-eval'
	// in script-src would defeat the whole point. Catch a regression
	// at compile time-ish.
	if !strings.Contains(CSPHeader, "script-src 'self'") {
		t.Errorf("CSPHeader missing script-src 'self': %q", CSPHeader)
	}
	if strings.Contains(CSPHeader, "script-src 'unsafe-inline'") ||
		strings.Contains(CSPHeader, "script-src 'unsafe-eval'") {
		t.Errorf("CSPHeader script-src must not allow unsafe-*: %q", CSPHeader)
	}
}

func TestCSPHeaderWithNonce(t *testing.T) {
	// BUG-019: the SPA shell folds a per-request nonce into script-src so the
	// sandboxed HTML-note iframe (which inherits this policy) can run the
	// note's inline scripts once the SPA stamps the matching nonce.
	h := cspHeader("AbC-123_x")
	if !strings.Contains(h, "script-src 'self' 'nonce-AbC-123_x'") {
		t.Errorf("nonce not folded into script-src: %q", h)
	}
	for _, want := range []string{"default-src 'self'", "frame-src 'self'", "object-src 'none'"} {
		if !strings.Contains(h, want) {
			t.Errorf("directive %q dropped from nonce'd CSP: %q", want, h)
		}
	}
	// Empty nonce is identical to the canonical constant — no stray 'nonce-'.
	if cspHeader("") != CSPHeader {
		t.Errorf("empty nonce should equal CSPHeader")
	}
	if strings.Contains(CSPHeader, "nonce-") {
		t.Errorf("canonical CSPHeader must not contain a nonce: %q", CSPHeader)
	}
}

func TestNewCSPNonce(t *testing.T) {
	a, err := NewCSPNonce()
	if err != nil {
		t.Fatalf("NewCSPNonce: %v", err)
	}
	if a == "" {
		t.Fatal("empty nonce")
	}
	if _, derr := base64.RawURLEncoding.DecodeString(a); derr != nil {
		t.Errorf("nonce not valid base64: %q (%v)", a, derr)
	}
	if b, _ := NewCSPNonce(); a == b {
		t.Errorf("nonces must differ across calls: %q == %q", a, b)
	}
}

// ---- Login rate-limit ----

func TestLoginRateLimit_BlocksAfterFailures(t *testing.T) {
	f := newAuthFixture(t)
	// Tighten the window for the duration of this test so the
	// failure burst cleans up after we're done.
	prevMax := LoginMaxFailures
	prevWin := LoginWindow
	LoginMaxFailures = 3
	LoginWindow = 1 * time.Minute
	defer func() {
		LoginMaxFailures = prevMax
		LoginWindow = prevWin
	}()

	body := `{"username":"owner","password":"wrong"}`
	for i := 0; i < LoginMaxFailures; i++ {
		w := f.request(http.MethodPost, "/api/v1/login", body, nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status=%d, want 401", i+1, w.Code)
		}
	}
	// Next attempt should be rate-limited.
	w := f.request(http.MethodPost, "/api/v1/login", body, nil)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("after %d failures: status=%d, want 429 body=%s", LoginMaxFailures, w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), CodeRateLimit) {
		t.Errorf("missing rate-limit code: %s", w.Body.String())
	}
}

func TestLoginRateLimit_ResetsOnSuccess(t *testing.T) {
	f := newAuthFixture(t)
	prevMax := LoginMaxFailures
	prevWin := LoginWindow
	LoginMaxFailures = 3
	LoginWindow = 1 * time.Minute
	defer func() {
		LoginMaxFailures = prevMax
		LoginWindow = prevWin
	}()

	wrong := `{"username":"owner","password":"wrong"}`
	right := `{"username":"owner","password":"supersecret"}`

	// 2 failures → still allowed
	for i := 0; i < 2; i++ {
		f.request(http.MethodPost, "/api/v1/login", wrong, nil)
	}
	// Successful login resets the counter.
	if w := f.request(http.MethodPost, "/api/v1/login", right, nil); w.Code != http.StatusOK {
		t.Fatalf("right password rejected: status=%d body=%s", w.Code, w.Body.String())
	}
	// 3 more failures should be allowed since the counter was reset.
	for i := 0; i < 2; i++ {
		w := f.request(http.MethodPost, "/api/v1/login", wrong, nil)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("attempt %d after reset: status=%d", i+1, w.Code)
		}
	}
}

func TestLoginRateLimit_DoesNotBlockOtherIPs(t *testing.T) {
	// Simulate two distinct clients via X-Forwarded-For. One IP
	// burns through its quota; the other should remain allowed. The
	// header is honoured only because the httptest peer (192.0.2.1) is
	// declared a trusted proxy.
	f := newAuthFixture(t)
	withTrustedProxies(t, "192.0.2.1")
	prevMax := LoginMaxFailures
	prevWin := LoginWindow
	LoginMaxFailures = 2
	LoginWindow = 1 * time.Minute
	defer func() {
		LoginMaxFailures = prevMax
		LoginWindow = prevWin
	}()

	wrong := `{"username":"owner","password":"wrong"}`
	for i := 0; i < LoginMaxFailures; i++ {
		w := f.request(http.MethodPost, "/api/v1/login", wrong, map[string]string{
			"X-Forwarded-For": "10.0.0.1",
		})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attacker attempt %d: status=%d", i+1, w.Code)
		}
	}
	// Attacker IP is now blocked.
	wAttacker := f.request(http.MethodPost, "/api/v1/login", wrong, map[string]string{
		"X-Forwarded-For": "10.0.0.1",
	})
	if wAttacker.Code != http.StatusTooManyRequests {
		t.Errorf("attacker not blocked: status=%d", wAttacker.Code)
	}
	// Different IP must still be allowed (will get 401, not 429).
	wOther := f.request(http.MethodPost, "/api/v1/login", wrong, map[string]string{
		"X-Forwarded-For": "10.0.0.2",
	})
	if wOther.Code == http.StatusTooManyRequests {
		t.Errorf("unrelated IP wrongly blocked: status=%d", wOther.Code)
	}
}

// Without a trusted proxy in front, rotating X-Forwarded-For must not buy a
// fresh rate-limit bucket per attempt (BUG-047).
func TestLoginRateLimit_SpoofedXFFDoesNotBypass(t *testing.T) {
	f := newAuthFixture(t)
	prevMax := LoginMaxFailures
	prevWin := LoginWindow
	LoginMaxFailures = 2
	LoginWindow = 1 * time.Minute
	defer func() {
		LoginMaxFailures = prevMax
		LoginWindow = prevWin
	}()

	wrong := `{"username":"owner","password":"wrong"}`
	for i := 0; i < LoginMaxFailures; i++ {
		w := f.request(http.MethodPost, "/api/v1/login", wrong, map[string]string{
			"X-Forwarded-For": fmt.Sprintf("10.0.0.%d", i+1),
		})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status=%d", i+1, w.Code)
		}
	}
	w := f.request(http.MethodPost, "/api/v1/login", wrong, map[string]string{
		"X-Forwarded-For": "10.0.0.99",
	})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("spoofed XFF bypassed the limiter: status=%d", w.Code)
	}
}
