package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/gosidian/gosidian/internal/canvas"
	"github.com/gosidian/gosidian/internal/parser"
)

// Obsidian canvases (IMP-144): GET /notes/<x.canvas> serves a canvas as a
// read-only note. Content is the text an agent reads too (canvas.Markdown),
// source the JSON as written, and canvas the cards as the web UI draws
// them: a text card's markdown rendered, a file card resolved to the note
// it opens (with the start of the note) or to the image it shows, each
// within the reader's scope.

// canvasCard is a card as the web UI draws it.
type canvasCard struct {
	canvas.Node
	// HTML is a text card's markdown rendered, or the start of a file
	// card's note (its section, with a subpath).
	HTML string `json:"html,omitempty"`
	// Path is the note a file card opens, when the reader may see it.
	Path string `json:"path,omitempty"`
	// Title is the title of that note.
	Title string `json:"title,omitempty"`
	// Image is the URL of a file card that is an image the reader may see.
	Image string `json:"image,omitempty"`
	// Parent is the group that holds the card.
	Parent string `json:"parent,omitempty"`
}

// canvasData is a canvas for the web UI, or the reason it cannot be drawn.
type canvasData struct {
	Nodes []canvasCard  `json:"nodes"`
	Edges []canvas.Edge `json:"edges"`
	Error string        `json:"error,omitempty"`
}

const (
	// canvasExcerptBytes bounds the start of a note a file card shows.
	canvasExcerptBytes = 2000
	// canvasMaxExcerpts bounds the file cards that show their note.
	canvasMaxExcerpts = 100
)

var canvasImageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".svg": true, ".avif": true, ".bmp": true}

func (r *Router) readCanvas(w http.ResponseWriter, req *http.Request, rel string) {
	c, err := r.deps.Vault.LoadCanvas(rel)
	if err != nil {
		writeReadOnlyLoadError(w, err)
		return
	}
	resp := noteResponse{
		Path:    c.Path,
		Title:   c.Title,
		Format:  "markdown",
		Kind:    "canvas",
		Source:  string(c.Content),
		ETag:    c.ETag(),
		Size:    c.Size,
		ModTime: c.ModTime.UTC().Format(rfc3339Z),
	}
	cv, err := canvas.Parse(c.Content)
	if err != nil {
		resp.Content = "# " + c.Title + "\n\nThis canvas cannot be shown: " + err.Error() + ".\n"
		resp.Canvas = &canvasData{Nodes: []canvasCard{}, Edges: []canvas.Edge{}, Error: err.Error()}
		WriteJSON(w, http.StatusOK, resp)
		return
	}
	pr := previewResolver{r: r, p: principalFromContext(req)}
	resp.Content = string(canvas.Markdown(c.Path, cv, func(file string) string {
		if p := r.canvasNote(pr, file); p != "" {
			return "[[" + strings.TrimSuffix(p, ".md") + "]]"
		}
		return "`" + file + "`"
	}))
	resp.Canvas = r.canvasCards(pr, cv)
	writeCanvas(w, req, resp)
}

// writeCanvas answers with an ETag of the response itself: the cards carry
// titles and excerpts of other notes, resolved for the reader, so the file's
// own ETag let a 304 keep stale excerpts and another reader's view
// (IMP-160, S2-8).
func writeCanvas(w http.ResponseWriter, req *http.Request, resp noteResponse) {
	body, err := json.Marshal(resp)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	sum := sha256.Sum256(body)
	etag := `W/"` + hex.EncodeToString(sum[:12]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if match := req.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(body, '\n'))
}

// canvasOpens reports whether the web UI opens rel in a note window.
func canvasOpens(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".md", ".html", ".base", ".canvas":
		return true
	}
	return false
}

// canvasNote is the vault path of the note (or base, or canvas) a file
// card names, when it exists and the reader may see it; "" otherwise.
func (r *Router) canvasNote(pr previewResolver, file string) string {
	rel, err := r.deps.Vault.Rel(file)
	if err != nil || !r.canSee(pr.p, rel) || !r.deps.Vault.Exists(rel) {
		return ""
	}
	return rel
}

// canvasCards resolves the cards of cv for the reader.
func (r *Router) canvasCards(pr previewResolver, cv *canvas.Canvas) *canvasData {
	out := &canvasData{Nodes: make([]canvasCard, 0, len(cv.Nodes)), Edges: cv.Edges}
	if out.Edges == nil {
		out.Edges = []canvas.Edge{}
	}
	parents := cv.Parents()
	excerpts := 0
	for _, n := range cv.Nodes {
		card := canvasCard{Node: n, Parent: parents[n.ID]}
		switch n.Type {
		case canvas.TypeText:
			card.HTML = r.renderCard(pr, []byte(n.Text))
		case canvas.TypeFile:
			rel := r.canvasNote(pr, n.File)
			if rel == "" {
				break
			}
			// /vault-files/ serves attachments only, and /notes/ notes, bases
			// and canvases only: an image elsewhere was a 404, any other file
			// a 400 (IMP-160, S2-12). Those cards show their name.
			if canvasImageExts[strings.ToLower(path.Ext(rel))] {
				if strings.Contains("/"+rel, "/attachments/") {
					card.Image = "/vault-files/" + rel
				}
				break
			}
			if !canvasOpens(rel) {
				break
			}
			card.Path = rel
			if note, err := r.deps.Vault.Load(rel); err == nil {
				card.Title = note.Title
				if r.deps.Index != nil {
					if n, err := r.deps.Index.Note(rel); err == nil && n != nil && n.Title != "" {
						card.Title = n.Title
					}
				}
				if excerpts < canvasMaxExcerpts {
					excerpts++
					card.HTML = r.renderCard(pr, canvasExcerpt(note.Content, n.Subpath, card.Title))
				}
			} else {
				// A base or a canvas: it opens, with nothing to show inline.
				card.Title = strings.TrimSuffix(path.Base(rel), path.Ext(rel))
			}
		}
		out.Nodes = append(out.Nodes, card)
	}
	return out
}

// renderCard renders markdown for a card, "" when the renderer is not
// configured or fails.
func (r *Router) renderCard(pr previewResolver, md []byte) string {
	if r.deps.Renderer == nil || len(strings.TrimSpace(string(md))) == 0 {
		return ""
	}
	html, err := r.deps.Renderer.Render(md, pr)
	if err != nil {
		return ""
	}
	return html
}

// canvasExcerpt is what a file card shows of its note: the section its
// subpath names (#Heading), else the body without a first heading that
// repeats the title the card already shows, cut at a line near
// canvasExcerptBytes.
func canvasExcerpt(content []byte, subpath, title string) []byte {
	body := parser.BodyAfterFrontmatter(content)
	if h := strings.TrimPrefix(subpath, "#"); h != "" && !strings.HasPrefix(h, "^") {
		if sec := parser.ExtractSection(content, h); sec != "" {
			body = sec
		}
	} else {
		trimmed := strings.TrimLeft(body, " \t\r\n")
		first, rest, _ := strings.Cut(trimmed, "\n")
		if h1, ok := strings.CutPrefix(strings.TrimSpace(first), "# "); ok && strings.EqualFold(strings.TrimSpace(h1), strings.TrimSpace(title)) {
			body = rest
		}
	}
	if len(body) > canvasExcerptBytes {
		cut := strings.LastIndexByte(body[:canvasExcerptBytes], '\n')
		if cut <= 0 {
			cut = canvasExcerptBytes
			for cut > 0 && !utf8.RuneStart(body[cut]) {
				cut--
			}
		}
		body = body[:cut] + "\n\n…"
	}
	return []byte(body)
}
