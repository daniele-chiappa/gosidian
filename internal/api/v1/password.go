package v1

import (
	"errors"
	"net/http"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/webauth"
)

// Passwords and re-authentication (IMP-063, IMP-088). A sensitive action —
// changing a password, removing the TOTP, resetting another account's
// password, minting an MCP token — asks for the caller's current password
// in its body: a stolen session token alone cannot use it to lock the
// owner out or to stay in after a logout. A wrong password counts against
// the account's limiter like a failed login, and answers 403, never 401,
// which the web UI would take for an expired session.

// confirmPassword verifies the caller's current password for a sensitive
// action, writing the error and returning false when it does not hold. An
// LDAP account is verified with a bind, as at login.
func (r *Router) confirmPassword(w http.ResponseWriter, user *RequestUser, password string) bool {
	if password == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "password is required to confirm this action")
		return false
	}
	acct := accountLimiterKey(user.Username)
	if r.loginLimiter != nil && !r.loginLimiter.allowed(acct) {
		WriteError(w, http.StatusTooManyRequests, CodeRateLimit, "too many failed attempts; try again later")
		return false
	}
	err := r.deps.Auth.WebAuth.CheckPassword(user.ID, password)
	if errors.Is(err, webauth.ErrNotLocalAccount) {
		err = webauth.ErrInvalidCredentials
		if r.deps.Auth.LDAP != nil && r.deps.Auth.LDAP.Authenticate(user.Username, password) == nil {
			err = nil
		}
	}
	if err != nil {
		if r.loginLimiter != nil {
			r.loginLimiter.registerFail(acct)
		}
		WriteError(w, http.StatusForbidden, CodeAuthInvalidCredentials, "wrong password")
		return false
	}
	if r.loginLimiter != nil {
		r.loginLimiter.reset(acct)
	}
	return true
}

type mePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// handleMePassword changes the caller's own password: POST
// /api/v1/me/password with the current one and the new one. The other web
// sessions of the account are closed, the current one stays; MCP tokens
// are separate credentials and stay too. It also clears the obligation to
// change a password the owner chose, and so stays reachable while that
// obligation gates every other route.
func (r *Router) handleMePassword(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	user := UserFromContext(req.Context())
	if user == nil || r.deps.Auth == nil || r.deps.Auth.WebAuth == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if user.isAnonymous() {
		WriteError(w, http.StatusForbidden, CodeAuthForbidden, "anonymous session has no account to manage")
		return
	}
	var body mePasswordRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if full, ok := r.deps.Auth.WebAuth.UserByID(user.ID); ok && full.AuthSource == "ldap" {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, webauth.ErrNotLocalAccount.Error()+": change it in the directory")
		return
	}
	if body.NewPassword == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "new_password is required")
		return
	}
	if !r.confirmPassword(w, user, body.CurrentPassword) {
		return
	}
	if body.NewPassword == body.CurrentPassword {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, webauth.ErrSamePassword.Error())
		return
	}
	if err := r.deps.Auth.WebAuth.SetPassword(user.ID, body.NewPassword, false); err != nil {
		writePasswordError(w, err)
		return
	}
	closed := 0
	if r.deps.Auth.SpaAuth != nil {
		closed = r.deps.Auth.SpaAuth.RevokeByUserExcept(user.ID, TokenFromContext(req.Context()))
	}
	if r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{Source: audit.SourceHTTP, Actor: user.Username, UserID: user.ID, Action: audit.ActionPasswordChange, Path: user.ID})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"sessions_closed": closed})
}

type resetPasswordRequest struct {
	Password      string `json:"password"`
	OwnerPassword string `json:"owner_password"`
}

// resetUserPassword sets another account's password, temporary: POST
// /admin/users/{id}/password with the new password and the owner's own.
// The account must change it at its next request, and its web sessions are
// closed. The owner changes its own password with /me/password.
func (r *Router) resetUserPassword(w http.ResponseWriter, req *http.Request, id string) {
	actor := UserFromContext(req.Context())
	if actor == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if id == actor.ID {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "change your own password from your settings (/api/v1/me/password)")
		return
	}
	var body resetPasswordRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	target, ok := r.deps.Auth.WebAuth.UserByID(id)
	if !ok {
		WriteError(w, http.StatusNotFound, CodeNotFound, "user not found")
		return
	}
	if target.AuthSource == "ldap" {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, webauth.ErrNotLocalAccount.Error())
		return
	}
	if !target.Enabled() {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "the account is disabled")
		return
	}
	if body.Password == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "password is required")
		return
	}
	if !r.confirmPassword(w, actor, body.OwnerPassword) {
		return
	}
	if err := r.deps.Auth.WebAuth.SetPassword(id, body.Password, true); err != nil {
		writePasswordError(w, err)
		return
	}
	closed, grants := 0, 0
	if r.deps.Auth.SpaAuth != nil {
		closed = r.deps.Auth.SpaAuth.RevokeByUser(id)
	}
	// The OAuth grants go too, as with the CLI reset: a connector authorized
	// with the old password would keep its access (BUG-111, S1-9).
	if tokens := r.mcpTokens(); tokens != nil {
		grants = tokens.RevokeOAuthByOwner(id)
	}
	if r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{Source: audit.SourceHTTP, Actor: actor.Username, UserID: actor.ID, Action: audit.ActionPasswordReset, Path: id})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"sessions_closed": closed, "grants_closed": grants, "must_change_password": true})
}

func writePasswordError(w http.ResponseWriter, err error) {
	if errors.Is(err, webauth.ErrNotLocalAccount) || errors.Is(err, webauth.ErrPasswordTooShort) {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
}
