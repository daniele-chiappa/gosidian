package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/scaffold"
)

// seedTemplatesForTest copies the binary-embedded templates into the
// test vault so handlers can read them from <vault>/.gosidian/templates/
// just like production. Returns the list of template names seeded.
func seedTemplatesForTest(t *testing.T, vaultRoot string) []string {
	t.Helper()
	seeded, err := scaffold.SeedTemplates(vaultRoot, embeddedTemplates, EmbeddedTemplatesRoot)
	if err != nil {
		t.Fatalf("seed templates: %v", err)
	}
	if len(seeded) < 3 {
		t.Fatalf("expected >=3 seeded templates, got %v", seeded)
	}
	return seeded
}

func TestMCP_ListBootstrapTemplates(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedTemplatesForTest(t, s.vault.Root)

	res, err := s.handleListBootstrapTemplates(context.Background(), call(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	body := resultText(t, res)
	var payload struct {
		Templates []templateEntry `json:"templates"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("parse: %v body=%s", err, body)
	}
	names := map[string]bool{}
	for _, e := range payload.Templates {
		names[e.Name] = true
		if e.Description == "" {
			t.Errorf("template %q missing description", e.Name)
		}
		if e.Prompt == "" {
			t.Errorf("template %q missing prompt", e.Name)
		}
		if e.FileCount == 0 {
			t.Errorf("template %q has zero files", e.Name)
		}
	}
	for _, want := range []string{"karpathy-wiki", "minimal", "team"} {
		if !names[want] {
			t.Errorf("expected template %q in listing, got %+v", want, names)
		}
	}
}

func TestMCP_ProjectScaffold_DefaultTemplate(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedTemplatesForTest(t, s.vault.Root)

	res, err := s.handleProjectScaffold(context.Background(), call(map[string]any{
		"project": "alpha",
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := resultText(t, res)
	var p scaffoldResult
	_ = json.Unmarshal([]byte(body), &p)
	if p.Template != "karpathy-wiki" {
		t.Errorf("default template = %q, want karpathy-wiki", p.Template)
	}
	// Karpathy-Wiki ships 8 content files (hot, README, log + 5 role index files).
	if len(p.Created) < 8 {
		t.Errorf("expected >=8 files created, got %d: %+v", len(p.Created), p.Created)
	}
	// Body substitution check: hot.md should carry the project name.
	hot, err := os.ReadFile(filepath.Join(s.vault.Root, "alpha/hot.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hot), "alpha") {
		t.Errorf("PROJECT not substituted in hot.md: %s", hot)
	}
}

func TestMCP_ProjectScaffold_MinimalTemplate(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedTemplatesForTest(t, s.vault.Root)

	res, _ := s.handleProjectScaffold(context.Background(), call(map[string]any{
		"project":  "beta",
		"template": "minimal",
	}))
	body := resultText(t, res)
	var p scaffoldResult
	_ = json.Unmarshal([]byte(body), &p)
	if p.Template != "minimal" {
		t.Errorf("template = %q, want minimal", p.Template)
	}
	// Minimal ships exactly 3 content files.
	if len(p.Created) != 3 {
		t.Errorf("expected 3 files, got %d: %+v", len(p.Created), p.Created)
	}
	for _, want := range []string{"beta/hot.md", "beta/README.md", "beta/log.md"} {
		found := false
		for _, got := range p.Created {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing %q in created: %+v", want, p.Created)
		}
	}
}

func TestMCP_ProjectScaffold_TeamTemplate(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedTemplatesForTest(t, s.vault.Root)

	res, _ := s.handleProjectScaffold(context.Background(), call(map[string]any{
		"project":  "gamma",
		"template": "team",
	}))
	body := resultText(t, res)
	var p scaffoldResult
	_ = json.Unmarshal([]byte(body), &p)
	if p.Template != "team" {
		t.Errorf("template = %q, want team", p.Template)
	}
	// Team = karpathy-wiki + 3 agent stubs (backend, frontend, devops).
	agentFiles := 0
	for _, f := range p.Created {
		if strings.HasPrefix(f, "gamma/agents/") && strings.HasSuffix(f, ".md") && !strings.HasSuffix(f, "README.md") {
			agentFiles++
		}
	}
	if agentFiles < 3 {
		t.Errorf("expected >=3 agent role files, got %d in %+v", agentFiles, p.Created)
	}
}

func TestMCP_ProjectScaffold_UnknownTemplate(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedTemplatesForTest(t, s.vault.Root)

	res, _ := s.handleProjectScaffold(context.Background(), call(map[string]any{
		"project":  "delta",
		"template": "does-not-exist",
	}))
	msg := expectError(t, res)
	if !strings.Contains(msg, "not found") {
		t.Errorf("expected 'not found' error, got: %q", msg)
	}
}

func TestMCP_ProjectScaffold_Idempotent(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedTemplatesForTest(t, s.vault.Root)
	ctx := context.Background()

	// First run — everything gets created.
	_, err := s.handleProjectScaffold(ctx, call(map[string]any{
		"project":  "eps",
		"template": "minimal",
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Second run — same project, same template: everything skipped.
	res, _ := s.handleProjectScaffold(ctx, call(map[string]any{
		"project":  "eps",
		"template": "minimal",
	}))
	body := resultText(t, res)
	var p scaffoldResult
	_ = json.Unmarshal([]byte(body), &p)
	if len(p.Created) != 0 {
		t.Errorf("re-run should not create anything, got %+v", p.Created)
	}
	if len(p.Skipped) != 3 {
		t.Errorf("re-run should skip 3 files, got %+v", p.Skipped)
	}
}

func TestMCP_ProjectScaffold_UserTemplateOverride(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedTemplatesForTest(t, s.vault.Root)

	// Simulate the user creating a custom template by cp-ing minimal and
	// editing one file. The listing + scaffold must honour it.
	root := s.vault.Root
	customRoot := filepath.Join(root, ".gosidian/templates/custom-one")
	srcRoot := filepath.Join(root, ".gosidian/templates/minimal")
	if err := copyDirForTest(srcRoot, customRoot); err != nil {
		t.Fatal(err)
	}
	// Patch the meta so the name reflects the custom template.
	meta := `name = "custom-one"
description = "Custom test template"
prompt = "Use for tests only."

[[variables]]
name = "PROJECT"
required = true

[[variables]]
name = "TODAY"
auto = "date"
`
	if err := os.WriteFile(filepath.Join(customRoot, "_template.toml"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}

	res, _ := s.handleListBootstrapTemplates(context.Background(), call(map[string]any{}))
	body := resultText(t, res)
	if !strings.Contains(body, `"custom-one"`) {
		t.Errorf("custom template not listed: %s", body)
	}
	// And the scaffold tool works against it.
	res, _ = s.handleProjectScaffold(context.Background(), call(map[string]any{
		"project":  "zeta",
		"template": "custom-one",
	}))
	body = resultText(t, res)
	if !strings.Contains(body, `"template":"custom-one"`) {
		t.Errorf("scaffold response missing template name: %s", body)
	}
}

// copyDirForTest is a small filesystem helper; stdlib os doesn't ship a
// cp -r and we don't want to pull one in for a handful of tests.
func copyDirForTest(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
