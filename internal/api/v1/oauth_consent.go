package v1

import (
	"errors"
	"github.com/gosidian/gosidian/internal/projects"
	"net/http"
	"strings"

	"github.com/gosidian/gosidian/internal/oauth"
	"github.com/gosidian/gosidian/internal/webauth"
)

// oauthConsentView is what the consent screen renders: the pending request
// as the OAuth server sees it, plus the projects the signed-in user may
// grant and whether write is on the table for their role.
type oauthConsentView struct {
	*oauth.ConsentView
	Projects []oauthProjectView `json:"projects"`
	CanWrite bool               `json:"can_write"`
	IsOwner  bool               `json:"is_owner"`
}

type oauthProjectView struct {
	Name       string `json:"name"`
	Public     bool   `json:"public"` // legacy alias of visibility == public
	Visibility string `json:"visibility"`
	NoteCount  int    `json:"note_count"`
}

// oauthDecisionRequest is the approve body. Empty projects means "every
// project I can see" (an owner then gets an unscoped grant, which also
// covers projects created later); empty scopes means read plus write when
// the role allows it.
type oauthDecisionRequest struct {
	Projects []string `json:"projects"`
	Scopes   []string `json:"scopes"`
}

// handleOAuthRequest routes /api/v1/oauth/requests/{id}[/approve|/deny].
// The anonymous open-mode guest cannot consent: a grant needs a real owner.
func (r *Router) handleOAuthRequest(w http.ResponseWriter, req *http.Request) {
	if r.deps.OAuth == nil {
		WriteError(w, http.StatusNotFound, CodeNotFound, "oauth is not enabled on this server")
		return
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v1/oauth/requests/"), "/")
	id, action, _ := strings.Cut(rest, "/")
	if id == "" || strings.Contains(action, "/") {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "expected /api/v1/oauth/requests/{id}[/approve|/deny]")
		return
	}
	user := UserFromContext(req.Context())
	if user == nil || user.isAnonymous() {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "sign in to authorize a client")
		return
	}
	switch {
	case req.Method == http.MethodGet && action == "":
		r.oauthConsentView(w, req, id)
	case req.Method == http.MethodPost && action == "approve":
		r.oauthApprove(w, req, id, user)
	case req.Method == http.MethodPost && action == "deny":
		r.oauthDeny(w, id)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

// visibleProjects lists the projects the principal may read, with the
// flags the consent screen shows.
func (r *Router) visibleProjects(req *http.Request) ([]oauthProjectView, error) {
	projs, err := r.deps.Vault.Projects()
	if err != nil {
		return nil, err
	}
	princ := principalFromContext(req)
	out := make([]oauthProjectView, 0, len(projs))
	for _, p := range projs {
		if !r.canAccessProject(princ, p.Name) {
			continue
		}
		vis := r.projectVisibility(p.Name)
		out = append(out, oauthProjectView{Name: p.Name, Public: vis == projects.VisibilityPublic, Visibility: vis, NoteCount: p.NoteCount})
	}
	return out, nil
}

func (r *Router) oauthConsentView(w http.ResponseWriter, req *http.Request, id string) {
	view, err := r.deps.OAuth.Pending(id)
	if err != nil {
		WriteError(w, http.StatusNotFound, CodeNotFound, "this authorization request is unknown or has expired; start again from the client")
		return
	}
	projects, err := r.visibleProjects(req)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	princ := principalFromContext(req)
	WriteJSON(w, http.StatusOK, oauthConsentView{
		ConsentView: view,
		Projects:    projects,
		CanWrite:    princ.CanWrite(),
		IsOwner:     princ.Role == webauth.RoleOwner,
	})
}

func (r *Router) oauthApprove(w http.ResponseWriter, req *http.Request, id string, user *RequestUser) {
	var body oauthDecisionRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	visible, err := r.visibleProjects(req)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	visibleSet := map[string]bool{}
	for _, p := range visible {
		visibleSet[p.Name] = true
	}
	princ := principalFromContext(req)
	var projects []string
	switch {
	case len(body.Projects) > 0:
		for _, p := range body.Projects {
			p = strings.TrimSpace(p)
			if !visibleSet[p] {
				WriteError(w, http.StatusForbidden, CodeAuthForbidden, "project not visible to this account: "+p)
				return
			}
			projects = append(projects, p)
		}
	case princ.Role == webauth.RoleOwner:
		projects = nil // unscoped: every project, including future ones
	default:
		for _, p := range visible {
			projects = append(projects, p.Name)
		}
		if len(projects) == 0 {
			WriteError(w, http.StatusForbidden, CodeAuthForbidden, "this account has no project to grant")
			return
		}
	}
	scopes := body.Scopes
	if len(scopes) == 0 {
		scopes = []string{oauth.ScopeRead}
		if princ.CanWrite() {
			scopes = append(scopes, oauth.ScopeWrite)
		}
	}
	for _, sc := range scopes {
		switch sc {
		case oauth.ScopeRead:
		case oauth.ScopeWrite:
			if !princ.CanWrite() {
				WriteError(w, http.StatusForbidden, CodeAuthForbidden, "this role can only grant read access")
				return
			}
		default:
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "unknown scope "+sc)
			return
		}
	}
	redirect, err := r.deps.OAuth.Approve(id, user.ID, user.Username, projects, scopes)
	if err != nil {
		if errors.Is(err, oauth.ErrNoSuchRequest) {
			WriteError(w, http.StatusNotFound, CodeNotFound, "this authorization request is unknown or has expired; start again from the client")
			return
		}
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{"redirect_to": redirect})
}

func (r *Router) oauthDeny(w http.ResponseWriter, id string) {
	redirect, err := r.deps.OAuth.Deny(id)
	if err != nil {
		WriteError(w, http.StatusNotFound, CodeNotFound, "this authorization request is unknown or has expired")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{"redirect_to": redirect})
}
