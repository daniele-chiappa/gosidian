package v1

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
)

// frameSources gives the origins, besides the SPA's own, whose pages a note
// shows in a frame: the chat plugins' pages of the bots (IMP-191). Nil: none.
var frameSources atomic.Pointer[func() []string]

// SetFrameSources sets what gives the origins the SPA may frame.
func SetFrameSources(fn func() []string) { frameSources.Store(&fn) }

// frameSrc is the CSP's frame-src: 'self' and the origins frameSources
// gives, each checked to be a bare http(s) origin, so that nothing a plugin
// answered can add a directive.
func frameSrc() string {
	out := "frame-src 'self'"
	p := frameSources.Load()
	if p == nil || *p == nil {
		return out
	}
	seen := map[string]bool{}
	for _, o := range (*p)() {
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.ContainsAny(u.Host, " ;,'\"") {
			continue
		}
		origin := u.Scheme + "://" + u.Host
		if !seen[origin] {
			seen[origin] = true
			out += " " + origin
		}
	}
	return out
}

// cspHeader builds the Content-Security-Policy applied to the SPA shell
// (index.html). Strict script-src 'self' (no inline, no eval), with
// 'unsafe-inline' on style-src because the Tailwind utility runtime and
// Reka UI primitives inject scoped <style> blocks. img/data and blob
// support image previews and DOMPurify-sanitized blob URLs. connect-src
// 'self' constrains XHR/fetch + EventSource to same-origin, closing the
// cross-origin exfil route an XSS would normally use even before our
// defense-in-depth DOMPurify sanitisation.
//
// When nonce is non-empty it is folded into script-src as 'nonce-<nonce>'.
// This exists for the sandboxed srcdoc iframe that renders HTML notes
// (ADR-011): an about:srcdoc document INHERITS the embedder's CSP and the
// policies combine by intersection, so the iframe's own injected
// 'unsafe-inline' script-src is cancelled by this 'self'. The SPA stamps
// the per-request nonce onto the note's <script> tags so they satisfy the
// inherited policy (BUG-019). The note still executes in an opaque origin
// (sandbox="allow-scripts" WITHOUT allow-same-origin) with the iframe's
// own default-src 'none' blocking the network, so the nonce does not widen
// the shell's own execution surface — it only un-breaks note interactivity.
func cspHeader(nonce string) string {
	scriptSrc := "script-src 'self'"
	if nonce != "" {
		scriptSrc += " 'nonce-" + nonce + "'"
	}
	return strings.Join([]string{
		"default-src 'self'",
		scriptSrc,
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"font-src 'self'",
		"connect-src 'self'",
		"worker-src 'self' blob:",
		// frame-src 'self' admits the sandboxed srcdoc iframe that renders HTML
		// notes (ADR-011). The iframe runs with sandbox="allow-scripts" WITHOUT
		// allow-same-origin (opaque origin) plus its own injected restrictive CSP,
		// so this does not widen the SPA's own execution surface. The origins
		// of the chat plugins follow: a bot's note shows its chat (IMP-191).
		frameSrc(),
		"frame-ancestors 'none'",
		"form-action 'self'",
		"base-uri 'self'",
		"object-src 'none'",
	}, "; ")
}

// CSPHeader is the nonce-less SPA shell policy. Kept as the canonical
// constant for tests and any caller that does not mint a per-request nonce.
var CSPHeader = cspHeader("")

// NewCSPNonce mints a fresh, unguessable nonce (128 bits, base64url) for a
// single SPA shell response. Returns an error only if the system CSPRNG
// fails, which the caller should treat as fatal for that request. Uses
// RawURLEncoding (no '+', '/', '=') to match the other token helpers in the
// codebase and stay trivially safe in both the CSP token and the HTML attr.
func NewCSPNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SecurityHeaders is the set of defense-in-depth headers attached to
// every response served by this package. Apart from CSP, they are
// cheap, well-understood, and friendly to old clients (browsers that
// don't understand them just ignore them).
//
// Intentionally NOT included:
//   - Strict-Transport-Security (HSTS): the operator may run gosidian
//     behind HTTPS via reverse proxy or directly via http; we don't
//     know which, and a wrong HSTS header can lock users out for a
//     year. Configurable in a future phase if a use case appears.
//   - X-XSS-Protection: deprecated in modern browsers; replaced by
//     CSP nonce/strict-dynamic patterns. Skipped to avoid noise.
//
// X-Frame-Options is duplicated by CSP frame-ancestors 'none' but
// older browsers (pre-Chrome 76, IE) read only XFO; cheap insurance.
var SecurityHeaders = map[string]string{
	"X-Content-Type-Options":            "nosniff",
	"X-Frame-Options":                   "DENY",
	"Referrer-Policy":                   "strict-origin-when-cross-origin",
	"X-Permitted-Cross-Domain-Policies": "none",
}

// applySecurityHeaders is the package-level entry point used by the
// v1 router and the SPA shell handler. Idempotent — calling it twice
// just rewrites the same values.
func applySecurityHeaders(w http.ResponseWriter) {
	for k, v := range SecurityHeaders {
		w.Header().Set(k, v)
	}
}

// SetSPAShellHeaders attaches the defense-in-depth headers plus the
// nonce-bearing CSP to the index.html response. The nonce MUST match the
// one stamped into the served HTML (<meta name="csp-nonce">) so the
// sandboxed HTML-note iframe can execute the note's inline scripts. Lives
// here so the shell header set has a single source of truth — bumps to
// CSP/HSTS land in this package and propagate to every consumer. Applying
// CSP to JSON API responses adds no value and confuses tooling that flags
// Content-Type: application/json with a CSP as misconfigured, so it stays
// shell-only.
func SetSPAShellHeaders(w http.ResponseWriter, nonce string) {
	applySecurityHeaders(w)
	w.Header().Set("Content-Security-Policy", cspHeader(nonce))
}

// securityHeadersMW wraps an http.Handler so every response passes
// through applySecurityHeaders. Used in the v1 router middleware
// stack after the JSON content-type middleware.
func securityHeadersMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		applySecurityHeaders(w)
		next.ServeHTTP(w, r)
	})
}
