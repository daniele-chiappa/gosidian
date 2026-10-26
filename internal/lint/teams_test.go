package lint

import (
	"context"
	"strings"
	"testing"
)

func TestLint_TeamInvalid(t *testing.T) {
	l, v, idx := newTestLinter(t)
	seed(t, v, idx, "p/agents/dev.md", "---\ntitle: Dev\ntype: agent\n---\n\nThe developer.\n")
	// An agent of another project is a note of the vault too.
	seed(t, v, idx, "q/agents/ops.md", "---\ntitle: Ops\ntype: agent\n---\n\nThe operator.\n")
	seed(t, v, idx, "p/team.md", "---\ntitle: Team\ntype: team\n---\n\n```team\nroles:\n  dev:\n    agent: p/agents/dev\n    run: claude.run\n  ops:\n    agent: q/agents/ops\n    run: pi.run\n```\n")
	seed(t, v, idx, "p/empty-type.md", "---\ntitle: T\ntype: team\n---\n\nNo block.\n")
	seed(t, v, idx, "p/two.md", "---\ntitle: T\ntype: team\n---\n\n```team\nroles:\n  dev: {run: claude.run}\n```\n\n```team\nroles:\n  dev: {run: claude.run}\n```\n")
	// The unknown field on line 3 of the block, which starts on line 3.
	seed(t, v, idx, "p/bad.md", "# Bad\n\n```team\nroles:\n  dev: {run: claude.run}\ncolour: red\n```\n")
	seed(t, v, idx, "p/noagent.md", "# No agent\n\n```team\nroles:\n  dev:\n    agent: p/agents/none\n    run: claude.run\n```\n")
	issues, err := l.Run(context.Background(), "p", []string{"team-invalid"}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Issue{}
	for _, is := range issues {
		got[is.File] = is
	}
	if is := got["p/empty-type.md"]; !strings.Contains(is.Message, "one ```team block, this one 0") {
		t.Errorf("empty type: %+v", is)
	}
	if is := got["p/two.md"]; !strings.Contains(is.Message, "one ```team block, this one 2") {
		t.Errorf("two blocks: %+v", is)
	}
	if is := got["p/bad.md"]; !strings.Contains(is.Message, "colour is no field of a team") || is.Line != 6 {
		t.Errorf("bad: %+v", is)
	}
	if is := got["p/noagent.md"]; !strings.Contains(is.Message, "role dev: agent p/agents/none.md is no note") || is.Line != 3 {
		t.Errorf("no agent: %+v", is)
	}
	if len(got) != 4 || len(issues) != 4 {
		t.Errorf("issues: %+v", issues)
	}
}

func TestLint_FrontmatterTagUnknown_AcceptsTeamAndTask(t *testing.T) {
	l, v, idx := newTestLinter(t)
	// type:team and type:task are in the built-in vocabulary (IMP-195); a
	// task carries no status tag, its status is a field.
	seed(t, v, idx, "proj/team.md", "---\ntitle: team\ntype: team\ntags: [proj, type:team]\n---\n\n# team\n")
	seed(t, v, idx, "proj/tasks/t1.md", "---\ntitle: t1\ntype: task\nstatus: submitted\ntags: [proj, type:task]\n---\n\n# t1\n")
	issues, err := l.Run(context.Background(), "proj", []string{"frontmatter-tag-unknown"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues for type:team + type:task, got %d: %+v", len(issues), issues)
	}
}
