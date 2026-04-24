package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gosidian/gosidian/internal/gitsync"
)

// gitSyncHealth is the structured state reported under /healthz. Mirrors
// gitsync.Status so operators can see the graceful-fail state without tail'ing
// logs (IMP-002). Time fields use pointers so the zero value is omitted from
// the JSON (omitempty doesn't work on time.Time struct values).
type gitSyncHealth struct {
	Enabled      bool       `json:"enabled"`
	Healthy      bool       `json:"healthy"`
	LastError    string     `json:"last_error,omitempty"`
	LastErrorAt  *time.Time `json:"last_error_at,omitempty"`
	LastCommitAt *time.Time `json:"last_commit_at,omitempty"`
}

// handleHealth answers liveness/readiness probes. It is intentionally
// unauthenticated (and will be whitelisted by the future auth middleware) so
// orchestrators can reach it without credentials.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	type payload struct {
		Status  string        `json:"status"`
		Version string        `json:"version,omitempty"`
		Vault   string        `json:"vault"`
		Notes   int           `json:"notes"`
		GitSync gitSyncHealth `json:"git_sync"`
	}

	out := payload{
		Status:  "ok",
		Version: s.version,
		Vault:   s.vault.Root,
		GitSync: buildGitSyncHealth(s.gitSync, s.gitSyncOn),
	}

	notes, err := s.index.AllNotes()
	if err != nil {
		out.Status = "degraded"
		writeJSON(w, http.StatusServiceUnavailable, out)
		return
	}
	out.Notes = len(notes)
	// Degraded gitsync is not enough to fail readiness — the service still
	// serves reads and writes — but we surface it so the caller can alert.
	if out.GitSync.Enabled && !out.GitSync.Healthy {
		out.Status = "degraded"
	}
	writeJSON(w, http.StatusOK, out)
}

// buildGitSyncHealth condenses the subsystem state into the JSON payload.
// syncer may be nil when git sync is disabled in config OR when the caller
// (e.g. a unit test) did not wire one. In both cases we trust configuredOn and
// assume healthy — there is no signal to report degradation from.
func buildGitSyncHealth(syncer *gitsync.Sync, configuredOn bool) gitSyncHealth {
	if syncer == nil {
		return gitSyncHealth{Enabled: configuredOn, Healthy: true}
	}
	st := syncer.Status()
	out := gitSyncHealth{
		Enabled:   st.Enabled,
		Healthy:   st.Healthy,
		LastError: st.LastError,
	}
	if !st.LastErrorAt.IsZero() {
		t := st.LastErrorAt
		out.LastErrorAt = &t
	}
	if !st.LastCommitAt.IsZero() {
		t := st.LastCommitAt
		out.LastCommitAt = &t
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
