package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gosidian/gosidian/internal/marks"
)

// markSet is a set of marks as the web UI reads it: Stale when the note
// changed since the marks were made on it.
type markSet struct {
	marks.Set
	Stale bool `json:"stale"`
}

// handleMarks answers GET /api/v1/marks?path=<note>: the sets of marks the
// pipelines put on a note the reader may see (IMP-188).
func (r *Router) handleMarks(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	notePath := strings.TrimSpace(req.URL.Query().Get("path"))
	if notePath == "" || !r.canSee(principalFromContext(req), notePath) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "no note "+notePath)
		return
	}
	out := []markSet{}
	if r.deps.Marks != nil {
		sets, err := r.deps.Marks.Sets(notePath)
		if err != nil {
			slog.Default().Warn("api/v1: the marks of a note", "path", notePath, "err", err)
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, "the marks could not be read: see the server log")
			return
		}
		hash := ""
		if n, err := r.deps.Vault.Load(notePath); err == nil {
			sum := sha256.Sum256(n.Content)
			hash = hex.EncodeToString(sum[:])
		}
		for _, s := range sets {
			out = append(out, markSet{Set: s, Stale: hash != "" && s.Hash != hash})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": notePath, "sets": out})
}
