package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/gosidian/gosidian/internal/projects"
)

// TestMCP_Lint_RulesEchoReflectsRequest guards BUG-013: the response `rules`
// field must report the rules actually run, not a hardcoded default. When an
// explicit (e.g. opt-in) rule is requested it must be echoed; with no rules the
// default set is echoed.
func TestMCP_Lint_RulesEchoReflectsRequest(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	// One note so the run has something to scan.
	if _, err := s.handleCreate(ctx, call(map[string]any{
		"path":    "proj/a.md",
		"content": "---\ntitle: a\ntags: [proj]\n---\n\n# a\n",
	})); err != nil {
		t.Fatal(err)
	}

	type lintResp struct {
		Rules []string `json:"rules"`
	}
	parse := func(t *testing.T, body string) lintResp {
		t.Helper()
		var got lintResp
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("parse: %v body=%s", err, body)
		}
		return got
	}

	// Explicit opt-in rule → echo must reflect exactly what was requested.
	res, err := s.handleLint(ctx, call(map[string]any{
		"project": "proj",
		"rules":   []any{"unlinked-mentions"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	got := parse(t, resultText(t, res))
	if len(got.Rules) != 1 || got.Rules[0] != "unlinked-mentions" {
		t.Fatalf("rules echo = %v, want [unlinked-mentions]", got.Rules)
	}

	// No rules → echo the default set (starts with broken-wikilink, excludes
	// the opt-in unlinked-mentions).
	res, err = s.handleLint(ctx, call(map[string]any{"project": "proj"}))
	if err != nil {
		t.Fatal(err)
	}
	got = parse(t, resultText(t, res))
	if len(got.Rules) == 0 || got.Rules[0] != "broken-wikilink" {
		t.Fatalf("default rules echo = %v, want default set starting broken-wikilink", got.Rules)
	}
	for _, r := range got.Rules {
		if r == "unlinked-mentions" {
			t.Fatalf("default echo must not include opt-in unlinked-mentions: %v", got.Rules)
		}
	}
}

// TestMCP_Lint_ProjectTagVocabulary wires the IMP-075 chain end-to-end: the
// use_tag_vocabulary project flag arms the linter, which then honours the
// vocabulary declared in <project>/memory/conventions.md frontmatter.
func TestMCP_Lint_ProjectTagVocabulary(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetProjects(pstore)

	seeds := []struct{ path, content string }{
		{"proj/memory/conventions.md", "---\ntitle: conventions\ntags: [proj, type:memory]\ntag_vocabulary: [\"cm:*\", anagrafica]\n---\n\n# conventions\n"},
		{"proj/n.md", "---\ntitle: n\ntags: [proj, cm:clienti, anagrafica]\n---\n\n# n\n"},
	}
	for _, e := range seeds {
		if _, err := s.handleCreate(ctx, call(map[string]any{"path": e.path, "content": e.content})); err != nil {
			t.Fatal(err)
		}
	}

	countTagIssues := func(t *testing.T) int {
		t.Helper()
		res, err := s.handleLint(ctx, call(map[string]any{
			"project": "proj",
			"rules":   []any{"frontmatter-tag-unknown"},
		}))
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Issues []struct {
				Rule string `json:"rule"`
			} `json:"issues"`
		}
		if err := json.Unmarshal([]byte(resultText(t, res)), &got); err != nil {
			t.Fatalf("parse: %v", err)
		}
		return len(got.Issues)
	}

	// Flag off: cm:clienti + anagrafica flagged.
	if n := countTagIssues(t); n != 2 {
		t.Fatalf("flag off: expected 2 tag issues, got %d", n)
	}

	// Flag on: declaration takes effect, zero issues.
	if err := pstore.Set("proj", projects.Flags{UseTagVocabulary: true}); err != nil {
		t.Fatal(err)
	}
	if n := countTagIssues(t); n != 0 {
		t.Fatalf("flag on: expected 0 tag issues, got %d", n)
	}
}
