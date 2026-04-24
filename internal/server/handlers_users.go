package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/webauth"
)

const inviteTTL = 24 * time.Hour

// userRow is the template view for one user entry.
type userRow struct {
	ID        string
	Username  string
	Role      string
	Created   string
	Disabled  bool
	DisabledDate string
	IsOwner   bool
}

type inviteRow struct {
	Token     string
	CreatedBy string // username
	Created   string
	Expires   string
	Status    string // pending | consumed | expired
	Consumer  string // username of consumer when known
}

// handleUsers renders /admin/users and processes owner-only actions (create
// invite, revoke invite, disable user). Members hitting this page receive
// 403.
func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	if s.webauth == nil || !s.webauth.Enabled() {
		http.Error(w, "auth disabled: nothing to administer", http.StatusNotFound)
		return
	}
	current := s.currentUser(r)
	if current == nil || current.Role != webauth.RoleOwner {
		http.Error(w, "forbidden: owner role required", http.StatusForbidden)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.renderUsers(w, r, usersPageState{})
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch r.FormValue("action") {
		case "create-invite":
			s.usersHandleCreateInvite(w, r, current)
		case "revoke-invite":
			s.usersHandleRevokeInvite(w, r)
		case "disable-user":
			s.usersHandleDisableUser(w, r)
		default:
			s.renderUsers(w, r, usersPageState{Error: "azione sconosciuta"})
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type usersPageState struct {
	OK               string
	Error            string
	CreatedInvite    string // plaintext invite token, shown once
	CreatedInviteURL string // convenience: absolute /signup?token=...
}

func (s *Server) usersHandleCreateInvite(w http.ResponseWriter, r *http.Request, current *webauth.User) {
	inv, err := s.webauth.CreateInvite(current.ID, inviteTTL)
	if err != nil {
		s.renderUsers(w, r, usersPageState{Error: "create invite: " + err.Error()})
		return
	}
	s.renderUsers(w, r, usersPageState{
		CreatedInvite:    inv.Token,
		CreatedInviteURL: "/signup?token=" + inv.Token,
		OK:               "Invite creato. Copia il link ora — non sarà più mostrato.",
	})
}

func (s *Server) usersHandleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.FormValue("token"))
	if token == "" {
		s.renderUsers(w, r, usersPageState{Error: "token richiesto"})
		return
	}
	if err := s.webauth.RevokeInvite(token); err != nil {
		s.renderUsers(w, r, usersPageState{Error: "revoke invite: " + err.Error()})
		return
	}
	s.renderUsers(w, r, usersPageState{OK: "Invite revocato."})
}

func (s *Server) usersHandleDisableUser(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		s.renderUsers(w, r, usersPageState{Error: "id richiesto"})
		return
	}
	if err := s.webauth.DisableUser(id); err != nil {
		s.renderUsers(w, r, usersPageState{Error: "disable: " + err.Error()})
		return
	}
	s.renderUsers(w, r, usersPageState{OK: "Utente disabilitato. Token MCP associati revocati."})
}

func (s *Server) renderUsers(w http.ResponseWriter, r *http.Request, state usersPageState) {
	users := s.webauth.ListUsers()
	userRows := make([]userRow, 0, len(users))
	byID := make(map[string]string, len(users))
	for _, u := range users {
		byID[u.ID] = u.Username
		row := userRow{
			ID:       u.ID,
			Username: u.Username,
			Role:     string(u.Role),
			Created:  u.CreatedAt.Format("2006-01-02"),
			IsOwner:  u.Role == webauth.RoleOwner,
		}
		if u.DisabledAt != nil {
			row.Disabled = true
			row.DisabledDate = u.DisabledAt.Format("2006-01-02")
		}
		userRows = append(userRows, row)
	}

	invites := s.webauth.ListInvites()
	now := time.Now()
	inviteRows := make([]inviteRow, 0, len(invites))
	for _, inv := range invites {
		status := "pending"
		consumer := ""
		if inv.ConsumedAt != nil {
			status = "consumed"
			consumer = byID[inv.ConsumedBy]
		} else if now.After(inv.ExpiresAt) {
			status = "expired"
		}
		inviteRows = append(inviteRows, inviteRow{
			Token:     inv.Token,
			CreatedBy: byID[inv.CreatedBy],
			Created:   inv.CreatedAt.Format("2006-01-02 15:04"),
			Expires:   inv.ExpiresAt.Format("2006-01-02 15:04"),
			Status:    status,
			Consumer:  consumer,
		})
	}

	data := map[string]any{
		"Title":            "Utenti",
		"Users":            userRows,
		"Invites":          inviteRows,
		"OK":               state.OK,
		"Error":            state.Error,
		"CreatedInvite":    state.CreatedInvite,
		"CreatedInviteURL": state.CreatedInviteURL,
	}
	s.renderPage(w, r, "users.html", data)
}
