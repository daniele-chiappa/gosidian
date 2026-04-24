package server

import (
	"net/http"
	"strings"
)

// handleTags renders /tags, optionally filtered by ?project=X. The project
// dropdown listing is populated from the current vault layout so the user
// never sees a stale selection.
func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))

	var (
		tags interface{}
		err  error
	)
	if project != "" {
		tags, err = s.index.TagsByProject(project)
	} else {
		tags, err = s.index.Tags()
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	projects, _ := s.vault.Projects()
	projectNames := make([]string, 0, len(projects))
	for _, p := range projects {
		projectNames = append(projectNames, p.Name)
	}

	s.renderPage(w, r, "tags.html", map[string]any{
		"Title":            "Tags",
		"Tags":             tags,
		"Projects":         projectNames,
		"SelectedProject":  project,
	})
}

// handleTagsByName renders /tags/<name>, optionally filtered by ?project=X.
func (s *Server) handleTagsByName(w http.ResponseWriter, r *http.Request) {
	tag := strings.TrimPrefix(r.URL.Path, "/tags/")
	if tag == "" {
		s.handleTags(w, r)
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("project"))

	var (
		notes interface{}
		err   error
	)
	if project != "" {
		notes, err = s.index.NotesByTagInProject(tag, project)
	} else {
		notes, err = s.index.NotesByTag(tag)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	projects, _ := s.vault.Projects()
	projectNames := make([]string, 0, len(projects))
	for _, p := range projects {
		projectNames = append(projectNames, p.Name)
	}

	s.renderPage(w, r, "tag_notes.html", map[string]any{
		"Title":           "#" + tag,
		"Tag":             tag,
		"Notes":           notes,
		"Projects":        projectNames,
		"SelectedProject": project,
	})
}
