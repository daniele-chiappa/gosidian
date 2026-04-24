package server

import (
	"net/http"
	"strings"

	"github.com/gosidian/gosidian/internal/audit"
)

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.renderProjectsList(w, r, "")
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		name := r.FormValue("name")
		clean, err := s.vault.CreateProject(name)
		if err != nil {
			s.renderProjectsList(w, r, err.Error())
			return
		}
		s.auditWrite(r, audit.ActionCreateProject, clean, "", 0)
		http.Redirect(w, r, "/projects/"+clean, http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) renderProjectsList(w http.ResponseWriter, r *http.Request, errMsg string) {
	projs, err := s.vault.Projects()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderPage(w, r, "projects.html", map[string]any{
		"Title":    "Projects",
		"Projects": projs,
		"Error":    errMsg,
	})
}

func (s *Server) handleProjectDetail(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/projects/")
	name = strings.TrimSuffix(name, "/")
	if name == "" {
		s.handleProjects(w, r)
		return
	}

	// Delete project: POST /projects/<name>/delete
	if strings.HasSuffix(name, "/delete") && r.Method == http.MethodPost {
		s.handleDeleteProject(w, r, strings.TrimSuffix(name, "/delete"))
		return
	}

	// Rename project: POST /projects/<name>/rename with form field "to"
	if strings.HasSuffix(name, "/rename") && r.Method == http.MethodPost {
		s.handleRenameProject(w, r, strings.TrimSuffix(name, "/rename"))
		return
	}

	// Project overview: GET /projects/<name>/dashboard
	if strings.HasSuffix(name, "/dashboard") && r.Method == http.MethodGet {
		s.handleProjectDashboard(w, r, strings.TrimSuffix(name, "/dashboard"))
		return
	}

	// Validate: the project must be an existing top-level dir.
	projs, err := s.vault.Projects()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var found *struct{ Name string }
	for _, p := range projs {
		if p.Name == name {
			found = &struct{ Name string }{Name: p.Name}
			break
		}
	}
	if found == nil {
		http.NotFound(w, r)
		return
	}

	notes, err := s.index.NotesByPrefix(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderPage(w, r, "project_detail.html", map[string]any{
		"Title":   name,
		"Project": name,
		"Notes":   notes,
	})
}

func (s *Server) handleRenameProject(w http.ResponseWriter, r *http.Request, name string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	to := strings.TrimSpace(r.FormValue("to"))
	if to == "" {
		http.Error(w, "missing 'to' field", http.StatusBadRequest)
		return
	}
	if err := s.vault.RenameProject(s.index, name, to); err != nil {
		http.Error(w, "rename failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.auditWrite(r, audit.ActionRenameProject, name, to, 0)
	http.Redirect(w, r, "/projects/"+to, http.StatusSeeOther)
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request, name string) {
	var removed []string
	if s.trash != nil {
		_, notes, err := s.trash.DiscardProject(name)
		if err != nil {
			http.Error(w, "trash project failed: "+err.Error(), http.StatusBadRequest)
			return
		}
		removed = notes
	} else {
		var err error
		removed, err = s.vault.DeleteProject(name)
		if err != nil {
			http.Error(w, "delete project failed: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	for _, p := range removed {
		if err := s.index.Delete(p); err != nil {
			http.Error(w, "index cleanup failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	s.auditWrite(r, audit.ActionDeleteProject, name, "", int64(len(removed)))
	http.Redirect(w, r, "/projects", http.StatusSeeOther)
}
