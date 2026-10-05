package views

import (
	"fmt"
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

// sort is a key, keys separated by commas or a list of keys, each ordering
// the ties of the one before; the memory_query of the view says them all
// (IMP-143).
func TestRun_MultiKeySort(t *testing.T) {
	idx := testIndex(t)
	for _, spec := range []string{
		"from: p/docs/improvements\nsort: status desc, priority asc, id",
		"from: p/docs/improvements\nsort: [status desc, priority asc, id]",
		"from: p/docs/improvements\nsort:\n  - status desc\n  - priority asc\n  - id",
	} {
		r := run(t, idx, spec, Context{})
		if got := ids(r); got != "IMP-001,IMP-003,IMP-002" {
			t.Errorf("%q = %s", spec, got)
		}
		if strings.Join(r.Spec.Columns, ",") != "title,status,priority,id" {
			t.Errorf("default columns = %v", r.Spec.Columns)
		}
		if q := r.QueryCall(); !strings.Contains(q, `"sort":"status desc, priority asc, id desc"`) || strings.Contains(q, `"order"`) {
			t.Errorf("query call: %s", q)
		}
	}
	for spec, msg := range map[string]string{
		"from: p\nsort: a, b, c, d, e": "at most 4 keys",
		"from: p\nsort: [a up]":        "order is asc or desc",
		"from: p\nsort: {a: 1}":        "is not a key or a list of keys",
	} {
		if _, err := Parse(spec, Context{}); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%q: err = %v, want %q", spec, err, msg)
		}
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

// A new row made from a view starts with the values its filters ask of the
// declared fields, typed by the schema, so it shows in the view.
func TestData_Defaults(t *testing.T) {
	s, err := dbschema.Parse("p/db.md", "type: database\nsource: p/rows\nfields:\n"+
		"  id: {type: text}\n  title: {type: text}\n"+
		"  status: {type: select, options: [open, done]}\n"+
		"  points: {type: number}\n  blocked: {type: checkbox}\n"+
		"  labels: {type: multi-select, options: [ui, mcp]}\n  owner: {type: text}\n")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := Parse("from: p/rows\nwhere:\n"+
		"  - status in [open, done]\n  - status = done\n"+ // the first filter on a field wins
		"  - points = 3\n  - blocked = false\n  - labels contains ui\n"+
		"  - owner != bob\n  - id = T-001\n  - title = x\n"+ // ne, id and title give nothing
		"  - priority = high\n", Context{}) // not declared
	if err != nil {
		t.Fatal(err)
	}
	c := Context{CanWrite: func(p string) bool { return p == "p/rows/_.md" }}
	d := (&Result{Spec: spec, Schema: s}).Data(c)
	want := map[string]any{"status": "open", "points": 3.0, "blocked": false}
	if !reflect.DeepEqual(d.Defaults, want) || !d.Creatable {
		t.Errorf("defaults = %#v, creatable = %v", d.Defaults, d.Creatable)
	}
	spec, _ = Parse("from: p/rows\nwhere:\n  - status = closed\n  - labels = ui\n", Context{})
	d = (&Result{Spec: spec, Schema: s}).Data(Context{})
	if !reflect.DeepEqual(d.Defaults, map[string]any{"labels": []any{"ui"}}) || d.Creatable {
		t.Errorf("an option the schema lacks is left out; a multi-select is a list: %#v, creatable = %v", d.Defaults, d.Creatable)
	}
	if d := (&Result{Spec: spec}).Data(c); d.Defaults != nil || d.Creatable {
		t.Errorf("without a schema: %+v", d)
	}
}

// A view of a database sorts a select by its options.
func TestCompute_SortsSelectByOptions(t *testing.T) {
	r, err := compute("from: p/docs/improvements\nsort: priority desc\ncolumns: [title, priority]", schemaContext(t), testIndex(t).Query)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range r.Hits {
		got = append(got, h.FieldValues()["priority"].(string))
	}
	if !reflect.DeepEqual(got, []string{"high", "low", "low"}) {
		t.Errorf("priorities = %v", got)
	}
	r, _ = compute("from: p/docs/improvements\nsort: priority asc\ncolumns: [title, priority]", schemaContext(t), testIndex(t).Query)
	if p := r.Hits[len(r.Hits)-1].FieldValues()["priority"]; p != "high" {
		t.Errorf("asc ends with %v, want high", p)
	}
}

func TestFairShares(t *testing.T) {
	for _, c := range []struct {
		sizes  []int
		budget int
		want   []int
	}{
		{[]int{100, 200}, 1000, []int{100, 200}},            // all fit
		{[]int{100, 5000, 300}, 1000, []int{100, 600, 300}}, // the small ones whole, the rest to the big one
		{[]int{900, 900}, 1000, []int{500, 500}},            // two big ones split evenly
	} {
		if got := fairShares(c.sizes, c.budget); !reflect.DeepEqual(got, c.want) {
			t.Errorf("fairShares(%v, %d) = %v, want %v", c.sizes, c.budget, got, c.want)
		}
	}
}

// A view cut to its budget keeps whole rows and ends with how many notes
// it leaves out and the memory_query that returns them all.
func TestMarkdownWithin(t *testing.T) {
	spec, err := Parse("from: p/docs/improvements\nwhere:\n  - status in [open, done]\n  - id exists\nsort: id asc\ncolumns: [title, status]", Context{})
	if err != nil {
		t.Fatal(err)
	}
	r := &Result{Spec: spec, Total: 5}
	for i := 1; i <= 5; i++ {
		r.Hits = append(r.Hits, index.QueryHit{
			Path:   fmt.Sprintf("p/docs/improvements/IMP-%03d.md", i),
			Title:  fmt.Sprintf("IMP-%03d — %s", i, strings.Repeat("long title ", 18)),
			Fields: map[string][]string{"status": {"open"}},
		})
	}
	full := r.Markdown()
	if got := r.MarkdownWithin(len(full)); got != full {
		t.Errorf("a view that fits is unchanged:\n%s", got)
	}
	cut := r.MarkdownWithin(900)
	if len(cut) > 900 || strings.Count(cut, "\n| [[") != 2 || !strings.Contains(cut, "IMP-002") || strings.Contains(cut, "IMP-003") {
		t.Errorf("want the first two rows within 900 bytes (%d):\n%s", len(cut), cut)
	}
	want := `_Showing 2 of 5 notes, cut to fit: all of them with memory_query({"project":"p","from":"p/docs/improvements",` +
		`"where":[{"field":"status","op":"in","value":["open","done"]},{"field":"id","op":"exists","value":true}],` +
		`"sort":"id","order":"asc","fields":["status"],"limit":5})._`
	if !strings.Contains(cut, want) {
		t.Errorf("tail line:\n%s\nwant\n%s", cut, want)
	}
	// A budget below the header keeps the header and no rows.
	if tiny := r.MarkdownWithin(10); !strings.HasPrefix(tiny, "| title | status |") || !strings.Contains(tiny, "Showing 0 of 5") {
		t.Errorf("tiny budget:\n%s", tiny)
	}
}

// Within a budget, a small view of the note stays whole and the big one is
// cut; without a budget nothing is.
func TestRenderNoteWithin(t *testing.T) {
	body := "# N\n\n```view\nfrom: p/docs/improvements\nsort: id asc\ncolumns: [title, status, created]\n```\n\n```view\nfrom: p/plans\nwhere:\n  - status = done\n```\n"
	full, _ := RenderNote([]byte(body), true, schemaContext(t), testIndex(t).Query)
	out, hash, cut := RenderNoteWithin([]byte(body), true, schemaContext(t), testIndex(t).Query, 0)
	if cut || string(out) != string(full) || hash == "" {
		t.Errorf("no budget: cut=%v", cut)
	}
	out, _, cut = RenderNoteWithin([]byte(body), true, schemaContext(t), testIndex(t).Query, 250)
	if !cut || !strings.Contains(string(out), "cut to fit") || !strings.Contains(string(out), "Plan B") {
		t.Errorf("budget 250: cut=%v\n%s", cut, out)
	}
}
