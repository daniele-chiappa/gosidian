package views

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if strings.Contains(string(web), "```view\nfrom: p/x") || !strings.Contains(string(web), "<div class=\"gosidian-view\">\n\nRESULT(from: p/x)") {
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
