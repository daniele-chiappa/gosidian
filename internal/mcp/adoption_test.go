package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/lint"
)

// embedNotes: hot.md lists the active plans with an embed of a section of
// the plans database note, whose heading has the level of hot.md's own (as
// odoo wrote it, 2026-10-05).
func embedNotes(t *testing.T) (*Server, context.Context) {
	t.Helper()
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	for path, content := range map[string]string{
		"p/plans.md":   "---\ntitle: Plans\ntype: database\nsource: p/plans\nfields:\n  status: {type: select, options: [draft, in-progress, done]}\ntags: [p, type:index]\n---\n\n# Plans\n\n## Attivi\n\n```view\nfrom: p/plans\nwhere:\n  - status in [in-progress, draft]\ncolumns: [title, status]\n```\n",
		"p/plans/a.md": "---\ntitle: Plan A\ntype: plan\nstatus: in-progress\ntags: [p, type:plan, status:in-progress]\n---\n\n# Plan A\n",
		"p/hot.md":     "---\ntitle: Hot\ntags: [p, type:index]\n---\n\n# Hot\n\n## Piani attivi\n\n![[p/plans#Attivi]]\n\nProssimi passi: chiudere A.\n\n## Altro\n\nNiente.\n",
	} {
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": path, "content": content})); err != nil || res.IsError {
			t.Fatalf("create %s: %v %+v", path, err, res)
		}
	}
	return s, ctx
}

// A section that embeds another note's section reads whole with
// render_views: the included heading does not end it (BUG-094).
func TestGetSection_Embed(t *testing.T) {
	s, ctx := embedNotes(t)
	res, _ := s.handleGetSection(ctx, call(map[string]any{"path": "p/hot.md", "heading": "Piani attivi", "render_views": true}))
	var out struct{ Heading, Content string }
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Piani attivi", "gosidian:embed —", "## Attivi", "[[p/plans/a\\|Plan A]]", "<!-- /gosidian:embed -->", "Prossimi passi: chiudere A."} {
		if !strings.Contains(out.Content, want) {
			t.Errorf("section lacks %q:\n%s", want, out.Content)
		}
	}
	if strings.Contains(out.Content, "## Altro") {
		t.Errorf("the section ends at the next heading of the note:\n%s", out.Content)
	}
	// A heading only the embed holds is found in the computed note.
	res, _ = s.handleGetSection(ctx, call(map[string]any{"path": "p/hot.md", "heading": "Attivi", "render_views": true}))
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil || out.Heading != "Attivi" {
		t.Errorf("embedded heading: %+v %v", out, err)
	}
}

// The outline counts the embeds of a section, and the lite bootstrap's
// outline is the written text's (BUG-094).
func TestOutline_Embeds(t *testing.T) {
	s, ctx := embedNotes(t)
	res, _ := s.handleGetOutline(ctx, call(map[string]any{"path": "p/hot.md"}))
	text := resultText(t, res)
	if !strings.Contains(text, `"text":"Piani attivi","id":"piani-attivi","embeds":1`) {
		t.Errorf("outline: %s", text)
	}
	res, _ = s.handleBootstrap(ctx, call(map[string]any{"project": "p", "mode": "lite"}))
	var boot struct {
		Hot bootstrapFile `json:"hot_md"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &boot); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, h := range boot.Hot.Headings {
		names = append(names, h.Text)
	}
	if strings.Join(names, ",") != "Hot,Piani attivi,Altro" || !strings.Contains(boot.Hot.Hint, "embeds:N") {
		t.Errorf("lite outline %v, hint %q", names, boot.Hot.Hint)
	}
}

// A plan listed by a view that hot.md embeds counts as referenced
// (BUG-095).
func TestLint_StatusIncoherentEmbed(t *testing.T) {
	s, ctx := embedNotes(t)
	issues, err := lint.New(s.vault, s.index).Run(ctx, "p", []string{"status-incoherent"}, lint.SeverityInfo)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Errorf("issues = %+v", issues)
	}
}
