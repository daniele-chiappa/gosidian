package v1

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/projectops"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/trash"
	"github.com/gosidian/gosidian/internal/webauth"
)

// trashView is the JSON shape returned for each trash entry. The
// `discarded_at` field uses RFC 3339 UTC for SPA-side date math; ID
// is opaque (filename inside the trash dir) and the only handle the
// SPA needs to call /restore or /purge.
type trashView struct {
	ID          string `json:"id"`
	OriginPath  string `json:"origin_path"`
	DiscardedAt string `json:"discarded_at"`
	IsDir       bool   `json:"is_dir"`
}

func (r *Router) handleTrash(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.deps.Trash == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "trash not enabled in server config")
		return
	}
	if denyGuestWrite(w, UserFromContext(req.Context())) {
		return // trash management is a member+ feature, not for read-only guests
	}
	entries, err := r.deps.Trash.List()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	princ := principalFromContext(req)
	out := make([]trashView, 0, len(entries))
	for _, e := range entries {
		if r.trashLevel(princ, e) < authz.LevelRead {
			continue // hide what the user could not read before it was trashed
		}
		out = append(out, trashView{
			ID:          e.ID,
			OriginPath:  e.OriginPath,
			DiscardedAt: e.DiscardedAt.UTC().Format(time.RFC3339),
			IsDir:       e.IsDir,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
}

// handleTrashItem dispatches per-entry operations. Subpath structure:
//
//	POST   /api/v1/trash/{id}/restore  → restore + reindex
//	DELETE /api/v1/trash/{id}          → purge (irreversible)
func (r *Router) handleTrashItem(w http.ResponseWriter, req *http.Request) {
	if r.deps.Trash == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "trash not enabled in server config")
		return
	}
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if denyGuestWrite(w, user) {
		return
	}
	rest := strings.TrimPrefix(req.URL.Path, "/api/v1/trash/")
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "trash id required")
		return
	}

	// Two valid shapes: "<id>" or "<id>/restore". Anything else is
	// invalid — there's no /api/v1/trash/{id}/foo subroute today.
	if i := strings.Index(rest, "/"); i >= 0 {
		id := rest[:i]
		action := rest[i+1:]
		if action != "restore" {
			WriteError(w, http.StatusNotFound, CodeNotFound, "unknown trash action")
			return
		}
		if req.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
			return
		}
		r.restoreTrash(w, req, id, user)
		return
	}

	if req.Method != http.MethodDelete {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	r.purgeTrash(w, req, rest, user)
}

func (r *Router) restoreTrash(w http.ResponseWriter, req *http.Request, id string, user *RequestUser) {
	e, ok := r.trashEntry(id)
	if !ok {
		WriteError(w, http.StatusNotFound, CodeNotFound, "trash entry not found")
		return
	}
	princ := user.principal()
	lvl := r.trashLevel(princ, e)
	if e.IsProject() {
		// A project comes back as it went: admin on it, as for the delete,
		// judged on the access stored with it (IMP-124).
		if denyTrashLevel(w, lvl, authz.LevelAdmin) {
			return
		}
		res, err := projectops.RestoreProject(r.deps.Vault, r.deps.Index, r.deps.Projects, r.mcpTokens(), r.deps.Trash, id, user.ID)
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
		r.auditNote(req, audit.ActionCreate, user, id, strings.Join(res.Restored, ","), int64(len(res.Restored)))
		r.publishSidebarEvent("create", res.Name)
		WriteJSON(w, http.StatusOK, map[string]any{"restored": res.Restored, "project": res.Name, "access_restored": res.AccessRestored})
		return
	}
	// Restoring re-creates a note, or a folder (IMP-150), in its origin
	// project — gate it on write access there (BUG-020 / per-project
	// access). A project that is gone would come back implicitly, without
	// its access: restore it first.
	if denyTrashLevel(w, lvl, authz.LevelWrite) {
		return
	}
	if project, ok := trashedNoteProject(e.OriginPath); ok && !projectops.FolderExists(r.deps.Vault, project) {
		WriteError(w, http.StatusConflict, CodeConflict, "project "+project+" does not exist: restore it first")
		return
	}
	// Trashed before the state dir was set there, it would come back into
	// the credentials (BUG-098).
	if _, err := r.deps.Vault.Rel(e.OriginPath); err != nil || r.deps.Vault.HoldsStateDir(e.OriginPath) {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "the origin is in the server's state directory")
		return
	}
	restored, _, err := r.deps.Trash.Restore(id)
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	// Reindex what came back. Trash returns vault-relative paths so
	// we can Load + Upsert directly, mirroring the v1.x HTML restore
	// handler.
	if r.deps.Index != nil {
		for _, p := range restored {
			note, lerr := r.deps.Vault.Load(p)
			if lerr != nil {
				continue
			}
			_ = r.deps.Index.Upsert(index.NoteDoc{
				Path:    note.Path,
				Title:   note.Title,
				Body:    string(note.Content),
				ModTime: note.ModTime.Unix(),
				Size:    note.Size,
			})
		}
	}
	r.auditNote(req, audit.ActionCreate, user, id, strings.Join(restored, ","), int64(len(restored)))
	// After the reindex: the tree is built from the index.
	r.publishNoteEvent(events.TopicTree, e.OriginPath, "create", nil)
	WriteJSON(w, http.StatusOK, map[string]any{"restored": restored})
}

func (r *Router) purgeTrash(w http.ResponseWriter, req *http.Request, id string, user *RequestUser) {
	e, ok := r.trashEntry(id)
	if !ok {
		WriteError(w, http.StatusNotFound, CodeNotFound, "trash entry not found")
		return
	}
	// Permanently deleting a trashed note or folder from a project the user
	// can't write to would be a cross-project mutation; a trashed project
	// needs admin on it, as its delete did.
	need := authz.LevelWrite
	if e.IsProject() {
		need = authz.LevelAdmin
	}
	if denyTrashLevel(w, r.trashLevel(user.principal(), e), need) {
		return
	}
	if err := r.deps.Trash.Purge(id); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	r.auditNote(req, audit.ActionDelete, user, id, "", 0)
	w.WriteHeader(http.StatusNoContent)
}

// trashEntry finds a trash entry by id.
func (r *Router) trashEntry(id string) (trash.Entry, bool) {
	entries, err := r.deps.Trash.List()
	if err != nil {
		return trash.Entry{}, false
	}
	for _, e := range entries {
		if e.ID == id {
			return e, true
		}
	}
	return trash.Entry{}, false
}

// trashLevel is the principal's level on what a trash entry was. A trashed
// project is judged on the access stored with it at delete time; the live
// store no longer has it and answered with the default visibility, so any
// account saw, restored (exposed) or purged another's private project
// (IMP-124). A project trashed without stored access, a note whose project
// is gone, and a note trashed before the current project with its name
// began, are the owner's alone.
func (r *Router) trashLevel(p authz.Principal, e trash.Entry) authz.Level {
	if p.Role == webauth.RoleOwner {
		return authz.LevelAdmin
	}
	// An origin that is not a vault path (an entry put in the trash by
	// hand) names no project anyone else could have read (BUG-083).
	if !trash.ValidOrigin(e.OriginPath) {
		return authz.LevelNone
	}
	if e.IsProject() {
		if r.deps.Projects == nil {
			return authz.LevelNone
		}
		a, ok, err := projectops.TrashedAccess(r.deps.Trash, e.ID)
		if err != nil || !ok {
			return authz.LevelNone
		}
		return p.Level(e.OriginPath, r.deps.Projects.AccessConfigWith(e.OriginPath, a))
	}
	if project, ok := trashedNoteProject(e.OriginPath); ok {
		if !projectops.FolderExists(r.deps.Vault, project) {
			return authz.LevelNone
		}
		// Trashed before the project took the name (created, restored,
		// renamed onto it): it came from an earlier project, whose readers
		// are not this one's.
		if since := r.projectFlag(project).Since; since > 0 && e.DiscardedAt.UnixNano() < since {
			return authz.LevelNone
		}
	}
	return r.levelOf(p, projectOf(e.OriginPath))
}

// trashedNoteProject returns the project folder a trashed note or folder
// came from; false for a note at the vault root, which has none.
func trashedNoteProject(origin string) (string, bool) {
	if !strings.Contains(origin, "/") {
		return "", false
	}
	return projectOf(origin), true
}

// denyTrashLevel answers 404 when the entry is not even readable by the
// principal (it does not list it either), 403 when it is but lvl is below
// need; true means the caller stops.
func denyTrashLevel(w http.ResponseWriter, lvl, need authz.Level) bool {
	switch {
	case lvl >= need:
		return false
	case lvl < authz.LevelRead:
		WriteError(w, http.StatusNotFound, CodeNotFound, "trash entry not found")
	default:
		WriteError(w, http.StatusForbidden, CodeAuthForbidden, "you do not have the access this needs on the trashed item's project")
	}
	return true
}
