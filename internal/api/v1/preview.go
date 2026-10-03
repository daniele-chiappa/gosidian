package v1

import (
	"net/http"
	"time"

	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/gosidian/gosidian/internal/views"
)

// withNoteExts lists target with each recognised note extension appended.
func withNoteExts(target string) []string {
	exts := vault.NoteExtensions()
	out := make([]string, 0, len(exts))
	for _, e := range exts {
		out = append(out, target+e)
	}
	return out
}

// pathWithNoteExt reports whether path equals target plus one of the
// recognised note extensions (extension-less wikilink form).
func pathWithNoteExt(path, target string) bool {
	for _, e := range vault.NoteExtensions() {
		if path == target+e {
			return true
		}
	}
	return false
}

// previewRequest carries raw markdown from the editor split-pane.
type previewRequest struct {
	Markdown string `json:"markdown"`
	// Path is the note being previewed, when there is one: its views
	// resolve this.<field> against it (IMP-127).
	Path string `json:"path,omitempty"`
}

// previewResponse returns sanitized HTML the SPA can drop into a
// preview pane via DOMPurify.sanitize. Server-side goldmark is
// already configured safe-by-default, but the SPA passes it through
// DOMPurify for defense in depth.
type previewResponse struct {
	HTML string `json:"html"`
}

// handlePreview renders markdown to HTML through the same parser
// stack used by the v1.x HTML handlers and the MCP `memory_get`
// rendered-body output. Wikilinks are resolved against the live
// index so the preview shows resolved links and dangling-link
// indicators identically to the read view.
func (r *Router) handlePreview(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.deps.Renderer == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "renderer not configured")
		return
	}
	var body previewRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}

	p := principalFromContext(req)
	md := []byte(body.Markdown)
	if r.deps.Index != nil {
		// ```view blocks become their computed result before rendering
		// (IMP-127), queried with the reader's own scope.
		c := views.Context{
			This:  views.ThisFields(body.Path, parser.ParseFrontmatterFields(parser.FrontmatterRawForPath(body.Path, md))),
			Today: time.Now(),
		}
		md, _ = views.RenderNote(md, false, c, r.viewQuery(p))
	}
	html, err := r.deps.Renderer.Render(md, previewResolver{r: r, p: p})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "render: "+err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, previewResponse{HTML: html})
}

// previewResolver resolves both `[[wiki-links]]` (Resolve) and `![[image]]`
// embeds (ResolveImage) for the preview renderer, consulting the live index and
// vault. It implements parser.Resolver and parser.ImageResolver.
type previewResolver struct {
	r *Router
	p authz.Principal
}

// Resolve translates `[[Note Title]]` into a vault path the principal may see,
// or "" for unknown targets (rendered as a dangling link).
func (pr previewResolver) Resolve(target string) string {
	r := pr.r
	if r.deps.Index == nil {
		return ""
	}
	// Try by exact path first (allows wikilinks like [[folder/Note]]). The
	// extension-less form is tried against every note extension, not just .md,
	// so links to .html notes resolve like they do in the index (BUG-024).
	// NotesByPrefix below only finds notes inside a folder named target, so
	// the note itself is looked up by path here (BUG-086).
	for _, cand := range append([]string{target}, withNoteExts(target)...) {
		if n, err := r.deps.Index.Note(cand); err == nil && n != nil && r.canSee(pr.p, n.Path) {
			return n.Path
		}
	}
	if rows, err := r.deps.Index.NotesByPrefix(target); err == nil {
		for _, n := range rows {
			if (n.Path == target || pathWithNoteExt(n.Path, target) || n.Title == target) && r.canSee(pr.p, n.Path) {
				return n.Path
			}
		}
	}
	// Fall back to a title scan via search — bounded result set. Resolve only to
	// notes the principal may see, so a guest's preview of a public note renders
	// private targets as dangling, not as live links.
	if rows, err := r.deps.Index.Search(target, 5); err == nil {
		for _, h := range rows {
			if h.Title == target && r.canSee(pr.p, h.Path) {
				return h.Path
			}
		}
	}
	return ""
}

// ResolveImage maps an `![[target]]` embed to an image URL: first an attachment
// by name/path, then a media note (type:image) referenced by name → its image.
// The actual byte access stays gated by the /vault-files handler.
func (pr previewResolver) ResolveImage(target string) string {
	r := pr.r
	if r.deps.Vault == nil {
		return ""
	}
	if rel, ok := r.deps.Vault.ResolveAttachmentByName(target); ok {
		return "/vault-files/" + rel
	}
	if path := pr.Resolve(target); path != "" {
		if note, err := r.deps.Vault.Load(path); err == nil {
			if ref, kind := r.deps.Vault.MediaRefForNote(note.Path, note.Content); kind == "image" && !ref.Broken {
				return ref.URL
			}
		}
	}
	return ""
}

// viewQuery runs the query of a view with the reader's scope, as
// POST /query does: a view never shows a note its reader cannot see.
func (r *Router) viewQuery(p authz.Principal) views.QueryFunc {
	return func(o index.QueryOptions) ([]index.QueryHit, int, error) {
		scope, err := r.searchScope(p, "")
		if err != nil {
			return nil, 0, err
		}
		o.Projects = scope
		hits, total, err := r.deps.Index.Query(o)
		if err != nil {
			return nil, 0, err
		}
		out := hits[:0]
		for _, h := range hits {
			if !r.canSee(p, h.Path) {
				total--
				continue
			}
			out = append(out, h)
		}
		return out, total, nil
	}
}
