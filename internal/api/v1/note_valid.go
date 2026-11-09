package v1

import (
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/gitsync"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/vault"
)

// The versions of a note that have a name, and the one readers must see
// (IMP-201). A note names its valid version in its frontmatter, and the
// note as it is today is the draft: who edits works there, who reads gets
// the valid version and can move to the draft. Naming versions and choosing
// the valid one are the project admin's; anyone who reads the note reads
// its tags. It says which version is right, it hides none.

// noteTagView is a named version of a note.
type noteTagView struct {
	Tag      string `json:"tag"`
	Label    string `json:"label"`
	SHA      string `json:"sha"`
	ShortSHA string `json:"short_sha"`
	By       string `json:"by,omitempty"`
	Date     string `json:"date"`
	Message  string `json:"message,omitempty"`
	Path     string `json:"path"`
}

// validView is the valid version of a note. Same says the note today is
// that version, so there is no draft to tell about. Missing says the tag
// gives no text the reader may read: they see the note as it is, and are
// told.
type validView struct {
	Tag      string `json:"tag"`
	Label    string `json:"label"`
	SHA      string `json:"sha,omitempty"`
	ShortSHA string `json:"short_sha,omitempty"`
	Date     string `json:"date,omitempty"`
	Same     bool   `json:"same"`
	Missing  bool   `json:"missing,omitempty"`
}

// mcpView is what a note chooses for who calls over MCP (IMP-228): Tag is a
// tag of the note, or "valid" for the version readers see. Label is the
// version that gives, Missing says it gives no text: they read the note as
// it is.
type mcpView struct {
	Tag     string `json:"tag"`
	Label   string `json:"label,omitempty"`
	Missing bool   `json:"missing,omitempty"`
}

func toNoteTagView(t gitsync.NoteTag) noteTagView {
	return noteTagView{Tag: t.Name, Label: t.Label, SHA: t.SHA, ShortSHA: t.ShortSHA, By: t.By, Date: t.Date.UTC().Format(rfc3339Z), Message: t.Message, Path: t.Path}
}

// validOf reads the valid version content names: nil for a note without
// one. Who reads the note reads it, wherever it sat: who chose it could
// read it (setValid), and a project renamed since is out of everyone's
// sight. It is what the note shows, while the history keeps its own rule.
func (r *Router) validOf(rel string, content []byte) *validView {
	v := r.deps.GitSync.Valid(rel, content, versionMaxBytes)
	if v == nil {
		return nil
	}
	out := &validView{Tag: v.Tag, Label: v.Label, Missing: v.Missing}
	if !v.Missing {
		out.SHA, out.ShortSHA, out.Date, out.Same = v.SHA, v.ShortSHA, v.Date.UTC().Format(rfc3339Z), v.Same
	}
	return out
}

// mcpOf reads what content chooses for who calls over MCP: nil for nothing.
func (r *Router) mcpOf(rel string, content []byte) *mcpView {
	tag := vault.MCPVersion(rel, content)
	if tag == "" {
		return nil
	}
	out := &mcpView{Tag: tag}
	if v := r.deps.GitSync.ForMCP(rel, content, versionMaxBytes); v != nil {
		out.Label, out.Missing = v.Label, v.Missing
	}
	return out
}

// noteVersion reads the version want names: "valid", for the note's valid
// version, or a tag of its history. It is nil for a valid version that is
// missing, or none: the reader is told through Valid.
func (r *Router) noteVersion(p authz.Principal, note *vault.Note, valid *validView, want string) (*gitsync.Version, error) {
	if want == "valid" {
		if valid == nil || valid.Missing {
			return nil, nil
		}
		chosen := r.deps.GitSync.Valid(note.Path, note.Content, versionMaxBytes)
		if chosen == nil || chosen.Missing {
			return nil, nil
		}
		return &chosen.Version, nil
	}
	if r.deps.GitSync == nil {
		return nil, gitsync.ErrDisabled
	}
	v, err := r.deps.GitSync.TagVersion(note.Path, want, versionMaxBytes)
	if err != nil {
		return nil, err
	}
	// A tag by name is a part of the history, with its rule (validOf).
	if v.Content == nil || !r.canSee(p, v.Path) {
		return nil, gitsync.ErrNoVersion
	}
	return &v, nil
}

// noteAt gives note as the version want names, or as it is. The date is the
// version's, so its etag is no etag to write the note with.
func (r *Router) noteAt(p authz.Principal, note *vault.Note, valid *validView, want string) (*vault.Note, error) {
	v, err := r.noteVersion(p, note, valid, want)
	if err != nil || v == nil {
		return note, err
	}
	at := *note
	at.Content, at.Size, at.ModTime = v.Content, v.Size, v.Date
	return &at, nil
}

func (r *Router) handleVersions(w http.ResponseWriter, req *http.Request, rel string) {
	switch req.Method {
	case http.MethodGet:
		r.readVersions(w, req, rel)
	case http.MethodPost:
		r.createNoteTag(w, req, rel)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) handleValid(w http.ResponseWriter, req *http.Request, rel string) {
	r.chooseVersion(w, req, rel, versionChoice{audit.ActionNoteValid, r.deps.Vault.SetValidVersion, false})
}

func (r *Router) handleMCPVersion(w http.ResponseWriter, req *http.Request, rel string) {
	r.chooseVersion(w, req, rel, versionChoice{audit.ActionNoteMCPVersion, r.deps.Vault.SetMCPVersion, true})
}

// versionChoice is what a note can choose a version for: readers, or who
// calls over MCP. follow says it takes "valid" as well as a tag.
type versionChoice struct {
	action audit.Action
	set    func(rel, tag string) error
	follow bool
}

func (r *Router) chooseVersion(w http.ResponseWriter, req *http.Request, rel string, c versionChoice) {
	switch req.Method {
	case http.MethodPut, http.MethodDelete:
		r.setVersion(w, req, rel, c)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

// readVersions answers GET /notes/{path}/versions: the note's tags, newest
// first, its valid version, and whether the reader may change them. The
// tags on versions in a project the reader cannot see are left out.
func (r *Router) readVersions(w http.ResponseWriter, req *http.Request, rel string) {
	p := principalFromContext(req)
	if !r.canSee(p, rel) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
		return
	}
	if r.deps.GitSync == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "git sync not wired")
		return
	}
	note, err := r.deps.Vault.Load(rel)
	if err != nil {
		writeLoadError(w, err)
		return
	}
	tags, err := r.deps.GitSync.NoteTags(rel)
	if err != nil {
		writeGitError(w, err)
		return
	}
	items := make([]noteTagView, 0, len(tags))
	for _, t := range tags {
		if r.canSee(p, t.Path) {
			items = append(items, toNoteTagView(t))
		}
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"valid":     r.validOf(rel, note.Content),
		"mcp":       r.mcpOf(rel, note.Content),
		"can_admin": r.canAdminProject(p, projectOf(rel)),
	})
}

// denyNoteVersions is the gate of what changes a note's versions: the
// project's admin, on a note, with git sync. It writes the refusal.
func (r *Router) denyNoteVersions(w http.ResponseWriter, req *http.Request, rel string) (*RequestUser, bool) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return nil, true
	}
	if denyGuestWrite(w, user) || r.denyAdminProject(w, user.principal(), projectOf(rel)) {
		return nil, true
	}
	if !r.deps.Vault.IsNoteFile(rel) {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "path must be a note file (.md, or .html when html notes are enabled)")
		return nil, true
	}
	if r.deps.GitSync == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "git sync not wired")
		return nil, true
	}
	return user, false
}

// noteTagRequest is the body of POST /notes/{path}/versions. Label is what
// people call the version, V3; SHA the version, a commit of the note's
// history, or none for the note as it is.
type noteTagRequest struct {
	Label   string `json:"label"`
	SHA     string `json:"sha"`
	Message string `json:"message"`
}

// createNoteTag answers POST /notes/{path}/versions: a tag on a version of
// the note, in the name of who asked. A label on another version is 409:
// a tag never moves.
func (r *Router) createNoteTag(w http.ResponseWriter, req *http.Request, rel string) {
	user, denied := r.denyNoteVersions(w, req, rel)
	if denied {
		return
	}
	var body noteTagRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if _, err := r.deps.Vault.Load(rel); err != nil {
		writeLoadError(w, err)
		return
	}
	done, err := r.deps.GitSync.TagNote(req.Context(), rel, strings.TrimSpace(body.SHA), strings.TrimSpace(body.Label), body.Message, user.Username)
	switch {
	case errors.Is(err, gitsync.ErrBadLabel):
		WriteError(w, http.StatusUnprocessableEntity, CodeValidationFormat, err.Error())
		return
	case errors.Is(err, gitsync.ErrLabelTaken):
		WriteError(w, http.StatusConflict, CodeConflict, err.Error())
		return
	case err != nil:
		writeGitError(w, err)
		return
	}
	r.auditNote(req, audit.ActionNoteTag, user, rel, done.Label, 0)
	WriteJSON(w, http.StatusCreated, map[string]any{"tag": toNoteTagView(done.NoteTag), "pushed": done.Pushed, "push_error": done.PushError})
}

// validRequest is the body of PUT /notes/{path}/valid: Tag is a tag of the
// note, whole or by its label. IfMatch is the etag of the note the window
// read, as the If-Match header.
type validRequest struct {
	Tag     string `json:"tag"`
	IfMatch string `json:"if_match"`
}

// validResponse is the note after a change of its valid version.
// CommitError says why the commit at once failed: the debounced commit
// takes the change later.
type validResponse struct {
	Note        noteResponse `json:"note"`
	MCP         *mcpView     `json:"mcp,omitempty"`
	CommitError string       `json:"commit_error,omitempty"`
	// Restored lists the attachments of the version that had gone through
	// the GC and came back from git; RestoreError why some did not.
	Restored     []string `json:"restored,omitempty"`
	RestoreError string   `json:"restore_error,omitempty"`
}

// restoreVersionFiles brings back what the note's version at tag names and
// the vault has lost.
func (r *Router) restoreVersionFiles(rel, tag string) ([]string, error) {
	v, err := r.deps.GitSync.TagVersion(rel, tag, versionMaxBytes)
	if err != nil {
		return nil, err
	}
	return r.deps.GitSync.RestoreFiles(v, r.deps.Vault, attach.ExtSet())
}

// setVersion answers PUT /notes/{path}/valid, the note's valid version from
// now on, and DELETE, none: every reader sees the note as it is; the same on
// /mcp-version for who calls over MCP. It is written as any write of the note
// is (the index, the event, the audit log), and committed at once: the history
// says who chose what.
func (r *Router) setVersion(w http.ResponseWriter, req *http.Request, rel string, c versionChoice) {
	user, denied := r.denyNoteVersions(w, req, rel)
	if denied {
		return
	}
	var body validRequest
	if req.Method == http.MethodPut {
		if err := DecodeJSON(req, &body); err != nil {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return
		}
		if body.Tag = strings.TrimSpace(body.Tag); body.Tag == "" {
			WriteError(w, http.StatusBadRequest, CodeValidationRequired, "tag is required: the version readers must see")
			return
		}
	}
	ifMatch := strings.TrimSpace(req.Header.Get("If-Match"))
	if ifMatch == "" {
		ifMatch = strings.TrimSpace(body.IfMatch)
	}
	p := user.principal()
	var tag gitsync.NoteTag
	if c.follow && body.Tag == vault.FollowValid {
		tag = gitsync.NoteTag{Name: vault.FollowValid, Label: vault.FollowValid}
	} else if body.Tag != "" {
		tags, err := r.deps.GitSync.NoteTags(rel)
		if err != nil {
			writeGitError(w, err)
			return
		}
		found, ok := gitsync.FindTag(tags, body.Tag)
		if !ok || !r.canSee(p, found.Path) {
			WriteError(w, http.StatusNotFound, CodeNotFound, "no such tag on the note's versions")
			return
		}
		tag = found
	}

	// Let go before the commit below: git sync leaves out a file whose
	// lock a writer holds (BUG-147).
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
	if err := c.set(rel, tag.Name); err != nil {
		if errors.Is(err, vault.ErrValidVersion) {
			WriteError(w, http.StatusUnprocessableEntity, CodeValidationFormat, err.Error())
			return
		}
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "write: "+err.Error())
		return
	}
	fresh, err := r.deps.Vault.Load(rel)
	if err != nil {
		writeLoadError(w, err)
		return
	}
	if r.deps.Index != nil {
		if err := r.deps.Index.Upsert(index.NoteDoc{Path: fresh.Path, Title: fresh.Title, Body: string(fresh.Content), ModTime: fresh.ModTime.Unix(), Size: fresh.Size}); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, "index: "+err.Error())
			return
		}
	}
	r.auditNote(req, c.action, user, rel, tag.Label, fresh.Size)
	r.publishNoteEvent(events.TopicNote, rel, "update", fresh)

	unlock()
	out := validResponse{Note: r.toNoteResponse(fresh)}
	out.Note.Valid = r.validOf(rel, fresh.Content)
	out.MCP = r.mcpOf(rel, fresh.Content)
	if tag.Name != "" && tag.Name != vault.FollowValid {
		restored, err := r.restoreVersionFiles(rel, tag.Name)
		out.Restored = restored
		for _, file := range restored {
			r.auditNote(req, audit.ActionUploadAttachment, user, file, "", 0)
		}
		if err != nil {
			out.RestoreError = err.Error()
		}
	}
	if err := r.deps.GitSync.CommitWrites(); err != nil {
		out.CommitError = err.Error()
	}
	w.Header().Set("ETag", quoteETag(fresh.ETag()))
	WriteJSON(w, http.StatusOK, out)
}
