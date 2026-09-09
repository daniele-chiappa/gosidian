package v1

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// commitView is the JSON shape returned per commit. SHA is the full
// hash; ShortSHA is a stable 7-char prefix the SPA can render in
// compact lists. Date is RFC 3339 UTC.
type commitView struct {
	SHA      string `json:"sha"`
	ShortSHA string `json:"short_sha"`
	Author   string `json:"author"`
	Date     string `json:"date"`
	Subject  string `json:"subject"`
}

// readHistory returns the git log of a single note. Requires the
// gitsync subsystem to be wired AND enabled in cfg; otherwise 503.
// `limit` query param caps results (default 50, max 500).
func (r *Router) readHistory(w http.ResponseWriter, req *http.Request, notePath string) {
	if !r.canSee(principalFromContext(req), notePath) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
		return
	}
	if r.deps.GitSync == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "git sync not wired")
		return
	}
	// Guard the path: history of a non-existent note would let the
	// caller probe arbitrary git paths. Vault.Load already maps to
	// the same fs space the gitsync.History expects.
	if _, err := r.deps.Vault.Load(notePath); err != nil {
		writeLoadError(w, err)
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(req.URL.Query().Get("limit")))
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	commits, err := r.deps.GitSync.History(notePath, limit)
	if err != nil {
		// Disabled gitsync surfaces "git sync disabled" — translate
		// to 503 so the SPA branches on availability cleanly.
		if strings.Contains(err.Error(), "git sync disabled") {
			WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, err.Error())
			return
		}
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	out := make([]commitView, 0, len(commits))
	for _, c := range commits {
		date := c.Date.UTC().Format(time.RFC3339)
		out = append(out, commitView{
			SHA:      c.SHA,
			ShortSHA: c.ShortSHA,
			Author:   c.Author,
			Date:     date,
			Subject:  c.Subject,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items": out,
		"total": len(out),
		"limit": limit,
	})
}
