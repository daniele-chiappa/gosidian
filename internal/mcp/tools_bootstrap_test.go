package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/initprompt"
	"github.com/gosidian/gosidian/internal/projects"
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
		Project           string                 `json:"project"`
		HotMD             bootstrapFile          `json:"hot_md"`
		Readme            bootstrapFile          `json:"readme"`
		AgentMD           bootstrapFile          `json:"agent_md"`
		ActivePlans       []noteRef              `json:"active_plans"`
		AvailableSkills   []noteRef              `json:"available_skills"`
		AvailableAgents   []noteRef              `json:"available_agents"`
		RecentNotes       []recentNoteResponse   `json:"recent_notes"`
		Stats             bootstrapStats         `json:"stats"`
		Missing           []string               `json:"missing"`
		DirectivesVersion int                    `json:"directives_version"`
		DirectivesBlock   string                 `json:"directives_block"`
		StubVersion       int                    `json:"stub_version"`
		Capabilities      *bootstrapCapabilities `json:"capabilities"`
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
	if p.AgentMD.Present {
		t.Errorf("agent instruction file not seeded, should be absent")
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
	// IMP-050: the instruction file is expected to live in the agent's working
	// dir (stub model), so its vault-absence is flagged expected_external rather
	// than reported in `missing`. hot.md + README.md are seeded → missing empty.
	if !p.AgentMD.ExpectedExternal {
		t.Errorf("agent_md.expected_external should be true when no vault instruction file: %+v", p.AgentMD)
	}
	for _, m := range p.Missing {
		if m == "AGENTS.md" {
			t.Errorf("AGENTS.md must not appear in missing (IMP-050): %v", p.Missing)
		}
	}
	if len(p.Missing) != 0 {
		t.Errorf("missing should be empty (hot.md + README.md seeded), got %v", p.Missing)
	}

	if p.DirectivesVersion != initprompt.DirectivesVersion {
		t.Errorf("directives_version = %d, want %d", p.DirectivesVersion, initprompt.DirectivesVersion)
	}
	if p.StubVersion != initprompt.StubVersion {
		t.Errorf("stub_version = %d, want %d", p.StubVersion, initprompt.StubVersion)
	}
	// directives_block must be served and carry its own version marker + the
	// project name (ADR-010: directives delivered via bootstrap, not embedded).
	if p.DirectivesBlock == "" {
		t.Error("directives_block should be served, got empty")
	}
	if !strings.Contains(p.DirectivesBlock, "gosidian:directives") {
		t.Error("directives_block missing its version marker")
	}
	if !strings.Contains(p.DirectivesBlock, proj) {
		t.Error("directives_block should be rendered for the project")
	}

	// capabilities: always present, mirrors live config (flags off in the test
	// server) and carries the attachment surface incl. the /upload hint.
	if p.Capabilities == nil {
		t.Fatal("capabilities block should always be present")
	}
	if p.Capabilities.HTMLNotes || p.Capabilities.MediaNotes {
		t.Errorf("test server has html/media notes off, got %+v", p.Capabilities)
	}
	if p.Capabilities.Attachments.MaxMiB != 10 {
		t.Errorf("attachments.max_mib = %d, want 10", p.Capabilities.Attachments.MaxMiB)
	}
	if len(p.Capabilities.Attachments.Extensions) == 0 {
		t.Error("attachments.extensions should not be empty")
	}
	if !strings.Contains(p.Capabilities.Attachments.UploadEndpointHint, "/upload") {
		t.Error("attachments.upload_endpoint_hint should mention /upload")
	}
}

func TestMCP_Bootstrap_AutoLite(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	// hot.md past the oversize threshold → auto mode serves it lite.
	big := "---\ntitle: hot\ntype: index\n---\n\n# Hot\n\n## Focus\n\n" +
		strings.Repeat("filler line for the oversize threshold\n", 600)
	if res, _ := s.handleCreate(ctx, call(map[string]any{"path": "auto/hot.md", "content": big})); res.IsError {
		t.Fatalf("seed: %s", expectError(t, res))
	}

	parseHot := func(body string) bootstrapFile {
		t.Helper()
		var p struct {
			HotMD bootstrapFile `json:"hot_md"`
		}
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatal(err)
		}
		return p.HotMD
	}

	// Default (mode unset) → lite shape with auto_lite flag.
	res, _ := s.handleBootstrap(ctx, call(map[string]any{"project": "auto"}))
	hot := parseHot(resultText(t, res))
	if !hot.AutoLite || hot.Content != "" || len(hot.Headings) == 0 {
		t.Errorf("auto: want lite shape with auto_lite, got auto_lite=%v len(content)=%d headings=%d",
			hot.AutoLite, len(hot.Content), len(hot.Headings))
	}

	// Explicit full → body served, no auto_lite.
	res, _ = s.handleBootstrap(ctx, call(map[string]any{"project": "auto", "mode": "full"}))
	hot = parseHot(resultText(t, res))
	if hot.AutoLite || hot.Content == "" {
		t.Errorf("full: want body, got auto_lite=%v len=%d", hot.AutoLite, len(hot.Content))
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
	// IMP-050: AGENTS.md no longer counts as missing vault scaffold — only
	// hot.md + README.md remain.
	if len(p.Missing) != 2 {
		t.Errorf("expected 2 missing convention files (hot.md, README.md), got %v", p.Missing)
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

func TestMCP_Bootstrap_MaintenanceDigest(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	proj := "mnt"

	// Healthy project: a fresh note, no broken links, small hot.md.
	if res, _ := s.handleCreate(ctx, call(map[string]any{"path": proj + "/hot.md", "content": "---\ntitle: hot\n---\n\n# Hot\n"})); res.IsError {
		t.Fatalf("seed hot: %s", expectError(t, res))
	}
	if res, _ := s.handleCreate(ctx, call(map[string]any{"path": proj + "/ok.md", "content": "# ok\n\n[[mnt/hot]]\n"})); res.IsError {
		t.Fatalf("seed ok: %s", expectError(t, res))
	}

	boot := func() bootstrapMaintenance {
		t.Helper()
		res, _ := s.handleBootstrap(ctx, call(map[string]any{"project": proj}))
		var out struct {
			Maintenance *bootstrapMaintenance `json:"maintenance"`
		}
		if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
			t.Fatal(err)
		}
		if out.Maintenance == nil {
			t.Fatal("maintenance block missing from bootstrap payload")
		}
		return *out.Maintenance
	}

	m := boot()
	if m.Attention || m.BrokenLinks != 0 || m.HotOversize {
		t.Errorf("healthy project should not raise attention: %+v", m)
	}
	if m.StaleCutoffDays != maintenanceStaleCutoffDays || m.HotSize == 0 {
		t.Errorf("digest basics wrong: %+v", m)
	}

	// A broken wikilink flips attention on.
	if res, _ := s.handleCreate(ctx, call(map[string]any{"path": proj + "/broken.md", "content": "# b\n\n[[mnt/nowhere]]\n"})); res.IsError {
		t.Fatalf("seed broken: %s", expectError(t, res))
	}
	m = boot()
	if m.BrokenLinks != 1 || !m.Attention {
		t.Errorf("broken wikilink should raise attention: %+v", m)
	}

	// Hot oversize (threshold override) also flips attention on its own.
	if res, _ := s.handleDelete(ctx, call(map[string]any{"path": proj + "/broken.md"})); res.IsError {
		t.Fatalf("cleanup broken: %s", expectError(t, res))
	}
	s.SetLintHotOversizeLimit(8) // hot.md is bigger than 8 bytes
	m = boot()
	if !m.HotOversize || !m.Attention || m.BrokenLinks != 0 {
		t.Errorf("oversize hot should raise attention: %+v", m)
	}

	// Stale accounting: an old note counts, unless tagged status:done.
	old := time.Now().AddDate(0, 0, -maintenanceStaleCutoffDays-10).Unix()
	if err := s.index.Upsert(index.NoteDoc{Path: proj + "/ancient.md", Title: "ancient", Body: "x", ModTime: old, Size: 1}); err != nil {
		t.Fatal(err)
	}
	m = boot()
	if m.StaleCount != 1 {
		t.Errorf("stale_count = %d, want 1", m.StaleCount)
	}
	if err := s.index.Upsert(index.NoteDoc{Path: proj + "/ancient.md", Title: "ancient", Body: "---\ntags: [status:done]\n---\n\nx", ModTime: old, Size: 1}); err != nil {
		t.Fatal(err)
	}
	m = boot()
	if m.StaleCount != 0 {
		t.Errorf("status:done note should not count as stale, got %d", m.StaleCount)
	}

	// Attachment embeds are unresolved in the links table by design (the
	// index resolves notes only) — they must NOT count as broken links,
	// whether the file exists or not (that check is the lint rule's job).
	s.SetLintHotOversizeLimit(0)
	if res, _ := s.handleCreate(ctx, call(map[string]any{"path": proj + "/guide.md", "content": "# g\n\n![[deadbeef.webp]]\n"})); res.IsError {
		t.Fatalf("seed guide: %s", expectError(t, res))
	}
	m = boot()
	if m.BrokenLinks != 0 {
		t.Errorf("attachment embed inflated broken_links: %+v", m)
	}
}

// TestMCP_Bootstrap_TagVocabulary asserts the IMP-075 surfacing: the
// tag_vocabulary block appears only when the project opted in
// (use_tag_vocabulary) and reports the sanitized entries declared in
// memory/conventions.md frontmatter.
func TestMCP_Bootstrap_TagVocabulary(t *testing.T) {
	s, _, _ := newTestServer(t)
	proj := seedBootstrapVault(t, s)
	ctx := context.Background()

	if _, err := s.handleCreate(ctx, call(map[string]any{
		"path":    proj + "/memory/conventions.md",
		"content": "---\ntitle: conventions\ntags: [type:memory]\ntag_vocabulary: [\"cm:*\", anagrafica, \"bad entry\"]\n---\n\n# conventions\n",
	})); err != nil {
		t.Fatal(err)
	}

	type vocabResp struct {
		TagVocabulary *struct {
			Enabled bool     `json:"enabled"`
			Note    string   `json:"note"`
			Extra   []string `json:"extra"`
		} `json:"tag_vocabulary"`
	}
	bootstrap := func(t *testing.T) vocabResp {
		t.Helper()
		res, err := s.handleBootstrap(ctx, call(map[string]any{"project": proj}))
		if err != nil {
			t.Fatal(err)
		}
		var got vocabResp
		if err := json.Unmarshal([]byte(resultText(t, res)), &got); err != nil {
			t.Fatalf("parse: %v", err)
		}
		return got
	}

	// No opt-in (no projects store flag) → block absent.
	if got := bootstrap(t); got.TagVocabulary != nil {
		t.Fatalf("tag_vocabulary should be absent without opt-in: %+v", got.TagVocabulary)
	}

	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetProjects(pstore)
	if err := pstore.Set(proj, projects.Flags{UseTagVocabulary: true}); err != nil {
		t.Fatal(err)
	}

	got := bootstrap(t)
	if got.TagVocabulary == nil || !got.TagVocabulary.Enabled {
		t.Fatal("tag_vocabulary block missing with flag on")
	}
	if got.TagVocabulary.Note != proj+"/memory/conventions.md" {
		t.Errorf("note = %q", got.TagVocabulary.Note)
	}
	// "bad entry" (whitespace) is filtered by ValidVocabularyEntries.
	if len(got.TagVocabulary.Extra) != 2 || got.TagVocabulary.Extra[0] != "cm:*" || got.TagVocabulary.Extra[1] != "anagrafica" {
		t.Errorf("extra = %v, want [cm:* anagrafica]", got.TagVocabulary.Extra)
	}
}
