package v1

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/webauth"
)

// Teams (IMP-101 phase 2): owner-only administration of the groups that
// carry one grant per project for all their users. Routes:
//
//	GET    /admin/teams                          list
//	POST   /admin/teams                          create {name, description}
//	GET    /admin/teams/{id}                     one team
//	PATCH  /admin/teams/{id}                     rename / describe
//	DELETE /admin/teams/{id}                     delete (drops its grants)
//	PUT    /admin/teams/{id}/users               add {user_id}
//	DELETE /admin/teams/{id}/users/{user_id}     remove
//	PUT    /admin/teams/{id}/grants              set {project, level}
//	DELETE /admin/teams/{id}/grants/{project}    remove

type teamUserView struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type teamGrantView struct {
	Project string `json:"project"`
	Level   string `json:"level"`
}

type teamView struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Users       []teamUserView  `json:"users"`
	Grants      []teamGrantView `json:"grants"`
	CreatedAt   string          `json:"created_at"`
}

type createTeamRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type updateTeamRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type teamUserRequest struct {
	UserID string `json:"user_id"`
}

type teamGrantRequest struct {
	Project string `json:"project"`
	Level   string `json:"level"`
}

func (r *Router) toTeamView(t projects.Team) teamView {
	view := teamView{
		ID:          t.ID,
		Name:        t.Name,
		Description: t.Description,
		Users:       []teamUserView{},
		Grants:      []teamGrantView{},
		CreatedAt:   t.CreatedAt.UTC().Format(rfc3339Z),
	}
	for _, id := range t.Users {
		uv := teamUserView{ID: id, Username: id}
		if u, ok := r.deps.Auth.WebAuth.UserByID(id); ok {
			uv.Username = u.Username
			uv.Role = string(u.Role)
		}
		view.Users = append(view.Users, uv)
	}
	names := make([]string, 0, len(t.Grants))
	for p := range t.Grants {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		view.Grants = append(view.Grants, teamGrantView{Project: p, Level: t.Grants[p]})
	}
	return view
}

func (r *Router) teamsReady(w http.ResponseWriter) bool {
	if r.deps.Projects == nil || r.deps.Auth == nil || r.deps.Auth.WebAuth == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "access store not configured")
		return false
	}
	return true
}

func (r *Router) handleAdminTeams(w http.ResponseWriter, req *http.Request) {
	if !r.teamsReady(w) {
		return
	}
	switch req.Method {
	case http.MethodGet:
		teams := r.deps.Projects.Teams()
		out := make([]teamView, 0, len(teams))
		for _, t := range teams {
			out = append(out, r.toTeamView(t))
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
	case http.MethodPost:
		user := UserFromContext(req.Context())
		var body createTeamRequest
		if err := DecodeJSON(req, &body); err != nil {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return
		}
		t, err := r.deps.Projects.CreateTeam(body.Name, body.Description)
		if err != nil {
			code := http.StatusBadRequest
			if strings.Contains(err.Error(), "already exists") {
				code = http.StatusConflict
			}
			WriteError(w, code, CodeValidationFormat, err.Error())
			return
		}
		r.auditAccess(user, "team_create", t.ID, t.Name)
		WriteJSON(w, http.StatusCreated, r.toTeamView(t))
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) handleAdminTeamItem(w http.ResponseWriter, req *http.Request) {
	if !r.teamsReady(w) {
		return
	}
	user := UserFromContext(req.Context())
	id, sub, _ := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v1/admin/teams/"), "/"), "/")
	seg, tail, _ := strings.Cut(sub, "/")
	if id == "" || (seg != "" && seg != "users" && seg != "grants") {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "expected /api/v1/admin/teams/{id}[/users[/{user_id}]|/grants[/{project}]]")
		return
	}
	team, found := r.deps.Projects.Team(id)
	if !found {
		WriteError(w, http.StatusNotFound, CodeNotFound, "team not found")
		return
	}
	switch seg {
	case "users":
		r.handleAdminTeamUsers(w, req, user, team, tail)
	case "grants":
		r.handleAdminTeamGrants(w, req, user, team, tail)
	default:
		r.handleAdminTeam(w, req, user, team)
	}
}

func (r *Router) handleAdminTeam(w http.ResponseWriter, req *http.Request, user *RequestUser, team projects.Team) {
	switch req.Method {
	case http.MethodGet:
		WriteJSON(w, http.StatusOK, r.toTeamView(team))
	case http.MethodPatch:
		var body updateTeamRequest
		if err := DecodeJSON(req, &body); err != nil {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return
		}
		t, err := r.deps.Projects.UpdateTeam(team.ID, body.Name, body.Description)
		if err != nil {
			code := http.StatusBadRequest
			if strings.Contains(err.Error(), "already exists") {
				code = http.StatusConflict
			}
			WriteError(w, code, CodeValidationFormat, err.Error())
			return
		}
		r.auditAccess(user, "team_update", t.ID, t.Name)
		WriteJSON(w, http.StatusOK, r.toTeamView(t))
	case http.MethodDelete:
		if err := r.deps.Projects.DeleteTeam(team.ID); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
			return
		}
		r.auditAccess(user, "team_delete", team.ID, team.Name)
		for p := range team.Grants {
			r.publishSidebarEvent("update", p)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) handleAdminTeamUsers(w http.ResponseWriter, req *http.Request, user *RequestUser, team projects.Team, userID string) {
	switch req.Method {
	case http.MethodPut, http.MethodPost:
		if userID != "" {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
			return
		}
		var body teamUserRequest
		if err := DecodeJSON(req, &body); err != nil {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return
		}
		target, ok := r.deps.Auth.WebAuth.UserByID(strings.TrimSpace(body.UserID))
		if !ok {
			WriteError(w, http.StatusNotFound, CodeNotFound, "user not found")
			return
		}
		if target.Role == webauth.RoleOwner {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "the owner already has full access")
			return
		}
		if !target.Enabled() {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "user is disabled")
			return
		}
		if err := r.deps.Projects.AddTeamUser(team.ID, target.ID); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
			return
		}
		r.auditAccess(user, "team_user_add", team.ID+"/"+target.ID, "")
		for p := range team.Grants {
			r.publishSidebarEvent("update", p)
		}
		t, _ := r.deps.Projects.Team(team.ID)
		WriteJSON(w, http.StatusCreated, r.toTeamView(t))
	case http.MethodDelete:
		if userID == "" {
			WriteError(w, http.StatusBadRequest, CodeValidationRequired, "user id required")
			return
		}
		if err := r.deps.Projects.RemoveTeamUser(team.ID, userID); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
			return
		}
		r.auditAccess(user, "team_user_remove", team.ID+"/"+userID, "")
		for p := range team.Grants {
			r.publishSidebarEvent("update", p)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) handleAdminTeamGrants(w http.ResponseWriter, req *http.Request, user *RequestUser, team projects.Team, project string) {
	switch req.Method {
	case http.MethodPut, http.MethodPost:
		if project != "" {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
			return
		}
		var body teamGrantRequest
		if err := DecodeJSON(req, &body); err != nil {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return
		}
		body.Project = strings.TrimSpace(body.Project)
		if !r.projectExists(body.Project) {
			WriteError(w, http.StatusNotFound, CodeNotFound, "project not found")
			return
		}
		if !projects.ValidLevel(body.Level) {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "level must be read, write or admin")
			return
		}
		if err := r.deps.Projects.SetTeamGrant(team.ID, body.Project, body.Level); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
			return
		}
		r.auditAccess(user, "team_grant_set", team.ID+"/"+body.Project, body.Level)
		r.publishSidebarEvent("update", body.Project)
		t, _ := r.deps.Projects.Team(team.ID)
		WriteJSON(w, http.StatusCreated, r.toTeamView(t))
	case http.MethodDelete:
		if project == "" {
			WriteError(w, http.StatusBadRequest, CodeValidationRequired, "project required")
			return
		}
		if err := r.deps.Projects.RemoveTeamGrant(team.ID, project); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
			return
		}
		r.auditAccess(user, "team_grant_remove", team.ID+"/"+project, "")
		r.publishSidebarEvent("update", project)
		w.WriteHeader(http.StatusNoContent)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}
