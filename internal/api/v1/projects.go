package v1

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/projectops"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/server/events"
)

// projectView is the JSON shape returned by /projects endpoints.
// Flags are flattened into the top level so the SPA reads them
// without a second request.
type projectView struct {
	Name          string `json:"name"`
	NoteCount     int    `json:"note_count"`
	HiddenFromMCP bool   `json:"hidden_from_mcp"`
	SkipGitSync   bool   `json:"skip_git_sync"`
	// Visibility is who may read the project: public | internal | private.
	Visibility string `json:"visibility"`
	// Public is the pre-v2.30 alias (visibility == public), kept for older
	// clients; new code reads Visibility.
	Public bool `json:"public"`
	// Access is the caller's own effective level on the project (read | write
	// | admin) so the SPA gates its controls without a second request.
	Access string `json:"access"`
	// MembersCount is how many accounts hold a direct grant; TeamsCount how
	// many teams hold one.
	MembersCount int `json:"members_count"`
	TeamsCount   int `json:"teams_count"`
	// UseGlobals opts the project into the shared "global" projects merge at
	// bootstrap. UseAnchors opts it into local agent-anchor materialisation.
	// Both only take effect when the respective server master switch is on
	// (settingsView.GlobalsEnabled / AnchorsEnabled).
	UseGlobals bool `json:"use_globals"`
	UseAnchors bool `json:"use_anchors"`
	// UseTagVocabulary opts the project into the per-project lint tag
	// vocabulary declared in memory/conventions.md (IMP-075).
	UseTagVocabulary bool `json:"use_tag_vocabulary"`
	// LeanReadBootstrap trims memory_bootstrap for read-only tokens.
	LeanReadBootstrap bool `json:"lean_read_bootstrap"`
	// AllowLocalMirror lets readers keep a local read-only copy (IMP-102).
	AllowLocalMirror bool `json:"allow_local_mirror"`
	// ModTime drives "most recent" sorting in the SPA's project
	// pickers (graph filter, switcher). RFC 3339 UTC. Empty when
	// the vault entry hasn't been stat-able.
	ModTime string `json:"mod_time,omitempty"`
}

type createProjectRequest struct {
	Name string `json:"name"`
}

// updateProjectRequest covers both flag toggles and rename. Either
// (or both) fields may be present — the handler applies whatever is
// set. NewName uses pointer-to-string so the JSON `null` means "no
// change" while empty string `""` means "validation error".
type updateProjectRequest struct {
	NewName       *string `json:"new_name,omitempty"`
	HiddenFromMCP *bool   `json:"hidden_from_mcp,omitempty"`
	SkipGitSync   *bool   `json:"skip_git_sync,omitempty"`
	// Visibility sets who may read the project; public is owner-only.
	Visibility *string `json:"visibility,omitempty"`
	// Public is the pre-v2.30 alias: true → public, false → internal.
	Public            *bool `json:"public,omitempty"`
	UseGlobals        *bool `json:"use_globals,omitempty"`
	UseAnchors        *bool `json:"use_anchors,omitempty"`
	UseTagVocabulary  *bool `json:"use_tag_vocabulary,omitempty"`
	LeanReadBootstrap *bool `json:"lean_read_bootstrap,omitempty"`
	AllowLocalMirror  *bool `json:"allow_local_mirror,omitempty"`
}

// handleProjects dispatches GET (list) / POST (create) on /projects.
// Per-project ops live on /projects/{slug} via handleProjectByName.
func (r *Router) handleProjects(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		r.listProjects(w, req)
	case http.MethodPost:
		r.createProject(w, req)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

// handleProjectByName routes per-project operations. The slug is
// everything after `/api/v1/projects/`. Subroutes like `/dashboard`
// are intentionally NOT supported in this slice — they arrive in
// Phase 1.3 with the admin views. Today the slug is single-segment.
func (r *Router) handleProjectByName(w http.ResponseWriter, req *http.Request) {
	rest := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v1/projects/"), "/")
	if rest == "" {
		r.handleProjects(w, req)
		return
	}
	// Sub-resource routing: /{name}/members[/{userID}], /{name}/teams[/{teamID}],
	// /{name}/access, /{name}/export.zip.
	if name, sub, ok := strings.Cut(rest, "/"); ok {
		seg, tail, _ := strings.Cut(sub, "/")
		switch seg {
		case "members":
			r.handleProjectMembers(w, req, name, tail)
		case "teams":
			r.handleProjectTeams(w, req, name, tail)
		case "access":
			r.handleProjectAccess(w, req, name)
		case "export.zip":
			if tail != "" {
				WriteError(w, http.StatusNotFound, CodeNotFound, "sub-resource not implemented")
				return
			}
			r.handleProjectExport(w, req, name)
		default:
			WriteError(w, http.StatusNotFound, CodeNotFound, "sub-resource not implemented")
		}
		return
	}
	switch req.Method {
	case http.MethodGet:
		r.getProject(w, req, rest)
	case http.MethodPut:
		r.updateProject(w, req, rest)
	case http.MethodDelete:
		r.deleteProject(w, req, rest)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

// listProjects walks vault.Projects() and merges flags from the
// per-project store. Cheap operation — typical vaults have <50
// top-level dirs.
func (r *Router) listProjects(w http.ResponseWriter, req *http.Request) {
	projs, err := r.deps.Vault.Projects()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	princ := principalFromContext(req)
	out := make([]projectView, 0, len(projs))
	for _, p := range projs {
		lvl := r.levelOf(princ, p.Name)
		if lvl < authz.LevelRead {
			continue // gated by visibility / grants
		}
		pv := r.projectViewFor(p.Name, lvl, p.NoteCount)
		pv.ModTime = formatModTime(p.ModTime)
		out = append(out, pv)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
}

// projectVisibility resolves a project's visibility, nil-safe (no store →
// the legacy everything-open default the nil AccessConfig also assumes).
func (r *Router) projectVisibility(name string) string {
	if r.deps.Projects == nil {
		return projects.VisibilityInternal
	}
	return r.deps.Projects.Visibility(name)
}

// projectViewFor assembles the wire shape of a project for a caller whose
// effective level is lvl. One place builds it so list, get and update never
// disagree on which fields exist.
func (r *Router) projectViewFor(name string, lvl authz.Level, noteCount int) projectView {
	flags := r.projectFlag(name)
	vis := r.projectVisibility(name)
	members, teams := 0, 0
	if r.deps.Projects != nil {
		members = r.deps.Projects.MembersCount(name)
		teams = r.deps.Projects.TeamsCount(name)
	}
	return projectView{
		Name:              name,
		NoteCount:         noteCount,
		HiddenFromMCP:     flags.HiddenFromMCP,
		SkipGitSync:       flags.SkipGitSync,
		Visibility:        vis,
		Public:            vis == projects.VisibilityPublic,
		Access:            lvl.String(),
		MembersCount:      members,
		TeamsCount:        teams,
		UseGlobals:        flags.UseGlobals,
		UseAnchors:        flags.UseAnchors,
		UseTagVocabulary:  flags.UseTagVocabulary,
		LeanReadBootstrap: flags.LeanReadBootstrap,
		AllowLocalMirror:  flags.AllowLocalMirror,
	}
}

func formatModTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(rfc3339Z)
}

func (r *Router) getProject(w http.ResponseWriter, req *http.Request, name string) {
	lvl := r.levelOf(principalFromContext(req), name)
	if lvl < authz.LevelRead {
		// No visibility/grant — 404 hides existence.
		WriteError(w, http.StatusNotFound, CodeNotFound, "project not found")
		return
	}
	projs, err := r.deps.Vault.Projects()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	for _, p := range projs {
		if p.Name == name {
			WriteJSON(w, http.StatusOK, r.projectViewFor(name, lvl, p.NoteCount))
			return
		}
	}
	WriteError(w, http.StatusNotFound, CodeNotFound, "project not found")
}

func (r *Router) createProject(w http.ResponseWriter, req *http.Request) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if denyGuestWrite(w, user) {
		return
	}
	if full, ok := r.deps.Auth.WebAuth.UserByID(user.ID); ok && !full.CanCreateProjects() {
		WriteError(w, http.StatusForbidden, CodeAuthForbidden, "this account may not create projects")
		return
	}
	var body createProjectRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if body.Name == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "name required")
		return
	}
	// The visibility is pinned at creation (the store default now), so a
	// later change of the default does not reclassify the project; the
	// creator administers what they just made, the owner already does.
	// projectops.Create saves both before the folder appears, from a fresh
	// entry, and refuses a name MCP tokens are scoped to (IMP-124).
	var admin string
	if !user.principal().CanAdmin() {
		admin = user.ID
	}
	clean, err := projectops.Create(r.deps.Vault, r.deps.Projects, r.mcpTokens(), body.Name, "", admin)
	switch {
	case errors.Is(err, projectops.ErrExists):
		WriteError(w, http.StatusConflict, CodeConflict, err.Error())
		return
	case errors.Is(err, projectops.ErrInvalid):
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	case err != nil:
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	r.auditNote(req, audit.ActionCreateProject, user, clean, "", 0)
	r.publishSidebarEvent("create", clean)
	WriteJSON(w, http.StatusCreated, r.projectViewFor(clean, r.levelOf(user.principal(), clean), 0))
}

func (r *Router) updateProject(w http.ResponseWriter, req *http.Request, name string) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if denyGuestWrite(w, user) {
		return
	}
	// Settings, visibility, rename and delete are project administration:
	// the owner or an admin grant. A 404 for a project the caller cannot
	// even read comes first so existence is not revealed by a 403.
	princ := user.principal()
	if !r.canAccessProject(princ, name) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "project not found")
		return
	}
	if r.denyAdminProject(w, princ, name) {
		return
	}
	var body updateProjectRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}

	// Verify the project exists before touching anything.
	if !r.projectExists(name) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "project not found")
		return
	}

	// Resolve the visibility change: the explicit field wins over the legacy
	// public alias. Making a project public opens it to every account, guests
	// included, so that step is the owner's alone.
	var newVisibility string
	if body.Visibility != nil {
		newVisibility = strings.TrimSpace(*body.Visibility)
		if !projects.ValidVisibility(newVisibility) {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "visibility must be public, internal or private")
			return
		}
	} else if body.Public != nil {
		// "Not public" takes a public project down to internal and leaves
		// any other as it is: it made a private project readable by every
		// member (BUG-112, S2-6).
		switch {
		case *body.Public:
			newVisibility = projects.VisibilityPublic
		case r.deps.Projects != nil && r.deps.Projects.Visibility(name) == projects.VisibilityPublic:
			newVisibility = projects.VisibilityInternal
		}
	}
	if newVisibility == projects.VisibilityPublic && !princ.CanAdmin() {
		WriteError(w, http.StatusForbidden, CodeAuthForbidden, "only the owner can make a project public")
		return
	}

	// Apply flags first (cheap, no fs movement) so a failing rename
	// still leaves the flags durable.
	flagsChanged := false
	if body.HiddenFromMCP != nil || body.SkipGitSync != nil || newVisibility != "" || body.UseGlobals != nil || body.UseAnchors != nil || body.UseTagVocabulary != nil || body.LeanReadBootstrap != nil || body.AllowLocalMirror != nil {
		current := r.projectFlag(name)
		if body.HiddenFromMCP != nil {
			current.HiddenFromMCP = *body.HiddenFromMCP
		}
		if body.SkipGitSync != nil {
			current.SkipGitSync = *body.SkipGitSync
		}
		if newVisibility != "" {
			current.Visibility = newVisibility
			current.Public = false
		}
		if body.UseGlobals != nil {
			current.UseGlobals = *body.UseGlobals
		}
		if body.UseAnchors != nil {
			current.UseAnchors = *body.UseAnchors
		}
		if body.UseTagVocabulary != nil {
			current.UseTagVocabulary = *body.UseTagVocabulary
		}
		if body.LeanReadBootstrap != nil {
			current.LeanReadBootstrap = *body.LeanReadBootstrap
		}
		if body.AllowLocalMirror != nil {
			current.AllowLocalMirror = *body.AllowLocalMirror
		}
		if r.deps.Projects != nil {
			if err := r.deps.Projects.Set(name, current); err != nil {
				WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
				return
			}
			flagsChanged = true
		}
	}

	finalName := name
	if body.NewName != nil {
		newName := strings.TrimSpace(*body.NewName)
		if newName == "" {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "new_name cannot be empty")
			return
		}
		if newName != name {
			_, err := projectops.Rename(r.deps.Vault, r.deps.Index, r.deps.Projects, r.mcpTokens(), name, newName)
			switch {
			case errors.Is(err, projectops.ErrInvalid):
				WriteError(w, http.StatusBadRequest, CodeValidationFormat, "rename: "+err.Error())
				return
			case err != nil && !errors.Is(err, projectops.ErrIncomplete):
				WriteError(w, http.StatusInternalServerError, CodeServerInternal, "rename: "+err.Error())
				return
			}
			r.auditNote(req, audit.ActionRenameProject, user, name, newName, 0)
			if err != nil {
				// The rename stands, audited and announced, but something
				// did not follow: say so rather than answer 200.
				if flagsChanged {
					r.auditNote(req, audit.ActionProjectFlagsUpdate, user, newName, "", 0)
				}
				r.publishSidebarEvent("update", newName)
				WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
				return
			}
			finalName = newName
		}
	}
	if flagsChanged {
		r.auditNote(req, audit.ActionProjectFlagsUpdate, user, finalName, "", 0)
	}
	r.publishSidebarEvent("update", finalName)

	projs, _ := r.deps.Vault.Projects()
	count := 0
	for _, p := range projs {
		if p.Name == finalName {
			count = p.NoteCount
			break
		}
	}
	WriteJSON(w, http.StatusOK, r.projectViewFor(finalName, r.levelOf(princ, finalName), count))
}

func (r *Router) deleteProject(w http.ResponseWriter, req *http.Request, name string) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if denyGuestWrite(w, user) {
		return
	}
	if !r.canAccessProject(user.principal(), name) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "project not found")
		return
	}
	if r.denyAdminProject(w, user.principal(), name) {
		return
	}
	if !r.projectExists(name) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "project not found")
		return
	}

	// Into the trash with its access saved for the restore, then out of the
	// index, the access store and the MCP token scopes (IMP-124).
	res, err := projectops.Delete(r.deps.Vault, r.deps.Index, r.deps.Projects, r.mcpTokens(), r.deps.Trash, name)
	switch {
	case errors.Is(err, projectops.ErrInvalid):
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "delete: "+err.Error())
		return
	case err != nil && !errors.Is(err, projectops.ErrIncomplete):
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "delete: "+err.Error())
		return
	}
	r.auditNote(req, audit.ActionDeleteProject, user, name, res.TrashID, int64(len(res.Removed)))
	r.publishSidebarEvent("delete", name)
	if err != nil {
		// The project is gone, but something did not follow: say so.
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// projectFlag is a nil-safe lookup so callers don't have to repeat
// the Projects-store nil check.
func (r *Router) projectFlag(name string) projects.Flags {
	if r.deps.Projects == nil {
		return projects.Flags{}
	}
	return r.deps.Projects.Get(name)
}

// projectExists reuses vault.Projects() — cheap and always
// authoritative against the on-disk state. Avoids a second Stat call.
func (r *Router) projectExists(name string) bool {
	return projectops.Exists(r.deps.Vault, name)
}

// mcpTokens returns the MCP token store, nil when auth is not configured.
func (r *Router) mcpTokens() *auth.Store {
	if r.deps.Auth == nil {
		return nil
	}
	return r.deps.Auth.MCPTokens
}

// publishSidebarEvent emits an SSE notification on the `sidebar`
// topic so other tabs invalidate their project-list cache.
func (r *Router) publishSidebarEvent(action, project string) {
	if r.deps.Events == nil {
		return
	}
	r.deps.Events.Publish(events.TopicSidebar, map[string]any{
		"action":  action,
		"project": project,
	})
}
