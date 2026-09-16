package v1

import (
	"net/http"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/webauth"
)

// projectMemberView is the JSON shape for a per-project membership. Username is
// resolved for display; the ACL itself stores only the user id + level.
type projectMemberView struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Level    string `json:"level"` // read | write
}

type setMemberRequest struct {
	UserID string `json:"user_id"`
	Level  string `json:"level"`
}

// handleProjectMembers manages the per-project membership ACL (owner-only):
//
//	GET    /projects/{name}/members            list members
//	PUT    /projects/{name}/members            upsert {user_id, level}
//	DELETE /projects/{name}/members/{user_id}  remove
//
// Membership only changes who may access a project under member_scope=members;
// in legacy mode the ACL is recorded but inert. Owner-only because sharing is an
// administrative decision (a write member manages content, not the member list).
func (r *Router) handleProjectMembers(w http.ResponseWriter, req *http.Request, project, userID string) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if user.Role != webauth.RoleOwner {
		WriteError(w, http.StatusForbidden, CodeAuthOwnerOnly, "owner role required to manage project members")
		return
	}
	if r.deps.Projects == nil || r.deps.Auth == nil || r.deps.Auth.WebAuth == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "membership store not configured")
		return
	}
	if !r.projectExists(project) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "project not found")
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

func (r *Router) listProjectMembers(w http.ResponseWriter, project string) {
	members := r.deps.Projects.MembersOf(project)
	out := make([]projectMemberView, 0, len(members))
	for _, m := range members {
		username := m.UserID
		if u, ok := r.deps.Auth.WebAuth.UserByID(m.UserID); ok {
			username = u.Username
		}
		out = append(out, projectMemberView{UserID: m.UserID, Username: username, Level: m.Level})
	}
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
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "level must be read or write")
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
