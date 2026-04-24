package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// seedBootstrapVault populates the test server with a small but realistic set
// of notes under the "gosidian" project so handleBootstrap has something to
// aggregate. Returns the project name used.
func seedBootstrapVault(t *testing.T, s *Server) string {
	t.Helper()
	ctx := context.Background()
	entries := []struct{ path, content string }{
		{"gosidian/hot.md", "---\ntitle: hot\ntype: index\n---\n\n# Hot\nwelcome"},
		{"gosidian/README.md", "---\ntitle: readme\ntype: index\n---\n\n# Readme"},
		{"gosidian/plans/active.md", "---\ntitle: active\ntype: plan\nstatus: in-progress\ndescription: a live task\nupdated: 2026-04-22\ntags: [type:plan, status:in-progress]\n---\n\n# Active"},
		{"gosidian/plans/old.md", "---\ntitle: old\ntype: plan\nstatus: done\ntags: [type:plan, status:done]\n---\n\n# Old"},
		{"gosidian/skills/build.md", "---\ntitle: build\ntype: skill\ndescription: how to build\ntags: [type:skill]\n---\n\n# build\n\n## Trigger phrase\n\n- rebuild the thing"},
		{"gosidian/agents/backend.md", "---\ntitle: backend-agent\ntype: agent\ntags: [type:agent]\n---\n\n# backend"},
		{"other/foreign.md", "---\ntitle: foreign\ntags: [type:plan, status:in-progress]\n---\n\nnot mine"},
	}
	for _, e := range entries {
		res, err := s.handleCreate(ctx, call(map[string]any{"path": e.path, "content": e.content}))
		if err != nil || (res != nil && res.IsError) {
			t.Fatalf("seed %q: err=%v res=%+v", e.path, err, res)
		}
	}
	return "gosidian"
}

func TestMCP_Bootstrap_HappyPath(t *testing.T) {
	s, _, _ := newTestServer(t)
	proj := seedBootstrapVault(t, s)

	res, err := s.handleBootstrap(context.Background(), call(map[string]any{"project": proj}))
	if err != nil {
		t.Fatal(err)
	}
	body := resultText(t, res)

	var p struct {
		Project         string              `json:"project"`
		HotMD           bootstrapFile       `json:"hot_md"`
		Readme          bootstrapFile       `json:"readme"`
		ClaudeMD        bootstrapFile       `json:"claude_md"`
		ActivePlans     []noteRef           `json:"active_plans"`
		AvailableSkills []noteRef           `json:"available_skills"`
		AvailableAgents []noteRef           `json:"available_agents"`
		RecentNotes     []recentNoteResponse `json:"recent_notes"`
		Stats           bootstrapStats      `json:"stats"`
		Missing         []string            `json:"missing"`
	}
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("parse: %v body=%s", err, body)
	}

	if p.Project != proj {
		t.Errorf("project = %q, want %q", p.Project, proj)
	}
	if !p.HotMD.Present || !p.Readme.Present {
		t.Errorf("hot/readme should be present: %+v %+v", p.HotMD, p.Readme)
	}
	if p.ClaudeMD.Present {
		t.Errorf("CLAUDE.md not seeded, should be absent")
	}
	if p.HotMD.ETag == "" {
		t.Errorf("hot_md etag should be non-empty")
	}

	// Active plans: only gosidian/plans/active.md (gosidian/plans/old.md is done;
	// other/foreign.md is outside project).
	if len(p.ActivePlans) != 1 || p.ActivePlans[0].Path != "gosidian/plans/active.md" {
		t.Errorf("active_plans = %+v, want only gosidian/plans/active.md", p.ActivePlans)
	}
	if len(p.AvailableSkills) != 1 || p.AvailableSkills[0].Path != "gosidian/skills/build.md" {
		t.Errorf("available_skills = %+v", p.AvailableSkills)
	}
	if len(p.AvailableAgents) != 1 {
		t.Errorf("available_agents = %+v", p.AvailableAgents)
	}
	if len(p.RecentNotes) == 0 || len(p.RecentNotes) > 5 {
		t.Errorf("recent_notes len = %d", len(p.RecentNotes))
	}
	if p.Stats.NotesCount < 6 {
		t.Errorf("expected at least 6 project notes, got %d", p.Stats.NotesCount)
	}
	if len(p.Stats.TopTags) == 0 {
		t.Errorf("top_tags should not be empty")
	}
	wantMissing := map[string]bool{"CLAUDE.md": true}
	for _, m := range p.Missing {
		delete(wantMissing, m)
	}
	if len(wantMissing) > 0 {
		t.Errorf("missing = %v, want to include CLAUDE.md", p.Missing)
	}
}

func TestMCP_Bootstrap_EmptyProject(t *testing.T) {
	s, _, _ := newTestServer(t)
	// No seed — project "void" has zero notes.

	res, err := s.handleBootstrap(context.Background(), call(map[string]any{"project": "void"}))
	if err != nil {
		t.Fatal(err)
	}
	body := resultText(t, res)

	var p struct {
		Missing []string       `json:"missing"`
		Stats   bootstrapStats `json:"stats"`
	}
	_ = json.Unmarshal([]byte(body), &p)
	if p.Stats.NotesCount != 0 {
		t.Errorf("expected 0 notes, got %d", p.Stats.NotesCount)
	}
	if len(p.Missing) != 3 {
		t.Errorf("expected 3 missing convention files, got %v", p.Missing)
	}
}

func TestMCP_Bootstrap_RequiresProject(t *testing.T) {
	s, _, _ := newTestServer(t)
	res, _ := s.handleBootstrap(context.Background(), call(map[string]any{}))
	msg := expectError(t, res)
	if msg == "" || msg == "unauthorized" {
		t.Errorf("expected project-required error, got %q", msg)
	}
}
