package mcp

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/vault"
)

// handleHTTPDownload serves the raw bytes of one note, authenticated by an MCP
// bearer token — the read-side counterpart of handleHTTPUpload and of the
// memory_ingest ticket (IMP-081). memory_get streams a body through the model
// context at ~1 token per character; an agent that needs a large note on its
// own disk (edit a self-contained .html report locally, then re-ingest it)
// fetches it here instead. Mounted at <basePath>/download by Handler.
//
//	GET /mcp/download?path=<vault-relative note path>
//	Authorization: Bearer <mcp-token>
//
// Response (200): the note bytes, Content-Type by extension, and an ETag
// carrying the same stamp memory_get returns so the caller can pass it back as
// if_match on the write that follows. Notes only: attachments are served by
// /vault-files/ with the same bearer. Reads are not audited, like the MCP
// read tools.
func (s *Server) handleHTTPDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed: GET ?path=<vault-relative note path>")
		return
	}
	tok := s.authenticate(r)
	if tok == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="gosidian"`)
		writeJSONError(w, http.StatusUnauthorized, "missing or invalid bearer token")
		return
	}
	if !tok.HasScope(auth.ScopeRead) {
		writeJSONError(w, http.StatusForbidden, "token lacks read scope")
		return
	}

	raw := strings.TrimSpace(r.URL.Query().Get("path"))
	if raw == "" {
		writeJSONError(w, http.StatusBadRequest, "missing path query param (vault-relative note path, e.g. proj/docs/report.html)")
		return
	}
	rel, err := s.vault.Rel(raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "path rejected: "+err.Error())
		return
	}
	// 404 rather than 403: a scoped token must not learn what exists outside
	// its projects (same fail-closed shape as /vault-files/ and the notes API).
	if !tok.AllowsPath(rel) {
		writeJSONError(w, http.StatusNotFound, "note not found")
		return
	}
	note, err := s.vault.Load(rel)
	if err != nil {
		switch {
		case errors.Is(err, vault.ErrNotNote):
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("%q is not a note (.md, or .html when html notes are enabled): attachments are served at /vault-files/%s with the same bearer token", rel, rel))
		case errors.Is(err, os.ErrNotExist):
			writeJSONError(w, http.StatusNotFound, "note not found")
		default:
			writeJSONError(w, http.StatusInternalServerError, "read note: "+err.Error())
		}
		return
	}

	// The bytes are user content served from the app origin: same inert
	// headers as /vault-files/ (ADR-021), and always a download, so an .html
	// note opened directly in a browser never renders in the origin.
	h := w.Header()
	attach.SetInertHeaders(h)
	h.Set("Content-Type", noteContentType(rel))
	h.Set("Content-Disposition", `attachment; filename="`+filepath.Base(rel)+`"`)
	h.Set("ETag", `"`+note.ETag()+`"`)
	h.Set("Cache-Control", "private, no-store")
	h.Set("Content-Length", strconv.Itoa(len(note.Content)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(note.Content)
}

// noteContentType maps a note path to its media type. Load already confined
// the path to the note extensions, so the fallback is only defensive.
func noteContentType(rel string) string {
	if strings.EqualFold(filepath.Ext(rel), ".html") {
		return "text/html; charset=utf-8"
	}
	return "text/markdown; charset=utf-8"
}
