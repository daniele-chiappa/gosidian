package v1

import (
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

	store := r.deps.Auth.WebAuth
	inv := store.FindInvite(body.Invite)
	if inv == nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "invite token unknown, consumed, or expired")
		return
	}

	user, err := store.AddUser(body.Username, body.Password, webauth.RoleMember)
	if err != nil {
		// Two-step is intentional: AddUser carries the precise reason
		// (duplicate username, weak password) which we surface verbatim.
		if isDuplicateUsername(err.Error()) {
			WriteError(w, http.StatusConflict, CodeConflict, err.Error())
			return
		}
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}

	// Mark the invite consumed only after the user landed on disk —
	// otherwise a duplicate-username race would burn the invite for nothing.
	if err := store.ClaimInvite(body.Invite, user.ID); err != nil {
		// Clean up the orphan account: AddUser already persisted, so we
		// must roll it back to keep the invite/account invariants.
		_ = store.DisableUser(user.ID)
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
