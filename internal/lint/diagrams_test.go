package lint

import (
	"context"
	"strings"
	"testing"
)

func TestLint_DiagramInvalid(t *testing.T) {
	l, v, idx := newTestLinter(t)
	seed(t, v, idx, "p/good.md", "# Good\n\n```diagram\nkind: flow\nnodes: [a, b]\nedges: [\"a -> b\"]\n```\n")
	// The edge names a node that is not there, on line 6 of the block, so
	// line 3 + 6 of the note.
	seed(t, v, idx, "p/bad.md", "# Bad\n\n```diagram\nkind: flow\nnodes: [a, b]\nedges:\n  - \"a -> b\"\n  - \"b -> c\"\n```\n")
	seed(t, v, idx, "p/empty-type.md", "---\ntitle: D\ntype: diagram\n---\n\nNo block.\n")
	seed(t, v, idx, "p/sample.md", "# Sample\n\n````md\n```diagram\nkind: nope\n```\n````\n")
	issues, err := l.Run(context.Background(), "p", []string{"diagram-invalid"}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Issue{}
	for _, is := range issues {
		got[is.File] = is
	}
	if is := got["p/bad.md"]; !strings.Contains(is.Message, `names "c"`) || is.Line != 8 {
		t.Errorf("bad: %+v", is)
	}
	if is := got["p/empty-type.md"]; !strings.Contains(is.Message, "one ```diagram block, this one 0") {
		t.Errorf("empty type: %+v", is)
	}
	if len(got) != 2 {
		t.Errorf("issues: %+v", issues)
	}
}
