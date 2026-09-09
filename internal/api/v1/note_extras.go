package v1

import (
	"net/http"
	"strconv"
	"strings"
)

// backlinkView is the JSON shape returned by /notes/{path}/backlinks.
// Mirrors the index.Backlink struct field-for-field so the SPA can
// render a "linked from" panel without a second round-trip.
type backlinkView struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

// readBacklinks lists notes that link to the requested path. Empty
// list returned (200 + items:[]) when the note exists but has no
// inbound links — mirrors the HTMX partial which renders an empty
// state in that case.
func (r *Router) readBacklinks(w http.ResponseWriter, req *http.Request, notePath string) {
	if r.deps.Index == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "index not configured")
		return
	}
	p := principalFromContext(req)
	if !r.canSee(p, notePath) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
		return
	}
	// Reject if the note doesn't exist — backlinks for ghost paths
	// would be confusing UX.
	if _, err := r.deps.Vault.Load(notePath); err != nil {
		writeLoadError(w, err)
		return
	}
	rows, err := r.deps.Index.Backlinks(notePath)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	out := make([]backlinkView, 0, len(rows))
	for _, b := range rows {
		if !r.canSee(p, b.Path) {
			continue // don't reveal inbound links from projects the guest can't see
		}
		out = append(out, backlinkView{Path: b.Path, Title: b.Title})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
}

// excerptView returns the first N lines of a note. Used by the SPA's
// wikilink hover preview composable. `lines` defaults to 5 and is
// capped at 30; clamping prevents pathological "excerpt" requests
// from streaming the whole note bodywards.
type excerptView struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Excerpt string `json:"excerpt"`
	Lines   int    `json:"lines"`
}

func (r *Router) readExcerpt(w http.ResponseWriter, req *http.Request, notePath string) {
	if !r.canSee(principalFromContext(req), notePath) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
		return
	}
	note, err := r.deps.Vault.Load(notePath)
	if err != nil {
		writeLoadError(w, err)
		return
	}
	lines := 5
	if v := strings.TrimSpace(req.URL.Query().Get("lines")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			lines = n
		}
	}
	if lines > 30 {
		lines = 30
	}
	excerpt := firstNLines(string(note.Content), lines)
	WriteJSON(w, http.StatusOK, excerptView{
		Path:    note.Path,
		Title:   note.Title,
		Excerpt: excerpt,
		Lines:   lines,
	})
}

// firstNLines walks `s` and returns the slice up to and including the
// nth newline, or the whole string when fewer lines exist. Frontmatter
// blocks (--- ... ---) are skipped so the preview shows real content,
// not YAML — matching the /api/note-excerpt v1.x partial.
func firstNLines(s string, n int) string {
	body := stripLeadingFrontmatter(s)
	if n <= 0 {
		return ""
	}
	count := 0
	for i := 0; i < len(body); i++ {
		if body[i] == '\n' {
			count++
			if count >= n {
				return body[:i+1]
			}
		}
	}
	return body
}

// stripLeadingFrontmatter removes a leading `---\n...---\n` block so
// the wikilink hover excerpt shows the body, not the YAML metadata.
// Tolerant of CRLF: a stray \r before \n is accepted.
func stripLeadingFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---") {
		return s
	}
	// Skip the opening fence line.
	first := strings.IndexByte(s, '\n')
	if first < 0 {
		return s
	}
	rest := s[first+1:]
	// Find the closing `---` at line start.
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return s
	}
	// Skip past the closing fence's own newline.
	end := idx + len("\n---")
	if end < len(rest) {
		// Allow CR before LF.
		if rest[end] == '\r' && end+1 < len(rest) {
			end++
		}
		if end < len(rest) && rest[end] == '\n' {
			end++
		}
	}
	return strings.TrimLeft(rest[end:], "\n")
}
