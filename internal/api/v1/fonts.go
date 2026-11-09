package v1

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/fonts"
)

// fontView is a font of the catalog as the API shows it.
type fontView struct {
	ID        string `json:"id"`
	Family    string `json:"family"`
	Style     string `json:"style"`
	Format    string `json:"format"`
	Faces     int    `json:"faces"`
	Embedding string `json:"embedding,omitempty"`
	Bytes     int64  `json:"bytes"`
	AddedAt   string `json:"added_at"`
}

func viewFont(f fonts.Font) fontView {
	return fontView{
		ID: f.ID, Family: f.Family, Style: f.Style, Format: f.Format, Faces: f.Faces,
		Embedding: f.Embedding, Bytes: f.Bytes, AddedAt: f.AddedAt.UTC().Format(rfc3339Z),
	}
}

// fontTypes are the media types of the formats the catalog keeps.
var fontTypes = map[string]string{"ttf": "font/ttf", "otf": "font/otf", "ttc": "font/collection"}

// handleFonts answers GET /api/v1/fonts: the catalog, for the editor (IMP-226).
func (r *Router) handleFonts(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	list, err := r.deps.Fonts.List()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "read the fonts")
		return
	}
	out := make([]fontView, 0, len(list))
	for _, f := range list {
		out = append(out, viewFont(f))
	}
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
}

// handleFontFile answers GET /api/v1/fonts/{id}: a font's file. Its name is
// its content, so the browser keeps it and asks once.
func (r *Router) handleFontFile(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	data, f, err := r.deps.Fonts.Read(strings.TrimPrefix(req.URL.Path, "/api/v1/fonts/"))
	if err != nil {
		WriteError(w, http.StatusNotFound, CodeNotFound, "no such font")
		return
	}
	w.Header().Set("Content-Type", fontTypes[f.Format])
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

// handleAdminFonts answers POST /api/v1/admin/fonts: the body is the file. The
// same file again changes nothing, and answers 200.
func (r *Router) handleAdminFonts(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	data, ok := readLimited(w, req, int64(r.web.LayoutMaxFontBytes))
	if !ok {
		return
	}
	f, added, err := r.deps.Fonts.Add(data)
	if errors.Is(err, fonts.ErrNotAFont) {
		WriteError(w, http.StatusUnprocessableEntity, CodeValidationFormat, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "keep the font")
		return
	}
	if !added {
		WriteJSON(w, http.StatusOK, viewFont(f))
		return
	}
	r.auditFont(req, audit.ActionFontAdd, f)
	WriteJSON(w, http.StatusCreated, viewFont(f))
}

// handleAdminFontItem answers DELETE /api/v1/admin/fonts/{id}.
func (r *Router) handleAdminFontItem(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodDelete {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimPrefix(req.URL.Path, "/api/v1/admin/fonts/")
	_, f, err := r.deps.Fonts.Read(id)
	if err == nil {
		err = r.deps.Fonts.Delete(id)
	}
	if errors.Is(err, os.ErrNotExist) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "no such font")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "remove the font")
		return
	}
	r.auditFont(req, audit.ActionFontDelete, f)
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) auditFont(req *http.Request, action audit.Action, f fonts.Font) {
	user := UserFromContext(req.Context())
	if r.deps.Audit == nil || user == nil {
		return
	}
	_ = r.deps.Audit.Write(audit.Entry{
		Source: audit.SourceHTTP,
		Actor:  user.Username,
		UserID: user.ID,
		Action: action,
		Path:   f.ID,
		To:     f.Family + " " + f.Style,
	})
}
