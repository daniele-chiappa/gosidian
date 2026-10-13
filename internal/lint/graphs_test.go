package lint

import (
	"context"
	"strings"
	"testing"
)

func TestLint_GraphInvalid(t *testing.T) {
	l, v, idx := newTestLinter(t)
	seed(t, v, idx, "p/good.md", "---\ntitle: Mappa\ntype: graph\n---\n\n```graph\nsource: vec-pg/chunks\nwith: {collection: procedure}\nmode: 3d\n```\n")
	// mode on line 3 of the block, which starts on line 3 of the note.
	seed(t, v, idx, "p/bad.md", "# Bad\n\n```graph\nsource: links\ntag: adr\nmode: 4d\n```\n")
	seed(t, v, idx, "p/empty-type.md", "---\ntitle: G\ntype: graph\n---\n\nNo block.\n")
	issues, err := l.Run(context.Background(), "p", []string{"graph-invalid"}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Issue{}
	for _, is := range issues {
		got[is.File] = is
	}
	if is := got["p/bad.md"]; !strings.Contains(is.Message, "2d or 3d") || is.Line != 6 {
		t.Errorf("bad: %+v", is)
	}
	if is := got["p/empty-type.md"]; !strings.Contains(is.Message, "one ```graph block, this one 0") {
		t.Errorf("empty type: %+v", is)
	}
	if len(got) != 2 {
		t.Errorf("issues: %+v", issues)
	}
}
