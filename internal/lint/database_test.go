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
