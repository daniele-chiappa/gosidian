package server

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gosidian/gosidian/internal/attach"
)

// handleAttach accepts a multipart upload from the editor (paste or drop)
// and stores it under <vault>/<project>/attachments/<sha256>.<ext>. Returns a
// JSON object with the relative path the editor can splice into the note.
func (s *Server) handleAttach(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("project"))

	if err := r.ParseMultipartForm(20 << 20); err != nil {
		http.Error(w, "bad multipart: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file field: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Read with a ceiling slightly above MaxBytes so we can detect oversized.
	limited := io.LimitReader(file, attach.MaxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		http.Error(w, "read: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if int64(len(data)) > attach.MaxBytes {
		http.Error(w, "file too large (max 10 MiB)", http.StatusRequestEntityTooLarge)
		return
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if _, _, err := attach.ValidateExt(ext); err != nil {
		http.Error(w, err.Error(), http.StatusUnsupportedMediaType)
		return
	}

	res, err := attach.Store(s.vault, data, header.Filename, project)
	if err != nil {
		http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"path":     res.Path,
		"filename": res.Filename,
		"markdown": res.Markdown,
	})
}

// handleVaultFile serves files under attachments/ subdirectories from the
// vault. Restricted to those subpaths: the rest of the vault stays opaque
// from this endpoint to avoid accidental disclosure of arbitrary notes.
func (s *Server) handleVaultFile(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/vault-files/")
	if rel == "" {
		http.NotFound(w, r)
		return
	}
	clean, err := s.vault.Rel(rel)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	// Only serve from a path containing /attachments/ or starting with attachments/.
	if !strings.Contains("/"+clean, "/attachments/") {
		http.NotFound(w, r)
		return
	}
	abs, err := s.vault.Abs(clean)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	ext := strings.ToLower(filepath.Ext(clean))
	ct := "application/octet-stream"
	if info, ok := attach.AllowedExt[ext]; ok {
		ct = info.MIME
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, abs)
}
