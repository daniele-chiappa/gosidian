package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedDiscoveryVault creates a small project with plans, skills, pinned and
// stale notes. Mirrors the bootstrap seed but broader so each discovery tool
// has material.
func seedDiscoveryVault(t *testing.T, s *Server) string {
	t.Helper()
	ctx := context.Background()
	notes := []struct{ path, content string }{
		{"proj/plans/a.md", "---\ntitle: a\ntype: plan\nstatus: in-progress\ndescription: alpha\ntags: [type:plan, status:in-progress]\n---\n\n# a"},
		{"proj/plans/b.md", "---\ntitle: b\ntype: plan\nstatus: done\ntags: [type:plan, status:done]\n---\n\n# b"},
		{"proj/plans/c.md", "---\ntitle: c\ntype: plan\nstatus: draft\ntags: [type:plan, status:draft]\n---\n\n# c"},
		{"proj/skills/build.md", "---\ntitle: build\ntype: skill\ndescription: rebuild the binary\ntags: [type:skill]\n---\n\n# build\n\n## Trigger phrase\n\n- \"rebuild the binary\"\n- ricompila"},
		{"proj/skills/deploy.md", "---\ntitle: deploy\ntype: skill\ntags: [type:skill]\n---\n\n# deploy\n\n## Trigger phrase\n\n- rollout"},
		{"proj/memory/focus.md", "---\ntitle: focus\ntags: [pinned, type:memory]\n---\n\n# focus"},
		{"proj/memory/old.md", "---\ntitle: old\ntags: [type:memory]\n---\n\n# old"},
	}
	for _, n := range notes {
		res, err := s.handleCreate(ctx, call(map[string]any{"path": n.path, "content": n.content}))
		if err != nil || (res != nil && res.IsError) {
			t.Fatalf("seed %q: err=%v res=%+v", n.path, err, res)
		}
	}
	return "proj"
}

func TestMCP_Plans_Unfiltered(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedDiscoveryVault(t, s)

	res, _ := s.handlePlans(context.Background(), call(map[string]any{"project": "proj"}))
	body := resultText(t, res)
	var p struct {
		Plans []planEntry `json:"plans"`
	}
	_ = json.Unmarshal([]byte(body), &p)
	if len(p.Plans) != 3 {
		t.Fatalf("expected 3 plans, got %d: %+v", len(p.Plans), p.Plans)
	}
	statuses := map[string]int{}
	for _, pl := range p.Plans {
		statuses[pl.Status]++
	}
	for _, want := range []string{"in-progress", "done", "draft"} {
		if statuses[want] != 1 {
			t.Errorf("missing status %q in plans", want)
		}
	}
}

func TestMCP_Plans_FilterByStatus(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedDiscoveryVault(t, s)

	res, _ := s.handlePlans(context.Background(), call(map[string]any{"project": "proj", "status": "in-progress"}))
	body := resultText(t, res)
	var p struct {
		Plans []planEntry `json:"plans"`
	}
	_ = json.Unmarshal([]byte(body), &p)
	if len(p.Plans) != 1 || p.Plans[0].Status != "in-progress" {
		t.Errorf("expected 1 in-progress plan, got %+v", p.Plans)
	}
	if p.Plans[0].Description != "alpha" {
		t.Errorf("description not captured: %+v", p.Plans[0])
	}
}

func TestMCP_Plans_RejectsUnknownStatus(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedDiscoveryVault(t, s)
	res, _ := s.handlePlans(context.Background(), call(map[string]any{"project": "proj", "status": "nonsense"}))
	if msg := expectError(t, res); msg == "" {
		t.Error("expected error for unknown status")
	}
}

func TestMCP_Skills_Unfiltered(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedDiscoveryVault(t, s)

	res, _ := s.handleSkills(context.Background(), call(map[string]any{"project": "proj"}))
	body := resultText(t, res)
	var p struct {
		Skills []skillEntry `json:"skills"`
	}
	_ = json.Unmarshal([]byte(body), &p)
	if len(p.Skills) != 2 {
		t.Errorf("expected 2 skills, got %d: %+v", len(p.Skills), p.Skills)
	}
}

func TestMCP_Skills_FilterByTrigger(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedDiscoveryVault(t, s)

	res, _ := s.handleSkills(context.Background(), call(map[string]any{"project": "proj", "trigger_phrase": "rollout"}))
	body := resultText(t, res)
	var p struct {
		Skills []skillEntry `json:"skills"`
	}
	_ = json.Unmarshal([]byte(body), &p)
	if len(p.Skills) != 1 || p.Skills[0].Path != "proj/skills/deploy.md" {
		t.Errorf("expected only deploy skill, got %+v", p.Skills)
	}
}

func TestMCP_Pinned(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedDiscoveryVault(t, s)

	res, _ := s.handlePinned(context.Background(), call(map[string]any{"project": "proj"}))
	body := resultText(t, res)
	var p struct {
		Notes []noteRef `json:"notes"`
	}
	_ = json.Unmarshal([]byte(body), &p)
	if len(p.Notes) != 1 || p.Notes[0].Path != "proj/memory/focus.md" {
		t.Errorf("expected pinned focus.md, got %+v", p.Notes)
	}
}

func TestMCP_Stale(t *testing.T) {
	s, v, _ := newTestServer(t)
	proj := seedDiscoveryVault(t, s)

	// Backdate proj/memory/old.md by 60 days so it's "stale" relative to the
	// default older_than=30d.
	oldPath := filepath.Join(v.Root, "proj/memory/old.md")
	past := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(oldPath, past, past); err != nil {
		t.Fatal(err)
	}
	// Rescan so the index picks up the new mtime.
	if err := v.ScanInto(s.index); err != nil {
		t.Fatal(err)
	}

	// A finished plan backdated the same way is stale too, but closed.
	donePath := filepath.Join(v.Root, "proj/plans/b.md")
	if err := os.Chtimes(donePath, past, past); err != nil {
		t.Fatal(err)
	}
	if err := v.ScanInto(s.index); err != nil {
		t.Fatal(err)
	}

	list := func(args map[string]any) []staleNoteResponse {
		t.Helper()
		args["project"] = proj
		args["older_than"] = "30d"
		res, _ := s.handleStale(context.Background(), call(args))
		var p struct {
			Notes []staleNoteResponse `json:"notes"`
		}
		if err := json.Unmarshal([]byte(resultText(t, res)), &p); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return p.Notes
	}

	got := list(map[string]any{})
	if len(got) != 2 {
		t.Fatalf("expected old.md and b.md, got %+v", got)
	}
	byPath := map[string]bool{}
	for _, n := range got {
		byPath[n.Path] = n.Closed
	}
	if closed, ok := byPath["proj/plans/b.md"]; !ok || !closed {
		t.Errorf("status:done plan should be listed as closed: %+v", got)
	}
	if closed, ok := byPath["proj/memory/old.md"]; !ok || closed {
		t.Errorf("old.md should be listed as open: %+v", got)
	}

	got = list(map[string]any{"exclude_closed": true})
	if len(got) != 1 || got[0].Path != "proj/memory/old.md" || got[0].Closed {
		t.Errorf("exclude_closed should leave only old.md, got %+v", got)
	}
}

func TestMCP_Stale_RejectInvalidOlderThan(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedDiscoveryVault(t, s)
	res, _ := s.handleStale(context.Background(), call(map[string]any{"project": "proj", "older_than": "xyz"}))
	if msg := expectError(t, res); msg == "" {
		t.Error("expected error for invalid duration")
	}
}

func TestMCP_Discovery_ProjectRequired(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedDiscoveryVault(t, s)
	ctx := context.Background()
	empty := call(map[string]any{})

	// All four discovery tools go through resolveProject; an admin token
	// (implicit, store is empty in newTestServer) with no project argument
	// must see a "project is required" error rather than scanning the vault.
	res, _ := s.handlePlans(ctx, empty)
	if expectError(t, res) == "" {
		t.Error("handlePlans should reject missing project")
	}
	res, _ = s.handleSkills(ctx, empty)
	if expectError(t, res) == "" {
		t.Error("handleSkills should reject missing project")
	}
	res, _ = s.handlePinned(ctx, empty)
	if expectError(t, res) == "" {
		t.Error("handlePinned should reject missing project")
	}
	res, _ = s.handleStale(ctx, empty)
	if expectError(t, res) == "" {
		t.Error("handleStale should reject missing project")
	}
}

// A plan that states its status only as a tag (147 notes of a real vault
// do) is found by the status filter and reports that status; a status field
// wins over a disagreeing tag.
func TestMCP_Plans_StatusFromTag(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedDiscoveryVault(t, s)
	ctx := context.Background()
	for path, content := range map[string]string{
		"proj/plans/tagonly.md": "---\ntitle: tagonly\ntype: plan\nupdated: 2026-09-26\ntags: [type:plan, status:draft]\n---\n\n# t",
		"proj/plans/both.md":    "---\ntitle: both\ntype: plan\nstatus: done\ntags: [type:plan, status:draft]\n---\n\n# b",
	} {
		if res, _ := s.handleCreate(ctx, call(map[string]any{"path": path, "content": content})); res.IsError {
			t.Fatalf("seed %s: %s", path, expectError(t, res))
		}
	}
	res, _ := s.handlePlans(ctx, call(map[string]any{"project": "proj", "status": "draft"}))
	var p struct {
		Plans []planEntry `json:"plans"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &p); err != nil {
		t.Fatal(err)
	}
	got := map[string]planEntry{}
	for _, pl := range p.Plans {
		got[pl.Path] = pl
	}
	if len(p.Plans) != 2 || got["proj/plans/tagonly.md"].Status != "draft" || got["proj/plans/tagonly.md"].Updated != "2026-09-26" {
		t.Errorf("draft plans: %+v", p.Plans)
	}
	if _, ok := got["proj/plans/both.md"]; ok {
		t.Error("the status field (done) must win over the draft tag")
	}
}
