package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/webauth"
)

// tokenRow is the per-row view model for the tokens table.
type tokenRow struct {
	ID      string
	Name    string
	Project string
	Scopes  string
	Created string
	Expires string
	Expired bool
	Owner   string // username, empty for admin-owned (CLI) tokens
	Mine    bool   // true when the row belongs to the current user
}

// ttlPresets maps the select option value to a Duration. The empty key means
// "no expiry" and produces a zero-valued ExpiresAt on the stored token.
var ttlPresets = map[string]time.Duration{
	"":     0,
	"7d":   7 * 24 * time.Hour,
	"30d":  30 * 24 * time.Hour,
	"90d":  90 * 24 * time.Hour,
	"1y":   365 * 24 * time.Hour,
}

// handleTokens renders the /admin/tokens page and handles POST actions
// (create / revoke) for MCP bearer tokens.
func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	if s.tokens == nil {
		http.Error(w, "token store not wired", http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.renderTokens(w, r, tokensPageState{})
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch r.FormValue("action") {
		case "create":
			s.tokensHandleCreate(w, r)
		case "revoke":
			s.tokensHandleRevoke(w, r)
		default:
			s.renderTokens(w, r, tokensPageState{Error: "azione sconosciuta"})
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// tokensPageState carries the transient fields (feedback messages + sticky
// form values after a failed POST) that the template needs beyond the always-
// rendered tokens list and project list.
type tokensPageState struct {
	CreatedPlain string // shown exactly once after a successful create
	CreatedID    string
	OK           string
	Error        string

	// Sticky form values on error, so the user doesn't retype.
	FormName    string
	FormProject string
	FormTTL     string
	FormRead    bool
	FormWrite   bool
}

func (s *Server) tokensHandleCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	project := strings.TrimSpace(r.FormValue("project"))
	ttlKey := r.FormValue("ttl")
	readOn := formCheckbox(r, "scope_read")
	writeOn := formCheckbox(r, "scope_write")

	state := tokensPageState{
		FormName:    name,
		FormProject: project,
		FormTTL:     ttlKey,
		FormRead:    readOn,
		FormWrite:   writeOn,
	}

	if name == "" {
		state.Error = "nome richiesto"
		s.renderTokens(w, r, state)
		return
	}
	var scopes []string
	if readOn {
		scopes = append(scopes, auth.ScopeRead)
	}
	if writeOn {
		scopes = append(scopes, auth.ScopeWrite)
	}
	if len(scopes) == 0 {
		state.Error = "almeno uno scope richiesto (read o write)"
		s.renderTokens(w, r, state)
		return
	}
	ttl, ok := ttlPresets[ttlKey]
	if !ok {
		state.Error = "TTL non valido"
		s.renderTokens(w, r, state)
		return
	}

	ownerID, ownerUsername := s.tokenOwnerFromRequest(r)
	plaintext, tok, err := s.tokens.Create(name, project, scopes, ttl, ownerID)
	if err != nil {
		state.Error = "create: " + err.Error()
		s.renderTokens(w, r, state)
		return
	}

	_ = s.audit.Write(audit.Entry{
		Source: audit.SourceHTTP,
		Actor:  ownerUsernameOrFallback(ownerUsername),
		UserID: ownerID,
		Action: audit.ActionTokenCreate,
		Path:   tok.ID,
	})

	// Success: clear sticky form values, show plaintext once.
	s.renderTokens(w, r, tokensPageState{
		CreatedPlain: plaintext,
		CreatedID:    tok.ID,
		OK:           "Token creato. Copia il plaintext ora — non sarà più mostrato.",
		FormRead:     true,
		FormWrite:    true,
	})
}

func (s *Server) tokensHandleRevoke(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		s.renderTokens(w, r, tokensPageState{Error: "id richiesto"})
		return
	}

	// Members can only revoke tokens they own. Owners can revoke anyone's.
	current := s.currentUser(r)
	if current != nil && current.Role != webauth.RoleOwner {
		target := s.findTokenByID(id)
		if target == nil {
			s.renderTokens(w, r, tokensPageState{Error: "revoke: token non trovato"})
			return
		}
		if target.OwnerUserID != current.ID {
			s.renderTokens(w, r, tokensPageState{Error: "revoke: non puoi revocare token di altri utenti"})
			return
		}
	}

	if err := s.tokens.Revoke(id); err != nil {
		s.renderTokens(w, r, tokensPageState{Error: "revoke: " + err.Error()})
		return
	}
	ownerID, ownerUsername := s.tokenOwnerFromRequest(r)
	_ = s.audit.Write(audit.Entry{
		Source: audit.SourceHTTP,
		Actor:  ownerUsernameOrFallback(ownerUsername),
		UserID: ownerID,
		Action: audit.ActionTokenRevoke,
		Path:   id,
	})
	s.renderTokens(w, r, tokensPageState{OK: "Token revocato: " + id})
}

// findTokenByID returns a copy of the token matching id, or nil.
func (s *Server) findTokenByID(id string) *auth.Token {
	for _, t := range s.tokens.List() {
		if t.ID == id {
			tCp := t
			return &tCp
		}
	}
	return nil
}

// renderTokens assembles the view model and renders the tokens.html template.
// On GET state is zero; on POST callers populate it with feedback + sticky form.
// Members see only their own tokens; owners see every token with an "owner"
// column.
func (s *Server) renderTokens(w http.ResponseWriter, r *http.Request, state tokensPageState) {
	current := s.currentUser(r)
	isOwner := current == nil || current.Role == webauth.RoleOwner
	ownerNames := s.ownerUsernameMap()

	rows := make([]tokenRow, 0)
	for _, t := range s.tokens.List() {
		if !isOwner && t.OwnerUserID != current.ID {
			continue
		}
		row := toTokenRow(t)
		row.Owner = ownerNames[t.OwnerUserID]
		if row.Owner == "" && t.OwnerUserID == "" {
			row.Owner = "(CLI)"
		}
		if current != nil && t.OwnerUserID == current.ID {
			row.Mine = true
		}
		rows = append(rows, row)
	}
	projects, _ := s.vault.Projects()
	projectNames := make([]string, 0, len(projects))
	for _, p := range projects {
		projectNames = append(projectNames, p.Name)
	}

	// Default form state on GET: both scopes selected, no preselected project.
	formRead := state.FormRead
	formWrite := state.FormWrite
	if r.Method == http.MethodGet {
		formRead = true
		formWrite = true
	}

	data := map[string]any{
		"Title":        "Token MCP",
		"Tokens":       rows,
		"ShowOwnerCol": isOwner,
		"Projects":     projectNames,
		"CreatedPlain": state.CreatedPlain,
		"CreatedID":    state.CreatedID,
		"OK":           state.OK,
		"Error":        state.Error,
		"FormName":     state.FormName,
		"FormProject":  state.FormProject,
		"FormTTL":      state.FormTTL,
		"FormRead":     formRead,
		"FormWrite":    formWrite,
		"TTLOptions": []struct {
			Value string
			Label string
		}{
			{"", "nessuna scadenza"},
			{"7d", "7 giorni"},
			{"30d", "30 giorni"},
			{"90d", "90 giorni"},
			{"1y", "1 anno"},
		},
	}
	s.renderPage(w, r, "tokens.html", data)
}

func toTokenRow(t auth.Token) tokenRow {
	project := t.Project
	if project == "" {
		project = "(admin)"
	}
	expires := "-"
	if !t.ExpiresAt.IsZero() {
		expires = t.ExpiresAt.Format("2006-01-02")
	}
	return tokenRow{
		ID:      t.ID,
		Name:    t.Name,
		Project: project,
		Scopes:  strings.Join(t.Scopes, ","),
		Created: t.CreatedAt.Format("2006-01-02"),
		Expires: expires,
		Expired: t.Expired(),
	}
}

// ownerUsernameMap returns a map of user_id → username for rendering the
// owner column of /admin/tokens. Returns an empty map when web auth is off.
func (s *Server) ownerUsernameMap() map[string]string {
	out := make(map[string]string)
	if s.webauth == nil {
		return out
	}
	for _, u := range s.webauth.ListUsers() {
		out[u.ID] = u.Username
	}
	return out
}

// tokenOwnerFromRequest returns (user_id, username) of the webauth session
// that owns the request, or ("", "") when auth is disabled.
func (s *Server) tokenOwnerFromRequest(r *http.Request) (string, string) {
	u := s.currentUser(r)
	if u == nil {
		return "", ""
	}
	return u.ID, u.Username
}

// ownerUsernameOrFallback returns the given username or "web" when empty.
// Used for the audit Actor field when web auth is disabled.
func ownerUsernameOrFallback(username string) string {
	if username == "" {
		return "web"
	}
	return username
}
