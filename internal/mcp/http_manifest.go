package mcp

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
)

// manifestNote is one note of a project manifest.
type manifestNote struct {
	Path  string `json:"path"`
	ETag  string `json:"etag"`
	Size  int64  `json:"size"`
	MTime string `json:"mtime"` // RFC 3339, UTC
	Title string `json:"title,omitempty"`
}

// handleHTTPManifest lists the notes of one project for a local read-only
// mirror (IMP-102): path, ETag (the stamp memory_get and /download return),
// size, modification time and title. The client diffs it against its copy
// and fetches what changed through /download. Only projects whose admin set
// allow_local_mirror can be listed, since a mirror copies the project's notes
// onto another machine; every listing is audited (mirror_sync).
//
//	GET /mcp/manifest?project=<top-level folder>
//	Authorization: Bearer <mcp-token>
//
// Mounted at <basePath>/manifest by Handler.
func (s *Server) handleHTTPManifest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed: GET ?project=<project>")
		return
	}
	tok := s.authenticate(r)
	if tok == nil {
		w.Header().Set("WWW-Authenticate", s.wwwAuthenticate())
		writeJSONError(w, http.StatusUnauthorized, "missing or invalid bearer token")
		return
	}
	if !tok.HasScope(auth.ScopeRead) {
		writeJSONError(w, http.StatusForbidden, "token lacks read scope")
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" || strings.ContainsAny(project, `/\`) || strings.HasPrefix(project, ".") {
		writeJSONError(w, http.StatusBadRequest, "missing or invalid project query param (a top-level folder, e.g. myproject)")
		return
	}
	// 404, not 403, outside the token's reach: a token must not learn which
	// projects exist beyond the ones it can read (same shape as /download).
	if !tok.AllowsProject(project) || s.projectHidden(project) {
		writeJSONError(w, http.StatusNotFound, "project not found")
		return
	}
	stats, err := s.vault.ProjectNotes(project)
	if errors.Is(err, fs.ErrNotExist) {
		writeJSONError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "list notes: "+err.Error())
		return
	}
	if s.projects == nil || !s.projects.AllowsLocalMirror(project) {
		writeJSONError(w, http.StatusForbidden, "local mirrors are off for this project: a project admin can enable allow_local_mirror (Projects → mirror)")
		return
	}

	titles := map[string]string{}
	if s.index != nil {
		if rows, err := s.index.NotesByPrefix(project); err == nil {
			for _, n := range rows {
				titles[n.Path] = n.Title
			}
		}
	}
	notes := make([]manifestNote, 0, len(stats))
	var bytes int64
	for _, st := range stats {
		notes = append(notes, manifestNote{
			Path:  st.Path,
			ETag:  st.ETag(),
			Size:  st.Size,
			MTime: st.ModTime.UTC().Format(time.RFC3339),
			Title: titles[st.Path],
		})
		bytes += st.Size
	}
	s.auditWrite(context.WithValue(r.Context(), tokenCtxKey, tok), audit.ActionMirrorSync, project, "", bytes)

	writeJSON(w, http.StatusOK, map[string]any{
		"project":      project,
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"notes":        notes,
	})
}
