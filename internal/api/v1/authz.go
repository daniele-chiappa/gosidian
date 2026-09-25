package v1

import (
	"net/http"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/webauth"
)

// principal projects a RequestUser onto the authz.Principal used by the
// shared authorization predicate.
func (ru *RequestUser) principal() authz.Principal {
	return authz.Principal{UserID: ru.ID, Role: ru.Role}
}

// principalFromContext returns the Principal for the current request. Every
// authed route runs requireAuth first, so a user is normally present; if it
// is somehow missing we fall back to a guest-role principal, which fails
// closed (sees only public projects, cannot write).
func principalFromContext(req *http.Request) authz.Principal {
	if u := UserFromContext(req.Context()); u != nil {
		return u.principal()
	}
	return authz.Principal{Role: webauth.RoleGuest}
}

// isPublic reports a project's Public flag, nil-safe. With no projects store
// configured nothing is public, so guests see nothing (fail closed).
func (r *Router) isPublic(name string) bool {
	if r.deps.Projects == nil {
		return false
	}
	return r.deps.Projects.IsPublic(name)
}

// projectOf returns the top-level project folder of a vault-relative note
// path ("gosidian/plans/x.md" -> "gosidian"). A path without a slash has no
// project folder and is returned as-is; since such a name won't match a
// Public project, guests are denied — consistent with private-by-default.
func projectOf(path string) string { return auth.ProjectOf(path) }

// memberScopeEnforced reports whether per-project membership gates access
// (member_scope = members). False = legacy: owner/member see every project.
func (r *Router) memberScopeEnforced() bool {
	return r.deps.Projects != nil && r.deps.Projects.MemberScope() == projects.MemberScopeMembers
}

// accessConfig builds the per-request inputs the shared authz predicate needs.
// It is the projects store's own AccessConfig (nil-safe), shared with the MCP
// runtime so both surfaces resolve membership identically.
func (r *Router) accessConfig() authz.AccessConfig {
	return r.deps.Projects.AccessConfig()
}

// canAccessProject reports whether the principal may read the named project.
func (r *Router) canAccessProject(p authz.Principal, project string) bool {
	return p.CanAccessProject(project, r.accessConfig())
}

// canSee reports whether the principal may read the note/path. Centralizes the
// projectOf + CanAccessProject pairing used by every read handler.
func (r *Router) canSee(p authz.Principal, path string) bool {
	return p.CanAccessProject(projectOf(path), r.accessConfig())
}

// seesAllProjects reports whether the principal sees every project, so a handler
// may skip per-note filtering. Owner always; member only in legacy mode — under
// member_scope=members a member is gated by membership like everyone else, so
// the per-note canSee filter must run. Distinct from authz.CanSeeAllProjects
// (purely role-based, still the right check for the member+ settings gate).
func (r *Router) seesAllProjects(p authz.Principal) bool {
	if p.Role == webauth.RoleOwner {
		return true
	}
	return p.Role == webauth.RoleMember && !r.memberScopeEnforced()
}

// canWriteProject reports whether the principal may mutate the project.
func (r *Router) canWriteProject(p authz.Principal, project string) bool {
	return p.CanWriteProject(project, r.accessConfig())
}

// denyGuestWrite writes a 403 and returns true when the user may not mutate
// (guest or any non-writing role). Mutating handlers call it right after the
// user nil-check: `if denyGuestWrite(w, user) { return }`. Role-only — the
// per-project write gate (denyWriteProject) runs once the target is known.
func denyGuestWrite(w http.ResponseWriter, user *RequestUser) bool {
	if user != nil && user.principal().CanWrite() {
		return false
	}
	WriteError(w, http.StatusForbidden, CodeAuthForbidden, "read-only role cannot write")
	return true
}

// denyWriteProject writes a 403 and returns true when the principal may not
// write to the project. A no-op for owner/member in legacy mode (they already
// passed denyGuestWrite); under member_scope=members a member needs a write
// membership. Call after the target project is known.
func (r *Router) denyWriteProject(w http.ResponseWriter, p authz.Principal, project string) bool {
	if r.canWriteProject(p, project) {
		return false
	}
	WriteError(w, http.StatusForbidden, CodeAuthForbidden, "you do not have write access to this project")
	return true
}
