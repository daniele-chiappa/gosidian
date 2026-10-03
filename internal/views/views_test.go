package views

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/dbschema"
	"github.com/gosidian/gosidian/internal/index"
)

func testIndex(t *testing.T) *index.Index {
	t.Helper()
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	notes := map[string]string{
		"p/docs/improvements/IMP-001.md": "---\ntitle: \"IMP-001 — one\"\nid: IMP-001\nstatus: open\npriority: high\ncreated: 2026-09-01\n---\n",
		"p/docs/improvements/IMP-002.md": "---\ntitle: \"IMP-002 — two | piped\"\nid: IMP-002\nstatus: done\npriority: low\ncreated: 2026-09-20\n---\n",
		"p/docs/improvements/IMP-003.md": "---\ntitle: \"IMP-003 — three `code`\"\nid: IMP-003\nstatus: open\npriority: low\ncreated: 2026-09-30\n---\n",
		"p/docs/improvements/sub/X.md":   "---\ntitle: nested\nid: X\nstatus: open\n---\n",
		"p/plans/plan-a.md":              "---\ntitle: Plan A\nstatus: in-progress\nimplements_imp: [IMP-001, IMP-003]\n---\n",
		"p/plans/plan-b.md":              "---\ntitle: Plan B\nstatus: done\nimplements_imp: [IMP-002]\n---\n",
	}
	for p, body := range notes {
		if err := idx.Upsert(index.NoteDoc{Path: p, Title: strings.TrimSuffix(filepath.Base(p), ".md"), Body: body, ModTime: 1, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
	}
	return idx
}

func run(t *testing.T, idx *index.Index, spec string, c Context) *Result {
	t.Helper()
	s, err := Parse(spec, c)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r, err := Run(s, idx.Query)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return r
}

func ids(r *Result) string {
	var out []string
	for _, h := range r.Hits {
		out = append(out, strings.TrimSuffix(filepath.Base(h.Path), ".md"))
	}
	return strings.Join(out, ",")
}

func TestRun_FolderWhereSort(t *testing.T) {
	idx := testIndex(t)
	r := run(t, idx, "from: p/docs/improvements\nwhere:\n  - status in [open, in-progress]\nsort: id asc\ncolumns: [id, title, priority]", Context{})
	if got := ids(r); got != "IMP-001,IMP-003" {
		t.Errorf("open rows = %s (nested notes must not count, done rows filtered)", got)
	}
	r = run(t, idx, "from: p/docs/improvements\nwhere:\n  - {field: priority, op: eq, value: low}\nsort: created desc", Context{})
	if got := ids(r); got != "IMP-003,IMP-002" {
		t.Errorf("map condition + sort desc = %s", got)
	}
	r = run(t, idx, "from: p/docs/improvements\nwhere:\n  - created >= today-15d", Context{Today: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)})
	if got := ids(r); got != "IMP-003,IMP-002" && got != "IMP-002,IMP-003" {
		t.Errorf("today-15d = %s", got)
	}
}

func TestRun_This(t *testing.T) {
	idx := testIndex(t)
	this := ThisFields("p/docs/improvements/IMP-001.md", map[string]any{"id": "IMP-001"})
	r := run(t, idx, "from: p/plans\nwhere:\n  - implements_imp contains this.id", Context{This: this})
	if got := ids(r); got != "plan-a" {
		t.Errorf("plans implementing this.id = %s", got)
	}
	if _, err := Parse("from: p/plans\nwhere:\n  - status = this.nope", Context{This: this}); err == nil {
		t.Error("this.<missing field> should be an error")
	}
}

func TestParse_Errors(t *testing.T) {
	for name, spec := range map[string]string{
		"no from":       "where:\n  - status = open",
		"bad condition": "from: p\nwhere:\n  - status is open",
		"bad as":        "from: p\nas: chart",
		"bad yaml":      "from: [p",
	} {
		if _, err := Parse(spec, Context{}); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestMarkdown(t *testing.T) {
	idx := testIndex(t)
	r := run(t, idx, "from: p/docs/improvements\nwhere:\n  - status = done\ncolumns: [id, title, priority]", Context{})
	md := r.Markdown()
	if !strings.Contains(md, "| id | title | priority |") || !strings.Contains(md, `[[p/docs/improvements/IMP-002\|IMP-002 — two \| piped]]`) {
		t.Errorf("table:\n%s", md)
	}
	r = run(t, idx, "from: p/docs/improvements\nwhere:\n  - status = open\nas: list\nlimit: 1\nsort: id asc\ncolumns: [title, priority]", Context{})
	md = r.Markdown()
	if !strings.HasPrefix(md, `- [[p/docs/improvements/IMP-001\|IMP-001 — one]] · priority: high`) || !strings.Contains(md, "Showing 1 of 2") {
		t.Errorf("list:\n%s", md)
	}
	// A backtick in a title would turn the link into an inline code span.
	r = run(t, idx, "from: p/docs/improvements\nwhere:\n  - id = IMP-003", Context{})
	if md = r.Markdown(); !strings.Contains(md, `[[p/docs/improvements/IMP-003\|IMP-003 — three code]]`) {
		t.Errorf("backticks kept in the link alias:\n%s", md)
	}
}

func TestFindBlocksAndExpand(t *testing.T) {
	body := "# Hot\n\n```view\nfrom: p/x\n```\n\nText.\n\n````markdown\n```view\nfrom: example\n```\n````\n\n~~~view\nfrom: p/y\n~~~\n"
	blocks := FindBlocks([]byte(body))
	if len(blocks) != 2 || blocks[0].Spec != "from: p/x\n" || blocks[1].Spec != "from: p/y\n" {
		t.Fatalf("blocks = %+v (the one inside ````markdown is a sample, not a block)", blocks)
	}
	render := func(spec string) string { return "RESULT(" + strings.TrimSpace(spec) + ")\n" }

	agent, hash := Expand([]byte(body), true, render)
	if !strings.Contains(string(agent), "```view\nfrom: p/x\n```\n"+resultOpen+"\n\nRESULT(from: p/x)\n\n"+resultClose) || hash == "" {
		t.Errorf("agent form:\n%s", agent)
	}
	// A result copied into the file is dropped, not shown twice.
	again, hash2 := Expand(agent, true, render)
	if strings.Count(string(again), resultOpen) != 2 || hash2 != hash {
		t.Errorf("re-expanding a persisted result duplicated it:\n%s", again)
	}

	web, _ := Expand([]byte(body), false, render)
	if strings.Contains(string(web), "```view\nfrom: p/x") || !strings.Contains(string(web), "<div class=\"gosidian-view\" data-view=\"0\">\n\nRESULT(from: p/x)") ||
		!strings.Contains(string(web), "<div class=\"gosidian-view\" data-view=\"1\">\n\nRESULT(from: p/y)") {
		t.Errorf("web form:\n%s", web)
	}
	if !strings.Contains(string(web), "```view\nfrom: example\n```") {
		t.Error("the sample inside ````markdown must stay as written")
	}
}

func TestRenderNote_ErrorIsAWarning(t *testing.T) {
	out, _ := RenderNote([]byte("```view\nwhere:\n  - x = 1\n```\n"), false, Context{}, testIndex(t).Query)
	if !strings.Contains(string(out), "⚠️ view: `from` is required") {
		t.Errorf("got:\n%s", out)
	}
}

func testSchema(t *testing.T) *dbschema.Schema {
	t.Helper()
	s, err := dbschema.Parse("p/docs/improvements.md", "type: database\nsource: p/docs/improvements\nfields:\n"+
		"  id: {type: text, required: true}\n"+
		"  status: {type: select, required: true, options: [open, in-progress, done]}\n"+
		"  priority: {type: select, options: [low, medium, high]}\n"+
		"  created: {type: date}\n")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func schemaContext(t *testing.T) Context {
	s := testSchema(t)
	return Context{Schema: func(folder string) *dbschema.Schema {
		if folder == s.Source {
			return s
		}
		return nil
	}}
}

func TestParse_Board(t *testing.T) {
	if _, err := Parse("from: p/x\nas: board", Context{}); err == nil || !strings.Contains(err.Error(), "needs group_by") {
		t.Errorf("board without group_by: %v", err)
	}
	if _, err := Parse("from: p/x\ngroup_by: status", Context{}); err == nil || !strings.Contains(err.Error(), "only with as: board") {
		t.Errorf("group_by on a table: %v", err)
	}
	s, err := Parse("from: p/x\nas: board\ngroup_by: status", Context{})
	if err != nil || s.GroupBy != "status" {
		t.Errorf("board: %+v %v", s, err)
	}
}

// A board's columns follow the select's options, empty ones included, then
// other values; without a schema, the values found.
func TestGroups(t *testing.T) {
	idx := testIndex(t)
	spec := "from: p/docs/improvements\nas: board\ngroup_by: status\nsort: id asc\ncolumns: [title, priority]"
	r, err := compute(spec, schemaContext(t), idx.Query)
	if err != nil {
		t.Fatal(err)
	}
	groups, _ := r.Groups()
	var got []string
	for _, g := range groups {
		got = append(got, g.Value+"="+ids(&Result{Hits: g.Hits}))
	}
	if strings.Join(got, " ") != "open=IMP-001,IMP-003 in-progress= done=IMP-002" {
		t.Errorf("groups with a schema: %v", got)
	}
	r, _ = compute(spec, Context{}, idx.Query)
	groups, _ = r.Groups()
	if len(groups) != 2 || groups[0].Value != "done" || groups[1].Value != "open" {
		t.Errorf("groups without a schema: %+v", groups)
	}
	if _, err := compute("from: p/docs/improvements\nas: board\ngroup_by: created", schemaContext(t), idx.Query); err == nil ||
		!strings.Contains(err.Error(), "select or checkbox") {
		t.Errorf("board on a date field: %v", err)
	}
}

func TestMarkdown_Board(t *testing.T) {
	r, err := compute("from: p/docs/improvements\nas: board\ngroup_by: status\nsort: id asc\ncolumns: [title, status, priority]",
		schemaContext(t), testIndex(t).Query)
	if err != nil {
		t.Fatal(err)
	}
	want := "**status: open** (2)\n\n" +
		"- [[p/docs/improvements/IMP-001\\|IMP-001 — one]] · priority: high\n" +
		"- [[p/docs/improvements/IMP-003\\|IMP-003 — three code]] · priority: low\n" +
		"\n**status: done** (1)\n\n" +
		"- [[p/docs/improvements/IMP-002\\|IMP-002 — two \\| piped]] · priority: low\n"
	if md := r.Markdown(); md != want {
		t.Errorf("board markdown:\n%s\nwant:\n%s", md, want)
	}
}

func TestData(t *testing.T) {
	c := schemaContext(t)
	c.CanWrite = func(p string) bool { return p != "p/docs/improvements/IMP-002.md" }
	r, err := compute("from: p/docs/improvements\nsort: id asc\ncolumns: [title, status, created, modified]", c, testIndex(t).Query)
	if err != nil {
		t.Fatal(err)
	}
	d := r.Data(c)
	if d.As != "table" || d.Database != "p/docs/improvements.md" || d.Source != "p/docs/improvements" || d.Total != 3 {
		t.Errorf("data = %+v", d)
	}
	wantCols := []Column{{Name: "title"}, {Name: "status", Type: "select", Options: []string{"open", "in-progress", "done"}, Required: true},
		{Name: "created", Type: "date"}, {Name: "modified"}}
	if !reflect.DeepEqual(d.Columns, wantCols) {
		t.Errorf("columns = %+v", d.Columns)
	}
	if len(d.Rows) != 3 || d.Rows[0].Path != "p/docs/improvements/IMP-001.md" || d.Rows[0].Title != "IMP-001 — one" ||
		d.Rows[0].Fields["status"] != "open" || d.Rows[0].Fields["created"] != "2026-09-01" || !d.Rows[0].Writable || d.Rows[1].Writable {
		t.Errorf("rows = %+v", d.Rows)
	}
	b, _ := compute("from: p/docs/improvements\nas: board\ngroup_by: status\ncolumns: [title]", c, testIndex(t).Query)
	bd := b.Data(c)
	if bd.Group == nil || bd.Group.Type != "select" || strings.Join(bd.Groups, ",") != "open,in-progress,done" {
		t.Errorf("board data = %+v", bd)
	}
	if bd.Rows[0].Fields["status"] == nil {
		t.Errorf("a board row lacks its group_by field: %+v", bd.Rows[0])
	}
}

// Each view of a note has its data at its data-view index, a failed one too.
func TestRenderNoteData(t *testing.T) {
	body := "# Note\n\n```view\nfrom: p/docs/improvements\nwhere:\n  - status = done\n```\n\n```view\nwhere:\n  - x = 1\n```\n"
	html, data := RenderNoteData([]byte(body), schemaContext(t), testIndex(t).Query)
	if len(data) != 2 || data[0].Error != "" || len(data[0].Rows) != 1 || data[1].Error == "" {
		t.Fatalf("data = %+v", data)
	}
	for _, want := range []string{`data-view="0"`, `data-view="1"`, "IMP-002", "⚠️ view: `from` is required"} {
		if !strings.Contains(string(html), want) {
			t.Errorf("html lacks %q:\n%s", want, html)
		}
	}
}

// The wikilinks of field values come resolved for the reader, so the web UI
// shows a relation as the links the HTML table had.
func TestData_Links(t *testing.T) {
	idx := testIndex(t)
	body := "---\ntitle: Linked\nid: IMP-009\nstatus: open\nplan: \"[[p/plans/plan-a|Plan A]]\"\nrelated: [\"[[Plan B]]\", \"[[nowhere]]\"]\n---\n"
	if err := idx.Upsert(index.NoteDoc{Path: "p/docs/improvements/IMP-009.md", Title: "Linked", Body: body, ModTime: 1, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	c := Context{Resolve: func(target string) string {
		return map[string]string{"p/plans/plan-a": "p/plans/plan-a.md", "Plan B": "p/plans/plan-b.md"}[target]
	}}
	r, err := compute("from: p/docs/improvements\nwhere:\n  - id = IMP-009\ncolumns: [title, plan, related]", c, idx.Query)
	if err != nil {
		t.Fatal(err)
	}
	links := r.Data(c).Rows[0].Links
	want := map[string][]Link{
		"plan":    {{Text: "Plan A", Path: "p/plans/plan-a.md"}},
		"related": {{Text: "Plan B", Path: "p/plans/plan-b.md"}, {Text: "nowhere"}},
	}
	if !reflect.DeepEqual(links, want) {
		t.Errorf("links = %+v", links)
	}
}
