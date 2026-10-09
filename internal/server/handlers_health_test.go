package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/config"
	"github.com/gosidian/gosidian/internal/gitsync"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/vault"
)

// TestTopLevelStatus locks the BUG-015 contract at the unit level: the
// readiness gate is a pure function of core (index) health and takes NO
// git-sync input, so a degraded backup can never influence it.
func TestTopLevelStatus(t *testing.T) {
	if got, code := topLevelStatus(true); got != "ok" || code != http.StatusOK {
		t.Errorf("topLevelStatus(true) = %q,%d; want ok,200", got, code)
	}
	if got, code := topLevelStatus(false); got != "degraded" || code != http.StatusServiceUnavailable {
		t.Errorf("topLevelStatus(false) = %q,%d; want degraded,503", got, code)
	}
}

type healthBody struct {
	Status   string  `json:"status"`
	Vault    *string `json:"vault"`
	Notes    *int    `json:"notes"`
	MCPTools *int    `json:"mcp_tools"`
	GitSync  struct {
		Enabled bool   `json:"enabled"`
		Healthy bool   `json:"healthy"`
		LastErr string `json:"last_error"`
	} `json:"git_sync"`
}

// newTestServer builds a Server over a fresh temp vault + a healthy index
// (AllNotes succeeds), so the top-level status is driven purely by whatever we
// wire into gitSync.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "idx.db"))
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return New(vault.New(dir), idx)
}

func getHealth(t *testing.T, s *Server) (int, healthBody) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.handleHealth(rec, req)
	var body healthBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /healthz: %v (body=%s)", err, rec.Body.String())
	}
	return rec.Code, body
}

// degradedSync returns a real gitsync.Sync driven into the degraded state by a
// push that fails because no origin remote is configured. Deterministic and
// offline (no DNS/network), unlike pointing at an unreachable remote.
func degradedSync(t *testing.T) *gitsync.Sync {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	cfg := config.GitConfig{
		Enabled:     true,
		Branch:      "main",
		AuthorName:  "Test Bot",
		AuthorEmail: "bot@example.com",
		Debounce:    time.Hour, // long; we Flush() directly instead of waiting
		Push:        true,      // push with no origin remote → fails → degraded
	}
	s := gitsync.New(dir, cfg)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	s.TriggerCommit() // arm pending so Flush actually commits
	s.Flush()         // commit succeeds locally, push fails → recordError
	if st := s.Status(); st.Healthy {
		t.Fatalf("expected degraded sync, got healthy: %+v", st)
	}
	return s
}

// TestHandleHealth_DegradedGitSyncStaysReady is the BUG-015 regression guard:
// a degraded backup must NOT fail readiness. /healthz returns 200 + "ok" while
// still surfacing git_sync.healthy=false + a last_error for operators/alerts.
func TestHandleHealth_DegradedGitSyncStaysReady(t *testing.T) {
	s := newTestServer(t)
	s.SetBuildInfo("test", true)
	s.SetGitSync(degradedSync(t))
	s.SetHealthDetailsAuthorizer(func(*http.Request) bool { return true })

	code, body := getHealth(t, s)
	if code != http.StatusOK || body.Status != "ok" {
		t.Errorf("degraded gitsync flipped readiness: code=%d status=%q; want 200/ok", code, body.Status)
	}
	if !body.GitSync.Enabled || body.GitSync.Healthy {
		t.Errorf("git_sync should be enabled+unhealthy; got %+v", body.GitSync)
	}
	if body.GitSync.LastErr == "" {
		t.Error("expected git_sync.last_error to be populated on a degraded backup")
	}
}

// TestHandleHealth_HealthyOK is a sanity check: no gitsync wired + healthy
// index → 200/ok.
func TestHandleHealth_HealthyOK(t *testing.T) {
	s := newTestServer(t)
	s.SetBuildInfo("test", false)
	code, body := getHealth(t, s)
	if code != http.StatusOK || body.Status != "ok" {
		t.Errorf("healthy server = %d/%q; want 200/ok", code, body.Status)
	}
}

// /healthz says how many MCP tools are registered once the MCP server is
// wired, and leaves the field out before (IMP-030).
func TestHandleHealth_MCPTools(t *testing.T) {
	s := newTestServer(t)
	if _, body := getHealth(t, s); body.MCPTools != nil {
		t.Errorf("mcp_tools without an MCP server = %d", *body.MCPTools)
	}
	s.SetMCPToolCount(func() int { return 61 })
	code, body := getHealth(t, s)
	if code != http.StatusOK || body.MCPTools == nil || *body.MCPTools != 61 {
		t.Errorf("mcp_tools = %v (HTTP %d)", body.MCPTools, code)
	}
}

// An anonymous probe learns the status, the version, the tool count and
// whether the backup is healthy; the vault path, the note count and git's
// error are for the owner (IMP-160, S2-10).
func TestHandleHealth_DetailsNeedTheOwner(t *testing.T) {
	s := newTestServer(t)
	s.SetBuildInfo("test", true)
	s.SetGitSync(degradedSync(t))
	owner := false
	s.SetHealthDetailsAuthorizer(func(*http.Request) bool { return owner })

	code, body := getHealth(t, s)
	if code != http.StatusOK || body.Status != "ok" || !body.GitSync.Enabled || body.GitSync.Healthy {
		t.Fatalf("anonymous /healthz = %d %+v", code, body)
	}
	if body.Vault != nil || body.Notes != nil || body.GitSync.LastErr != "" {
		t.Errorf("anonymous /healthz shows the details: %+v", body)
	}

	owner = true
	_, body = getHealth(t, s)
	if body.Vault == nil || *body.Vault == "" || body.Notes == nil || body.GitSync.LastErr == "" {
		t.Errorf("owner /healthz lacks the details: %+v", body)
	}
}
