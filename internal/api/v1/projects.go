package v1

import (
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
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
	// Public marks the project as visible to guest-role users (read-only).
	Public bool `json:"public"`
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
	NewName          *string `json:"new_name,omitempty"`
	HiddenFromMCP    *bool   `json:"hidden_from_mcp,omitempty"`
	SkipGitSync      *bool   `json:"skip_git_sync,omitempty"`
	Public           *bool   `json:"public,omitempty"`
	UseGlobals       *bool   `json:"use_globals,omitempty"`
	UseAnchors       *bool   `json:"use_anchors,omitempty"`
	UseTagVocabulary *bool   `json:"use_tag_vocabulary,omitempty"`
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
		if !r.canAccessProject(princ, p.Name) {
			continue // gated by visibility / membership
		}
		flags := r.projectFlag(p.Name)
		out = append(out, projectView{
			Name:             p.Name,
			NoteCount:        p.NoteCount,
			HiddenFromMCP:    flags.HiddenFromMCP,
			SkipGitSync:      flags.SkipGitSync,
			Public:           flags.Public,
			UseGlobals:       flags.UseGlobals,
			UseAnchors:       flags.UseAnchors,
			UseTagVocabulary: flags.UseTagVocabulary,
			ModTime:          formatModTime(p.ModTime),
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
}

func formatModTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(rfc3339Z)
}

func (r *Router) getProject(w http.ResponseWriter, req *http.Request, name string) {
	if !r.canAccessProject(principalFromContext(req), name) {
		// No visibility/membership — 404 hides existence.
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
			f := r.projectFlag(name)
			WriteJSON(w, http.StatusOK, projectView{
				Name:             p.Name,
				NoteCount:        p.NoteCount,
				HiddenFromMCP:    f.HiddenFromMCP,
				SkipGitSync:      f.SkipGitSync,
				Public:           f.Public,
				UseGlobals:       f.UseGlobals,
				UseAnchors:       f.UseAnchors,
				UseTagVocabulary: f.UseTagVocabulary,
			})
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
	// Under member_scope=members the creator must keep access to what they just
	// made; owners see everything, so only non-owner creators need a membership.
	if r.deps.Projects != nil && !user.principal().CanAdmin() {
		_ = r.deps.Projects.SetMember(clean, user.ID, projects.LevelWrite)
	}
	r.auditNote(req, audit.ActionCreateProject, user, clean, "", 0)
	r.publishSidebarEvent("create", clean)
	WriteJSON(w, http.StatusCreated, projectView{Name: clean})
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
	if r.denyWriteProject(w, user.principal(), name) {
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

	// Apply flags first (cheap, no fs movement) so a failing rename
	// still leaves the flags durable.
	flagsChanged := false
	if body.HiddenFromMCP != nil || body.SkipGitSync != nil || body.Public != nil || body.UseGlobals != nil || body.UseAnchors != nil || body.UseTagVocabulary != nil {
		current := r.projectFlag(name)
		if body.HiddenFromMCP != nil {
			current.HiddenFromMCP = *body.HiddenFromMCP
		}
		if body.SkipGitSync != nil {
			current.SkipGitSync = *body.SkipGitSync
		}
		if body.Public != nil {
			current.Public = *body.Public
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
				_ = r.deps.Projects.Rename(name, newName)
			}
			r.auditNote(req, audit.ActionRenameProject, user, name, newName, 0)
			finalName = newName
		}
	}
	if flagsChanged {
		r.auditNote(req, audit.ActionProjectFlagsUpdate, user, finalName, "", 0)
	}
	r.publishSidebarEvent("update", finalName)

	flags := r.projectFlag(finalName)
	projs, _ := r.deps.Vault.Projects()
	count := 0
	for _, p := range projs {
		if p.Name == finalName {
			count = p.NoteCount
			break
		}
	}
	WriteJSON(w, http.StatusOK, projectView{
		Name:             finalName,
		NoteCount:        count,
		HiddenFromMCP:    flags.HiddenFromMCP,
		SkipGitSync:      flags.SkipGitSync,
		Public:           flags.Public,
		UseGlobals:       flags.UseGlobals,
		UseAnchors:       flags.UseAnchors,
		UseTagVocabulary: flags.UseTagVocabulary,
	})
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
	if r.denyWriteProject(w, user.principal(), name) {
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
