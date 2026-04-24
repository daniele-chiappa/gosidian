package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCP_Handoff_CreateAndPending(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	// Create a handoff from go-backend → web-ui-htmx.
	res, err := s.handleCreateHandoff(ctx, call(map[string]any{
		"project":       "gosidian",
		"from_agent":    "go-backend",
		"to_agent":      "web-ui-htmx",
		"summary":       "Token ownership wired up; UI side not yet filtered.",
		"pending_items": []any{"add role badge in sidebar", "write 403 page"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := resultText(t, res)
	var created struct {
		Path      string `json:"path"`
		FromAgent string `json:"from_agent"`
		ToAgent   string `json:"to_agent"`
	}
	_ = json.Unmarshal([]byte(body), &created)
	if !strings.HasPrefix(created.Path, "gosidian/handoffs/") {
		t.Errorf("unexpected path: %q", created.Path)
	}
	if created.FromAgent != "go-backend" || created.ToAgent != "web-ui-htmx" {
		t.Errorf("agent fields wrong: %+v", created)
	}

	// Pending query for web-ui-htmx should surface the new note.
	res, _ = s.handlePendingHandoffs(ctx, call(map[string]any{
		"project":   "gosidian",
		"for_agent": "web-ui-htmx",
	}))
	body = resultText(t, res)
	var p struct {
		Handoffs []handoffEntry `json:"handoffs"`
	}
	_ = json.Unmarshal([]byte(body), &p)
	if len(p.Handoffs) != 1 || p.Handoffs[0].ToAgent != "web-ui-htmx" {
		t.Fatalf("expected 1 handoff to web-ui-htmx, got %+v", p.Handoffs)
	}

	// Pending query for a different agent should return empty.
	res, _ = s.handlePendingHandoffs(ctx, call(map[string]any{
		"project":   "gosidian",
		"for_agent": "mcp-server-dev",
	}))
	body = resultText(t, res)
	_ = json.Unmarshal([]byte(body), &p)
	if len(p.Handoffs) != 0 {
		t.Errorf("expected 0 handoffs for mcp-server-dev, got %+v", p.Handoffs)
	}
}

func TestMCP_Compact_KeepLastN(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	logBody := "---\ntitle: log\n---\n\n# Log\n\n" +
		"## 2026-01-01 — first\n\nbody 1\n\n" +
		"## 2026-02-01 — second\n\nbody 2\n\n" +
		"## 2026-03-01 — third\n\nbody 3\n\n" +
		"## 2026-04-01 — fourth\n\nbody 4\n\n"
	if _, err := s.handleCreate(ctx, call(map[string]any{"path": "log.md", "content": logBody})); err != nil {
		t.Fatal(err)
	}

	// Dry-run first.
	res, _ := s.handleCompact(ctx, call(map[string]any{
		"path":            "log.md",
		"keep_last_n":     float64(2),
		"archive_summary": "- earlier entries collapsed",
		"dry_run":         true,
	}))
	body := resultText(t, res)
	var plan compactResult
	_ = json.Unmarshal([]byte(body), &plan)
	if !plan.DryRun {
		t.Error("expected dry_run flag true")
	}
	if plan.OriginalEntries != 4 || plan.KeptEntries != 2 || plan.ArchivedEntries != 2 {
		t.Errorf("unexpected counts: %+v", plan)
	}

	// Real compaction.
	res, _ = s.handleCompact(ctx, call(map[string]any{
		"path":            "log.md",
		"keep_last_n":     float64(2),
		"archive_summary": "- earlier entries collapsed",
	}))
	body = resultText(t, res)
	_ = json.Unmarshal([]byte(body), &plan)
	if plan.DryRun || plan.ArchivedEntries != 2 {
		t.Errorf("unexpected result: %+v", plan)
	}

	// Verify the file content.
	res, _ = s.handleGet(ctx, call(map[string]any{"path": "log.md"}))
	body = resultText(t, res)
	if !strings.Contains(body, "archived 2 entries before") {
		t.Errorf("missing archive marker: %s", body)
	}
	if !strings.Contains(body, "2026-03-01") || !strings.Contains(body, "2026-04-01") {
		t.Errorf("expected last 2 entries preserved: %s", body)
	}
	if strings.Contains(body, "2026-01-01") || strings.Contains(body, "2026-02-01") {
		t.Errorf("expected first 2 entries archived: %s", body)
	}
}

func TestMCP_Compact_NoopWhenBelowLimit(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	logBody := "---\ntitle: short\n---\n\n## 2026-01-01 — alone\n\none\n"
	if _, err := s.handleCreate(ctx, call(map[string]any{"path": "short.md", "content": logBody})); err != nil {
		t.Fatal(err)
	}
	res, _ := s.handleCompact(ctx, call(map[string]any{
		"path":            "short.md",
		"keep_last_n":     float64(5),
		"archive_summary": "n/a",
	}))
	body := resultText(t, res)
	var plan compactResult
	_ = json.Unmarshal([]byte(body), &plan)
	if !plan.Noop {
		t.Errorf("expected noop, got: %+v", plan)
	}
}

func TestMCP_Compact_RejectsWithoutEntries(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	// A plain note with no ## YYYY-MM-DD headings.
	if _, err := s.handleCreate(ctx, call(map[string]any{"path": "plain.md", "content": "# Heading\n\nbody\n"})); err != nil {
		t.Fatal(err)
	}
	res, _ := s.handleCompact(ctx, call(map[string]any{
		"path":            "plain.md",
		"keep_last_n":     float64(1),
		"archive_summary": "nothing",
	}))
	if msg := expectError(t, res); !strings.Contains(msg, "not compatible") {
		t.Errorf("expected incompat error, got: %q", msg)
	}
}

func TestMCP_SelfStats(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	res, err := s.handleSelfStats(ctx, call(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	body := resultText(t, res)
	var p struct {
		Token     selfStatsTokenInfo `json:"token"`
		RateLimit WriteLimiterStats  `json:"rate_limit"`
	}
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("parse: %v body=%s", err, body)
	}
	if p.Token.ID != "admin" {
		t.Errorf("expected admin token, got %+v", p.Token)
	}
	if p.RateLimit.MaxPerMinute <= 0 {
		t.Errorf("expected positive rate limit, got %+v", p.RateLimit)
	}
	if p.RateLimit.Used != 0 {
		t.Errorf("expected zero usage right after boot, got %d", p.RateLimit.Used)
	}
}

func TestMCP_Outlinks_CrossProjectFlag(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	if _, err := s.handleCreateProject(ctx, call(map[string]any{"name": "projA"})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.handleCreateProject(ctx, call(map[string]any{"name": "projB"})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.handleCreate(ctx, call(map[string]any{"path": "projB/foreign.md", "content": "# foreign"})); err != nil {
		t.Fatal(err)
	}
	src := "projA/src.md"
	if _, err := s.handleCreate(ctx, call(map[string]any{
		"path":    src,
		"content": "# src\n\nLinks [[projB/foreign]] and [[projA/local]].\n",
	})); err != nil {
		t.Fatal(err)
	}
	// Create the local target so it resolves too.
	if _, err := s.handleCreate(ctx, call(map[string]any{"path": "projA/local.md", "content": "# local"})); err != nil {
		t.Fatal(err)
	}
	if err := s.index.ResolveAll(); err != nil {
		t.Fatal(err)
	}

	// Without the flag, no cross_project field.
	res, _ := s.handleOutlinks(ctx, call(map[string]any{"path": src}))
	body := resultText(t, res)
	if strings.Contains(body, "cross_project") {
		t.Errorf("cross_project should not leak without flag: %s", body)
	}

	// With the flag, projB/foreign must be flagged.
	res, _ = s.handleOutlinks(ctx, call(map[string]any{"path": src, "include_cross_project": true}))
	body = resultText(t, res)
	var p struct {
		Outlinks []outlinkEntry `json:"outlinks"`
	}
	_ = json.Unmarshal([]byte(body), &p)
	sawCross := false
	sawLocal := false
	for _, o := range p.Outlinks {
		if strings.Contains(o.ResolvedPath, "projB/") {
			if !o.CrossProject {
				t.Errorf("expected cross_project true on %+v", o)
			}
			sawCross = true
		}
		if strings.Contains(o.ResolvedPath, "projA/local") {
			if o.CrossProject {
				t.Errorf("local link should not be cross_project: %+v", o)
			}
			sawLocal = true
		}
	}
	if !sawCross || !sawLocal {
		t.Errorf("expected to see both cross-project and local outlinks, got %+v", p.Outlinks)
	}
}
