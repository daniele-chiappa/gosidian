package v1

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/projectops"
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
// name, or "" when nothing was created; an existing folder with that name,
// or MCP tokens scoped to it, is an error the caller reports without failing
// the account creation. projectops.Create starts the project from a fresh
// access entry, so grants left under the name by a deleted project do not
// reach the new account's private project (IMP-124).
func ProvisionPersonalProject(v *vault.Vault, ps *projects.Store, tokens *auth.Store, al *audit.Log, u webauth.User, force bool) (string, error) {
	if v == nil || ps == nil {
		return "", nil
	}
	if u.Role == webauth.RoleOwner || u.Role == webauth.RoleGuest {
		return "", nil
	}
	if !force && !ps.PersonalProjectsEnabled() {
		return "", nil
	}
	name, err := projectops.Create(v, ps, tokens, u.Username, projects.VisibilityPrivate, u.ID)
	if err != nil {
		return "", fmt.Errorf("create project %q: %w", u.Username, err)
	}
	if al != nil {
		_ = al.Write(audit.Entry{Source: audit.SourceHTTP, Actor: u.Username, UserID: u.ID, Action: ActionPersonalProjectCreate, Path: name})
	}
	return name, nil
}

// PersonalProjectHook returns the account-creation hook main installs: a new
// account gets its personal project. It does nothing when the username
// belonged to a disabled account (replaced): createUser, the only caller
// that reuses a username, moves that account's personal project out of the
// way first and then provisions this one, as the admin who asked
// (reclaimPersonalProject). Failures are only logged: the account exists
// either way.
func PersonalProjectHook(v *vault.Vault, ps *projects.Store, tokens *auth.Store, al *audit.Log) func(webauth.User, *webauth.User) {
	return func(u webauth.User, replaced *webauth.User) {
		if replaced != nil {
			return
		}
		name, err := ProvisionPersonalProject(v, ps, tokens, al, u, false)
		switch {
		case err != nil:
			log.Printf("webauth: user %s created, personal project not provisioned: %v", u.Username, err)
		case name != "":
			log.Printf("webauth: user %s created, personal project %q provisioned", u.Username, name)
		}
	}
}

// reclaimPersonalProject gives u, created with the username of the disabled
// account archived, its personal project. The disabled account's personal
// project holds that name: it is renamed after the archived account first,
// notes included, unless personalProjectKept says it must stay (BUG-076,
// ADR-032). Returns the new personal project, the name the old one moved to,
// and why u has no personal project when it should have one.
func (r *Router) reclaimPersonalProject(req *http.Request, actor *RequestUser, archived, u webauth.User) (personal, moved, warning string) {
	ps := r.deps.Projects
	if ps == nil || u.Role != webauth.RoleMember || !ps.PersonalProjectsEnabled() {
		return "", "", ""
	}
	name := u.Username
	if r.projectExists(name) {
		why := r.personalProjectKept(archived, name)
		var err error
		if why == "" {
			err = r.archiveProject(name, archived.Username)
			if err != nil && !errors.Is(err, projectops.ErrIncomplete) {
				why = err.Error()
			}
		}
		if why != "" {
			log.Printf("webauth: username %s reused, project %q left in place: %s", name, name, why)
			return "", "", fmt.Sprintf("no personal project: the project %q stays as it is (%s)", name, why)
		}
		moved = archived.Username
		if actor != nil {
			r.auditNote(req, audit.ActionRenameProject, actor, name, moved, 0)
		}
		r.publishSidebarEvent("update", moved)
		if err != nil {
			// The old project moved, but tokens may still name the reused
			// name: a fresh project under it would fall to them.
			log.Printf("webauth: username %s reused, project moved to %q incompletely: %v", name, moved, err)
			return "", moved, "no personal project: " + err.Error()
		}
	}
	personal, err := ProvisionPersonalProject(r.deps.Vault, ps, r.mcpTokens(), r.deps.Audit, u, false)
	if err != nil {
		return "", moved, "no personal project: " + err.Error()
	}
	if personal != "" {
		r.publishSidebarEvent("create", personal)
	}
	return personal, moved, ""
}

// personalProjectKept says why the project name must not move with the
// disabled account archived, "" when it may. It must be the personal project
// provisioned for that account, with no project of that name renamed or
// deleted since, and no other account may hold a member or team grant on it
// (disabling the account removed its own). MCP tokens scoped to it follow
// the rename (projectops.Rename), so they never reach the new account's
// project.
func (r *Router) personalProjectKept(archived webauth.User, name string) string {
	if !stillProvisionedFor(r.deps.Audit, archived.ID, name) {
		return "it is not the personal project of " + archived.Username
	}
	ps := r.deps.Projects
	if n := ps.MembersCount(name) + ps.TeamsCount(name); n > 0 {
		return fmt.Sprintf("%d members or teams hold a grant on it", n)
	}
	return ""
}

// archiveProject renames project from to to, with its grants and the MCP
// tokens scoped to it.
func (r *Router) archiveProject(from, to string) error {
	_, err := projectops.Rename(r.deps.Vault, r.deps.Index, r.deps.Projects, r.mcpTokens(), from, to)
	return err
}

// stillProvisionedFor reports whether the audit log records project as the
// personal project provisioned for the account userID, with no project of
// that name renamed away or deleted since.
func stillProvisionedFor(al *audit.Log, userID, project string) bool {
	created, err := al.TailFiltered(audit.TailOpts{UserID: userID, Action: ActionPersonalProjectCreate, Limit: 500})
	if err != nil {
		return false
	}
	var since time.Time
	for _, e := range created {
		if e.Path == project {
			since = e.TS
		}
	}
	if since.IsZero() {
		return false
	}
	for _, action := range []audit.Action{audit.ActionRenameProject, audit.ActionDeleteProject} {
		later, err := al.TailFiltered(audit.TailOpts{Action: action, PathPrefix: project + "/", Since: since, Limit: 500})
		if err != nil {
			return false
		}
		for _, e := range later {
			if e.Path == project {
				return false
			}
		}
	}
	return true
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
	name, err := ProvisionPersonalProject(r.deps.Vault, r.deps.Projects, r.mcpTokens(), r.deps.Audit, u, true)
	if err != nil {
		WriteError(w, http.StatusConflict, CodeConflict, err.Error())
		return
	}
	r.publishSidebarEvent("create", name)
	WriteJSON(w, http.StatusCreated, map[string]any{"personal_project": name})
}
