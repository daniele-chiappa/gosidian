package v1

import (
	"net/http"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/webauth"
)

// filesCookieName carries the SPA session token to /vault-files/ (ADR-022,
// BUG-031). The browser cannot attach an Authorization header to <img>,
// download links or same-origin fetch() from the note renderers, so the token
// travels as an HttpOnly cookie scoped to that path only. The value is the
// same SPA token the Bearer flow uses (the server stores only its hash), so
// revocation and expiry apply identically.
const filesCookieName = "gosidian_files"

func filesCookie(token string, secure bool, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     filesCookieName,
		Value:    token,
		Path:     "/vault-files/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	}
}

func clearFilesCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     filesCookieName,
		Value:    "",
		Path:     "/vault-files/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	}
}

// setSessionCookies issues the cookies that carry the session where an
// Authorization header cannot go — attachments (gosidian_files) and the
// event stream (gosidian_events) — at login. Every later authenticated API
// call re-issues them through requireAuth when the browser lacks them
// (self-healing for sessions persisted before a cookie existed).
func (r *Router) setSessionCookies(w http.ResponseWriter, req *http.Request, token string, hardExpiry time.Time) {
	secure := webauth.IsSecureRequest(req)
	http.SetCookie(w, filesCookie(token, secure, hardExpiry))
	http.SetCookie(w, eventsCookie(token, secure, hardExpiry))
}

// clearSessionCookies removes both at logout.
func clearSessionCookies(w http.ResponseWriter, req *http.Request) {
	secure := webauth.IsSecureRequest(req)
	http.SetCookie(w, clearFilesCookie(secure))
	http.SetCookie(w, clearEventsCookie(secure))
}

// VaultFileAuthorizer returns the gate the server consults for /vault-files/
// (server.SetVaultFileAuthorizer). Result: 0 = allow, 401 = no usable
// credential, 404 = the principal must not learn the file exists (same
// fail-closed shape as the notes API). Credential order: Authorization Bearer
// (SPA session token, else an MCP token with the read scope whose project
// scope covers the path), then the gosidian_files cookie, then the open-mode
// guest. Reads are not audited, consistently with the notes API.
func (r *Router) VaultFileAuthorizer() func(req *http.Request, rel string) int {
	return func(req *http.Request, rel string) int {
		d := r.deps.Auth
		if d == nil || d.SpaAuth == nil || d.WebAuth == nil {
			return http.StatusUnauthorized
		}
		if tok := extractBearer(req); tok != "" {
			if p, ok := r.spaPrincipal(tok); ok {
				return r.seeOr404(p, rel)
			}
			if d.MCPTokens != nil {
				if mt, err := d.MCPTokens.Validate(tok); err == nil && mt.HasScope(auth.ScopeRead) {
					return r.mcpTokenGate(mt, rel)
				}
			}
			return http.StatusUnauthorized
		}
		if c, err := req.Cookie(filesCookieName); err == nil && c.Value != "" {
			if p, ok := r.spaPrincipal(c.Value); ok {
				return r.seeOr404(p, rel)
			}
			return http.StatusUnauthorized
		}
		if d.OpenMode {
			return r.seeOr404(authz.Principal{Role: webauth.RoleGuest}, rel)
		}
		return http.StatusUnauthorized
	}
}

// mcpTokenGate is the /vault-files/ gate for an MCP token: its declared
// scope narrowed to what the account that owns it may read now, as the MCP
// server narrows it on every call (mcp effectiveToken). The declared scope
// alone let a token with no project list, which every account may create
// for itself, read the attachments of every project, and kept working for
// a disabled owner or a revoked grant (BUG-100). Tokens without an owner
// (CLI, admin) keep their declared scope. A project hidden from MCP is
// refused, as by the MCP download ticket.
func (r *Router) mcpTokenGate(mt *auth.Token, rel string) int {
	project := projectOf(rel)
	if !mt.AllowsPath(rel) || (r.deps.Projects != nil && r.deps.Projects.Get(project).HiddenFromMCP) {
		return http.StatusNotFound
	}
	if mt.OwnerUserID == "" {
		return 0
	}
	user, ok := r.deps.Auth.WebAuth.UserByID(mt.OwnerUserID)
	if !ok || !user.Enabled() {
		return http.StatusUnauthorized
	}
	return r.seeOr404(authz.Principal{UserID: user.ID, Role: user.Role, Restricted: user.Restricted}, rel)
}

// spaPrincipal resolves a SPA session token to its principal, mirroring the
// checks requireAuth performs (valid token, existing enabled user, no pending
// TOTP enrolment, no password to change first: with a temporary password the
// attachments were readable without changing it, BUG-111 S1-11).
func (r *Router) spaPrincipal(token string) (authz.Principal, bool) {
	d := r.deps.Auth
	spaTok, err := d.SpaAuth.Validate(token)
	if err != nil {
		return authz.Principal{}, false
	}
	user, ok := d.WebAuth.UserByID(spaTok.UserID)
	if !ok || !user.Enabled() || d.WebAuth.TOTPEnrollmentRequired(user) || user.MustChangePassword {
		return authz.Principal{}, false
	}
	return authz.Principal{UserID: user.ID, Role: user.Role, Restricted: user.Restricted}, true
}

func (r *Router) seeOr404(p authz.Principal, rel string) int {
	if r.canSee(p, projectOf(rel)) {
		return 0
	}
	return http.StatusNotFound
}
