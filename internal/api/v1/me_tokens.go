package v1

import (
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
)

// Self-service MCP tokens (IMP-101 phase 3):
//
//	GET    /me/tokens        the caller's own tokens (static and OAuth grants)
//	POST   /me/tokens        mint a static token owned by the caller
//	DELETE /me/tokens/{id}   revoke one of the caller's tokens
//
// A token owned by an account never outruns it: the MCP runtime narrows it
// on every request to what the account may currently read and write (see
// internal/mcp effectiveToken). Two modes: "inherit" records no project
// list and so follows the account's live access, later grants included;
// "custom" records an explicit subset, validated now and intersected live.

type meTokenRequest struct {
	Name        string   `json:"name"`
	Mode        string   `json:"mode,omitempty"` // inherit (default) | custom
	Projects    []string `json:"projects,omitempty"`
	Scopes      []string `json:"scopes,omitempty"` // read (default) | write
	TTLMS       int64    `json:"ttl_ms,omitempty"`
	ToolProfile string   `json:"tool_profile,omitempty"`
}

const (
	tokenModeInherit = "inherit"
	tokenModeCustom  = "custom"
)

func (r *Router) meTokensReady(w http.ResponseWriter) bool {
	if r.deps.Auth == nil || r.deps.Auth.MCPTokens == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "mcp token store not configured")
		return false
	}
	return true
}

func (r *Router) handleMeTokens(w http.ResponseWriter, req *http.Request) {
	if !r.meTokensReady(w) {
		return
	}
	user := UserFromContext(req.Context())
	if user == nil || user.isAnonymous() {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "sign in to manage tokens")
		return
	}
	switch req.Method {
	case http.MethodGet:
		tokens := r.deps.Auth.MCPTokens.List()
		out := make([]mcpTokenView, 0)
		for i := range tokens {
			if tokens[i].OwnerUserID == user.ID {
				out = append(out, mcpTokenToView(&tokens[i]))
			}
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
	case http.MethodPost:
		r.createMeToken(w, req, user)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) createMeToken(w http.ResponseWriter, req *http.Request, user *RequestUser) {
	var body meTokenRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "name required")
		return
	}
	mode := strings.TrimSpace(body.Mode)
	if mode == "" {
		mode = tokenModeInherit
	}
	if mode != tokenModeInherit && mode != tokenModeCustom {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "mode must be inherit or custom")
		return
	}
	princ := user.principal()
	scopes := body.Scopes
	if len(scopes) == 0 {
		scopes = []string{auth.ScopeRead}
	}
	for _, sc := range scopes {
		switch sc {
		case auth.ScopeRead:
		case auth.ScopeWrite:
			if !princ.CanWrite() {
				WriteError(w, http.StatusForbidden, CodeAuthForbidden, "a read-only account cannot mint a write token")
				return
			}
		default:
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "unknown scope: "+sc)
			return
		}
	}
	if !auth.ValidToolProfile(body.ToolProfile) {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "unknown tool_profile: "+body.ToolProfile)
		return
	}
	// The project list: none for inherit; for custom every entry must be a
	// project the account may read right now (the runtime keeps checking).
	var projects []string
	if mode == tokenModeCustom {
		for _, p := range body.Projects {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if !r.canAccessProject(princ, p) || !r.projectExists(p) {
				WriteError(w, http.StatusForbidden, CodeAuthForbidden, "project not visible to this account: "+p)
				return
			}
			projects = append(projects, p)
		}
		if len(projects) == 0 {
			WriteError(w, http.StatusBadRequest, CodeValidationRequired, "custom mode needs at least one project")
			return
		}
	}
	var ttl time.Duration
	if body.TTLMS > 0 {
		ttl = time.Duration(body.TTLMS) * time.Millisecond
	}
	plain, tok, err := r.deps.Auth.MCPTokens.Create(body.Name, projects, scopes, ttl, user.ID)
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
		_ = r.deps.Audit.Write(audit.Entry{Source: audit.SourceHTTP, Actor: user.Username, UserID: user.ID, Action: audit.ActionTokenCreate, Path: tok.ID, To: mode})
	}
	WriteJSON(w, http.StatusCreated, mcpTokenCreatedResponse{
		Token:     plain,
		Record:    mcpTokenToView(&tok),
		UsageHint: "Authorization: Bearer " + plain,
	})
}

func (r *Router) handleMeTokenItem(w http.ResponseWriter, req *http.Request) {
	if !r.meTokensReady(w) {
		return
	}
	user := UserFromContext(req.Context())
	if user == nil || user.isAnonymous() {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "sign in to manage tokens")
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v1/me/tokens/"), "/")
	if id == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "token id required")
		return
	}
	if req.Method != http.MethodDelete {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	// A token that is not the caller's does not exist for them.
	tok, found := r.findMCPToken(id)
	if !found || tok.OwnerUserID != user.ID {
		WriteError(w, http.StatusNotFound, CodeNotFound, "token not found")
		return
	}
	if err := r.deps.Auth.MCPTokens.Revoke(id); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	if r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{Source: audit.SourceHTTP, Actor: user.Username, UserID: user.ID, Action: audit.ActionTokenRevoke, Path: id})
	}
	w.WriteHeader(http.StatusNoContent)
}
