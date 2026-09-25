package v1

import (
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/authz"
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
	// MembersCount is how many accounts hold an explicit grant.
	MembersCount int `json:"members_count"`
	// UseGlobals opts the project into the shared "global" projects merge at
	// bootstrap. UseAnchors opts it into local agent-anchor materialisation.
	// Both only take effect when the respective server master switch is on
	// (settingsView.GlobalsEnabled / AnchorsEnabled).
	UseGlobals bool `json:"use_globals"`
	UseAnchors bool `json:"use_anchors"`
	// UseTagVocabulary opts the project into the per-project lint tag
	// vocabulary declared in memory/conventions.md (IMP-075).
	UseTagVocabulary bool `json:"use_tag_vocabulary"`
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
	Public           *bool `json:"public,omitempty"`
	UseGlobals       *bool `json:"use_globals,omitempty"`
	UseAnchors       *bool `json:"use_anchors,omitempty"`
	UseTagVocabulary *bool `json:"use_tag_vocabulary,omitempty"`
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
	// Sub-resource routing: /{name}/members[/{userID}].
	if name, sub, ok := strings.Cut(rest, "/"); ok {
		seg, tail, _ := strings.Cut(sub, "/")
		if seg == "members" {
			r.handleProjectMembers(w, req, name, tail)
			return
		}
		WriteError(w, http.StatusNotFound, CodeNotFound, "sub-resource not implemented")
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
	members := 0
	if r.deps.Projects != nil {
		members = r.deps.Projects.MembersCount(name)
	}
	return projectView{
		Name:             name,
		NoteCount:        noteCount,
		HiddenFromMCP:    flags.HiddenFromMCP,
		SkipGitSync:      flags.SkipGitSync,
		Visibility:       vis,
		Public:           vis == projects.VisibilityPublic,
		Access:           lvl.String(),
		MembersCount:     members,
		UseGlobals:       flags.UseGlobals,
		UseAnchors:       flags.UseAnchors,
		UseTagVocabulary: flags.UseTagVocabulary,
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
	var body createProjectRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if body.Name == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "name required")
		return
	}
	clean, err := r.deps.Vault.CreateProject(body.Name)
	if err != nil {
		// The vault returns a clear error string on duplicate / invalid name.
		if strings.Contains(err.Error(), "already exists") {
			WriteError(w, http.StatusConflict, CodeConflict, err.Error())
			return
		}
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if r.deps.Projects != nil {
		// Pin the visibility at creation so a later change of the store
		// default does not retroactively reclassify this project.
		if f := r.deps.Projects.Get(clean); f.Visibility == "" {
			f.Visibility = r.deps.Projects.DefaultVisibility()
			if err := r.deps.Projects.Set(clean, f); err != nil {
				WriteError(w, http.StatusInternalServerError, CodeServerInternal, "project created, but its visibility could not be saved: "+err.Error())
				return
			}
		}
		// The creator administers what they just made; the owner already
		// does. Say so if the grant cannot be saved, rather than answering
		// 201 to someone who is now locked out of a private project.
		if !user.principal().CanAdmin() {
			if err := r.deps.Projects.SetMember(clean, user.ID, projects.LevelAdmin); err != nil {
				WriteError(w, http.StatusInternalServerError, CodeServerInternal, "project created, but the creator's grant could not be saved (an owner can grant access): "+err.Error())
				return
			}
		}
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
		newVisibility = projects.VisibilityInternal
		if *body.Public {
			newVisibility = projects.VisibilityPublic
		}
	}
	if newVisibility == projects.VisibilityPublic && !princ.CanAdmin() {
		WriteError(w, http.StatusForbidden, CodeAuthForbidden, "only the owner can make a project public")
		return
	}

	// Apply flags first (cheap, no fs movement) so a failing rename
	// still leaves the flags durable.
	flagsChanged := false
	if body.HiddenFromMCP != nil || body.SkipGitSync != nil || newVisibility != "" || body.UseGlobals != nil || body.UseAnchors != nil || body.UseTagVocabulary != nil {
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
			if err := r.deps.Vault.RenameProject(r.deps.Index, name, newName); err != nil {
				WriteError(w, http.StatusBadRequest, CodeValidationFormat, "rename: "+err.Error())
				return
			}
			if r.deps.Projects != nil {
				if err := r.deps.Projects.Rename(name, newName); err != nil {
					// The vault directory is already renamed; flags and
					// members are still keyed by the old name. Surface it
					// instead of returning 200 with a project that looks
					// unflagged and member-less.
					WriteError(w, http.StatusInternalServerError, CodeServerInternal, "project renamed on disk, but its flags and members could not follow: "+err.Error())
					return
				}
			}
			r.auditNote(req, audit.ActionRenameProject, user, name, newName, 0)
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

	var removed []string
	if r.deps.Trash != nil {
		_, notes, err := r.deps.Trash.DiscardProject(name)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, "trash: "+err.Error())
			return
		}
		removed = notes
	} else {
		var err error
		removed, err = r.deps.Vault.DeleteProject(name)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, "delete: "+err.Error())
			return
		}
	}
	if r.deps.Index != nil {
		for _, p := range removed {
			_ = r.deps.Index.Delete(p)
		}
	}
	if r.deps.Projects != nil {
		_ = r.deps.Projects.Delete(name)
	}
	r.auditNote(req, audit.ActionDeleteProject, user, name, "", int64(len(removed)))
	r.publishSidebarEvent("delete", name)
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
	projs, err := r.deps.Vault.Projects()
	if err != nil {
		return false
	}
	for _, p := range projs {
		if p.Name == name {
			return true
		}
	}
	return false
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
