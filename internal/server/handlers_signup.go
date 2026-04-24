package server

import (
	"net/http"
	"strings"

	"github.com/gosidian/gosidian/internal/webauth"
)

// handleSignup renders the signup form when an invite token is supplied via
// ?token=… and creates a new member account on POST. The route is listed in
// isOpenPath so unauthenticated visitors can complete registration; invite
// validation is what actually gates the flow.
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if s.webauth == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// If no invites have ever been minted (first bootstrap), point users to
	// the CLI setup — signup does not provision the owner.
	if !s.webauth.Enabled() {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.FormValue("token")
	}
	token = strings.TrimSpace(token)

	switch r.Method {
	case http.MethodGet:
		inv := s.webauth.FindInvite(token)
		if inv == nil {
			s.renderSignup(w, r, "", "Invite non valido o scaduto.")
			return
		}
		s.renderSignup(w, r, token, "")
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		inv := s.webauth.FindInvite(token)
		if inv == nil {
			s.renderSignup(w, r, "", "Invite non valido o scaduto.")
			return
		}
		username := strings.TrimSpace(r.FormValue("username"))
		password := r.FormValue("password")
		confirm := r.FormValue("password_confirm")
		if username == "" {
			s.renderSignup(w, r, token, "Username richiesto.")
			return
		}
		if len(password) < 8 {
			s.renderSignup(w, r, token, "La password deve avere almeno 8 caratteri.")
			return
		}
		if password != confirm {
			s.renderSignup(w, r, token, "Le due password non coincidono.")
			return
		}

		user, err := s.webauth.AddUser(username, password, webauth.RoleMember)
		if err != nil {
			s.renderSignup(w, r, token, "Registrazione fallita: "+err.Error())
			return
		}
		if err := s.webauth.ClaimInvite(token, user.ID); err != nil {
			// User was created but claim failed — unusual. Log but continue
			// with login to avoid a confusing dead-end for the visitor.
			s.renderSignup(w, r, token, "Registrazione completata, ma invite non marcato come consumato: "+err.Error())
			return
		}
		sid, err := s.webauth.CreateSession(user.ID, loginSessionTTL)
		if err != nil {
			http.Error(w, "session create: "+err.Error(), http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, webauth.SessionCookie(sid, loginSessionTTL, webauth.IsSecureRequest(r)))
		http.Redirect(w, r, "/", http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) renderSignup(w http.ResponseWriter, r *http.Request, token, errMsg string) {
	data := map[string]any{
		"Title":     "Registrazione",
		"Token":     token,
		"Error":     errMsg,
		"Valid":     token != "",
		"NoSidebar": true,
	}
	s.renderPage(w, r, "signup.html", data)
}
