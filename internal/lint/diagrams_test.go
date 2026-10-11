package lint

import (
	"context"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/diagram"
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

func TestLint_DiagramTwinStale(t *testing.T) {
	l, v, idx := newTestLinter(t)
	yaml := "kind: tree\nroot: A\n"
	twin := func(src string) string {
		return "```mermaid\n%% diagram-twin " + diagram.Hash(src) + " — made from the diagram above: change that one\nflowchart TB\n```\n"
	}
	seed(t, v, idx, "p/current.md", "# C\n\n```diagram\n"+yaml+"```\n\n"+twin(yaml))
	// An agent changed the diagram and left the twin.
	seed(t, v, idx, "p/stale.md", "# S\n\n```diagram\nkind: tree\nroot: B\n```\n\n"+twin(yaml))
	seed(t, v, idx, "p/orphan.md", "# O\n\nText.\n\n"+twin(yaml))
	seed(t, v, idx, "p/plain.md", "# P\n\n```diagram\n"+yaml+"```\n\n```mermaid\nflowchart TB\n```\n")
	issues, err := l.Run(context.Background(), "p", []string{"diagram-twin-stale"}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Issue{}
	for _, is := range issues {
		got[is.File] = is
	}
	if is := got["p/stale.md"]; is.Line != 8 || !strings.Contains(is.Message, "another version of the diagram at line 3") {
		t.Errorf("stale: %+v", is)
	}
	if is := got["p/orphan.md"]; is.Line != 5 || !strings.Contains(is.Message, "no ```diagram block is right above it") {
		t.Errorf("orphan: %+v", is)
	}
	if len(got) != 2 {
		t.Errorf("issues: %+v", issues)
	}
}
