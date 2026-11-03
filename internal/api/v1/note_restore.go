package v1

import (
	"bytes"
	"net/http"
	"strings"
	"sync"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/server/events"
)

// restoreRequest is the body of POST /notes/{path}/restore.
type restoreRequest struct {
	// SHA is the version, a commit of the note's history.
	SHA string `json:"sha"`
	// IfMatch is the etag of the note as the window read it, as the
	// If-Match header: the note changed since is 412, as for PUT.
	IfMatch string `json:"if_match"`
}

// restoreResponse says whether the note changed: a version the same as
// the note writes nothing. Commit is the commit of the restore, at the top
// of the history; none when the commit failed (CommitError), and the
// debounced commit takes the note later.
type restoreResponse struct {
	Restored    bool         `json:"restored"`
	Note        noteResponse `json:"note"`
	Commit      *commitView  `json:"commit,omitempty"`
	CommitError string       `json:"commit_error,omitempty"`
}

// restoreNote answers POST /notes/{path}/restore (IMP-222, F-01): the text
// of a version of the note's history written back at its path today, as a
// save writes it (the index, the event an open editor sees, the audit log
// with the action restore), then committed at once in the writer's name,
// "restore <path> to <sha7>". For who may write the note; the version
// must be one the principal may read.
func (r *Router) restoreNote(w http.ResponseWriter, req *http.Request, rel string) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if denyGuestWrite(w, user) {
		return
	}
	if r.denyWriteProject(w, user.principal(), projectOf(rel)) {
		return
	}
	if !r.deps.Vault.IsNoteFile(rel) {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "path must be a note file (.md, or .html when html notes are enabled)")
		return
	}
	if r.deps.GitSync == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "git sync not wired")
		return
	}
	var body restoreRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	body.SHA = strings.TrimSpace(body.SHA)
	if body.SHA == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "sha is required: the version to restore")
		return
	}
	ifMatch := strings.TrimSpace(req.Header.Get("If-Match"))
	if ifMatch == "" {
		ifMatch = strings.TrimSpace(body.IfMatch)
	}

	// Let go before the commit below, or on any way out before it.
	unlock := sync.OnceFunc(r.deps.Vault.LockPath(rel))
	defer unlock()
	existing, err := r.deps.Vault.Load(rel)
	if err != nil {
		writeLoadError(w, err)
		return
	}
	if ifMatch != "" && !etagMatches(ifMatch, existing.ETag()) {
		WriteErrorWithDetails(w, http.StatusPreconditionFailed, CodeConcurrencyEtag,
			"note was modified since the last read",
			map[string]any{"current_etag": quoteETag(existing.ETag())})
		return
	}
	v, err := r.deps.GitSync.Version(rel, body.SHA, versionMaxBytes)
	if err != nil {
		writeGitError(w, err)
		return
	}
	p := user.principal()
	if !r.canSee(p, v.Path) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "no such version of the note")
		return
	}
	switch {
	case v.Status == "deleted":
		WriteError(w, http.StatusUnprocessableEntity, CodeValidationFormat, "the note did not exist in that version: nothing to restore")
		return
	case v.Content == nil:
		WriteError(w, http.StatusUnprocessableEntity, CodeValidationFormat, "that version is over 1 MiB: restore it with git")
		return
	}
	if bytes.Equal(v.Content, existing.Content) {
		w.Header().Set("ETag", quoteETag(existing.ETag()))
		WriteJSON(w, http.StatusOK, restoreResponse{Note: r.toNoteResponse(existing)})
		return
	}

	if err := r.writeAndIndex(rel, v.Content); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "write: "+err.Error())
		return
	}
	fresh, err := r.deps.Vault.Load(rel)
	if err != nil {
		writeLoadError(w, err)
		return
	}
	// The audit log tells the git sync who restored which version: the
	// commit below names them.
	r.auditNote(req, audit.ActionRestore, user, rel, v.SHA, int64(len(v.Content)))
	r.publishNoteEvent(events.TopicNote, rel, "update", fresh)

	// The note's lock goes before the commit: git sync leaves out a file
	// whose lock a writer holds (BUG-147).
	unlock()
	out := restoreResponse{Restored: true, Note: r.toNoteResponse(fresh)}
	if err := r.deps.GitSync.CommitWrites(); err != nil {
		out.CommitError = err.Error()
	} else if h, err := r.deps.GitSync.History(rel, 1); err == nil && len(h) == 1 {
		c := r.commitViewFor(p, h[0])
		out.Commit = &c
	}
	w.Header().Set("ETag", quoteETag(fresh.ETag()))
	WriteJSON(w, http.StatusOK, out)
}
