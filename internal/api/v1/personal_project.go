package v1

import (
	"fmt"
	"net/http"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/gosidian/gosidian/internal/webauth"
)

// ActionPersonalProjectCreate is the audit action recorded when an account's
// personal project is provisioned.
const ActionPersonalProjectCreate audit.Action = "personal_project_create"

// ProvisionPersonalProject creates the account's personal project (IMP-101
// phase 3): a private project named after the account where it holds the
// admin grant, so a new account has a place to work before anyone grants it
// anything. Owners and guests get none. Unless force is set, the store
// setting (Settings → Project access) must allow it. Returns the project
// name, or "" when nothing was created; an existing folder with that name is
// an error the caller reports without failing the account creation.
func ProvisionPersonalProject(v *vault.Vault, ps *projects.Store, al *audit.Log, u webauth.User, force bool) (string, error) {
	if v == nil || ps == nil {
		return "", nil
	}
	if u.Role == webauth.RoleOwner || u.Role == webauth.RoleGuest {
		return "", nil
	}
	if !force && !ps.PersonalProjectsEnabled() {
		return "", nil
	}
	name, err := v.CreateProject(u.Username)
	if err != nil {
		return "", fmt.Errorf("create project %q: %w", u.Username, err)
	}
	f := ps.Get(name)
	f.Visibility = projects.VisibilityPrivate
	if err := ps.Set(name, f); err != nil {
		return name, fmt.Errorf("set visibility on %q: %w", name, err)
	}
	if err := ps.SetMember(name, u.ID, projects.LevelAdmin); err != nil {
		return name, fmt.Errorf("grant admin on %q: %w", name, err)
	}
	if al != nil {
		_ = al.Write(audit.Entry{Source: audit.SourceHTTP, Actor: u.Username, UserID: u.ID, Action: ActionPersonalProjectCreate, Path: name})
	}
	return name, nil
}

// personalProjectOf returns the account's personal project when it exists:
// a project named after the account on which it holds the admin grant.
func (r *Router) personalProjectOf(u webauth.User) string {
	if r.deps.Projects == nil || u.Role == webauth.RoleOwner {
		return ""
	}
	if !r.projectExists(u.Username) {
		return ""
	}
	if lvl, ok := r.deps.Projects.MemberLevel(u.Username, u.ID); !ok || lvl != projects.LevelAdmin {
		return ""
	}
	return u.Username
}

// createPersonalProject serves POST /admin/users/{id}/personal-project: the
// owner provisions the personal project by hand, e.g. for an account created
// before the feature or while the setting was off.
func (r *Router) createPersonalProject(w http.ResponseWriter, req *http.Request, u webauth.User) {
	if u.Role == webauth.RoleOwner || u.Role == webauth.RoleGuest {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "only member accounts have a personal project")
		return
	}
	if !u.Enabled() {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "user is disabled")
		return
	}
	if existing := r.personalProjectOf(u); existing != "" {
		WriteError(w, http.StatusConflict, CodeConflict, "personal project already exists: "+existing)
		return
	}
	name, err := ProvisionPersonalProject(r.deps.Vault, r.deps.Projects, r.deps.Audit, u, true)
	if err != nil {
		WriteError(w, http.StatusConflict, CodeConflict, err.Error())
		return
	}
	r.publishSidebarEvent("create", name)
	WriteJSON(w, http.StatusCreated, map[string]any{"personal_project": name})
}
