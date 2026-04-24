package server

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/index"
)

type todoNoteCount struct {
	Path  string
	Title string
	Count int
}

// handleProjectDashboard renders /projects/{name}/dashboard — a one-page
// overview of what's going on in the project: plans in progress, most-recent
// activity, freshly-modified notes, and a tally of open TODO checkboxes.
//
// All widgets read from the existing index/audit/vault stores; no new state
// is persisted.
func (s *Server) handleProjectDashboard(w http.ResponseWriter, r *http.Request, name string) {
	// Validate: must be an existing project.
	projs, err := s.vault.Projects()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	found := false
	noteCount := 0
	for _, p := range projs {
		if p.Name == name {
			found = true
			noteCount = p.NoteCount
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}

	var activePlans []index.NoteRow
	if inProgress, err := s.index.NotesByTag("status:in-progress"); err == nil {
		prefix := name + "/plans/"
		for _, n := range inProgress {
			if strings.HasPrefix(n.Path, prefix) {
				activePlans = append(activePlans, n)
			}
		}
	}

	var hotFiles []index.RecentNote
	if hot, err := s.index.RecentNotes(name, 0, 5); err == nil {
		hotFiles = hot
	}

	var recentActions []audit.Entry
	if s.audit != nil {
		rows, err := s.audit.Tail(500)
		if err == nil {
			prefix := name + "/"
			for i := len(rows) - 1; i >= 0; i-- {
				e := rows[i]
				if e.Path == name || strings.HasPrefix(e.Path, prefix) {
					recentActions = append(recentActions, e)
					if len(recentActions) >= 10 {
						break
					}
				}
			}
		}
	}

	var todoByNote []todoNoteCount
	todoCount := 0
	if notes, err := s.index.NotesByPrefix(name); err == nil {
		for _, n := range notes {
			note, err := s.vault.Load(n.Path)
			if err != nil {
				continue
			}
			cnt := bytes.Count(note.Content, []byte("\n- [ ] "))
			if bytes.HasPrefix(note.Content, []byte("- [ ] ")) {
				cnt++
			}
			if cnt > 0 {
				todoByNote = append(todoByNote, todoNoteCount{
					Path: n.Path, Title: n.Title, Count: cnt,
				})
				todoCount += cnt
			}
		}
	}

	s.renderPage(w, r, "project_dashboard.html", map[string]any{
		"Title":         name + " — dashboard",
		"Project":       name,
		"NoteCount":     noteCount,
		"ActivePlans":   activePlans,
		"HotFiles":      hotFiles,
		"RecentActions": recentActions,
		"TodoCount":     todoCount,
		"TodoByNote":    todoByNote,
	})
}
