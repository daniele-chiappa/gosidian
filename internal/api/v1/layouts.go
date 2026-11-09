package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/frontmatter"
	"github.com/gosidian/gosidian/internal/gitsync"
	"github.com/gosidian/gosidian/internal/layout"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/vault"
)

// The page layouts of the notes (IMP-226). A note has a design when its
// frontmatter names one in layout_file: an attachment, so it stays out of
// the tree and out of the note's text, which is a normal markdown. GET
// /api/v1/layouts/{path} puts the design back together into the package
// designcraft's editor opens; PUT takes the package it saves apart into
// that document and the attachments each asset links, relative to it so
// that renaming the project leaves them right; POST
// /api/v1/layouts/{path}/export keeps a PDF an app wants beside the note.
// The window of the plugin's app calls them with the session of whoever is
// logged in.

// missingHeader names the assets whose link the reader may not follow,
// or that is gone: they open without their bytes.
const missingHeader = "Gosidian-Layout-Missing"

// pageHeader and templateHeader say, for a note without a design, the page
// and the layout note its design starts from (IMP-232), path-escaped: a
// header carries no accents.
const (
	pageHeader     = "Gosidian-Layout-Page"
	templateHeader = "Gosidian-Layout-Template"
)

func (r *Router) handleLayouts(w http.ResponseWriter, req *http.Request) {
	rel, action := strings.TrimPrefix(req.URL.Path, "/api/v1/layouts/"), ""
	for _, a := range []string{"/export", "/pieces/number", "/pieces/edit", "/pieces/reorder", "/pieces"} {
		if cut, ok := strings.CutSuffix(rel, a); ok {
			rel, action = cut, a
			break
		}
	}
	clean, err := r.deps.Vault.Rel(rel)
	if err != nil || rel == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "invalid path")
		return
	}
	switch {
	case action == "/export" && req.Method == http.MethodPost:
		r.exportLayout(w, req, clean)
	case action == "/pieces" && req.Method == http.MethodGet:
		r.layoutPieces(w, req, clean)
	case action == "/pieces" && req.Method == http.MethodPost:
		r.addPiece(w, req, clean)
	case action == "/pieces/number" && req.Method == http.MethodPost:
		r.numberPieces(w, req, clean)
	case action == "/pieces/edit" && req.Method == http.MethodPost:
		r.editPiece(w, req, clean)
	case action == "/pieces/reorder" && req.Method == http.MethodPost:
		r.reorderPieces(w, req, clean)
	case action == "" && req.Method == http.MethodGet:
		r.openLayout(w, req, clean)
	case action == "" && req.Method == http.MethodPut:
		r.saveLayout(w, req, clean)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) layoutLimits() layout.Limits {
	return layout.Limits{MaxBytes: r.web.LayoutMaxBytes, MaxDocument: r.web.LayoutMaxDocumentBytes, MaxEntries: r.web.LayoutMaxEntries}
}

// loadLayout is the markdown note at rel; false, answered, when it is none.
func (r *Router) loadLayout(w http.ResponseWriter, rel string) (*vault.Note, bool) {
	if path.Ext(rel) != ".md" {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "a layout is a markdown note")
		return nil, false
	}
	note, err := r.deps.Vault.Load(rel)
	if err != nil {
		writeLoadError(w, err)
		return nil, false
	}
	return note, true
}

// openLayout answers GET: the package of the note, each asset with the
// bytes of its link when the reader may read it; 204 for a note that has
// no design yet, which the editor starts anew. A design that does not read
// is an error, not a new one: the editor would save over it.
func (r *Router) openLayout(w http.ResponseWriter, req *http.Request, rel string) {
	p := principalFromContext(req)
	if !r.canSee(p, rel) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
		return
	}
	note, ok := r.loadLayout(w, rel)
	if !ok {
		return
	}
	// ?version= opens the design a version of the note holds, to read: its
	// files come from git when the GC took them (IMP-201).
	var at *gitsync.Version
	content := note.Content
	if want := req.URL.Query().Get("version"); want != "" {
		var err error
		if at, err = r.noteVersion(p, note, r.validOf(rel, note.Content), want); err != nil {
			writeGitError(w, err)
			return
		}
		if at != nil {
			content = at.Content
		}
	}
	w.Header().Set("ETag", quoteETag(layout.ETag(layout.DesignFile(content))))
	w.Header().Set("Cache-Control", "no-store")
	opened, err := layout.Open(content, r.layoutLimits(), func(link string, max int64) ([]byte, string, bool) {
		data, ext, ok := r.readLayoutLink(p, link, max)
		if ok || at == nil {
			return data, ext, ok
		}
		// The version's own files, where it sat, or what the reader may read.
		clean, err := r.deps.Vault.Rel(link)
		if err != nil || (!r.canSee(p, clean) && projectOf(clean) != projectOf(at.Path)) {
			return nil, "", false
		}
		if data, err = r.deps.GitSync.FileAt(at.SHA, clean, max); err != nil {
			return nil, "", false
		}
		return data, strings.ToLower(strings.TrimPrefix(path.Ext(clean), ".")), true
	})
	var unreadable *layout.Unreadable
	switch {
	case errors.As(err, &unreadable):
		WriteError(w, http.StatusUnprocessableEntity, CodeValidationFormat, unreadable.Error())
		return
	case err != nil:
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	case opened.Package == nil:
		page, template := layout.Start(content)
		if page != "" {
			w.Header().Set(pageHeader, url.PathEscape(page))
		}
		if template != "" {
			w.Header().Set(templateHeader, url.PathEscape(template))
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(opened.Missing) > 0 {
		w.Header().Set(missingHeader, strings.Join(opened.Missing, ","))
	}
	w.Header().Set("Content-Type", layout.MIME)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(opened.Package)
}

// readLayoutLink reads the attachment an asset links, at most max bytes,
// when the reader may read it: its bytes and its extension.
func (r *Router) readLayoutLink(p authz.Principal, link string, max int64) ([]byte, string, bool) {
	clean, err := r.deps.Vault.Rel(link)
	if err != nil || !r.canSee(p, clean) {
		return nil, "", false
	}
	data, err := r.deps.Vault.ReadAttachment(clean, max)
	if err != nil {
		return nil, "", false
	}
	return data, strings.ToLower(strings.TrimPrefix(path.Ext(clean), ".")), true
}

// saveLayout answers PUT: the package the editor saved, taken apart. Every
// file is checked before anything is written (layout.Take, layout.Store).
func (r *Router) saveLayout(w http.ResponseWriter, req *http.Request, rel string) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if denyGuestWrite(w, user) || r.denyWriteProject(w, user.principal(), projectOf(rel)) {
		return
	}
	lim := r.layoutLimits()
	raw, ok := readLimited(w, req, lim.MaxBytes)
	if !ok {
		return
	}
	taken, problem := layout.Take(raw, lim, attach.Check)
	if problem != nil {
		writeLayoutProblem(w, problem)
		return
	}

	unlock := r.deps.Vault.LockPath(rel)
	defer unlock()
	note, ok := r.loadLayout(w, rel)
	if !ok {
		return
	}
	current := layout.ETag(layout.DesignFile(note.Content))
	if ifMatch := strings.TrimSpace(req.Header.Get("If-Match")); ifMatch != "" && !etagMatches(ifMatch, current) {
		WriteErrorWithDetails(w, http.StatusPreconditionFailed, CodeConcurrencyEtag, "the design was saved since it was opened",
			map[string]any{"current_etag": quoteETag(current)})
		return
	}
	project := projectOf(rel)
	content, out, problem := taken.Store(rel, note.Content, func(data []byte, name string) (string, error) {
		res, err := r.storeAttachment(req, data, name, project)
		if err != nil {
			return "", err
		}
		return res.Path, nil
	})
	if problem != nil {
		writeLayoutProblem(w, problem)
		return
	}
	r.writeLayoutNote(w, req, user, rel, content, func(string) any {
		w.Header().Set("ETag", quoteETag(out.ETag))
		return out
	})
}

// writeLayoutProblem answers a package refused: 400 for one wrong as a
// package, 422 for a file in it the vault does not take.
func writeLayoutProblem(w http.ResponseWriter, p *layout.Problem) {
	status := http.StatusUnprocessableEntity
	if p.Malformed {
		status = http.StatusBadRequest
	}
	if p.Asset != nil {
		WriteErrorWithDetails(w, status, CodeValidationFormat, p.Msg, map[string]any{"asset": *p.Asset})
		return
	}
	WriteError(w, status, CodeValidationFormat, p.Msg)
}

// exportLayout answers POST …/export?name=<file>.pdf: a PDF an app wants
// kept, as an attachment the note names in layout_pdfs. With &version=<tag>
// it is that version's, by its label, beside the others; without, the
// draft's, which takes the place of the one before (IMP-229).
func (r *Router) exportLayout(w http.ResponseWriter, req *http.Request, rel string) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if denyGuestWrite(w, user) || r.denyWriteProject(w, user.principal(), projectOf(rel)) {
		return
	}
	name := req.URL.Query().Get("name")
	if name == "" || strings.ToLower(path.Ext(name)) != ".pdf" {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "name: the exported file's name, a .pdf")
		return
	}
	data, ok := readLimited(w, req, attach.MaxBytes())
	if !ok {
		return
	}
	if err := attach.Check(data, ".pdf"); err != nil {
		WriteError(w, http.StatusUnprocessableEntity, CodeValidationFormat, name+": "+err.Error())
		return
	}
	unlock := r.deps.Vault.LockPath(rel)
	defer unlock()
	note, ok := r.loadLayout(w, rel)
	if !ok {
		return
	}
	label := layout.DraftPDF
	if tag := req.URL.Query().Get("version"); tag != "" {
		if tag == "valid" {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "version: a tag of the note")
			return
		}
		v, err := r.noteVersion(user.principal(), note, nil, tag)
		if err != nil {
			writeGitError(w, err)
			return
		}
		if v == nil {
			WriteError(w, http.StatusNotFound, CodeNotFound, "no such version of the note")
			return
		}
		label = tag[strings.LastIndexByte(tag, '/')+1:]
	}
	res, err := r.storeAttachment(req, data, name, projectOf(rel))
	if err != nil {
		WriteError(w, http.StatusUnprocessableEntity, CodeValidationFormat, name+": "+err.Error())
		return
	}
	pdfs := layout.WithPDF(layout.PDFs(note.Content), label, res.Path)
	content, err := frontmatter.SetKeys(note.Content, []frontmatter.Field{{Key: "layout_pdfs", Value: pdfs}}, []string{"layout_pdf"})
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "frontmatter: "+err.Error()+": edit the note as text")
		return
	}
	r.writeLayoutNote(w, req, user, rel, content, func(etag string) any {
		return map[string]string{"path": res.Path, "label": label, "note": rel, "etag": etag}
	})
}

// layoutPieces answers GET …/pieces: the pieces of the note's text, in its
// order, with the note's etag; with ?version=, those of that version, for
// the design it opens.
func (r *Router) layoutPieces(w http.ResponseWriter, req *http.Request, rel string) {
	p := principalFromContext(req)
	if !r.canSee(p, rel) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
		return
	}
	note, ok := r.loadLayout(w, rel)
	if !ok {
		return
	}
	content := note.Content
	if want := req.URL.Query().Get("version"); want != "" {
		at, err := r.noteVersion(p, note, r.validOf(rel, note.Content), want)
		if err != nil {
			writeGitError(w, err)
			return
		}
		if at != nil {
			content = at.Content
		}
	}
	w.Header().Set("ETag", quoteETag(note.ETag()))
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, http.StatusOK, r.piecesBody(req, rel, note.ETag(), content))
}

// piecesBody is the answer about a note's pieces, each image with the
// attachment it names when the reader may read it.
func (r *Router) piecesBody(req *http.Request, rel, etag string, content []byte) map[string]any {
	pieces := layout.Pieces(content)
	if pieces == nil {
		pieces = []layout.Piece{}
	}
	p := principalFromContext(req)
	var described map[string]string
	for i := range pieces {
		img := pieces[i].Image
		if img == nil {
			continue
		}
		if described == nil {
			described, _ = pipeline.MediaDescriptions(apiNotes{r: r, p: p}, projectOf(rel), rel)
		}
		img.Path = r.pieceImagePath(p, rel, img.Src)
		img.Described = described[img.Path]
	}
	return map[string]any{"path": rel, "etag": etag, "pieces": pieces}
}

// pieceImagePath is the attachment an image of the note at rel names, "" when
// it names none the reader may read.
func (r *Router) pieceImagePath(p authz.Principal, rel, src string) string {
	found := layout.ImageTarget(rel, src, func(name string) string {
		f, _ := r.deps.Vault.ResolveAttachment(name, projectOf(rel), func(project string) bool { return p.CanAccessProject(project, r.accessConfig()) })
		return f
	})
	clean, err := r.deps.Vault.Rel(found)
	if err != nil || found == "" || !r.canSee(p, clean) || !strings.Contains("/"+clean, "/attachments/") || !r.deps.Vault.Exists(clean) {
		return ""
	}
	return clean
}

// numberPieces answers POST …/pieces/number: a comment before the blocks
// that have none, every one, or with {"n": 3} those of that piece, each a
// piece of its own. The answer is the pieces as they are now.
func (r *Router) numberPieces(w http.ResponseWriter, req *http.Request, rel string) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if denyGuestWrite(w, user) || r.denyWriteProject(w, user.principal(), projectOf(rel)) {
		return
	}
	var in struct {
		N *int `json:"n"`
	}
	raw, ok := readLimited(w, req, 1<<10)
	if !ok {
		return
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil || in.N != nil && *in.N < 0 {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "the body is {} or {\"n\": <a piece>}")
			return
		}
	}
	unlock := r.deps.Vault.LockPath(rel)
	defer unlock()
	note, ok := r.loadLayout(w, rel)
	if !ok {
		return
	}
	if ifMatch := strings.TrimSpace(req.Header.Get("If-Match")); ifMatch != "" && !etagMatches(ifMatch, note.ETag()) {
		WriteErrorWithDetails(w, http.StatusPreconditionFailed, CodeConcurrencyEtag, "note was modified since the last read",
			map[string]any{"current_etag": quoteETag(note.ETag())})
		return
	}
	which := layout.All
	if in.N != nil {
		which = *in.N
	}
	content := layout.Number(note.Content, which)
	if bytes.Equal(content, note.Content) {
		w.Header().Set("ETag", quoteETag(note.ETag()))
		WriteJSON(w, http.StatusOK, r.piecesBody(req, rel, note.ETag(), content))
		return
	}
	r.writeLayoutNote(w, req, user, rel, content, func(etag string) any { return r.piecesBody(req, rel, etag, content) })
}

// addPiece answers POST …/pieces: a text or an image added from the design
// comes into the note as a piece of its own, after the piece {"after": 3},
// before every piece with -1, or at its end. The answer is the pieces as they
// are now and the new one, in "added".
func (r *Router) addPiece(w http.ResponseWriter, req *http.Request, rel string) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if denyGuestWrite(w, user) || r.denyWriteProject(w, user.principal(), projectOf(rel)) {
		return
	}
	raw, ok := readLimited(w, req, r.layoutLimits().MaxDocument)
	if !ok {
		return
	}
	var in struct {
		After int    `json:"after"`
		Text  string `json:"text"`
		Image string `json:"image"`
		Alt   string `json:"alt"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || (strings.TrimSpace(in.Text) == "") == (in.Image == "") {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "the body is {after?, text} or {after?, image, alt?}")
		return
	}
	text := in.Text
	if in.Image != "" {
		clean, err := r.deps.Vault.Rel(in.Image)
		if err != nil || !r.canSee(principalFromContext(req), clean) || !strings.Contains("/"+clean, "/attachments/") || !r.deps.Vault.Exists(clean) {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "image: no attachment the reader may read")
			return
		}
		text = "![" + strings.NewReplacer("[", "", "]", "", "\n", " ").Replace(in.Alt) + "](" + attach.FileURL(clean) + ")"
	}
	unlock := r.deps.Vault.LockPath(rel)
	defer unlock()
	note, ok := r.loadLayout(w, rel)
	if !ok {
		return
	}
	content, n := layout.Add(note.Content, in.After, text)
	r.writeLayoutNote(w, req, user, rel, content, func(etag string) any {
		body := r.piecesBody(req, rel, etag, content)
		for _, p := range body["pieces"].([]layout.Piece) {
			if p.N == n {
				body["added"] = p
			}
		}
		return body
	})
}

// reorderPieces answers POST …/pieces/reorder with {"order": [{n, hash}, …]}:
// the numbered pieces of the note in that order, each read with that hash
// (IMP-231). A piece changed since is a 409, as for edit.
func (r *Router) reorderPieces(w http.ResponseWriter, req *http.Request, rel string) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if denyGuestWrite(w, user) || r.denyWriteProject(w, user.principal(), projectOf(rel)) {
		return
	}
	raw, ok := readLimited(w, req, 1<<20)
	if !ok {
		return
	}
	var in struct {
		Order []layout.Placed `json:"order"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || len(in.Order) == 0 {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "the body is {\"order\": [{n, hash}, …]}")
		return
	}
	unlock := r.deps.Vault.LockPath(rel)
	defer unlock()
	note, ok := r.loadLayout(w, rel)
	if !ok {
		return
	}
	content, err := layout.Reorder(note.Content, in.Order)
	switch {
	case errors.Is(err, layout.ErrPieceChanged):
		WriteError(w, http.StatusConflict, CodeConflict, err.Error())
		return
	case errors.Is(err, layout.ErrOrder):
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	case err != nil:
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	if bytes.Equal(content, note.Content) {
		WriteJSON(w, http.StatusOK, r.piecesBody(req, rel, note.ETag(), content))
		return
	}
	r.writeLayoutNote(w, req, user, rel, content, func(etag string) any { return r.piecesBody(req, rel, etag, content) })
}

// editPiece answers POST …/pieces/edit: a new text for a piece, {n, hash,
// text}, from the design (IMP-228). The hash is the piece's as it was read:
// a change elsewhere in the note does not stop this, one to the same piece
// is a 409. The answer is the pieces as they are now.
func (r *Router) editPiece(w http.ResponseWriter, req *http.Request, rel string) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if denyGuestWrite(w, user) || r.denyWriteProject(w, user.principal(), projectOf(rel)) {
		return
	}
	raw, ok := readLimited(w, req, r.layoutLimits().MaxDocument)
	if !ok {
		return
	}
	var in struct {
		N    int    `json:"n"`
		Hash string `json:"hash"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || in.Hash == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "the body is {n, hash, text}")
		return
	}
	unlock := r.deps.Vault.LockPath(rel)
	defer unlock()
	note, ok := r.loadLayout(w, rel)
	if !ok {
		return
	}
	content, err := layout.Replace(note.Content, in.N, in.Hash, in.Text)
	switch {
	case errors.Is(err, layout.ErrPieceChanged):
		WriteError(w, http.StatusConflict, CodeConflict, err.Error())
		return
	case errors.Is(err, layout.ErrEmptyPiece):
		WriteError(w, http.StatusUnprocessableEntity, CodeValidationFormat, err.Error())
		return
	case err != nil:
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	if bytes.Equal(content, note.Content) {
		WriteJSON(w, http.StatusOK, r.piecesBody(req, rel, note.ETag(), content))
		return
	}
	r.writeLayoutNote(w, req, user, rel, content, func(etag string) any { return r.piecesBody(req, rel, etag, content) })
}

// designKeys are the frontmatter's fields of a note's design.
var designKeys = []string{"layout_file", "layout_preview", "layout_pdf", "layout_pdfs", "layout_format", "layout_generator"}

// withoutDesign is a note as it goes out of the vault, a normal markdown:
// its design stays behind, and the comments that number its pieces too.
func withoutDesign(content []byte) []byte {
	content = []byte(parser.WithoutPieceMarks(string(content)))
	if !bytes.Contains(content, []byte("layout_file:")) {
		return content
	}
	out, err := frontmatter.SetKeys(content, nil, designKeys)
	if err != nil {
		return content
	}
	return out
}

// writeLayoutNote writes the note, in the index, the audit log and the
// events as an edit of the note is, and answers with body(its new etag).
func (r *Router) writeLayoutNote(w http.ResponseWriter, req *http.Request, user *RequestUser, rel string, content []byte, body func(etag string) any) {
	if err := r.writeAndIndex(rel, content); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "write: "+err.Error())
		return
	}
	fresh, err := r.deps.Vault.Load(rel)
	if err != nil {
		writeLoadError(w, err)
		return
	}
	r.auditNote(req, audit.ActionUpdate, user, rel, "", int64(len(content)))
	r.publishNoteEvent(events.TopicNote, rel, "update", fresh)
	w.Header().Set("ETag", quoteETag(fresh.ETag()))
	WriteJSON(w, http.StatusOK, body(fresh.ETag()))
}

// readLimited reads a request's body, at most max bytes; false, answered
// with 413, past them.
func readLimited(w http.ResponseWriter, req *http.Request, max int64) ([]byte, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, req.Body, max))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		WriteError(w, http.StatusRequestEntityTooLarge, CodeValidationFormat, fmt.Sprintf("larger than %d bytes", max))
		return nil, false
	case err != nil:
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "the body does not read: "+err.Error())
		return nil, false
	}
	return b, true
}
