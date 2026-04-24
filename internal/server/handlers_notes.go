package server

import (
	"html/template"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/vault"
)

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	notes, err := s.index.AllNotes()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	projs, _ := s.vault.Projects()
	s.renderPage(w, r, "index.html", map[string]any{
		"Title":    "Gosidian",
		"Notes":    notes,
		"Projects": projs,
	})
}

type noteViewData struct {
	Title     string
	Note      *vault.Note
	HTML      template.HTML
	Backlinks []index.Backlink
	Outline   []parser.Heading
	NoSidebar bool // unused for note view, kept so layout.html .NoSidebar evaluates
}

func (s *Server) handleNotes(w http.ResponseWriter, r *http.Request) {
	// /notes/new is handled separately and must take precedence
	if r.URL.Path == "/notes/new" {
		s.handleNewNote(w, r)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, "/notes/")
	if rel == "" {
		http.NotFound(w, r)
		return
	}

	// Edit form: /notes/<path>/edit
	if strings.HasSuffix(rel, "/edit") {
		s.handleEdit(w, r, strings.TrimSuffix(rel, "/edit"))
		return
	}

	// Delete note: POST /notes/<path>/delete
	if strings.HasSuffix(rel, "/delete") && r.Method == http.MethodPost {
		s.handleDeleteNote(w, r, strings.TrimSuffix(rel, "/delete"))
		return
	}

	// Rename note: POST /notes/<path>/rename with form field "to"
	if strings.HasSuffix(rel, "/rename") && r.Method == http.MethodPost {
		s.handleRenameNote(w, r, strings.TrimSuffix(rel, "/rename"))
		return
	}

	// Move note: POST /notes/<path>/move with form field "project"
	if strings.HasSuffix(rel, "/move") && r.Method == http.MethodPost {
		s.handleMoveNote(w, r, strings.TrimSuffix(rel, "/move"))
		return
	}

	// Per-note history: GET /notes/<path>/history (+ optional ?sha=)
	if strings.HasSuffix(rel, "/history") && r.Method == http.MethodGet {
		s.handleHistory(w, r, strings.TrimSuffix(rel, "/history"))
		return
	}

	// Restore historical version: POST /notes/<path>/restore with form sha=
	if strings.HasSuffix(rel, "/restore") && r.Method == http.MethodPost {
		s.handleRestoreVersion(w, r, strings.TrimSuffix(rel, "/restore"))
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.renderNote(w, r, rel)
	case http.MethodPost:
		s.handleSave(w, r, rel)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleRenameNote(w http.ResponseWriter, r *http.Request, rel string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	to := strings.TrimSpace(r.FormValue("to"))
	if to == "" {
		http.Error(w, "missing 'to' field", http.StatusBadRequest)
		return
	}
	if _, err := s.vault.RenameNote(s.index, rel, to); err != nil {
		http.Error(w, "rename failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Compute the canonical destination path the way RenameNote did.
	dest := to
	if !strings.HasSuffix(strings.ToLower(dest), ".md") {
		dest += ".md"
	}
	s.auditWrite(r, audit.ActionRename, rel, dest, 0)
	http.Redirect(w, r, "/notes/"+dest, http.StatusSeeOther)
}

func (s *Server) handleMoveNote(w http.ResponseWriter, r *http.Request, rel string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	project := strings.TrimSpace(r.FormValue("project"))
	if _, err := s.vault.MoveNote(s.index, rel, project); err != nil {
		http.Error(w, "move failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	dest := path.Base(rel)
	if project != "" {
		dest = project + "/" + dest
	}
	s.auditWrite(r, audit.ActionRename, rel, dest, 0)
	http.Redirect(w, r, "/notes/"+dest, http.StatusSeeOther)
}

func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request, rel string) {
	clean, err := s.vault.Rel(rel)
	if err != nil {
		http.Error(w, "invalid note path", http.StatusBadRequest)
		return
	}
	if s.trash != nil {
		if _, err := s.trash.DiscardNote(clean); err != nil {
			http.Error(w, "trash failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		if err := s.vault.Delete(clean); err != nil {
			http.Error(w, "delete failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := s.index.Delete(clean); err != nil {
		http.Error(w, "index delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.auditWrite(r, audit.ActionDelete, clean, "", 0)
	// Redirect: if the note was inside a project, go to that project; else home.
	redirect := "/"
	if idx := strings.Index(clean, "/"); idx > 0 {
		redirect = "/projects/" + clean[:idx]
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (s *Server) renderNote(w http.ResponseWriter, r *http.Request, rel string) {
	note, err := s.vault.Load(rel)
	if err != nil {
		http.Error(w, "note not found: "+rel, http.StatusNotFound)
		return
	}
	html, err := s.renderer.Render(note.Content, s.resolver())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	backlinks, _ := s.index.Backlinks(note.Path)
	outline := parser.ExtractHeadings(note.Content)

	s.renderPage(w, r, "note_view.html", map[string]any{
		"Title":     note.Title,
		"Note":      note,
		"HTML":      template.HTML(html),
		"Backlinks": backlinks,
		"Outline":   outline,
	})
}

func (s *Server) handleEdit(w http.ResponseWriter, r *http.Request, rel string) {
	note, err := s.vault.Load(rel)
	if err != nil {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}
	s.renderPage(w, r, "note_edit.html", map[string]any{
		"Title": note.Title,
		"Note":  note,
		"Body":  string(note.Content),
	})
}

func (s *Server) handleSave(w http.ResponseWriter, r *http.Request, rel string) {
	// Accept either form submission (content field) or raw body.
	var content []byte
	if ct := r.Header.Get("Content-Type"); strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		content = []byte(r.FormValue("content"))
	} else {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		content = body
	}

	if err := s.vault.Save(rel, content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Reindex synchronously so the preview reflects the latest state.
	if note, err := s.vault.Load(rel); err == nil {
		_ = s.index.Upsert(index.NoteDoc{
			Path:    note.Path,
			Title:   note.Title,
			Body:    string(note.Content),
			ModTime: note.ModTime.Unix(),
			Size:    note.Size,
		})
	}
	s.auditWrite(r, audit.ActionUpdate, rel, "", int64(len(content)))
	s.renderNote(w, r, rel)
}

// handlePreview renders form-submitted markdown content to HTML using the same
// renderer and resolver as the normal view path. Used by HTMX live preview in
// the edit form.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	content := r.FormValue("content")
	html, err := s.renderer.Render([]byte(content), s.resolver())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

func (s *Server) handleNewNote(w http.ResponseWriter, r *http.Request) {
	title := r.URL.Query().Get("title")
	project := r.URL.Query().Get("project")
	if title == "" || project == "" {
		if err := r.ParseForm(); err == nil {
			if title == "" {
				title = r.FormValue("title")
			}
			if project == "" {
				project = r.FormValue("project")
			}
		}
	}
	if title == "" {
		title = "Untitled"
	}
	rel := sanitizeFilename(title) + ".md"
	if project != "" {
		rel = sanitizeFilename(project) + "/" + rel
	}
	// Validate path stays inside vault.
	if _, err := s.vault.Rel(rel); err != nil {
		http.Error(w, "invalid note path", http.StatusBadRequest)
		return
	}
	// avoid clobber
	if _, err := s.vault.Load(rel); err == nil {
		http.Redirect(w, r, "/notes/"+rel, http.StatusSeeOther)
		return
	}
	content := []byte("# " + title + "\n")
	if err := s.vault.Save(rel, content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if note, err := s.vault.Load(rel); err == nil {
		_ = s.index.Upsert(index.NoteDoc{
			Path:    note.Path,
			Title:   note.Title,
			Body:    string(note.Content),
			ModTime: note.ModTime.Unix(),
			Size:    note.Size,
		})
	}
	s.auditWrite(r, audit.ActionCreate, rel, "", int64(len(content)))
	http.Redirect(w, r, "/notes/"+rel, http.StatusSeeOther)
}

func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "\\", "-")
	s = strings.ReplaceAll(s, ":", "-")
	if s == "" {
		return "untitled"
	}
	return path.Clean(s)
}
