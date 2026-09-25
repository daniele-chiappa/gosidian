package v1

import (
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
)

// mcpTokenView is the JSON shape returned by /admin/tokens. Plaintext
// is NEVER included — the response on POST is a separate
// `mcpTokenCreatedResponse` so the SPA explicitly handles the
// "show once, never again" affordance for the freshly minted secret.
type mcpTokenView struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Project          string   `json:"project,omitempty"`  // display: full scope, comma-joined when multi
	Projects         []string `json:"projects,omitempty"` // multi-project scope list
	Scopes           []string `json:"scopes"`
	OwnerUserID      string   `json:"owner_user_id,omitempty"`
	CreatedAt        string   `json:"created_at"`
	ExpiresAt        string   `json:"expires_at,omitempty"`
	Expired          bool     `json:"expired,omitempty"`
	SelfImproveOptIn bool     `json:"self_improve_opt_in"`
	ToolProfile      string   `json:"tool_profile,omitempty"` // "" | "full" | "core"
	Kind             string   `json:"kind,omitempty"`         // "" static bearer | "oauth" grant minted by a consent (IMP-092)
	ClientID         string   `json:"client_id,omitempty"`    // OAuth client the grant was issued to
}

type mcpTokenCreatedResponse struct {
	Token     string       `json:"token"` // plaintext, shown once
	Record    mcpTokenView `json:"record"`
	UsageHint string       `json:"usage_hint"`
}

type createMCPTokenRequest struct {
	Name        string   `json:"name"`
	Project     string   `json:"project,omitempty"`  // single project (SPA form)
	Projects    []string `json:"projects,omitempty"` // multi-project scope; wins over Project when set
	Scopes      []string `json:"scopes"`
	TTLMS       int64    `json:"ttl_ms,omitempty"`
	ToolProfile string   `json:"tool_profile,omitempty"` // "" | "full" | "core" (worker subset)
}

// updateMCPTokenRequest is the PATCH body for an existing token. Fields are
// pointers so absent ones are left untouched; today only the self-improve
// opt-in flag is mutable (IMP-051).
type updateMCPTokenRequest struct {
	SelfImproveOptIn *bool `json:"self_improve_opt_in"`
}

func (r *Router) handleAdminTokens(w http.ResponseWriter, req *http.Request) {
	if r.deps.Auth == nil || r.deps.Auth.MCPTokens == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "mcp token store not configured")
		return
	}
	switch req.Method {
	case http.MethodGet:
		r.listMCPTokens(w, req)
	case http.MethodPost:
		r.createMCPToken(w, req)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) handleAdminTokenItem(w http.ResponseWriter, req *http.Request) {
	if r.deps.Auth == nil || r.deps.Auth.MCPTokens == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "mcp token store not configured")
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v1/admin/tokens/"), "/")
	if id == "" || strings.Contains(id, "/") {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "expected /api/v1/admin/tokens/{id}")
		return
	}
	switch req.Method {
	case http.MethodDelete:
		r.revokeMCPToken(w, req, id)
	case http.MethodPatch:
		r.updateMCPToken(w, req, id)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) revokeMCPToken(w http.ResponseWriter, req *http.Request, id string) {
	user := UserFromContext(req.Context())
	if err := r.deps.Auth.MCPTokens.Revoke(id); err != nil {
		WriteError(w, http.StatusNotFound, CodeNotFound, err.Error())
		return
	}
	if r.deps.Audit != nil && user != nil {
		_ = r.deps.Audit.Write(audit.Entry{
			Source: audit.SourceHTTP,
			Actor:  user.Username,
			UserID: user.ID,
			Action: audit.ActionTokenRevoke,
			Path:   id,
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// updateMCPToken applies a partial update to an existing MCP token. Today the
// only mutable field is the self-improve opt-in flag (IMP-051): owners can
// enrol/withdraw a token from the self-improvement loop without recreating it
// or hand-editing tokens.json. Returns the updated view.
func (r *Router) updateMCPToken(w http.ResponseWriter, req *http.Request, id string) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	var body updateMCPTokenRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if body.SelfImproveOptIn == nil {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "no updatable field provided")
		return
	}
	if err := r.deps.Auth.MCPTokens.SetSelfImproveOptIn(id, *body.SelfImproveOptIn); err != nil {
		WriteError(w, http.StatusNotFound, CodeNotFound, err.Error())
		return
	}
	if r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{
			Source: audit.SourceHTTP,
			Actor:  user.Username,
			UserID: user.ID,
			Action: audit.ActionTokenUpdate,
			Path:   id,
		})
	}
	// Return the updated record so the SPA can reflect the new state.
	tok, ok := r.findMCPToken(id)
	if !ok {
		WriteError(w, http.StatusNotFound, CodeNotFound, "token not found after update")
		return
	}
	WriteJSON(w, http.StatusOK, mcpTokenToView(tok))
}

// findMCPToken returns the stored token whose ID matches, by scanning the
// store's List() snapshot. Used to echo back the updated record after a PATCH.
func (r *Router) findMCPToken(id string) (*auth.Token, bool) {
	tokens := r.deps.Auth.MCPTokens.List()
	for i := range tokens {
		if tokens[i].ID == id {
			return &tokens[i], true
		}
	}
	return nil, false
}

func (r *Router) listMCPTokens(w http.ResponseWriter, _ *http.Request) {
	tokens := r.deps.Auth.MCPTokens.List()
	out := make([]mcpTokenView, 0, len(tokens))
	for i := range tokens {
		out = append(out, mcpTokenToView(&tokens[i]))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
}

func (r *Router) createMCPToken(w http.ResponseWriter, req *http.Request) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	var body createMCPTokenRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if body.Name == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "name required")
		return
	}
	if len(body.Scopes) == 0 {
		// Sensible default: read-only. Owners can grant write
		// explicitly per the existing /admin/tokens HTML form.
		body.Scopes = []string{auth.ScopeRead}
	}
	for _, s := range body.Scopes {
		if s != auth.ScopeRead && s != auth.ScopeWrite {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "unknown scope: "+s)
			return
		}
	}
	if !auth.ValidToolProfile(body.ToolProfile) {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "unknown tool_profile: "+body.ToolProfile)
		return
	}
	var ttl time.Duration
	if body.TTLMS > 0 {
		ttl = time.Duration(body.TTLMS) * time.Millisecond
	}
	projects := body.Projects
	if len(projects) == 0 && body.Project != "" {
		projects = []string{body.Project}
	}
	plain, tok, err := r.deps.Auth.MCPTokens.Create(body.Name, projects, body.Scopes, ttl, user.ID)
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if body.ToolProfile != "" {
		if err := r.deps.Auth.MCPTokens.SetToolProfile(tok.ID, body.ToolProfile); err != nil {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return
		}
		tok.ToolProfile = body.ToolProfile
	}
	if r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{
			Source: audit.SourceHTTP,
			Actor:  user.Username,
			UserID: user.ID,
			Action: audit.ActionTokenCreate,
			Path:   tok.ID,
		})
	}
	WriteJSON(w, http.StatusCreated, mcpTokenCreatedResponse{
		Token:     plain,
		Record:    mcpTokenToView(&tok),
		UsageHint: "Authorization: Bearer " + plain,
	})
}

func mcpTokenToView(t *auth.Token) mcpTokenView {
	v := mcpTokenView{
		ID:               t.ID,
		Name:             t.Name,
		Project:          strings.Join(t.ProjectList(), ", "),
		Projects:         t.ProjectList(),
		Scopes:           append([]string(nil), t.Scopes...),
		OwnerUserID:      t.OwnerUserID,
		CreatedAt:        t.CreatedAt.UTC().Format(rfc3339Z),
		SelfImproveOptIn: t.SelfImproveOptIn,
		ToolProfile:      t.ToolProfile,
		Kind:             t.Kind,
		ClientID:         t.ClientID,
	}
	if !t.ExpiresAt.IsZero() {
		v.ExpiresAt = t.ExpiresAt.UTC().Format(rfc3339Z)
		v.Expired = t.Expired()
	}
	return v
}

// ---- /api/v1/admin/spa-tokens ---------------------------------------------

// spaTokenView mirrors auth.SpaToken but flattens timestamps to RFC
// 3339 strings the SPA can render directly. Hash is dropped — the SPA
// admin UI shows ID (8-hex display id) and the user's UA string only.
type spaTokenView struct {
	ID         string `json:"id"`
	UserID     string `json:"user_id"`
	UserAgent  string `json:"user_agent,omitempty"`
	IssuedAt   string `json:"issued_at"`
	ExpiresAt  string `json:"expires_at"`
	HardExpiry string `json:"hard_expiry"`
	LastSeenAt string `json:"last_seen_at"`
}

func (r *Router) handleAdminSpaTokens(w http.ResponseWriter, req *http.Request) {
	if r.deps.Auth == nil || r.deps.Auth.SpaAuth == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "spa token store not configured")
		return
	}
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	userID := strings.TrimSpace(req.URL.Query().Get("user_id"))
	if userID == "" {
		// All-users listing: enumerate every webauth user and union
		// their active SPA sessions. ListByUser returns only
		// non-expired entries.
		all := []spaTokenView{}
		if r.deps.Auth.WebAuth != nil {
			for _, u := range r.deps.Auth.WebAuth.ListUsers() {
				for _, t := range r.deps.Auth.SpaAuth.ListByUser(u.ID) {
					all = append(all, spaTokenToView(t))
				}
			}
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": all, "total": len(all)})
		return
	}
	tokens := r.deps.Auth.SpaAuth.ListByUser(userID)
	out := make([]spaTokenView, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, spaTokenToView(t))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
}

func (r *Router) handleAdminSpaTokenItem(w http.ResponseWriter, req *http.Request) {
	if r.deps.Auth == nil || r.deps.Auth.SpaAuth == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "spa token store not configured")
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v1/admin/spa-tokens/"), "/")
	if id == "" || strings.Contains(id, "/") {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "expected /api/v1/admin/spa-tokens/{id}")
		return
	}
	if req.Method != http.MethodDelete {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if err := r.deps.Auth.SpaAuth.RevokeByID(id); err != nil {
		WriteError(w, http.StatusNotFound, CodeNotFound, err.Error())
		return
	}
	if user := UserFromContext(req.Context()); user != nil && r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{
			Source: audit.SourceHTTP,
			Actor:  user.Username,
			UserID: user.ID,
			Action: audit.ActionSpaTokenRevoke,
			Path:   id,
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

func spaTokenToView(t auth.SpaToken) spaTokenView {
	return spaTokenView{
		ID:         t.ID,
		UserID:     t.UserID,
		UserAgent:  t.UserAgent,
		IssuedAt:   t.IssuedAt.UTC().Format(rfc3339Z),
		ExpiresAt:  t.ExpiresAt.UTC().Format(rfc3339Z),
		HardExpiry: t.HardExpiry.UTC().Format(rfc3339Z),
		LastSeenAt: t.LastSeenAt.UTC().Format(rfc3339Z),
	}
}
