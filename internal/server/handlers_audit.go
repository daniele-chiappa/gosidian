package server

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
)

// handleAudit renders the audit log with optional filters.
//
// Query params (all optional):
//   - project:  match entries whose path is inside "<project>/" (or the
//     project itself, for project-level ops)
//   - actor:    substring match on the actor field (case-insensitive)
//   - action:   exact match on the action name (create/update/append/…)
//   - since:    relative duration (24h, 7d) or RFC3339 timestamp
//   - limit:    max entries to return after filtering (default 500, max 5000)
//
// Filtering is applied client-side on a tail of up to 5000 entries from the
// jsonl file. Adequate for personal-use logs; a reverse-line scanner is the
// obvious next step if the log grows much beyond that.
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	var entries []audit.Entry
	var path string

	q := r.URL.Query()
	project := strings.TrimSpace(q.Get("project"))
	actor := strings.TrimSpace(q.Get("actor"))
	action := strings.TrimSpace(q.Get("action"))
	sinceRaw := strings.TrimSpace(q.Get("since"))

	limit := 500
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 5000 {
			limit = n
		}
	}

	var sinceTime time.Time
	var sinceErr string
	if sinceRaw != "" {
		parsed, ok := parseSinceClause(sinceRaw)
		if !ok {
			sinceErr = "valore 'since' non valido (atteso 24h, 7d, oppure RFC3339)"
		} else {
			sinceTime = parsed
		}
	}

	if s.audit != nil {
		path = s.audit.Path()
		// Tail a wider window than `limit` so we can still return `limit`
		// rows after filtering; cap at 5000 to keep memory bounded.
		tail := 5000
		rows, err := s.audit.Tail(tail)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		actorLower := strings.ToLower(actor)
		projectPrefix := ""
		if project != "" {
			projectPrefix = project + "/"
		}

		filtered := make([]audit.Entry, 0, limit)
		// Walk newest-to-oldest so the `limit` keeps the most recent matches.
		for i := len(rows) - 1; i >= 0; i-- {
			e := rows[i]
			if !sinceTime.IsZero() && e.TS.Before(sinceTime) {
				continue
			}
			if action != "" && string(e.Action) != action {
				continue
			}
			if actorLower != "" && !strings.Contains(strings.ToLower(e.Actor), actorLower) {
				continue
			}
			if project != "" {
				// Match path inside the project, OR the project itself for
				// project-level actions (create_project / delete_project /
				// rename_project).
				if e.Path != project && !strings.HasPrefix(e.Path, projectPrefix) {
					continue
				}
			}
			filtered = append(filtered, e)
			if len(filtered) >= limit {
				break
			}
		}
		// Present in file order (oldest first, newest last) like the original
		// unfiltered view. filtered is currently newest-first, so reverse.
		for i, j := 0, len(filtered)-1; i < j; i, j = i+1, j-1 {
			filtered[i], filtered[j] = filtered[j], filtered[i]
		}
		entries = filtered
	}

	// Build preserved-query-string for filter form action.
	form := url.Values{}
	if project != "" {
		form.Set("project", project)
	}
	if actor != "" {
		form.Set("actor", actor)
	}
	if action != "" {
		form.Set("action", action)
	}
	if sinceRaw != "" {
		form.Set("since", sinceRaw)
	}

	s.renderPage(w, r, "audit.html", map[string]any{
		"Title":    "Audit log",
		"Entries":  entries,
		"Path":     path,
		"Project":  project,
		"Actor":    actor,
		"Action":   action,
		"Since":    sinceRaw,
		"Limit":    limit,
		"SinceErr": sinceErr,
		"Total":    len(entries),
	})
}

// parseSinceClause accepts a duration ("24h", "7d") or an RFC3339 timestamp
// and returns the corresponding lower-bound time. Returns ok=false on parse
// failure. The 'd' suffix is rewritten to hours because time.ParseDuration
// doesn't understand days natively.
func parseSinceClause(raw string) (time.Time, bool) {
	dur := raw
	if strings.HasSuffix(dur, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(dur, "d"))
		if err == nil {
			dur = strconv.Itoa(days*24) + "h"
		}
	}
	if d, err := time.ParseDuration(dur); err == nil {
		return time.Now().Add(-d), true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// auditWrite is a helper that records a mutating HTTP operation. It tries to
// be informative without ever blocking — failures are silently swallowed.
func (s *Server) auditWrite(r *http.Request, action audit.Action, path, to string, size int64) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Write(audit.Entry{
		Source: audit.SourceHTTP,
		Actor:  clientIP(r),
		Action: action,
		Path:   path,
		To:     to,
		Size:   size,
	})
}
