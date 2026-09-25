package v1

import (
	"net/http"
	"strings"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/webauth"
)

// projectTeamView is a team's grant on a project, for display.
type projectTeamView struct {
	TeamID string `json:"team_id"`
	Name   string `json:"name"`
	Level  string `json:"level"` // read | write | admin
}

type accessCandidateUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type accessCandidateTeam struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// accessCandidates lists what a project admin may still add: enabled
// non-owner accounts and teams without a grant on the project yet.
type accessCandidates struct {
	Users []accessCandidateUser `json:"users"`
	Teams []accessCandidateTeam `json:"teams"`
}

// projectAccessView is the payload of GET /projects/{name}/access: who holds
// a grant on the project, directly or through a team. Candidates are
// included only for callers who may administer the project, so the account
// directory is never listed to a plain reader.
type projectAccessView struct {
	Project    string              `json:"project"`
	Visibility string              `json:"visibility"`
	Users      []projectMemberView `json:"users"`
	Teams      []projectTeamView   `json:"teams"`
	CanAdmin   bool                `json:"can_admin"`
	Candidates *accessCandidates   `json:"candidates,omitempty"`
}

type setTeamGrantRequest struct {
	TeamID string `json:"team_id"`
	Level  string `json:"level"`
}

// requireProjectAdmin resolves the caller and checks they may administer the
// project's grants: the owner, or an admin grant on it. A project the caller
// cannot even read answers 404 so its existence stays hidden.
func (r *Router) requireProjectAdmin(w http.ResponseWriter, req *http.Request, project string) (*RequestUser, bool) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return nil, false
	}
	if r.deps.Projects == nil || r.deps.Auth == nil || r.deps.Auth.WebAuth == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "access store not configured")
		return nil, false
	}
	princ := user.principal()
	if !r.canAccessProject(princ, project) || !r.projectExists(project) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "project not found")
		return nil, false
	}
	if !r.canAdminProject(princ, project) {
		WriteError(w, http.StatusForbidden, CodeAuthForbidden, "admin access to this project is required to manage who can use it")
		return nil, false
	}
	return user, true
}

// handleProjectAccess serves GET /projects/{name}/access.
func (r *Router) handleProjectAccess(w http.ResponseWriter, req *http.Request, project string) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if r.deps.Projects == nil || r.deps.Auth == nil || r.deps.Auth.WebAuth == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "access store not configured")
		return
	}
	princ := user.principal()
	if !r.canAccessProject(princ, project) || !r.projectExists(project) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "project not found")
		return
	}
	view := projectAccessView{
		Project:    project,
		Visibility: r.projectVisibility(project),
		Users:      r.projectMemberViews(project),
		Teams:      []projectTeamView{},
		CanAdmin:   r.canAdminProject(princ, project),
	}
	for _, tg := range r.deps.Projects.TeamGrantsOn(project) {
		view.Teams = append(view.Teams, projectTeamView{TeamID: tg.TeamID, Name: tg.TeamName, Level: tg.Level})
	}
	if view.CanAdmin {
		granted := map[string]bool{}
		for _, u := range view.Users {
			granted[u.UserID] = true
		}
		cands := &accessCandidates{Users: []accessCandidateUser{}, Teams: []accessCandidateTeam{}}
		for _, u := range r.deps.Auth.WebAuth.ListUsers() {
			if u.Role == webauth.RoleOwner || !u.Enabled() || granted[u.ID] {
				continue
			}
			cands.Users = append(cands.Users, accessCandidateUser{ID: u.ID, Username: u.Username, Role: string(u.Role)})
		}
		teamGranted := map[string]bool{}
		for _, t := range view.Teams {
			teamGranted[t.TeamID] = true
		}
		for _, t := range r.deps.Projects.Teams() {
			if !teamGranted[t.ID] {
				cands.Teams = append(cands.Teams, accessCandidateTeam{ID: t.ID, Name: t.Name})
			}
		}
		view.Candidates = cands
	}
	WriteJSON(w, http.StatusOK, view)
}

// handleProjectTeams manages the team grants on a project:
//
//	PUT    /projects/{name}/teams            upsert {team_id, level}
//	DELETE /projects/{name}/teams/{team_id}  remove
//
// Owner or project admin, like the per-user grants.
func (r *Router) handleProjectTeams(w http.ResponseWriter, req *http.Request, project, teamID string) {
	user, ok := r.requireProjectAdmin(w, req, project)
	if !ok {
		return
	}
	switch req.Method {
	case http.MethodPut, http.MethodPost:
		if teamID != "" {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
			return
		}
		var body setTeamGrantRequest
		if err := DecodeJSON(req, &body); err != nil {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return
		}
		body.TeamID = strings.TrimSpace(body.TeamID)
		if body.TeamID == "" {
			WriteError(w, http.StatusBadRequest, CodeValidationRequired, "team_id required")
			return
		}
		if !projects.ValidLevel(body.Level) {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "level must be read, write or admin")
			return
		}
		team, found := r.deps.Projects.Team(body.TeamID)
		if !found {
			WriteError(w, http.StatusNotFound, CodeNotFound, "team not found")
			return
		}
		if err := r.deps.Projects.SetTeamGrant(team.ID, project, body.Level); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
			return
		}
		r.auditAccess(user, "project_team_set", project+"/"+team.ID, body.Level)
		r.publishSidebarEvent("update", project)
		WriteJSON(w, http.StatusCreated, projectTeamView{TeamID: team.ID, Name: team.Name, Level: body.Level})
	case http.MethodDelete:
		if teamID == "" {
			WriteError(w, http.StatusBadRequest, CodeValidationRequired, "team id required")
			return
		}
		if _, found := r.deps.Projects.Team(teamID); !found {
			WriteError(w, http.StatusNotFound, CodeNotFound, "team not found")
			return
		}
		if err := r.deps.Projects.RemoveTeamGrant(teamID, project); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
			return
		}
		r.auditAccess(user, "project_team_remove", project+"/"+teamID, "")
		r.publishSidebarEvent("update", project)
		w.WriteHeader(http.StatusNoContent)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

// auditAccess records a grant or team mutation. to carries the level when
// one was set.
func (r *Router) auditAccess(actor *RequestUser, action audit.Action, path, to string) {
	if r.deps.Audit == nil {
		return
	}
	_ = r.deps.Audit.Write(audit.Entry{Source: audit.SourceHTTP, Actor: actor.Username, UserID: actor.ID, Action: action, Path: path, To: to})
}
