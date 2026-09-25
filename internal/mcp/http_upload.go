package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
)

// handleHTTPUpload accepts a multipart file upload authenticated by an MCP
// bearer token (IMP-059). It is the primary cheap-ingestion path for agents on
// the same network: the file bytes travel over HTTP, never through the model
// context as base64. Mounted at <basePath>/upload by Handler (e.g. /mcp/upload),
// sharing the same token store as the SSE transport.
//
//	POST /mcp/upload?project=<name>
//	Authorization: Bearer <mcp-token>
//	Content-Type: multipart/form-data   (file in the "file" field)
//
// Response (200): {path, url, mime, kind, size, hash}. The returned `path` can
// be passed to memory_create_media_note as `attachment`, or spliced into a note
// body as a /vault-files/ link — no bytes ever go through the model context.
func (s *Server) handleHTTPUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed: POST a multipart file in the 'file' field")
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

	// Resolve + enforce the project scope, mirroring the MCP tools.
	project, err := scopedProject(tok, r.URL.Query().Get("project"))
	if err != nil {
		writeJSONError(w, http.StatusForbidden, err.Error())
		return
	}

	// The cap must sit on the body itself: ParseMultipartForm's argument only
	// bounds in-memory buffering, everything beyond it is spooled to disk
	// before any size check downstream could run.
	r.Body = http.MaxBytesReader(w, r.Body, multipartBodyCap)
	if err := r.ParseMultipartForm(multipartBodyCap); err != nil {
		writeMultipartError(w, err)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "missing 'file' field (multipart/form-data)")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, attach.MaxBytes+1))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "read upload: "+err.Error())
		return
	}
	if len(data) > attach.MaxBytes {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "file too large (max 10 MiB)")
		return
	}
	if msg := s.writeLimitViolation(tok, len(data)); msg != "" {
		writeJSONError(w, http.StatusTooManyRequests, msg)
		return
	}

	// attach.Store validates the extension, magic-bytes MIME, and the 10 MiB cap.
	res, err := attach.Store(s.vault, data, hdr.Filename, project)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	// Audit under the authenticated token (reuse the tool audit path).
	s.auditWrite(context.WithValue(r.Context(), tokenCtxKey, tok), audit.ActionUploadAttachment, res.Path, "", int64(len(data)))

	ext := strings.ToLower(filepath.Ext(res.Filename))
	mime := "application/octet-stream"
	kind := "document"
	if info, ok := attach.AllowedExt[ext]; ok {
		mime = info.MIME
		if info.IsImage {
			kind = "image"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": res.Path,
		"url":  "/vault-files/" + res.Path,
		"mime": mime,
		"kind": kind,
		"size": len(data),
		"hash": strings.TrimSuffix(res.Filename, ext),
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

// multipartBodyCap bounds a multipart upload body: the attachment cap plus
// room for the multipart framing and a small metadata field or two.
const multipartBodyCap = attach.MaxBytes + (1 << 20)

// writeMultipartError maps a ParseMultipartForm failure to a status: a body
// that tripped MaxBytesReader is 413, anything else is a malformed request.
func writeMultipartError(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large (max 10 MiB file)")
		return
	}
	writeJSONError(w, http.StatusBadRequest, "bad multipart body: "+err.Error())
}
