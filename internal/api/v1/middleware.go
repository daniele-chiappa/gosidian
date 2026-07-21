package v1

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/webauth"
)

// ctxKey is the unexported type for context keys defined in this
// package. Keeps lookups namespaced and immune from cross-package
// collisions.
type ctxKey int

const (
	ctxKeyUser ctxKey = iota + 1
	ctxKeyToken
	ctxKeyTraceID
)

// anonymousUserID is the synthetic ID of the open-mode guest principal
// injected by requireAuth when GOSIDIAN_OPEN_MODE=readonly and no token is
// present. It is not a valid derived account ID, so it never collides with a
// real user. See BUG-018.
const anonymousUserID = "anonymous"

// RequestUser is the minimal projection of a webauth.User attached to
// the request context after auth middleware succeeds. Handlers consume
// this through UserFromContext. Locale is resolved from the SPA's
// settings store (or the Accept-Language header) on a per-request basis;
// it is not stored on webauth.User itself.
type RequestUser struct {
	ID       string
	Username string
	Role     webauth.Role
}

// isAnonymous reports whether this is the synthetic open-mode guest (no real
// account). Per-account routes (e.g. TOTP enrolment) must reject it.
func (u *RequestUser) isAnonymous() bool {
	return u != nil && u.ID == anonymousUserID
}

// UserFromContext returns the authenticated user, or nil if the
// request did not pass requireAuth.
func UserFromContext(ctx context.Context) *RequestUser {
	v, _ := ctx.Value(ctxKeyUser).(*RequestUser)
	return v
}

// TokenFromContext returns the SPA token plaintext as it arrived in
// the Authorization header. Used by /logout to revoke the token that
// authorized the request.
func TokenFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyToken).(string)
	return v
}

// AuthDeps bundles the dependencies the auth middleware needs. Wired
// once by the server and threaded through the chain.
type AuthDeps struct {
	WebAuth   *webauth.Store
	SpaAuth   *auth.SpaTokenStore
	MCPTokens *auth.Store               // long-lived MCP credentials, surfaced via /admin/tokens
	LDAP      webauth.LDAPAuthenticator // optional; nil = LDAP disabled
	Logger    *slog.Logger
	// OpenMode, when true, lets token-less requests through as an anonymous
	// guest (read-only, public projects only) instead of a 401. Off by default;
	// set from GOSIDIAN_OPEN_MODE=readonly. See BUG-018.
	OpenMode bool
}

// requireAuth enforces a valid Bearer token. On failure it writes a
// 401 ErrorResponse and short-circuits the chain. On success it
// stores RequestUser and the raw token in context.
func (d *AuthDeps) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractBearer(r)
		if token == "" {
			if d.OpenMode {
				// Open-mode (read-only): a token-less request runs as an anonymous
				// guest. The existing RBAC does the rest — guests see only public
				// projects (CanAccessProject) and denyGuestWrite/requireOwner
				// reject every mutation and admin route. See BUG-018.
				ru := &RequestUser{ID: anonymousUserID, Username: anonymousUserID, Role: webauth.RoleGuest}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyUser, ru)))
				return
			}
			WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "missing or malformed Authorization header")
			return
		}
		spaTok, err := d.SpaAuth.Validate(token)
		if err != nil {
			code := CodeAuthTokenInvalid
			if strings.Contains(err.Error(), "expired") {
				code = CodeAuthTokenExpired
			}
			WriteError(w, http.StatusUnauthorized, code, err.Error())
			return
		}
		user, ok := d.WebAuth.UserByID(spaTok.UserID)
		if !ok || !user.Enabled() {
			// User removed or disabled since token was issued. Revoke
			// and 401 — the SPA will route to /login next tick.
			_ = d.SpaAuth.RevokeByHash(spaTok.Hash)
			WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "user no longer exists or is disabled")
			return
		}
		// Server-side enforcement of the TOTP enrolment interstitial: a user
		// whose effective policy mandates TOTP but who has not enrolled a secret
		// yet holds a valid token (so they can reach the enrol/confirm flow), but
		// every other route is refused. Without this the SPA-only interstitial is
		// bypassable by hitting the API directly with the issued token. The 403
		// also lets the SPA show the interstitial mid-session if an owner flips
		// the global mode to "required". See BUG-020.
		if d.WebAuth.TOTPEnrollmentRequired(user) && !enrollmentExemptPath(r.URL.Path) {
			WriteError(w, http.StatusForbidden, CodeAuthEnrollmentRequired,
				"two-factor enrolment required before accessing this resource")
			return
		}
		ru := &RequestUser{
			ID:       user.ID,
			Username: user.Username,
			Role:     user.Role,
		}
		ctx := context.WithValue(r.Context(), ctxKeyUser, ru)
		ctx = context.WithValue(ctx, ctxKeyToken, token)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// enrollmentExemptPath reports whether path stays reachable for an
// authenticated user who still owes a TOTP enrolment: the enrolment flow
// itself, plus the session-lifecycle endpoints so the interstitial session can
// refresh its token, read its own identity, and log out. Everything else is
// gated until a secret is enrolled. See requireAuth / BUG-020.
func enrollmentExemptPath(p string) bool {
	switch p {
	case "/api/v1/totp/enroll", "/api/v1/totp/confirm", "/api/v1/refresh", "/api/v1/logout", "/api/v1/me":
		return true
	}
	return false
}

// requireOwner runs after requireAuth and rejects non-owner sessions.
func (d *AuthDeps) requireOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFromContext(r.Context())
		if u == nil || u.Role != webauth.RoleOwner {
			WriteError(w, http.StatusForbidden, CodeAuthOwnerOnly, "owner role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// originValidate rejects mutating requests whose Origin header does
// not match the request's host. Bearer-in-header already protects
// against classic CSRF, but checking Origin is cheap defense in depth
// against confused-deputy bugs in client libraries.
func originValidate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			// Some non-browser clients (curl, server-to-server) omit Origin —
			// allowed; the Bearer token still gates access.
			next.ServeHTTP(w, r)
			return
		}
		// Compare origin host against request host (loose: allow http/https).
		if !strings.HasSuffix(origin, "://"+r.Host) {
			WriteError(w, http.StatusForbidden, CodeAuthForbidden, "origin mismatch")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// jsonHeaders ensures every response advertises JSON, even on routes
// that fall through without writing a body (rare, but the SPA fetch
// helpers expect a Content-Type to mirror).
func jsonHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		next.ServeHTTP(w, r)
	})
}

// observe is a thin instrumentation wrapper that logs slow handlers.
// Real metrics are collected by the existing internal/metrics route
// label dispatcher; this is just a developer-friendly slow-query log.
func observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		if d := time.Since(started); d > 500*time.Millisecond {
			// EscapedPath, not Path: the decoded form can carry client-chosen
			// control bytes (%0A → newline) that would forge log lines in the
			// text format. The newline strip on top is redundant in practice
			// (the escaped form has no raw control bytes) but it is the
			// sanitizer CodeQL's go/log-injection taint model recognizes.
			path := strings.ReplaceAll(strings.ReplaceAll(r.URL.EscapedPath(), "\n", ""), "\r", "")
			slog.Default().Warn("api/v1: slow handler",
				"method", r.Method, "path", path, "elapsed", d.String())
		}
	})
}

func extractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, prefix))
}
