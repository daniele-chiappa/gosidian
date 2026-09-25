package v1

import (
	"net/http"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/projects"
)

// projectMemberView is the JSON shape for a per-project membership. Username is
// resolved for display; the ACL itself stores only the user id + level.
type projectMemberView struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Level    string `json:"level"` // read | write | admin
}

type setMemberRequest struct {
	UserID string `json:"user_id"`
	Level  string `json:"level"`
}

// handleProjectMembers manages the per-user grants on a project:
//
//	GET    /projects/{name}/members            list grants
//	PUT    /projects/{name}/members            upsert {user_id, level}
//	DELETE /projects/{name}/members/{user_id}  remove
//
// Owner or project admin (an admin grant on the project): sharing is an
// administrative decision, and phase 2 delegates it to the accounts that
// administer the project. A write grant manages content, not the grant list.
func (r *Router) handleProjectMembers(w http.ResponseWriter, req *http.Request, project, userID string) {
	user, ok := r.requireProjectAdmin(w, req, project)
	if !ok {
		return
	}
	switch req.Method {
	case http.MethodGet:
		if userID != "" {
			WriteError(w, http.StatusNotFound, CodeNotFound, "not found")
			return
		}
		r.listProjectMembers(w, project)
	case http.MethodPut, http.MethodPost:
		if userID != "" {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
			return
		}
		r.setProjectMember(w, req, user, project)
	case http.MethodDelete:
		if userID == "" {
			WriteError(w, http.StatusBadRequest, CodeValidationRequired, "user id required")
			return
		}
		r.removeProjectMember(w, req, user, project, userID)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

// projectMemberViews resolves the direct grants on a project for display.
func (r *Router) projectMemberViews(project string) []projectMemberView {
	members := r.deps.Projects.MembersOf(project)
	out := make([]projectMemberView, 0, len(members))
	for _, m := range members {
		v := projectMemberView{UserID: m.UserID, Username: m.UserID, Level: m.Level}
		if u, ok := r.deps.Auth.WebAuth.UserByID(m.UserID); ok {
			v.Username = u.Username
		}
		out = append(out, v)
	}
	return out
}

func (r *Router) listProjectMembers(w http.ResponseWriter, project string) {
	out := r.projectMemberViews(project)
	WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
}

func (r *Router) setProjectMember(w http.ResponseWriter, req *http.Request, actor *RequestUser, project string) {
	var body setMemberRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if body.UserID == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "user_id required")
		return
	}
	if !projects.ValidLevel(body.Level) {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "level must be read, write or admin")
		return
	}
	target, ok := r.deps.Auth.WebAuth.UserByID(body.UserID)
	if !ok {
		WriteError(w, http.StatusNotFound, CodeNotFound, "user not found")
		return
	}
	if target.Role.CanAdmin() {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "the owner already has full access")
		return
	}
	if err := r.deps.Projects.SetMember(project, body.UserID, body.Level); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	if r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{Source: audit.SourceHTTP, Actor: actor.Username, UserID: actor.ID, Action: "project_member_set", Path: project + "/" + body.UserID})
	}
	r.publishSidebarEvent("update", project)
	WriteJSON(w, http.StatusCreated, projectMemberView{UserID: target.ID, Username: target.Username, Level: body.Level})
}

func (r *Router) removeProjectMember(w http.ResponseWriter, req *http.Request, actor *RequestUser, project, userID string) {
	if err := r.deps.Projects.RemoveMember(project, userID); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	if r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{Source: audit.SourceHTTP, Actor: actor.Username, UserID: actor.ID, Action: "project_member_remove", Path: project + "/" + userID})
	}
	r.publishSidebarEvent("update", project)
	w.WriteHeader(http.StatusNoContent)
}
