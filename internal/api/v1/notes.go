package v1

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/vault"
)

// noteSummary is the lightweight projection returned by list endpoints.
// Full note bodies belong in the single-note GET response so list calls
// stay cheap.
type noteSummary struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

// noteResponse is the shape returned by GET /notes/{path...}. ETag is
// duplicated as a JSON field for clients that don't (or can't) read
// response headers — but the canonical wire location remains the
// `ETag` HTTP header so caching middlewares behave naturally.
type noteResponse struct {
	Path    string          `json:"path"`
	Title   string          `json:"title"`
	Content string          `json:"content"`
	Format  string          `json:"format"`          // "markdown" | "html" — drives the SPA's renderer choice
	Kind    string          `json:"kind,omitempty"`  // "image" for a resolved media note (ADR-013); empty otherwise
	Media   *vault.MediaRef `json:"media,omitempty"` // resolved image payload when Kind=="image"
	ETag    string          `json:"etag"`
	Size    int64           `json:"size"`
	ModTime string          `json:"mod_time"`
}

type createNoteRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type updateNoteRequest struct {
	Content string `json:"content"`
}

// handleNotes dispatches GET (list) and POST (create) on /api/v1/notes.
// The single-note operations live on /api/v1/notes/<path...> and reach
// handleNoteByPath instead.
func (r *Router) handleNotes(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		r.listNotes(w, req)
	case http.MethodPost:
		r.createNote(w, req)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

// handleNoteByPath routes per-path operations: GET, PUT, DELETE plus
// the read-only subroutes /backlinks and /excerpt. The path is
// everything after `/api/v1/notes/` — empty falls back to the
// list/create endpoint above so a misconfigured client doesn't 404 in
// a confusing way.
func (r *Router) handleNoteByPath(w http.ResponseWriter, req *http.Request) {
	rel := strings.TrimPrefix(req.URL.Path, "/api/v1/notes/")
	if rel == "" || rel == "/" {
		r.handleNotes(w, req)
		return
	}
	// Subroute detection: the raw rel may end with /backlinks,
	// /excerpt, /history. Strip + dispatch before path validation so
	// the handler sees only the note path proper.
	for _, sub := range noteSubroutes {
		if suffix := "/" + sub; strings.HasSuffix(rel, suffix) {
			notePath := strings.TrimSuffix(rel, suffix)
			clean, err := r.deps.Vault.Rel(notePath)
			if err != nil {
				WriteError(w, http.StatusBadRequest, CodeValidationFormat, "invalid path: "+err.Error())
				return
			}
			r.dispatchNoteSubroute(w, req, clean, sub)
			return
		}
	}
	clean, err := r.deps.Vault.Rel(rel)
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "invalid path: "+err.Error())
		return
	}
	switch req.Method {
	case http.MethodGet:
		r.readNote(w, req, clean)
	case http.MethodPut:
		r.updateNote(w, req, clean)
	case http.MethodDelete:
		r.deleteNote(w, req, clean)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

// noteSubroutes lists the read-only suffixes routed off /notes/{path}/.
// Order matters only for documentation; the loop matches by suffix
// regardless. New entries get a handler in dispatchNoteSubroute below.
var noteSubroutes = []string{"backlinks", "excerpt", "history"}

func (r *Router) dispatchNoteSubroute(w http.ResponseWriter, req *http.Request, notePath, sub string) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	switch sub {
	case "backlinks":
		r.readBacklinks(w, req, notePath)
	case "excerpt":
		r.readExcerpt(w, req, notePath)
	case "history":
		r.readHistory(w, req, notePath)
	default:
		WriteError(w, http.StatusNotFound, CodeNotFound, "unknown note subroute")
	}
}

// listNotes returns a paginated note summary slice. Filters are
// composable: `?project=foo&prefix=foo/sub&tag=type:plan&limit=50&offset=0`.
// Project narrows by top-level directory, prefix narrows further within
// that, tag intersects via the index. limit defaults to 100 and is
// capped at 500 so a misconfigured caller can't OOM the server.
func (r *Router) listNotes(w http.ResponseWriter, req *http.Request) {
	if r.deps.Index == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "index not configured")
		return
	}
	q := req.URL.Query()
	project := strings.TrimSpace(q.Get("project"))
	prefix := strings.TrimSpace(q.Get("prefix"))
	tag := strings.TrimSpace(q.Get("tag"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}

	var rows []index.NoteRow
	var err error
	switch {
	case tag != "":
		rows, err = r.deps.Index.NotesByTag(tag)
	case project != "":
		rows, err = r.deps.Index.NotesByPrefix(project)
	case prefix != "":
		rows, err = r.deps.Index.NotesByPrefix(prefix)
	default:
		rows, err = r.deps.Index.AllNotes()
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "list failed: "+err.Error())
		return
	}

	// Per-role visibility: drop notes the principal can't see (guests →
	// public-only) before paginating, so total/offset reflect the visible set.
	if p := principalFromContext(req); !r.seesAllProjects(p) {
		visible := rows[:0]
		for _, n := range rows {
			if r.canSee(p, n.Path) {
				visible = append(visible, n)
			}
		}
		rows = visible
	}

	// Manual pagination — the index helpers don't yet expose
	// limit/offset natively. Cheap given list endpoints are <100ms
	// even with full vault scan.
	total := len(rows)
	if offset >= total {
		rows = nil
	} else {
		end := offset + limit
		if end > total {
			end = total
		}
		rows = rows[offset:end]
	}

	out := make([]noteSummary, 0, len(rows))
	for _, n := range rows {
		out = append(out, noteSummary{Path: n.Path, Title: n.Title})
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items":  out,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// createNote handles POST /notes. The body carries path + content; we
// reject if the path already exists so clients don't accidentally
// overwrite via the wrong verb (PUT is the explicit upsert).
func (r *Router) createNote(w http.ResponseWriter, req *http.Request) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if denyGuestWrite(w, user) {
		return
	}
	var body createNoteRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if body.Path == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "path required")
		return
	}
	clean, err := r.deps.Vault.Rel(body.Path)
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "invalid path: "+err.Error())
		return
	}
	if !r.deps.Vault.IsNoteFile(clean) {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "path must be a note file (.md, or .html when html notes are enabled)")
		return
	}
	if r.denyWriteProject(w, user.principal(), projectOf(clean)) {
		return
	}
	unlock := r.deps.Vault.LockPath(clean)
	defer unlock()
	if _, err := r.deps.Vault.Load(clean); err == nil {
		WriteError(w, http.StatusConflict, CodeConflict, "note already exists")
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "load probe: "+err.Error())
		return
	}

	if err := r.writeAndIndex(clean, []byte(body.Content)); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "write: "+err.Error())
		return
	}
	note, _ := r.deps.Vault.Load(clean)
	r.auditNote(req, audit.ActionCreate, user, clean, "", int64(len(body.Content)))
	r.publishNoteEvent(events.TopicNote, clean, "create", note)
	r.publishNoteEvent(events.TopicTree, clean, "create", note)

	w.Header().Set("ETag", quoteETag(note.ETag()))
	WriteJSON(w, http.StatusCreated, r.toNoteResponse(note))
}

// readNote handles GET /notes/{path...}. The ETag header carries the
// version stamp clients pass back as If-Match on PUT.
func (r *Router) readNote(w http.ResponseWriter, req *http.Request, rel string) {
	if !r.canSee(principalFromContext(req), rel) {
		// 404 (not 403) so we don't leak the existence of projects/notes a
		// guest isn't allowed to see.
		WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
		return
	}
	note, err := r.deps.Vault.Load(rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
			return
		}
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "load: "+err.Error())
		return
	}
	// ?inline returns the content with image references inlined as data: URIs,
	// for a self-contained download. The stored note is untouched; this is the
	// only place the bytes are embedded into the markup. No 304 fast-path here:
	// the inlined body differs from the bare note the etag stamps.
	if req.URL.Query().Has("inline") {
		resp := r.toNoteResponse(note)
		resp.Content = r.inlineImages(resp.Content, resp.Format, principalFromContext(req))
		WriteJSON(w, http.StatusOK, resp)
		return
	}

	etag := quoteETag(note.ETag())
	w.Header().Set("ETag", etag)
	// 304 fast path for browser-style conditional requests.
	if match := req.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	WriteJSON(w, http.StatusOK, r.toNoteResponse(note))
}

// updateNote handles PUT /notes/{path...} with optimistic locking via
// If-Match. Mismatch returns 412 with the current etag in the details
// payload so the SPA can render a ConflictDialog without a follow-up
// GET round-trip.
func (r *Router) updateNote(w http.ResponseWriter, req *http.Request, rel string) {
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
	// ADR-021: the notes endpoints only touch note files (POST already did).
	if !r.deps.Vault.IsNoteFile(rel) {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "path must be a note file (.md, or .html when html notes are enabled)")
		return
	}
	unlock := r.deps.Vault.LockPath(rel)
	defer unlock()
	existing, err := r.deps.Vault.Load(rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
			return
		}
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "load: "+err.Error())
		return
	}
	if ifMatch := strings.TrimSpace(req.Header.Get("If-Match")); ifMatch != "" {
		if !etagMatches(ifMatch, existing.ETag()) {
			WriteErrorWithDetails(
				w, http.StatusPreconditionFailed,
				CodeConcurrencyEtag,
				"note was modified since the last read",
				map[string]any{
					"current_etag":            quoteETag(existing.ETag()),
					"current_size":            existing.Size,
					"current_content_excerpt": excerptForConflict(existing.Content),
				},
			)
			return
		}
	}
	var body updateNoteRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}

	if err := r.writeAndIndex(rel, []byte(body.Content)); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "write: "+err.Error())
		return
	}
	fresh, _ := r.deps.Vault.Load(rel)
	r.auditNote(req, audit.ActionUpdate, user, rel, "", int64(len(body.Content)))
	r.publishNoteEvent(events.TopicNote, rel, "update", fresh)

	w.Header().Set("ETag", quoteETag(fresh.ETag()))
	WriteJSON(w, http.StatusOK, r.toNoteResponse(fresh))
}

// deleteNote routes through trash when configured (soft delete) and
// falls back to hard delete via vault.Delete otherwise. Both branches
// always cleanup the index.
func (r *Router) deleteNote(w http.ResponseWriter, req *http.Request, rel string) {
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
	// ADR-021: the notes endpoints only touch note files (POST already did).
	if !r.deps.Vault.IsNoteFile(rel) {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "path must be a note file (.md, or .html when html notes are enabled)")
		return
	}
	unlock := r.deps.Vault.LockPath(rel)
	defer unlock()
	if _, err := r.deps.Vault.Load(rel); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
			return
		}
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "load: "+err.Error())
		return
	}
	if r.deps.Trash != nil {
		if _, err := r.deps.Trash.DiscardNote(rel); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, "trash: "+err.Error())
			return
		}
	} else {
		if err := r.deps.Vault.Delete(rel); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, "delete: "+err.Error())
			return
		}
	}
	if r.deps.Index != nil {
		_ = r.deps.Index.Delete(rel)
	}
	r.auditNote(req, audit.ActionDelete, user, rel, "", 0)
	r.publishNoteEvent(events.TopicNote, rel, "delete", nil)
	r.publishNoteEvent(events.TopicTree, rel, "delete", nil)
	w.WriteHeader(http.StatusNoContent)
}

// writeAndIndex mirrors the MCP helper of the same name in
// internal/mcp/tools.go: persist the content first, then upsert the
// index entry from the freshly loaded note. Keeping the two paths
// behaviourally identical means an MCP write and an SPA write produce
// the same ETag and the same indexed shape.
func (r *Router) writeAndIndex(rel string, content []byte) error {
	if err := r.deps.Vault.Save(rel, content); err != nil {
		return err
	}
	if r.deps.Index == nil {
		return nil
	}
	note, err := r.deps.Vault.Load(rel)
	if err != nil {
		return err
	}
	return r.deps.Index.Upsert(index.NoteDoc{
		Path:    note.Path,
		Title:   note.Title,
		Body:    string(note.Content),
		ModTime: note.ModTime.Unix(),
		Size:    note.Size,
	})
}

// auditNote shapes an audit entry the same way the HTML and MCP paths
// do, so SOC dashboards can grep across all three audiences.
func (r *Router) auditNote(req *http.Request, action audit.Action, user *RequestUser, path, to string, size int64) {
	if r.deps.Audit == nil {
		return
	}
	_ = r.deps.Audit.Write(audit.Entry{
		Source: audit.SourceHTTP,
		Actor:  user.Username,
		UserID: user.ID,
		Action: action,
		Path:   path,
		To:     to,
		Size:   size,
	})
}

// publishNoteEvent fires an SSE event so other tabs/clients can
// invalidate their caches. Best-effort: a nil hub or a closed
// channel is silently dropped. fresh may be nil for delete events
// (no content remains to advertise an etag for).
func (r *Router) publishNoteEvent(topic events.Topic, path, action string, fresh *vault.Note) {
	if r.deps.Events == nil {
		return
	}
	payload := map[string]any{
		"action": action,
		"path":   path,
	}
	if fresh != nil {
		payload["etag"] = fresh.ETag()
	}
	r.deps.Events.Publish(topic, payload)
}

// toNoteResponse converts a vault.Note to the wire shape. Sharing it
// across create/read/update keeps the JSON tag list in one place. It is a
// Router method (not a free function) so it can resolve the image overlay for
// media notes via the vault — populated only when the media_notes feature is on
// and the note declares type:image (ADR-013), otherwise Kind/Media stay empty.
func (r *Router) toNoteResponse(n *vault.Note) noteResponse {
	resp := noteResponse{
		Path:    n.Path,
		Title:   n.Title,
		Content: string(n.Content),
		Format:  noteFormat(n.Path),
		ETag:    n.ETag(),
		Size:    n.Size,
		ModTime: n.ModTime.UTC().Format(rfc3339Z),
	}
	if ref, kind := r.deps.Vault.MediaRefForNote(n.Path, n.Content); kind != "" {
		resp.Kind = kind
		resp.Media = ref
	}
	return resp
}

// noteFormat classifies a note by extension so the SPA knows whether to render
// it through the markdown pipeline or the sandboxed HTML iframe.
func noteFormat(path string) string {
	if strings.HasSuffix(strings.ToLower(path), ".html") {
		return "html"
	}
	return "markdown"
}

// quoteETag wraps the raw stamp in RFC 7232 strong-validator quotes.
// Both the response header and the request header (If-Match,
// If-None-Match) use this form so byte-for-byte comparison is enough.
func quoteETag(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "\"") {
		return raw
	}
	return "\"" + raw + "\""
}

// etagMatches normalises around the optional surrounding quotes so a
// client that strips them (or a caching proxy that doesn't) still
// matches.
func etagMatches(provided, current string) bool {
	provided = strings.TrimSpace(provided)
	provided = strings.Trim(provided, "\"")
	if provided == "*" {
		// Wildcard If-Match: succeed as long as the resource exists.
		return true
	}
	current = strings.Trim(current, "\"")
	return provided == current
}

// excerptForConflict returns the first ~200 bytes of the current
// content, byte-truncated, so the SPA's ConflictDialog can show a
// peek without forcing another GET. Not for diffing — the SPA fires
// a full GET when the user picks "manual merge".
func excerptForConflict(b []byte) string {
	const max = 200
	if len(b) <= max {
		return string(b)
	}
	// Trim back to the previous newline to avoid splitting a UTF-8
	// rune in the middle.
	cut := max
	for cut > 0 && b[cut-1] != '\n' {
		cut--
	}
	if cut == 0 {
		cut = max
	}
	return string(b[:cut]) + "…"
}
