package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// Relations for agents (IMP-127 iteration 2): frontmatter links in
// backlinks and outlinks with their field, link conditions in
// memory_query, and the row views of a row with render_views (IMP-139).
func TestRelations_AgentTools(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	write := func(path, content string) {
		t.Helper()
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": path, "content": content})); err != nil || res.IsError {
			t.Fatalf("create %s: %v %+v", path, err, res)
		}
	}
	write("p/docs/bugs.md", "---\ntitle: Bugs\ntags: [p, type:index]\ntype: database\nsource: p/docs/bugs\nfields:\n  id: {type: text}\n  status: {type: select, options: [open, done]}\n"+
		"row_views:\n  - title: Plans that fix it\n    from: p/plans\n    where: [related contains this]\n    columns: [title, status]\n---\n# Bugs\n")
	write("p/docs/bugs/BUG-1.md", "---\ntitle: BUG-1\nid: BUG-1\nstatus: open\ntags: [p]\n---\n\n# BUG-1\n\nThe bug.\n")
	write("p/plans/fix.md", "---\ntitle: Fix\nstatus: done\nrelated: [\"[[BUG-1]]\"]\ntags: [p]\n---\n\nBody.\n")
	write("p/plans/mention.md", "---\ntitle: Mention\nstatus: draft\ntags: [p]\n---\n\nSee [[p/docs/bugs/BUG-1]].\n")

	decode := func(res *mcplib.CallToolResult, err error, v any) {
		t.Helper()
		if err != nil || res.IsError {
			t.Fatalf("call: %v %+v", err, res)
		}
		if err := json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), v); err != nil {
			t.Fatal(err)
		}
	}

	var bl struct {
		Backlinks []backlinkEntry `json:"backlinks"`
	}
	res, err := s.handleBacklinks(ctx, call(map[string]any{"path": "p/docs/bugs/BUG-1.md"}))
	decode(res, err, &bl)
	want := []backlinkEntry{{Path: "p/plans/fix.md", Title: "Fix", Fields: []string{"related"}}, {Path: "p/plans/mention.md", Title: "Mention"}}
	if !reflect.DeepEqual(bl.Backlinks, want) {
		t.Errorf("backlinks = %+v", bl.Backlinks)
	}
	// A path without its extension, as a wikilink writes it (BUG-092).
	var bl2 struct {
		Backlinks []backlinkEntry `json:"backlinks"`
	}
	res, err = s.handleBacklinks(ctx, call(map[string]any{"path": "p/docs/bugs/BUG-1"}))
	decode(res, err, &bl2)
	if len(bl2.Backlinks) != 2 {
		t.Errorf("backlinks without .md = %+v", bl2.Backlinks)
	}
	var ol struct {
		Outlinks []outlinkEntry `json:"outlinks"`
	}
	res, err = s.handleOutlinks(ctx, call(map[string]any{"path": "p/plans/fix"}))
	decode(res, err, &ol)
	if len(ol.Outlinks) != 1 || ol.Outlinks[0].Field != "related" || ol.Outlinks[0].ResolvedPath != "p/docs/bugs/BUG-1.md" {
		t.Errorf("outlinks = %+v", ol.Outlinks)
	}

	var q struct {
		Notes []queryNote `json:"notes"`
	}
	res, err = s.handleQuery(ctx, call(map[string]any{"project": "p", "sort": "path",
		"where": []any{map[string]any{"field": "links", "op": "contains", "value": "[[BUG-1]]"}}}))
	decode(res, err, &q)
	if len(q.Notes) != 2 || q.Notes[0].Path != "p/plans/fix.md" || q.Notes[1].Path != "p/plans/mention.md" {
		t.Errorf("links contains = %+v", q.Notes)
	}
	res, err = s.handleQuery(ctx, call(map[string]any{"project": "p",
		"where": []any{map[string]any{"field": "related", "op": "contains", "value": "[[p/nothing]]"}}}))
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].(mcplib.TextContent).Text, "names no note") {
		t.Errorf("a link to no note should be an error: %+v", res)
	}

	var got struct {
		Content       string `json:"content"`
		Hint          string `json:"hint"`
		ViewsRendered bool   `json:"views_rendered"`
	}
	res, err = s.handleGet(ctx, call(map[string]any{"path": "p/docs/bugs/BUG-1.md"}))
	decode(res, err, &got)
	if strings.Contains(got.Content, "row-views") || !strings.Contains(got.Hint, "1 row view(s) of p/docs/bugs.md not shown") {
		t.Errorf("without render_views: hint %q", got.Hint)
	}
	res, err = s.handleGet(ctx, call(map[string]any{"path": "p/docs/bugs/BUG-1.md", "render_views": true}))
	decode(res, err, &got)
	if !got.ViewsRendered || !strings.HasPrefix(got.Content, "---\ntitle: BUG-1") || !strings.Contains(got.Content, "**Plans that fix it**") ||
		!strings.Contains(got.Content, "[[p/plans/fix\\|Fix]] | done") || strings.Contains(got.Content, "Mention") {
		t.Errorf("with render_views:\n%s", got.Content)
	}
	var sec struct {
		Content string `json:"content"`
		Hint    string `json:"hint"`
	}
	res, err = s.handleGetSection(ctx, call(map[string]any{"path": "p/docs/bugs/BUG-1.md", "heading": "BUG-1", "render_views": true}))
	decode(res, err, &sec)
	if !strings.HasPrefix(sec.Content, "# BUG-1") || !strings.Contains(sec.Content, "**Plans that fix it**") {
		t.Errorf("get_section with render_views:\n%s", sec.Content)
	}
	// A note that is not a row has no row views and no hint about them.
	got.Hint = ""
	res, err = s.handleGet(ctx, call(map[string]any{"path": "p/plans/fix.md"}))
	decode(res, err, &got)
	if strings.Contains(got.Hint, "row view") {
		t.Errorf("not a row: hint %q", got.Hint)
	}
}

// Rollups for agents (IMP-127 iteration 3): memory_query over the rows of a
// database computes the rollups named in fields, sorts by one, and refuses
// a condition on one; a write of a rollup into a row is flagged.
func TestRollups_MemoryQuery(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	write := func(path, content string) *mcplib.CallToolResult {
		t.Helper()
		res, err := s.handleCreate(ctx, call(map[string]any{"path": path, "content": content}))
		if err != nil || res.IsError {
			t.Fatalf("create %s: %v %+v", path, err, res)
		}
		return res
	}
	write("p/docs/bugs.md", "---\ntitle: Bugs\ntags: [p, type:index]\ntype: database\nsource: p/docs/bugs\nfields:\n  id: {type: text}\n  status: {type: select, options: [open, done]}\n"+
		"  fixes:\n    type: rollup\n    from: p/plans\n    where: [related contains this]\n---\n# Bugs\n")
	write("p/docs/bugs/BUG-1.md", "---\ntitle: BUG-1\nid: BUG-1\nstatus: open\ntags: [p]\n---\n")
	write("p/docs/bugs/BUG-2.md", "---\ntitle: BUG-2\nid: BUG-2\nstatus: open\ntags: [p]\n---\n")
	write("p/plans/fix.md", "---\ntitle: Fix\nstatus: done\nrelated: [\"[[BUG-1]]\"]\ntags: [p]\n---\n")
	write("p/plans/fix2.md", "---\ntitle: Fix again\nstatus: draft\nrelated: [\"[[BUG-1]]\", \"[[BUG-2]]\"]\ntags: [p]\n---\n")

	out := runQuery(t, s, ctx, map[string]any{"from": "p/docs/bugs", "fields": []any{"id", "fixes"}, "sort": "fixes", "order": "desc"})
	// A rollup is a number in the response, not text (BUG-096).
	if out.Total != 2 || len(out.Notes) != 2 || out.Notes[0].Fields["fixes"] != float64(2) || out.Notes[1].Fields["fixes"] != float64(1) {
		t.Errorf("query = %+v", out)
	}
	// Several keys, a rollup among them: status ties, fixes orders the tie;
	// the field read for the sort stays out of the response (IMP-143).
	out = runQuery(t, s, ctx, map[string]any{"from": "p/docs/bugs", "fields": []any{"fixes"}, "sort": "status asc, fixes asc"})
	if len(out.Notes) != 2 || out.Notes[0].Path != "p/docs/bugs/BUG-2.md" || out.Notes[1].Path != "p/docs/bugs/BUG-1.md" || out.Notes[0].Fields["status"] != nil {
		t.Errorf("status, then fixes = %+v", out)
	}
	res, _ := s.handleQuery(ctx, call(map[string]any{"from": "p/docs/bugs", "where": []any{map[string]any{"field": "fixes", "op": "gt", "value": "1"}}}))
	if msg := expectError(t, res); !strings.Contains(msg, "is a rollup") {
		t.Errorf("where on a rollup: %s", msg)
	}
	// Over several folders a rollup has no rows to be computed over: said,
	// not an empty answer (BUG-096).
	for _, args := range []map[string]any{
		{"from": []any{"p/docs/bugs", "p/plans"}, "where": []any{map[string]any{"field": "fixes", "op": "gte", "value": float64(1)}}},
		{"from": []any{"p/docs/bugs", "p/plans"}, "fields": []any{"fixes"}},
	} {
		res, _ := s.handleQuery(ctx, call(args))
		if msg := expectError(t, res); !strings.Contains(msg, `"fixes" is a rollup of the database of p/docs/bugs`) || !strings.Contains(msg, "from: [p/docs/bugs]") {
			t.Errorf("%v: %s", args, msg)
		}
	}
	req := call(map[string]any{"path": "p/docs/bugs/BUG-3.md", "content": "---\ntitle: BUG-3\nid: BUG-3\nstatus: open\nfixes: 4\ntags: [p]\n---\n"})
	req.Params.Name = "memory_create"
	res, err := callNotesMiddleware(s.handleCreate)(ctx, req)
	if err != nil || res.IsError {
		t.Fatalf("create: %v %+v", err, res)
	}
	if n := strings.Join(resultNotices(t, res), " "); !strings.Contains(n, "computed when the row is read") {
		t.Errorf("a written rollup is flagged in notices: %q", n)
	}
}

// Embeds for agents (IMP-127 iteration 3, phase 3): memory_get with
// render_views includes the section between markers, its view computed for
// the note that embeds it; without, the hint says so.
func TestEmbeds_MemoryGet(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	for path, content := range map[string]string{
		"p/templates/blocks.md": "---\ntitle: Blocks\ntags: [p]\n---\n\n# Blocks\n\n## Links here\n\n```view\nfrom: p/notes\nwhere: [links contains this]\nas: list\n```\n",
		"p/notes/a.md":          "---\ntitle: A\ntags: [p]\n---\n\n# A\n\n![[p/templates/blocks#Links here]]\n",
		"p/notes/b.md":          "---\ntitle: B\ntags: [p]\n---\n\nSee [[p/notes/a]].\n",
	} {
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": path, "content": content})); err != nil || res.IsError {
			t.Fatalf("create %s: %v %+v", path, err, res)
		}
	}
	var got struct {
		Content string `json:"content"`
		Hint    string `json:"hint"`
	}
	res, _ := s.handleGet(ctx, call(map[string]any{"path": "p/notes/a.md"}))
	if err := json.Unmarshal([]byte(resultText(t, res)), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Hint, "1 embed(s) here") || strings.Contains(got.Content, "gosidian:embed") {
		t.Errorf("without render_views: %+v", got)
	}
	res, _ = s.handleGet(ctx, call(map[string]any{"path": "p/notes/a.md", "render_views": true}))
	if err := json.Unmarshal([]byte(resultText(t, res)), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Content, "<!-- gosidian:embed — included from [[p/templates/blocks#Links here]]") || !strings.Contains(got.Content, "[[p/notes/b\\|B]]") {
		t.Errorf("with render_views:\n%s", got.Content)
	}
}

// memory_snapshot freezes a note beside it, the note unchanged; a second
// one the same day adds the time; a non-markdown path is refused.
func TestSnapshot_Tool(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	for path, content := range map[string]string{
		"p/hot.md":     "---\ntitle: Hot\ntags: [p, type:index]\n---\n\n# Hot\n\nNotes: `=count(p/notes)`.\n\n```view\nfrom: p/notes\nas: list\n```\n",
		"p/notes/a.md": "---\ntitle: A\ntags: [p]\n---\n\nA.\n",
	} {
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": path, "content": content})); err != nil || res.IsError {
			t.Fatalf("create %s: %v %+v", path, err, res)
		}
	}
	before, _ := s.vault.Load("p/hot.md")
	var out struct {
		Path   string `json:"path"`
		Views  int    `json:"views"`
		Values int    `json:"values"`
	}
	res, _ := s.handleSnapshot(ctx, call(map[string]any{"path": "p/hot"}))
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().Format("2006-01-02")
	if out.Path != "p/hot.snapshots/"+day+".md" || out.Views != 1 || out.Values != 1 {
		t.Fatalf("snapshot = %+v", out)
	}
	snap, err := s.vault.Load(out.Path)
	if err != nil {
		t.Fatal(err)
	}
	if c := string(snap.Content); !strings.Contains(c, "type: snapshot\n") || !strings.Contains(c, "Notes: 1 (`` `=count(p/notes)` ``).") || !strings.Contains(c, "[[p/notes/a\\|A]]") || strings.Contains(c, "```view") {
		t.Errorf("snapshot:\n%s", c)
	}
	if after, _ := s.vault.Load("p/hot.md"); string(after.Content) != string(before.Content) {
		t.Error("the note itself changed")
	}
	res, _ = s.handleSnapshot(ctx, call(map[string]any{"path": "p/hot.md"}))
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil || !strings.HasPrefix(out.Path, "p/hot.snapshots/"+day+"-") {
		t.Errorf("second snapshot = %+v %v", out, err)
	}
	res, _ = s.handleSnapshot(ctx, call(map[string]any{"path": "p/attachments/x.png"}))
	if msg := expectError(t, res); !strings.Contains(msg, "not a markdown note") {
		t.Errorf("non-markdown: %s", msg)
	}
}

// memory_get reads an Obsidian base as a read-only note, its views
// translated and computed with render_views; no tool writes it (IMP-118).
func TestBase_MemoryGet(t *testing.T) {
	s, _, dir := newTestServer(t)
	ctx := context.Background()
	if res, err := s.handleCreate(ctx, call(map[string]any{"path": "p/Books/dune.md", "content": "---\ntitle: Dune\nstatus: reading\ntags: [p]\n---\n"})); err != nil || res.IsError {
		t.Fatalf("create: %v %+v", err, res)
	}
	base := "views:\n  - type: cards\n    name: Shelf\n    filters: 'file.inFolder(\"Books\")'\n    order: [file.name, status, formula.x]\n"
	if err := os.WriteFile(filepath.Join(dir, "p/books.base"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Kind, Content, Source, Hint string
		Rendered                    bool `json:"views_rendered"`
	}
	res, _ := s.handleGet(ctx, call(map[string]any{"path": "p/books.base", "render_views": true}))
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	if out.Kind != "base" || out.Source != base || !out.Rendered || !strings.Contains(out.Hint, "read-only") {
		t.Errorf("base = %+v", out)
	}
	for _, want := range []string{"## Shelf", "- ⚠️ a cards view has no equivalent", "- ⚠️ the column formula.x has no equivalent", "[[p/Books/dune\\|Dune]]", "| reading |"} {
		if !strings.Contains(out.Content, want) {
			t.Errorf("content lacks %q:\n%s", want, out.Content)
		}
	}
	res, _ = s.handleGet(ctx, call(map[string]any{"path": "p/books.base"}))
	out.Rendered = false
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	if out.Rendered || !strings.Contains(out.Hint, "pass render_views:true") || !strings.Contains(out.Content, "```view\nfrom: p/Books/**\n") {
		t.Errorf("without render_views the views are left as blocks: %+v", out)
	}
	res, _ = s.handleEdit(ctx, call(map[string]any{"path": "p/books.base", "old_string": "cards", "new_string": "table"}))
	if !res.IsError || !strings.Contains(res.Content[0].(mcplib.TextContent).Text, "Obsidian base, read-only") {
		t.Errorf("edit = %+v", res)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "p/books.base")); string(data) != base {
		t.Errorf("the base changed: %q", data)
	}
}
