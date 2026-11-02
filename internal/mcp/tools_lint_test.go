package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/lint"
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

// The MCP lint flags a skill that memory_get would truncate, with the same
// threshold memory_get uses.
func TestMCP_Lint_SkillOversizeUsesGetCap(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	body := "---\ntitle: huge\ntype: skill\ntags: [proj, type:skill]\n---\n\n# huge\n\n"
	for _, size := range []int{getBodySoftCap - len(body) - 10, getBodySoftCap} {
		content := body + strings.Repeat("x", size)
		path := fmt.Sprintf("proj/skills/s%d.md", size)
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": path, "content": content})); err != nil || res.IsError {
			t.Fatalf("create %s: %v %+v", path, err, res)
		}
	}
	res, err := s.handleLint(ctx, call(map[string]any{"project": "proj", "rules": []any{"skill-oversize"}}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)
	if strings.Count(text, `"rule":"skill-oversize"`) != 1 || !strings.Contains(text, fmt.Sprintf("proj/skills/s%d.md", getBodySoftCap)) {
		t.Errorf("want only the note over %d bytes flagged: %s", getBodySoftCap, text)
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

// toolDescriptions returns, from tools/list, a tool's description and its
// parameters' descriptions by name.
func toolDescriptions(t *testing.T, s *Server, name string) (string, map[string]string) {
	t.Helper()
	var resp struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				InputSchema struct {
					Properties map[string]struct {
						Description string `json:"description"`
					} `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(rpcMessage(t, s, context.Background(), "tools/list", `{}`)), &resp); err != nil {
		t.Fatal(err)
	}
	for _, tl := range resp.Result.Tools {
		if tl.Name == name {
			params := map[string]string{}
			for p, v := range tl.InputSchema.Properties {
				params[p] = v.Description
			}
			return tl.Description, params
		}
	}
	t.Fatalf("%s is not in tools/list", name)
	return "", nil
}

// memory_lint names every rule it runs by default, with what it checks, so
// an agent can pick them: the list follows the linter's (M1.3).
func TestLintTool_NamesEveryDefaultRule(t *testing.T) {
	s, _, _ := newTestServer(t)
	desc, params := toolDescriptions(t, s, "memory_lint")
	for _, rule := range lint.DefaultRules() {
		if !strings.Contains(desc, rule) {
			t.Errorf("the description does not name %s", rule)
		}
	}
	if !strings.Contains(params["rules"], "unlinked-mentions") {
		t.Errorf("the rules parameter does not name the opt-in rule: %s", params["rules"])
	}
}

// memory_ingest says what a file becomes where its format is off, as the
// server does: an attachment for a .csv or an image, a refusal for a .html
// (M1.3).
func TestIngestTool_SaysWhatAFormatOffDoes(t *testing.T) {
	s, _, _ := newTestServer(t)
	desc, _ := toolDescriptions(t, s, "memory_ingest")
	for _, want := range []string{"where those formats are on, else a plain attachment", ".html too where html notes are on (else refused)", "(.html only where html notes are on, else left out)"} {
		if !strings.Contains(desc, want) {
			t.Errorf("the description lacks %q", want)
		}
	}
}
