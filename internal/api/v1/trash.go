package v1

import (
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/index"
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
		if !r.canSee(princ, e.OriginPath) {
			continue // hide trashed notes from projects the user can't access
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
	// Restoring re-creates a note in its origin project — gate it on write
	// access there. Look the origin up before the restore mutates anything.
	// See BUG-020 / per-project access.
	{
		if entries, lerr := r.deps.Trash.List(); lerr == nil {
			for _, e := range entries {
				if e.ID == id {
					if r.denyWriteProject(w, user.principal(), projectOf(e.OriginPath)) {
						return
					}
					break
				}
			}
		}
	}
	restored, err := r.deps.Trash.Restore(id)
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
	WriteJSON(w, http.StatusOK, map[string]any{"restored": restored})
}

func (r *Router) purgeTrash(w http.ResponseWriter, req *http.Request, id string, user *RequestUser) {
	// Permanently deleting a trashed note from a project the user can't write to
	// would be a cross-project mutation — gate it on write access there.
	{
		if entries, lerr := r.deps.Trash.List(); lerr == nil {
			for _, e := range entries {
				if e.ID == id {
					if r.denyWriteProject(w, user.principal(), projectOf(e.OriginPath)) {
						return
					}
					break
				}
			}
		}
	}
	if err := r.deps.Trash.Purge(id); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	r.auditNote(req, audit.ActionDelete, user, id, "", 0)
	w.WriteHeader(http.StatusNoContent)
}
