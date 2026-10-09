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

// handleHealth answers liveness/readiness probes. It is unauthenticated so
// orchestrators reach it without credentials, and then says only what a
// probe needs: status, version, the MCP tool count and whether git sync is
// healthy. With the owner's credentials (healthDetails) it adds the vault
// path, the note count and git's last error and times: git's stderr may
// carry the remote's URL, and none of it is for an anonymous caller
// (IMP-160, S2-10).
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	type payload struct {
		Status  string  `json:"status"`
		Version string  `json:"version,omitempty"`
		Vault   *string `json:"vault,omitempty"`
		Notes   *int    `json:"notes,omitempty"`
		// MCPTools is how many tools the MCP server registered (IMP-030):
		// after a rebuild it tells whether a new tool is in, without
		// reconnecting an MCP client, which keeps the list it first read.
		MCPTools int           `json:"mcp_tools,omitempty"`
		GitSync  gitSyncHealth `json:"git_sync"`
	}

	details := s.healthDetails != nil && s.healthDetails(r)
	git := buildGitSyncHealth(s.gitSync, s.gitSyncOn)
	if !details {
		git = gitSyncHealth{Enabled: git.Enabled, Healthy: git.Healthy}
	}
	out := payload{Version: s.version, GitSync: git}
	if s.mcpTools != nil {
		out.MCPTools = s.mcpTools()
	}

	// Liveness/readiness is decided by the *core* (can we read the index?),
	// NOT by the git-sync backup. A degraded backup still serves reads and
	// writes, so it must never flip the container to unhealthy — the
	// container HEALTHCHECK (cmd/gosidian/healthcheck.go) exits non-zero on
	// any status != "ok". Backup degradation stays observable via
	// git_sync.healthy below and the Prometheus gauge gosidian_gitsync_status
	// (=2). See BUG-015 and ADR-002 (push-only, fail-loud, manual reconcile).
	// A count, not every note loaded at each probe.
	notes, err := s.index.NoteCount()
	indexOK := err == nil
	status, code := topLevelStatus(indexOK)
	out.Status = status
	if details {
		root := s.vault.Root
		out.Vault = &root
		if indexOK {
			out.Notes = &notes
		}
	}
	writeJSON(w, code, out)
}

// topLevelStatus maps the *core* health (is the index readable?) to the
// /healthz top-level status and HTTP code consumed by the container
// HEALTHCHECK, which treats any status != "ok" as a failure. It deliberately
// takes no git-sync input: a degraded backup is reported in the git_sync
// sub-object and the Prometheus gauge, never in this readiness gate — a stuck
// backup must not mark the container unhealthy (BUG-015).
func topLevelStatus(indexOK bool) (string, int) {
	if !indexOK {
		return "degraded", http.StatusServiceUnavailable
	}
	return "ok", http.StatusOK
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
