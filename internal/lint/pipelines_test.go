package lint

import (
	"context"
	"strings"
	"testing"
)

func TestLint_PipelineInvalid(t *testing.T) {
	l, v, idx := newTestLinter(t)
	block := func(y string) string {
		return "---\ntitle: P\ntype: pipeline\n---\n\n```pipeline\n" + y + "```\n"
	}
	seed(t, v, idx, "p/pipelines/good.md", block("nodes:\n  - {id: a, use: note.read, with: {path: x}}\n"))
	seed(t, v, idx, "p/pipelines/unknown.md", block("nodes:\n  - {id: a, use: shell.run}\n"))
	seed(t, v, idx, "p/pipelines/ref.md", block("nodes:\n  - {id: a, use: note.read, with: {path: \"{{ b.path }}\"}}\n  - {id: b, use: note.read, with: {path: x}}\n"))
	seed(t, v, idx, "p/pipelines/none.md", "---\ntitle: P\ntype: pipeline\n---\n\nNo block.\n")
	seed(t, v, idx, "p/notes/plain.md", "---\ntitle: Plain\n---\n\n```pipeline\nnot: [yaml\n```\n")
	issues, err := l.Run(context.Background(), "p", []string{"pipeline-invalid"}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, is := range issues {
		got[is.File] = is.Message
	}
	for file, want := range map[string]string{
		"p/pipelines/unknown.md": "shell.run",
		"p/pipelines/ref.md":     "b",
		"p/pipelines/none.md":    "no ```pipeline block",
	} {
		if !strings.Contains(got[file], want) {
			t.Errorf("%s: %q, want it to name %q", file, got[file], want)
		}
	}
	if len(got) != 3 {
		t.Errorf("issues: %+v", issues)
	}
}
