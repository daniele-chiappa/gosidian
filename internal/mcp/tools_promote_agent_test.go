package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func TestPromoteAgent(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	foreign := "---\n" +
		"name: rc-database\n" +
		"description: DB ops for rc\n" +
		"tools: Read, Bash, mcp__gosidian__memory_get\n" +
		"---\n\n" +
		"You are the rc database engineer. Be careful with migrations.\n"

	res, _ := s.handlePromoteAgent(ctx, call(map[string]any{
		"project": "rc", "slug": "rc-database", "content": foreign, "profile": "claude",
	}))
	var p struct {
		Path   string `json:"path"`
		Anchor *struct {
			Path      string `json:"path"`
			Content   string `json:"content"`
			Canonical string `json:"canonical"`
		} `json:"anchor"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Path != "rc/agents/rc-database.md" {
		t.Errorf("path = %q", p.Path)
	}

	note, err := s.vault.Load("rc/agents/rc-database.md")
	if err != nil {
		t.Fatalf("canonical note not created: %v", err)
	}
	body := string(note.Content)
	for _, want := range []string{
		"type: agent",
		"tags: [rc, type:agent]",
		"description: DB ops for rc",
		"You are the rc database engineer",
		"harness:",
		"tools: [Read, Bash, mcp__gosidian__memory_get]",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("canonical note missing %q:\n%s", want, body)
		}
	}

	if p.Anchor == nil {
		t.Fatal("expected anchor in response")
	}
	if p.Anchor.Path != ".claude/agents/rc-database.md" {
		t.Errorf("anchor path = %q", p.Anchor.Path)
	}
	if p.Anchor.Canonical != "rc/agents/rc-database.md" {
		t.Errorf("anchor canonical = %q", p.Anchor.Canonical)
	}
	if !strings.Contains(p.Anchor.Content, `memory_get({ path: "rc/agents/rc-database.md" })`) {
		t.Errorf("anchor missing canonical pull:\n%s", p.Anchor.Content)
	}

	// Idempotency guard: promoting again over an existing canonical note
	// errors, and the error teaches the adopt flag (IMP-071).
	res2, _ := s.handlePromoteAgent(ctx, call(map[string]any{
		"project": "rc", "slug": "rc-database", "content": foreign,
	}))
	if !res2.IsError {
		t.Error("expected error promoting an already-existing canonical agent")
	}
	var errText strings.Builder
	for _, c := range res2.Content {
		if tc, ok := c.(mcplib.TextContent); ok {
			errText.WriteString(tc.Text)
		}
	}
	if !strings.Contains(errText.String(), "adopt_into_existing") {
		t.Errorf("exists error should hint at adopt_into_existing: %q", errText.String())
	}
}

func TestPromoteAgent_AdoptIntoExisting(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	foreign := "---\n" +
		"name: alpha-name\n" +
		"description: foreign desc\n" +
		"tools: Read, Bash\n" +
		"---\n\n" +
		"Foreign role text with unique nuggets.\n"

	type adoptResp struct {
		Path                 string `json:"path"`
		AdoptedIntoExisting  bool   `json:"adopted_into_existing"`
		ForeignBodyForReview string `json:"foreign_body_for_review"`
		Review               string `json:"review"`
		Anchor               *struct {
			Path      string `json:"path"`
			Content   string `json:"content"`
			Canonical string `json:"canonical"`
		} `json:"anchor"`
	}
	adopt := func(slug string) adoptResp {
		t.Helper()
		res, _ := s.handlePromoteAgent(ctx, call(map[string]any{
			"project": "rc", "slug": slug, "content": foreign,
			"profile": "claude", "adopt_into_existing": true,
		}))
		if res.IsError {
			t.Fatalf("adopt failed: %s", resultText(t, res))
		}
		var p adoptResp
		if err := json.Unmarshal([]byte(resultText(t, res)), &p); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return p
	}

	// 1. Canonical without harness block → block inserted, body untouched,
	// foreign body echoed back for the fold-check.
	_, _ = s.handleCreate(ctx, call(map[string]any{
		"path":    "rc/agents/alpha.md",
		"content": "---\ntitle: Alpha\ndescription: canonical desc\ntags: [rc, type:agent]\ntype: agent\n---\n\nCanonical role text, source of truth.\n",
	}))
	p := adopt("alpha")
	if !p.AdoptedIntoExisting || p.Path != "rc/agents/alpha.md" {
		t.Errorf("adopted=%v path=%q", p.AdoptedIntoExisting, p.Path)
	}
	if !strings.Contains(p.ForeignBodyForReview, "unique nuggets") || p.Review == "" {
		t.Errorf("missing foreign body / review instruction: %+v", p)
	}
	note, err := s.vault.Load("rc/agents/alpha.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(note.Content)
	for _, want := range []string{
		"Canonical role text, source of truth.",
		"harness:",
		"name: alpha-name",
		"tools: [Read, Bash]",
		"description: canonical desc",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("canonical missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "unique nuggets") {
		t.Errorf("foreign body must NOT be merged server-side:\n%s", body)
	}
	if p.Anchor == nil || p.Anchor.Canonical != "rc/agents/alpha.md" {
		t.Fatalf("anchor = %+v", p.Anchor)
	}
	if !strings.Contains(p.Anchor.Content, "name: alpha-name") {
		t.Errorf("anchor should reflect the inserted harness name:\n%s", p.Anchor.Content)
	}

	// 2. Canonical WITH harness block → note untouched (existing harness wins).
	pre := "---\ntitle: Beta\ntags: [rc, type:agent]\ntype: agent\nharness:\n  name: beta-canonical\n---\n\nBeta canonical body.\n"
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "rc/agents/beta.md", "content": pre}))
	p = adopt("beta")
	note, err = s.vault.Load("rc/agents/beta.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(note.Content) != pre {
		t.Errorf("canonical with harness must stay byte-identical:\n%s", note.Content)
	}
	if p.Anchor == nil || !strings.Contains(p.Anchor.Content, "name: beta-canonical") {
		t.Fatalf("anchor should come from the existing harness: %+v", p.Anchor)
	}

	// 3. Flag with NO existing canonical → normal promote (no-op flag, safe retry).
	p = adopt("gamma")
	if p.AdoptedIntoExisting {
		t.Error("fresh canonical: adopted_into_existing should be false")
	}
	if _, err := s.vault.Load("rc/agents/gamma.md"); err != nil {
		t.Errorf("fresh canonical not created: %v", err)
	}
}

// Claude Code's native subagent format writes tools as a YAML inline list;
// promoting it must not nest the brackets (BUG-045a).
func TestPromoteAgent_InlineToolsList(t *testing.T) {
	s, _, _ := newTestServer(t)
	foreign := "---\nname: lister\ndescription: d\ntools: [Read, Edit, \"Bash\"]\n---\nbody\n"
	res, _ := s.handlePromoteAgent(context.Background(), call(map[string]any{
		"project": "rc", "slug": "lister", "content": foreign, "profile": "claude",
	}))
	_ = resultText(t, res)
	note, err := s.vault.Load("rc/agents/lister.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(note.Content)
	if !strings.Contains(body, "tools: [Read, Edit, Bash]") || strings.Contains(body, "[[") {
		t.Fatalf("tools list mangled:\n%s", body)
	}
}

// adopt_into_existing must not add a second harness: key when the canonical
// note already carries one as an inline value (BUG-045b).
func TestPromoteAgent_AdoptDoesNotDuplicateInlineHarness(t *testing.T) {
	s, _, dir := newTestServer(t)
	writeVaultFile(t, dir, "rc/agents/inl.md", "---\ntitle: inl\ntype: agent\nharness: legacy\ntags: [rc, type:agent]\n---\n\ncanonical body\n")
	foreign := "---\nname: inl-foreign\ntools: Read\n---\nforeign body\n"
	for i := 0; i < 2; i++ {
		res, _ := s.handlePromoteAgent(context.Background(), call(map[string]any{
			"project": "rc", "slug": "inl", "content": foreign, "adopt_into_existing": true,
		}))
		_ = resultText(t, res)
	}
	note, _ := s.vault.Load("rc/agents/inl.md")
	if n := strings.Count(string(note.Content), "harness:"); n != 1 {
		t.Fatalf("harness key count = %d, want 1:\n%s", n, note.Content)
	}
}
