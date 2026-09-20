package v1

import (
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/webauth"
)

// inviteDefaultTTL mirrors the value the v1.x HTML handler used so
// invites generated from the SPA admin page age the same way as
// those minted from the legacy form. 24h is long enough for the
// owner to share the link asynchronously and short enough to limit
// blast radius if the token leaks.
const inviteDefaultTTL = 24 * time.Hour

type adminUserView struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Role         string `json:"role"`
	TOTPPolicy   string `json:"totp_policy,omitempty"` // "" inherit | enabled | disabled
	TOTPEnrolled bool   `json:"totp_enrolled"`
	CreatedAt    string `json:"created_at"`
	DisabledAt   string `json:"disabled_at,omitempty"`
}

func toAdminUserView(u webauth.User) adminUserView {
	uv := adminUserView{
		ID:           u.ID,
		Username:     u.Username,
		Role:         string(u.Role),
		TOTPPolicy:   u.TOTPPolicy,
		TOTPEnrolled: u.TOTPSec != "",
		CreatedAt:    u.CreatedAt.UTC().Format(rfc3339Z),
	}
	if u.DisabledAt != nil {
		uv.DisabledAt = u.DisabledAt.UTC().Format(rfc3339Z)
	}
	return uv
}

type inviteView struct {
	Token      string `json:"token"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  string `json:"created_at"`
	ExpiresAt  string `json:"expires_at"`
	ConsumedBy string `json:"consumed_by,omitempty"`
	ConsumedAt string `json:"consumed_at,omitempty"`
	Pending    bool   `json:"pending"`
}

// ---- /api/v1/admin/users ----

func (r *Router) handleAdminUsers(w http.ResponseWriter, req *http.Request) {
	if r.deps.Auth == nil || r.deps.Auth.WebAuth == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "web auth not configured")
		return
	}
	switch req.Method {
	case http.MethodGet:
		users := r.deps.Auth.WebAuth.ListUsers()
		out := make([]adminUserView, 0, len(users))
		for _, u := range users {
			out = append(out, toAdminUserView(u))
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
	case http.MethodPost:
		r.createUser(w, req)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

type createUserRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	Role       string `json:"role,omitempty"`        // member (default) | guest
	TOTPPolicy string `json:"totp_policy,omitempty"` // "" inherit | enabled | disabled
}

// createUser provisions a new account directly (owner-only, POST /admin/users)
// — the admin-driven counterpart to the invite + /signup self-service flow. The
// owner is a singleton, so only member/guest can be created here; webauth.AddUser
// carries the validation (>= 8 char password, unique username) and a duplicate
// maps to 409 exactly as /signup does. An optional totp_policy sets the per-user
// override at creation instead of a follow-up PATCH.
func (r *Router) createUser(w http.ResponseWriter, req *http.Request) {
	actor := UserFromContext(req.Context())
	var body createUserRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if body.Username == "" || body.Password == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "username and password are required")
		return
	}
	role := webauth.RoleMember
	if rs := strings.TrimSpace(body.Role); rs != "" {
		role = webauth.Role(rs)
	}
	if role != webauth.RoleMember && role != webauth.RoleGuest {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "role must be 'member' or 'guest'")
		return
	}
	// Validate the optional override up front so we never create an account and
	// then have to roll it back on a bad policy value.
	policy := strings.TrimSpace(body.TOTPPolicy)
	switch policy {
	case webauth.TOTPInherit, webauth.TOTPEnabled, webauth.TOTPDisabled:
	default:
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "totp_policy must be '', 'enabled' or 'disabled'")
		return
	}
	user, err := r.deps.Auth.WebAuth.AddUser(body.Username, body.Password, role)
	if err != nil {
		// AddUser carries the precise reason; duplicate username → 409 (like /signup).
		if isDuplicateUsername(err.Error()) {
			WriteError(w, http.StatusConflict, CodeConflict, err.Error())
			return
		}
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if policy != webauth.TOTPInherit {
		if err := r.deps.Auth.WebAuth.SetTOTPPolicy(user.ID, policy); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
			return
		}
	}
	if actor != nil && r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{
			Source: audit.SourceHTTP,
			Actor:  actor.Username,
			UserID: actor.ID,
			Action: audit.ActionUserCreate,
			Path:   user.ID,
		})
	}
	if u, ok := r.deps.Auth.WebAuth.UserByID(user.ID); ok {
		WriteJSON(w, http.StatusCreated, toAdminUserView(*u))
		return
	}
	WriteJSON(w, http.StatusCreated, toAdminUserView(*user))
}

// handleAdminUserItem covers DELETE → DisableUser. The webauth API
// has no symmetric Enable, so re-activating a disabled user requires
// editing accounts.json directly. That's intentional — disabling is a
// safety action and the explicit lockout is a feature, not a gap.
func (r *Router) handleAdminUserItem(w http.ResponseWriter, req *http.Request) {
	if r.deps.Auth == nil || r.deps.Auth.WebAuth == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "web auth not configured")
		return
	}
	id, sub, _ := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v1/admin/users/"), "/"), "/")
	if id == "" || (sub != "" && sub != "totp") {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "expected /api/v1/admin/users/{id} or /api/v1/admin/users/{id}/totp")
		return
	}
	if sub == "totp" {
		if req.Method != http.MethodDelete {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
			return
		}
		r.resetUserTOTP(w, req, id)
		return
	}
	switch req.Method {
	case http.MethodPatch:
		r.updateUserRole(w, req, id)
		return
	case http.MethodDelete:
		// Disable logic runs below.
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if err := r.deps.Auth.WebAuth.DisableUser(id); err != nil {
		// DisableUser returns a clear "not found" or "owner cannot be
		// disabled" — surface verbatim so the SPA can render a useful
		// message.
		if strings.Contains(err.Error(), "not found") {
			WriteError(w, http.StatusNotFound, CodeNotFound, err.Error())
			return
		}
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	// Cascade: revoke MCP tokens owned by this user. The cascade hook
	// is normally wired by main.go but the API path also calls it
	// inline so admin-initiated disable doesn't depend on cmd/main
	// wiring order.
	if r.deps.Auth.MCPTokens != nil {
		_ = r.deps.Auth.MCPTokens.RevokeByOwner(id)
	}
	if r.deps.Auth.SpaAuth != nil {
		_ = r.deps.Auth.SpaAuth.RevokeByUser(id)
	}
	// Strip the user from every project ACL so no stale membership lingers.
	if r.deps.Projects != nil {
		_ = r.deps.Projects.RemoveUserEverywhere(id)
	}
	if user := UserFromContext(req.Context()); user != nil && r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{
			Source: audit.SourceHTTP,
			Actor:  user.Username,
			UserID: user.ID,
			Action: audit.ActionUserDisable,
			Path:   id,
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

type updateUserRequest struct {
	Role       *string `json:"role,omitempty"`
	TOTPPolicy *string `json:"totp_policy,omitempty"` // "" inherit | enabled | disabled
}

// updateUserRole changes a user's role (member↔guest) and/or their per-user
// TOTP override (owner-only, PATCH /admin/users/{id}). The owner is a singleton
// and immutable; promoting to owner is unsupported. Demoting to guest cascades
// a revoke of any MCP tokens the user owned — guests hold no agent credentials.
func (r *Router) updateUserRole(w http.ResponseWriter, req *http.Request, id string) {
	actor := UserFromContext(req.Context())
	var body updateUserRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if body.Role == nil && body.TOTPPolicy == nil {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "role or totp_policy required")
		return
	}
	if body.Role != nil {
		role := webauth.Role(strings.TrimSpace(*body.Role))
		if role != webauth.RoleMember && role != webauth.RoleGuest {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "role must be 'member' or 'guest'")
			return
		}
		if err := r.deps.Auth.WebAuth.SetRole(id, role); err != nil {
			switch {
			case strings.Contains(err.Error(), "not found"):
				WriteError(w, http.StatusNotFound, CodeNotFound, err.Error())
			case strings.Contains(err.Error(), "owner"):
				WriteError(w, http.StatusForbidden, CodeAuthForbidden, err.Error())
			default:
				WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			}
			return
		}
		if role == webauth.RoleGuest && r.deps.Auth.MCPTokens != nil {
			_ = r.deps.Auth.MCPTokens.RevokeByOwner(id)
		}
	}
	if body.TOTPPolicy != nil {
		if err := r.deps.Auth.WebAuth.SetTOTPPolicy(id, strings.TrimSpace(*body.TOTPPolicy)); err != nil {
			if strings.Contains(err.Error(), "not found") {
				WriteError(w, http.StatusNotFound, CodeNotFound, err.Error())
			} else {
				WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			}
			return
		}
	}
	if actor != nil && r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{
			Source: audit.SourceHTTP,
			Actor:  actor.Username,
			UserID: actor.ID,
			Action: audit.ActionUserUpdate,
			Path:   id,
		})
	}
	if u, ok := r.deps.Auth.WebAuth.UserByID(id); ok {
		WriteJSON(w, http.StatusOK, toAdminUserView(*u))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resetUserTOTP clears a user's TOTP secret and recovery codes (owner-only,
// DELETE /admin/users/{id}/totp): the escape hatch for a lost authenticator.
// The per-user policy stays, so an account that must have two-factor meets
// the enrolment interstitial at its next login; sessions stay too — a lost
// device is not a compromised account (disable + token revoke covers that).
// Allowed on the owner's own account: the caller already proved ownership.
func (r *Router) resetUserTOTP(w http.ResponseWriter, req *http.Request, id string) {
	if err := r.deps.Auth.WebAuth.ResetTOTP(id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			WriteError(w, http.StatusNotFound, CodeNotFound, err.Error())
			return
		}
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	if actor := UserFromContext(req.Context()); actor != nil && r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{
			Source: audit.SourceHTTP,
			Actor:  actor.Username,
			UserID: actor.ID,
			Action: audit.ActionTOTPReset,
			Path:   id,
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- /api/v1/admin/invites ----

func (r *Router) handleAdminInvites(w http.ResponseWriter, req *http.Request) {
	if r.deps.Auth == nil || r.deps.Auth.WebAuth == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "web auth not configured")
		return
	}
	switch req.Method {
	case http.MethodGet:
		r.listInvites(w, req)
	case http.MethodPost:
		r.createInvite(w, req)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) listInvites(w http.ResponseWriter, _ *http.Request) {
	invites := r.deps.Auth.WebAuth.ListInvites()
	out := make([]inviteView, 0, len(invites))
	for _, iv := range invites {
		view := inviteView{
			Token:     iv.Token,
			CreatedBy: iv.CreatedBy,
			CreatedAt: iv.CreatedAt.UTC().Format(rfc3339Z),
			ExpiresAt: iv.ExpiresAt.UTC().Format(rfc3339Z),
			Pending:   iv.Pending(),
		}
		if iv.ConsumedAt != nil {
			view.ConsumedBy = iv.ConsumedBy
			view.ConsumedAt = iv.ConsumedAt.UTC().Format(rfc3339Z)
		}
		out = append(out, view)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
}

func (r *Router) createInvite(w http.ResponseWriter, req *http.Request) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	// Body is optional; accept empty or {} as defaults.
	var body struct {
		TTLMS int64 `json:"ttl_ms,omitempty"`
	}
	if req.ContentLength > 0 {
		if err := DecodeJSON(req, &body); err != nil {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return
		}
	}
	ttl := inviteDefaultTTL
	if body.TTLMS > 0 {
		ttl = time.Duration(body.TTLMS) * time.Millisecond
	}
	inv, err := r.deps.Auth.WebAuth.CreateInvite(user.ID, ttl)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	if r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{
			Source: audit.SourceHTTP,
			Actor:  user.Username,
			UserID: user.ID,
			Action: "invite_create",
			Path:   inv.Token,
		})
	}
	WriteJSON(w, http.StatusCreated, inviteView{
		Token:     inv.Token,
		CreatedBy: inv.CreatedBy,
		CreatedAt: inv.CreatedAt.UTC().Format(rfc3339Z),
		ExpiresAt: inv.ExpiresAt.UTC().Format(rfc3339Z),
		Pending:   true,
	})
}

func (r *Router) handleAdminInviteItem(w http.ResponseWriter, req *http.Request) {
	if r.deps.Auth == nil || r.deps.Auth.WebAuth == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "web auth not configured")
		return
	}
	token := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v1/admin/invites/"), "/")
	if token == "" || strings.Contains(token, "/") {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "expected /api/v1/admin/invites/{token}")
		return
	}
	if req.Method != http.MethodDelete {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if err := r.deps.Auth.WebAuth.RevokeInvite(token); err != nil {
		WriteError(w, http.StatusNotFound, CodeNotFound, err.Error())
		return
	}
	if user := UserFromContext(req.Context()); user != nil && r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{
			Source: audit.SourceHTTP,
			Actor:  user.Username,
			UserID: user.ID,
			Action: "invite_revoke",
			Path:   token,
		})
	}
	w.WriteHeader(http.StatusNoContent)
}
