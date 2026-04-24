package server

import (
	"encoding/json"
	"net/http"
	"path"
	"strings"
)

// handleCommandPalette serves the data the Cmd+K overlay needs: a flat list
// of notes, projects, and tags. One round-trip at first open, cached in JS
// thereafter.
func (s *Server) handleCommandPalette(w http.ResponseWriter, r *http.Request) {
	type noteItem struct {
		Path  string `json:"path"`
		Title string `json:"title"`
	}
	type projectItem struct {
		Name      string `json:"name"`
		NoteCount int    `json:"noteCount"`
	}
	type tagItem struct {
		Tag   string `json:"tag"`
		Count int    `json:"count"`
	}

	notes := []noteItem{}
	if rows, err := s.index.AllNotes(); err == nil {
		for _, n := range rows {
			title := n.Title
			if title == "" {
				b := path.Base(n.Path)
				title = strings.TrimSuffix(b, path.Ext(b))
			}
			notes = append(notes, noteItem{Path: n.Path, Title: title})
		}
	}

	projects := []projectItem{}
	if ps, err := s.vault.Projects(); err == nil {
		for _, p := range ps {
			projects = append(projects, projectItem{Name: p.Name, NoteCount: p.NoteCount})
		}
	}

	tags := []tagItem{}
	if ts, err := s.index.Tags(); err == nil {
		for _, t := range ts {
			tags = append(tags, tagItem{Tag: t.Tag, Count: t.Count})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"notes":    notes,
		"projects": projects,
		"tags":     tags,
	})
}
