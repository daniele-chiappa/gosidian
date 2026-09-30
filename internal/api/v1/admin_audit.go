package v1

import (
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
)

// auditEntryView is the JSON shape returned by /admin/audit. Mirrors
// audit.Entry except times are RFC 3339 strings and empty fields are
// omitted to keep the payload tight.
type auditEntryView struct {
	TS     string `json:"ts"`
	Source string `json:"source"`
	Token  string `json:"token,omitempty"`
	Actor  string `json:"actor,omitempty"`
	UserID string `json:"user_id,omitempty"`
	Action string `json:"action"`
	Path   string `json:"path,omitempty"`
	To     string `json:"to,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

// handleAdminAudit returns the most recent audit entries matching
// the supplied filters. All filters are optional; calling without
// any returns the latest `limit` entries.
//
// Query params:
//   - limit       1-500, default 50
//   - actor       exact match
//   - user_id     exact match
//   - action      exact match (e.g. "spa_token_create")
//   - source      "http" or "mcp"
//   - path_prefix vault-relative prefix
//   - since       RFC 3339 timestamp; entries older are dropped
//   - until       RFC 3339 timestamp; entries newer are dropped
func (r *Router) handleAdminAudit(w http.ResponseWriter, req *http.Request) {
	if r.deps.Audit == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "audit log not configured")
		return
	}
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}

	q := req.URL.Query()
	limit := limitParam(req, 50, 500)

	opts := audit.TailOpts{
		Actor:      strings.TrimSpace(q.Get("actor")),
		UserID:     strings.TrimSpace(q.Get("user_id")),
		Action:     audit.Action(strings.TrimSpace(q.Get("action"))),
		Source:     audit.Source(strings.TrimSpace(q.Get("source"))),
		PathPrefix: strings.TrimSpace(q.Get("path_prefix")),
		Limit:      limit,
	}
	if v := strings.TrimSpace(q.Get("since")); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			opts.Since = t
		} else {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "since must be RFC 3339")
			return
		}
	}
	if v := strings.TrimSpace(q.Get("until")); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			opts.Until = t
		} else {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "until must be RFC 3339")
			return
		}
	}

	entries, err := r.deps.Audit.TailFiltered(opts)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	out := make([]auditEntryView, 0, len(entries))
	for _, e := range entries {
		out = append(out, auditEntryView{
			TS:     e.TS.UTC().Format(rfc3339Z),
			Source: string(e.Source),
			Token:  e.Token,
			Actor:  e.Actor,
			UserID: e.UserID,
			Action: string(e.Action),
			Path:   e.Path,
			To:     e.To,
			Size:   e.Size,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items": out,
		"total": len(out),
		"limit": limit,
	})
}
