package server

import (
	"net/http"
	"strings"

	"github.com/gosidian/gosidian/internal/index"
)

// handleTrash renders the trash bin contents (only meaningful when the bin
// is enabled in config). Without a bin it shows a placeholder explaining
// how to enable it.
func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	if s.trash == nil {
		s.renderPage(w, r, "trash.html", map[string]any{
			"Title":   "Trash",
			"Enabled": false,
			"Entries": nil,
		})
		return
	}
	entries, err := s.trash.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderPage(w, r, "trash.html", map[string]any{
		"Title":   "Trash",
		"Enabled": true,
		"Entries": entries,
	})
}

// handleTrashAction routes /trash/<id>/restore and /trash/<id>/purge POSTs,
// plus /trash/purge-all for emptying the bin.
func (s *Server) handleTrashAction(w http.ResponseWriter, r *http.Request) {
	if s.trash == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/trash/")
	if rest == "purge-all" {
		if err := s.trash.PurgeAll(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/trash", http.StatusSeeOther)
		return
	}
	switch {
	case strings.HasSuffix(rest, "/restore"):
		id := strings.TrimSuffix(rest, "/restore")
		restored, err := s.trash.Restore(id)
		if err != nil {
			http.Error(w, "restore failed: "+err.Error(), http.StatusBadRequest)
			return
		}
		// Re-index the restored notes so search/sidebar pick them up.
		for _, p := range restored {
			if note, err := s.vault.Load(p); err == nil {
				_ = s.index.Upsert(index.NoteDoc{
					Path: note.Path, Title: note.Title,
					Body: string(note.Content), ModTime: note.ModTime.Unix(), Size: note.Size,
				})
			}
		}
		http.Redirect(w, r, "/trash", http.StatusSeeOther)
	case strings.HasSuffix(rest, "/purge"):
		id := strings.TrimSuffix(rest, "/purge")
		if err := s.trash.Purge(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/trash", http.StatusSeeOther)
	default:
		http.NotFound(w, r)
	}
}
