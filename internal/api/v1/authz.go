package v1

import (
	"net/http"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/webauth"
)

// principal projects a RequestUser onto the authz.Principal used by the
// shared authorization predicate.
func (ru *RequestUser) principal() authz.Principal {
	return authz.Principal{UserID: ru.ID, Role: ru.Role, Restricted: ru.Restricted}
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

// projectOf returns the top-level project folder of a vault-relative note
// path ("gosidian/plans/x.md" -> "gosidian"). A path without a slash has no
// project folder and is returned as-is; since such a name won't match a
// readable project, the read is denied — consistent with private-by-default.
func projectOf(path string) string { return auth.ProjectOf(path) }

// accessConfig builds the per-request inputs the shared authz predicate needs.
// It is the projects store's own AccessConfig (nil-safe), shared with the SSE
// stream and the MCP runtime so every surface resolves access identically.
func (r *Router) accessConfig() authz.AccessConfig {
	return r.deps.Projects.AccessConfig()
}

// levelOf returns the principal's effective level on the project.
func (r *Router) levelOf(p authz.Principal, project string) authz.Level {
	return p.Level(project, r.accessConfig())
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

// seesAllProjects reports whether the principal sees every project, so a
// handler may skip per-note filtering. Only the owner does: every other
// account is gated by visibility and grants, so the per-note canSee filter
// must run. Distinct from authz.CanSeeAllProjects (a role-tier check, still
// the right gate for the member+ settings page).
func (r *Router) seesAllProjects(p authz.Principal) bool {
	return p.Role == webauth.RoleOwner
}

// searchScope returns the projects a search may return for the principal,
// optionally narrowed to one project: nil = no restriction (the owner,
// unfiltered), otherwise the readable subset — possibly empty, which
// matches nothing. The index applies it inside the query, so the limit
// counts only readable notes (BUG-058).
func (r *Router) searchScope(p authz.Principal, project string) ([]string, error) {
	if r.seesAllProjects(p) {
		if project == "" {
			return nil, nil
		}
		return []string{project}, nil
	}
	candidates := []string{project}
	if project == "" {
		var err error
		if candidates, err = r.deps.Index.Projects(); err != nil {
			return nil, err
		}
	}
	out := []string{}
	for _, name := range candidates {
		if r.canAccessProject(p, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// canWriteProject reports whether the principal may mutate the project.
func (r *Router) canWriteProject(p authz.Principal, project string) bool {
	return p.CanWriteProject(project, r.accessConfig())
}

// canAdminProject reports whether the principal may change the project's
// settings (visibility, flags, rename, delete).
func (r *Router) canAdminProject(p authz.Principal, project string) bool {
	return p.CanAdminProject(project, r.accessConfig())
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
// write to the project (no write or admin grant, or only a read-level
// access from the project's visibility). Call after the target is known.
func (r *Router) denyWriteProject(w http.ResponseWriter, p authz.Principal, project string) bool {
	if r.canWriteProject(p, project) {
		return false
	}
	WriteError(w, http.StatusForbidden, CodeAuthForbidden, "you do not have write access to this project")
	return true
}

// denyAdminProject writes a 403 and returns true when the principal may not
// change the project's settings (owner, or an admin grant on the project).
func (r *Router) denyAdminProject(w http.ResponseWriter, p authz.Principal, project string) bool {
	if r.canAdminProject(p, project) {
		return false
	}
	WriteError(w, http.StatusForbidden, CodeAuthForbidden, "you do not have admin access to this project")
	return true
}
