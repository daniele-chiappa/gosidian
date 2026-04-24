package server

import (
	"net/http"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/index"
)

// handleHistory renders the per-note git history page. Without a sha query
// param it shows the commit list and the diff of the most recent commit;
// with ?sha=... it shows the diff of that specific commit. The note path
// must already be a valid vault path.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request, rel string) {
	clean, err := s.vault.Rel(rel)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if s.gitSync == nil {
		http.Error(w, "git sync not configured for this vault", http.StatusServiceUnavailable)
		return
	}
	commits, err := s.gitSync.History(clean, 100)
	if err != nil {
		http.Error(w, "history: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sha := r.URL.Query().Get("sha")
	if sha == "" && len(commits) > 0 {
		sha = commits[0].SHA
	}
	var diff string
	if sha != "" {
		d, err := s.gitSync.Show(clean, sha)
		if err != nil {
			diff = "(unable to show diff: " + err.Error() + ")"
		} else {
			diff = d
		}
	}

	s.renderPage(w, r, "history.html", map[string]any{
		"Title":    "History — " + clean,
		"Path":     clean,
		"Commits":  commits,
		"Selected": sha,
		"Diff":     diff,
	})
}

// handleRestoreVersion writes the historical content of a file back to the
// working tree and reindexes. The audit + watcher will pick up the change
// and the next debounced commit will record the restore.
func (s *Server) handleRestoreVersion(w http.ResponseWriter, r *http.Request, rel string) {
	clean, err := s.vault.Rel(rel)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if s.gitSync == nil {
		http.Error(w, "git sync not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sha := r.FormValue("sha")
	data, err := s.gitSync.Restore(clean, sha)
	if err != nil {
		http.Error(w, "restore: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.vault.Save(clean, data); err != nil {
		http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if note, err := s.vault.Load(clean); err == nil {
		_ = s.index.Upsert(index.NoteDoc{
			Path:    note.Path,
			Title:   note.Title,
			Body:    string(note.Content),
			ModTime: note.ModTime.Unix(),
			Size:    note.Size,
		})
	}
	s.auditWrite(r, audit.ActionUpdate, clean, sha[:min(len(sha), 7)], int64(len(data)))
	http.Redirect(w, r, "/notes/"+clean, http.StatusSeeOther)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
