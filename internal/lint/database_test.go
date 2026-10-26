package lint

import (
	"context"
	"strings"
	"testing"
)

const dbNote = "---\ntitle: Backlog\ntags: [p, type:index]\ntype: database\nsource: p/docs/improvements\nfields:\n" +
	"  id: {type: text, required: true}\n  status: {type: select, required: true, options: [open, done]}\n" +
	"  closed: {type: date}\n---\n\n# Backlog\n"

func TestLint_DatabaseFieldInvalid(t *testing.T) {
	_, v, idx := newTestLinter(t)
	seed(t, v, idx, "p/docs/improvements.md", dbNote)
	seed(t, v, idx, "p/docs/improvements/IMP-001.md",
		"---\ntitle: ok\nid: IMP-001\nstatus: done\nclosed: 2026-10-02\ntags: [p, type:doc]\n---\n\n# ok\n")
	// The drift seen in the IMP-127 lab: `resolved:` invented for `closed:`.
	seed(t, v, idx, "p/docs/improvements/IMP-002.md",
		"---\ntitle: drift\nid: IMP-002\nstatus: done\nresolved: 2026-10-02\ntags: [p, type:doc]\n---\n\n# drift\n")
	// A schema written as prose for humans: reported, not silently skipped.
	seed(t, v, idx, "p/docs/bugs.md",
		"---\ntitle: Bugs\ntags: [p, type:index]\ntype: database\nsource: p/docs/bugs\nfields:\n  - \"id: text\"\n---\n\n# Bugs\n")
	// Not a row: notes outside the source folder are never checked.
	seed(t, v, idx, "p/docs/other.md", "---\ntitle: other\ntags: [p]\nresolved: yes\n---\n")

	issues, err := New(v, idx).Run(context.Background(), "p", []string{"database-field-invalid"}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, is := range issues {
		if is.Rule != "database-field-invalid" || is.Severity != SeverityWarning {
			t.Errorf("unexpected issue %+v", is)
		}
		got[is.File] = is.Message
	}
	if len(issues) != 2 {
		t.Fatalf("want 2 issues (drifted row, unparsable schema), got %+v", issues)
	}
	if !strings.Contains(got["p/docs/improvements/IMP-002.md"], `field "resolved" is not in the schema`) {
		t.Errorf("drifted row: %q", got["p/docs/improvements/IMP-002.md"])
	}
	if !strings.Contains(got["p/docs/bugs.md"], "schema does not parse") {
		t.Errorf("prose schema: %q", got["p/docs/bugs.md"])
	}
}

// A schema field named as something gosidian computes is reported on the
// database note (BUG-140); its rows are not, since they follow the schema.
func TestLint_DatabaseReservedField(t *testing.T) {
	_, v, idx := newTestLinter(t)
	seed(t, v, idx, "p/docs/stacks.md", "---\ntitle: Stacks\ntags: [p, type:index]\ntype: database\nsource: p/docs/stacks\nfields:\n  path: {type: text}\n---\n\n# Stacks\n")
	seed(t, v, idx, "p/docs/stacks/app.md", "---\ntitle: app\npath: /opt/app\ntags: [p, type:doc]\n---\n\n# app\n")
	issues, err := New(v, idx).Run(context.Background(), "p", []string{"database-field-invalid"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].File != "p/docs/stacks.md" || !strings.Contains(issues[0].Message, `field "path" is a name gosidian already gives a meaning`) {
		t.Fatalf("issues: %+v", issues)
	}
}

// IMP-127: an Active plans section made of a view on the plans folder
// references the in-progress plans it lists.
func TestLint_StatusIncoherent_PlansListedByAView(t *testing.T) {
	_, v, idx := newTestLinter(t)
	seed(t, v, idx, "p/plans/20261002-a.md", "---\ntitle: Plan A\ntype: plan\nstatus: in-progress\ntags: [p, type:plan]\n---\n")
	seed(t, v, idx, "p/plans/20261002-b.md", "---\ntitle: Plan B\ntype: plan\nstatus: in-progress\ntags: [p, type:plan]\n---\n")
	hot := "---\ntitle: Hot\ntags: [p]\n---\n# Hot\n\n## Active plans\n\n```view\nfrom: p/plans\nwhere:\n  - title = Plan A\n```\n"
	seed(t, v, idx, "p/hot.md", hot)
	issues, err := New(v, idx).Run(context.Background(), "p", []string{"status-incoherent"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].File != "p/plans/20261002-b.md" {
		t.Errorf("want only Plan B (not listed by the view) flagged, got %+v", issues)
	}
}

// Row views (IMP-139) that would not compute are reported on the database
// note; a frontmatter link that resolves nowhere names its field.
func TestLint_RowViewsAndFrontmatterLinks(t *testing.T) {
	_, v, idx := newTestLinter(t)
	seed(t, v, idx, "p/docs/improvements.md", strings.Replace(dbNote, "---\n\n# Backlog",
		"row_views:\n  - title: Plans\n    from: p/plans\n    where: [implements contains this.id, links contains this]\n"+
			"  - title: Broken\n    from: p/plans\n    where: [status = this.nope]\n  - title: Typo\n    from: p/plans\n    where: [status ~ open]\n---\n\n# Backlog", 1))
	seed(t, v, idx, "p/plans/a.md", "---\ntitle: A\ntags: [p]\nrelated: \"[[p/missing]]\"\n---\n\nSee [[p/docs/improvements]].\n")

	issues, err := New(v, idx).Run(context.Background(), "p", []string{"database-field-invalid", "broken-wikilink"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, is := range issues {
		msgs = append(msgs, is.File+": "+is.Message)
	}
	all := strings.Join(msgs, "\n")
	for _, want := range []string{
		`p/docs/improvements.md: row view "Typo" does not compute`,
		`p/plans/a.md: wikilink target "p/missing" in frontmatter field "related" does not resolve to any note`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, `"Plans"`) || len(issues) != 3 {
		t.Errorf("want the two broken row views and the link, got:\n%s", all)
	}
}

// A database with rows: {type: plan} checks only its rows: the index of the
// folder is not one.
func TestLint_DatabaseRows(t *testing.T) {
	_, v, idx := newTestLinter(t)
	seed(t, v, idx, "p/plans.md", "---\ntitle: Plans\ntags: [p, type:index]\ntype: database\nsource: p/plans\nrows: {type: plan}\nfields:\n  status: {type: select, required: true, options: [draft, done]}\n---\n")
	seed(t, v, idx, "p/plans/README.md", "---\ntitle: Plans index\ntags: [p, type:index]\n---\n")
	seed(t, v, idx, "p/plans/a.md", "---\ntitle: A\ntype: plan\nstatus: draft\ntags: [p]\n---\n")
	seed(t, v, idx, "p/plans/b.md", "---\ntitle: B\ntags: [p, type:plan]\n---\n")
	issues, err := New(v, idx).Run(context.Background(), "p", []string{"database-field-invalid"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].File != "p/plans/b.md" || !strings.Contains(issues[0].Message, `required field "status"`) {
		t.Errorf("issues = %+v", issues)
	}
}

// A namespaced tag that disagrees with its field is reported (IMP-210):
// status on any note, and the select fields of a database's schema on its
// rows. A tag with no field stands for it; type is left alone.
func TestLint_FieldTagMismatch(t *testing.T) {
	_, v, idx := newTestLinter(t)
	seed(t, v, idx, "p/plans/a.md", "---\ntitle: A\ntype: plan\nstatus: done\ntags: [p, type:plan, status:in-progress]\n---\n")
	seed(t, v, idx, "p/plans/b.md", "---\ntitle: B\ntype: plan\nstatus: in-progress\ntags: [p, type:plan, status:In-Progress]\n---\n")
	seed(t, v, idx, "p/plans/c.md", "---\ntitle: C\ntype: plan\ntags: [p, type:plan, status:draft]\n---\n")
	seed(t, v, idx, "p/docs/tasks.md", "---\ntitle: Tasks\ntags: [p, type:index]\ntype: database\nsource: p/docs/tasks\nfields:\n"+
		"  priority: {type: select, options: [low, high]}\n  labels: {type: multi-select, options: [ui, mcp]}\n  owner: {type: text}\n---\n")
	seed(t, v, idx, "p/docs/tasks/t1.md", "---\ntitle: t1\npriority: high\nlabels: [ui, mcp]\nowner: ann\ntags: [p, type:doc, priority:low, labels:ui, owner:bob]\n---\n")
	// Outside the database, priority is no checked namespace.
	seed(t, v, idx, "p/docs/other.md", "---\ntitle: other\npriority: high\ntags: [p, type:doc, priority:low]\n---\n")
	issues, err := New(v, idx).Run(context.Background(), "p", []string{"field-tag-mismatch"}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, is := range issues {
		got[is.File] = append(got[is.File], is.Message)
	}
	if len(issues) != 2 || len(got["p/plans/a.md"]) != 1 || len(got["p/docs/tasks/t1.md"]) != 1 {
		t.Fatalf("issues: %+v", issues)
	}
	if !strings.Contains(got["p/plans/a.md"][0], `tag "status:in-progress" disagrees with the field status: done`) {
		t.Errorf("plan: %q", got["p/plans/a.md"][0])
	}
	if !strings.Contains(got["p/docs/tasks/t1.md"][0], `"priority:low"`) {
		t.Errorf("row: %q", got["p/docs/tasks/t1.md"][0])
	}
}
