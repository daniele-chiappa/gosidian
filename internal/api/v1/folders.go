package v1

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/server/events"
)

// handleFolderByPath serves the folder actions of the tree's context menu
// (IMP-150):
//
//	GET    /api/v1/folders/{path}/export.zip → zip of the folder
//	DELETE /api/v1/folders/{path}            → the folder into the trash
//
// A project folder has its own routes under /api/v1/projects/: its delete
// also drops its access and its place in token scopes.
func (r *Router) handleFolderByPath(w http.ResponseWriter, req *http.Request) {
	rest := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v1/folders/"), "/")
	if dir, ok := strings.CutSuffix(rest, "/export.zip"); ok && req.Method == http.MethodGet {
		r.exportFolder(w, req, dir)
		return
	}
	if req.Method != http.MethodDelete {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	r.deleteFolder(w, req, rest)
}

// exportFolder streams the zip of any folder of a project the account can
// read, with the limit and the audit of the project export (IMP-099). The
// names in the archive start at the folder itself.
func (r *Router) exportFolder(w http.ResponseWriter, req *http.Request, dir string) {
	user, ok := r.exportUser(w, req)
	if !ok {
		return
	}
	clean, err := r.deps.Vault.Rel(dir)
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "invalid path: "+err.Error())
		return
	}
	if !r.canSee(user.principal(), clean) || !r.folderExists(clean) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "folder not found")
		return
	}
	name := strings.ReplaceAll(clean, "/", "-") + "-" + time.Now().UTC().Format("20060102") + ".zip"
	r.streamExport(w, req, user, clean, name)
}

// deleteFolder moves a folder inside a project, with all it holds, into the
// trash as one entry. Without a trash it refuses: a recursive delete for
// good is not a right-click away.
func (r *Router) deleteFolder(w http.ResponseWriter, req *http.Request, dir string) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if denyGuestWrite(w, user) {
		return
	}
	if r.deps.Trash == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "deleting a folder needs the trash, which is not enabled in server config")
		return
	}
	clean, err := r.deps.Vault.Rel(dir)
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "invalid path: "+err.Error())
		return
	}
	if !strings.Contains(clean, "/") {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "a project is deleted from the projects page, not as a folder")
		return
	}
	princ := user.principal()
	if !r.canSee(princ, clean) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "folder not found")
		return
	}
	if r.denyWriteProject(w, princ, projectOf(clean)) {
		return
	}
	if !r.folderExists(clean) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "folder not found")
		return
	}
	if r.deps.Vault.HoldsStateDir(clean) {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "the folder holds the server's state directory")
		return
	}
	id, removed, err := r.deps.Trash.DiscardFolder(clean)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "trash: "+err.Error())
		return
	}
	if r.deps.Index != nil {
		for _, p := range removed {
			_ = r.deps.Index.Delete(p)
		}
	}
	r.auditNote(req, audit.ActionDelete, user, clean, id, int64(len(removed)))
	for _, p := range removed {
		r.publishNoteEvent(events.TopicNote, p, "delete", nil)
	}
	r.publishNoteEvent(events.TopicTree, clean, "delete", nil)
	WriteJSON(w, http.StatusOK, map[string]any{"trash_id": id, "removed": removed})
}

// folderExists reports whether rel (already through Vault.Rel) is a real
// directory of the vault, not a symlink to one.
func (r *Router) folderExists(rel string) bool {
	st, err := os.Lstat(filepath.Join(r.deps.Vault.Root, filepath.FromSlash(rel)))
	return err == nil && st.IsDir()
}
