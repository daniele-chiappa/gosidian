package v1

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/webauth"
)

// signupRequest matches the OpenAPI SignupRequest schema. The invite
// token is the gating mechanism: only owners can mint invites, only
// the recipient can redeem one.
type signupRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Invite   string `json:"invite"`
}

// handleSignup redeems a single-use invite token to create a new
// member account. The newly created user does NOT auto-login — the
// SPA redirects to the login page after the response so the standard
// auth flow issues a fresh Bearer token.
func (r *Router) handleSignup(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.deps.Auth == nil || r.deps.Auth.WebAuth == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "web auth not configured")
		return
	}

	var body signupRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if body.Username == "" || body.Password == "" || body.Invite == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "username, password and invite are required")
		return
	}

	// The invite is checked, the account created and the invite consumed
	// in one save (RedeemInvite): a failure keeps neither.
	user, err := r.deps.Auth.WebAuth.RedeemInvite(body.Invite, body.Username, body.Password)
	switch {
	case errors.Is(err, webauth.ErrInviteInvalid):
		WriteError(w, http.StatusBadRequest, CodeAuthInviteInvalid, err.Error())
		return
	case errors.Is(err, webauth.ErrAccountsSave):
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	case err != nil && isDuplicateUsername(err.Error()):
		WriteError(w, http.StatusConflict, CodeConflict, err.Error())
		return
	case err != nil:
		// The account's own validation (a weak password), surfaced verbatim.
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}

	if r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{
			Source: audit.SourceHTTP,
			Actor:  user.Username,
			UserID: user.ID,
			Action: audit.ActionSpaTokenCreate, // signup-followed-by-login candidate; keep explicit
			Path:   "signup",
		})
	}

	WriteJSON(w, http.StatusCreated, userView{
		ID:       user.ID,
		Username: user.Username,
		Role:     string(user.Role),
	})
}

// isDuplicateUsername sniffs the AddUser error to map it to 409.
// AddUser returns "username %q already exists" so the substring is
// stable enough for routing without reaching for typed errors.
func isDuplicateUsername(msg string) bool {
	return strings.Contains(msg, "already exists")
}
