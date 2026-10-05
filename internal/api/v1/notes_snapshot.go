package v1

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/views"
)

// snapshotResponse is the result of POST /notes/{path}/snapshot: the new
// snapshot note, and how many views, values and embeds it froze.
type snapshotResponse struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	views.FreezeStats
}

// createSnapshot answers POST /notes/{path}/snapshot (IMP-127 iteration 3,
// phase 3): the note frozen as it reads now for the reader (views as their
// rows, values as numbers, embeds included), written beside it in
// <name>.snapshots/YYYY-MM-DD.md. The note itself does not change.
func (r *Router) createSnapshot(w http.ResponseWriter, req *http.Request, rel string) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	p := user.principal()
	if !r.canSee(p, rel) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
		return
	}
	if !strings.HasSuffix(strings.ToLower(rel), ".md") {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "only a markdown note has views to freeze")
		return
	}
	if denyGuestWrite(w, user) || r.denyWriteProject(w, p, projectOf(rel)) {
		return
	}
	note, err := r.deps.Vault.Load(rel)
	if err != nil {
		writeLoadError(w, err)
		return
	}
	now := time.Now()
	dest := views.SnapshotPath(rel, now, func(path string) bool {
		_, err := r.deps.Vault.Load(path)
		return err == nil
	})
	var frozen []byte
	var st views.FreezeStats
	if r.deps.Index != nil {
		c := views.Context{
			This:    views.ThisFields(rel, parser.ParseFrontmatterFields(parser.FrontmatterRawForPath(rel, note.Content))),
			Today:   now,
			Schema:  r.viewSchema(p),
			Resolve: previewResolver{r: r, p: p}.Resolve,
			Load:    r.viewLoad(p),
		}
		frozen, st = views.Freeze(note.Content, c, r.viewQuery(p))
	} else {
		frozen = note.Content
	}
	content := views.SnapshotNote(rel, note.Title, projectOf(rel), frozen, now)

	unlock := r.deps.Vault.LockPath(dest)
	defer unlock()
	if _, err := r.deps.Vault.Load(dest); err == nil {
		WriteError(w, http.StatusConflict, CodeConflict, "a snapshot "+dest+" already exists: try again")
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "load probe: "+err.Error())
		return
	}
	if err := r.writeAndIndex(dest, content); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "write: "+err.Error())
		return
	}
	fresh, err := r.deps.Vault.Load(dest)
	if err != nil {
		writeLoadError(w, err)
		return
	}
	r.auditNote(req, audit.ActionCreate, user, dest, "", int64(len(content)))
	r.publishNoteEvent(events.TopicNote, dest, "create", fresh)
	r.publishNoteEvent(events.TopicTree, dest, "create", fresh)
	WriteJSON(w, http.StatusCreated, snapshotResponse{Path: dest, Source: rel, FreezeStats: st})
}
