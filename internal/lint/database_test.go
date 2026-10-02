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
