package lint

import (
	"context"
	"strings"
	"testing"
)

func TestLint_ChatInvalid(t *testing.T) {
	l, v, idx := newTestLinter(t)
	seed(t, v, idx, "p/pipelines/chat.md", "---\ntitle: Chat\ntype: pipeline\n---\n\nThe bot's pipeline.\n")
	seed(t, v, idx, "p/good.md", "---\ntitle: Bot\ntype: chat\n---\n\n```chat\npipeline: pipelines/chat\n```\n")
	// The unknown field on line 2 of the block, which starts on line 3.
	seed(t, v, idx, "p/bad.md", "# Bad\n\n```chat\npipeline: pipelines/chat\ncolour: red\n```\n")
	seed(t, v, idx, "p/nopipe.md", "# No pipeline\n\n```chat\npipeline: pipelines/none\n```\n")
	seed(t, v, idx, "p/empty-type.md", "---\ntitle: B\ntype: chat\n---\n\nNo block.\n")
	issues, err := l.Run(context.Background(), "p", []string{"chat-invalid"}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Issue{}
	for _, is := range issues {
		got[is.File] = is
	}
	if is := got["p/bad.md"]; !strings.Contains(is.Message, "colour is no field of a bot") || is.Line != 5 {
		t.Errorf("bad: %+v", is)
	}
	if is := got["p/nopipe.md"]; !strings.Contains(is.Message, "pipelines/none.md is no note") {
		t.Errorf("no pipeline: %+v", is)
	}
	if is := got["p/empty-type.md"]; !strings.Contains(is.Message, "one ```chat block, this one 0") {
		t.Errorf("empty type: %+v", is)
	}
	if len(got) != 3 {
		t.Errorf("issues: %+v", issues)
	}
}
