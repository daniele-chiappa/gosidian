package v1

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/gitsync"
)

// commitView is the JSON shape returned per commit. SHA is the full
// hash; ShortSHA is a stable 7-char prefix the SPA can render in
// compact lists. Date is RFC 3339 UTC. Path is the note's path in that
// commit, From the one before in the commit that renamed it (IMP-222).
type commitView struct {
	SHA      string `json:"sha"`
	ShortSHA string `json:"short_sha"`
	Author   string `json:"author"`
	Date     string `json:"date"`
	Subject  string `json:"subject"`
	Path     string `json:"path"`
	From     string `json:"from,omitempty"`
}

// versionMaxBytes is the largest version whose text and diff are given:
// the note the web UI opens to edit, whose text fits in a request body.
const versionMaxBytes = 1 << 20

// versionView is a version of a note (IMP-222, F-01): its text and the
// diff from it to the note as it is now, or why they are left out.
type versionView struct {
	commitView
	Content *string `json:"content,omitempty"`
	Diff    *string `json:"diff,omitempty"`
	Same    bool    `json:"same"`
	// Omitted says why there is no content nor diff: "deleted" (the commit
	// deleted the note), "too_large" (it or the note now over 1 MiB),
	// "not_text".
	Omitted string `json:"omitted,omitempty"`
	// ETag is the note's as the diff read it: the if_match of a restore
	// from what the reader saw.
	ETag string `json:"etag"`
}

// readHistory returns the git log of a single note. Requires the
// gitsync subsystem to be wired AND enabled in cfg; otherwise 503.
// `limit` query param caps results (default 50, max 500). The versions
// the note had in a project the principal cannot see are left out.
func (r *Router) readHistory(w http.ResponseWriter, req *http.Request, notePath string) {
	p := principalFromContext(req)
	if !r.canSee(p, notePath) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
		return
	}
	if r.deps.GitSync == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "git sync not wired")
		return
	}
	// Guard the path: history of a non-existent note would let the
	// caller probe arbitrary git paths. Vault.Load already maps to
	// the same fs space the gitsync.History expects.
	if _, err := r.deps.Vault.Load(notePath); err != nil {
		writeLoadError(w, err)
		return
	}
	limit := limitParam(req, 50, 500)
	commits, err := r.deps.GitSync.History(notePath, limit)
	if err != nil {
		writeGitError(w, err)
		return
	}
	out := make([]commitView, 0, len(commits))
	for _, c := range commits {
		if r.canSee(p, c.Path) {
			out = append(out, r.commitViewFor(p, c))
		}
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items": out,
		"total": len(out),
		"limit": limit,
	})
}

// readVersion answers GET /notes/{path}/history/{sha}: the note as that
// commit of its history held it, under the name it had then, and the diff
// from it to the note now. A sha that is no version of the note, or a
// version in a project the principal cannot see, is 404.
func (r *Router) readVersion(w http.ResponseWriter, req *http.Request, notePath, sha string) {
	p := principalFromContext(req)
	if !r.canSee(p, notePath) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
		return
	}
	if r.deps.GitSync == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "git sync not wired")
		return
	}
	note, err := r.deps.Vault.Load(notePath)
	if err != nil {
		writeLoadError(w, err)
		return
	}
	v, err := r.deps.GitSync.Version(notePath, sha, versionMaxBytes)
	if err != nil {
		writeGitError(w, err)
		return
	}
	if !r.canSee(p, v.Path) {
		WriteError(w, http.StatusNotFound, CodeNotFound, gitsync.ErrNoVersion.Error())
		return
	}
	out := versionView{commitView: r.commitViewFor(p, v.Commit), ETag: note.ETag()}
	switch {
	case v.Status == "deleted":
		out.Omitted = "deleted"
	case v.Content == nil || note.Size > versionMaxBytes:
		out.Omitted = "too_large"
	case !isText(v.Content) || !isText(note.Content):
		out.Omitted = "not_text"
	default:
		diff, err := gitsync.Diff(v.Content, note.Content)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, "diff: "+err.Error())
			return
		}
		content := string(v.Content)
		out.Content, out.Diff = &content, &diff
		out.Same = bytes.Equal(v.Content, note.Content)
	}
	WriteJSON(w, http.StatusOK, out)
}

// commitViewFor is c as p may read it: the name a note had in a project p
// cannot see is not said, neither as from nor in the subject of the move.
func (r *Router) commitViewFor(p authz.Principal, c gitsync.Commit) commitView {
	v := commitView{
		SHA:      c.SHA,
		ShortSHA: c.ShortSHA,
		Author:   c.Author,
		Date:     c.Date.UTC().Format(time.RFC3339),
		Subject:  c.Subject,
		Path:     c.Path,
	}
	if c.From != "" && r.canSee(p, c.From) {
		v.From = c.From
	}
	// The subject of a move, as the git sync writes it: "rename <from> ->
	// <to>", maybe "and N more" (gitsync.write.line).
	if from, ok := strings.CutPrefix(c.Subject, "rename "); ok && !strings.HasPrefix(from, "project ") && !r.canSee(p, from) {
		v.Subject = "rename"
		if _, to, ok := strings.Cut(from, " -> "); ok {
			v.Subject = "rename (from another project) -> " + to
		}
	}
	return v
}

// writeGitError answers a failure of the git history: off is 503 so the
// SPA branches on availability cleanly, a sha of no version 404.
func writeGitError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gitsync.ErrDisabled):
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, err.Error())
	case errors.Is(err, gitsync.ErrNoVersion):
		WriteError(w, http.StatusNotFound, CodeNotFound, err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
	}
}

// isText says whether b reads as text: UTF-8 without NUL.
func isText(b []byte) bool {
	return utf8.Valid(b) && bytes.IndexByte(b, 0) < 0
}
