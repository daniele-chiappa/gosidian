package mcp

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/gosidian/gosidian/internal/auth"
)

// handleHTTPAppend appends a markdown body to one note, authenticated by an
// MCP bearer token — the write-side twin of handleHTTPDownload for callers
// that hold a token but no MCP session, such as Claude Code hooks writing a
// session digest (IMP-094). Mounted at <basePath>/append by Handler.
//
//	POST /mcp/append?path=<vault-relative note path>
//	Authorization: Bearer <mcp-token>        (write scope)
//	If-Match: "<etag>"                       (optional, from /download)
//	Content-Type: text/markdown              (body = the text to append)
//
// Response (200): {"path", "etag", "created"}. The append runs through the
// same pipeline as memory_append (lock, precondition, limits, index, audit,
// events), so this channel cannot skip a guard the tool enforces.
func (s *Server) handleHTTPAppend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed: POST ?path=<vault-relative note path> with the markdown body")
		return
	}
	tok := s.authenticate(r)
	if tok == nil {
		w.Header().Set("WWW-Authenticate", s.wwwAuthenticate())
		writeJSONError(w, http.StatusUnauthorized, "missing or invalid bearer token")
		return
	}
	if !tok.HasScope(auth.ScopeWrite) {
		writeJSONError(w, http.StatusForbidden, "token lacks write scope")
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("path"))
	if raw == "" {
		writeJSONError(w, http.StatusBadRequest, "missing path query param (vault-relative note path, e.g. proj/log.md)")
		return
	}
	rel, err := s.vault.Rel(raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "path rejected: "+err.Error())
		return
	}
	if !s.vault.IsNoteFile(rel) {
		writeJSONError(w, http.StatusBadRequest, "not a note path: keep the .md (or .html) extension")
		return
	}
	// 404 rather than 403 outside the token's projects: a scoped token must
	// not learn what exists elsewhere (same shape as /download).
	if !tok.AllowsPath(rel) {
		writeJSONError(w, http.StatusNotFound, "note not found")
		return
	}
	if !tok.AllowsWrite(rel) {
		writeJSONError(w, http.StatusForbidden, "the account behind this token may only read project "+auth.ProjectOf(rel))
		return
	}

	// The body is the addition; cap it at the note size limit so a runaway
	// script cannot spool an unbounded body before the size check.
	limit := s.maxNoteBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if int64(len(body)) > limit {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "body exceeds the note size limit")
		return
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		writeJSONError(w, http.StatusBadRequest, "empty body: nothing to append")
		return
	}
	ifMatch := r.Header.Get("If-Match")
	if ifMatch == "" {
		ifMatch = r.URL.Query().Get("if_match")
	}

	ctx := context.WithValue(r.Context(), tokenCtxKey, tok)
	res, aerr := s.appendNote(ctx, tok, rel, string(body), ifMatch)
	if aerr != nil {
		writeJSONError(w, aerr.status, aerr.msg)
		return
	}
	w.Header().Set("ETag", `"`+res.ETag+`"`)
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    res.Path,
		"etag":    res.ETag,
		"created": res.Created,
	})
}
