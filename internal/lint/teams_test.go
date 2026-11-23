package lint

import (
	"context"
	"fmt"
	"slices"
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

// The guides and skills of a team's roles (IMP-233): a note missing from
// the project, a skill that is no note type: skill; the team's guide once,
// not once a role.
func TestLint_TeamInvalid_GuidesAndSkills(t *testing.T) {
	l, v, idx := newTestLinter(t)
	seed(t, v, idx, "p/skills/test.md", "---\ntitle: Test\ntype: skill\n---\n\nRun the tests.\n")
	seed(t, v, idx, "p/skills/plain.md", "---\ntitle: Plain\ntype: note\n---\n\nNo skill.\n")
	seed(t, v, idx, "p/guide/codice.md", "---\ntitle: Codice\n---\n\nThe rules.\n")
	seed(t, v, idx, "p/team.md", "---\ntitle: Team\ntype: team\n---\n\n```team\nguide: regole\nroles:\n"+
		"  dev: {run: claude.run, guide: guide/codice, skills: [skills/test, skills/plain, skills/none]}\n"+
		"  rev: {run: claude.run, guide: guide/none}\n```\n")
	issues, err := l.Run(context.Background(), "p", []string{"team-invalid"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, is := range issues {
		got = append(got, is.Message)
	}
	want := []string{
		"the team at line 6: guide p/regole.md is no note of the project",
		"the team at line 6: role dev: skill p/skills/plain.md is no note type: skill",
		"the team at line 6: role dev: skill p/skills/none.md is no note of the project",
		"the team at line 6: role rev: guide p/guide/none.md is no note of the project",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("issues:\n%s", strings.Join(got, "\n"))
	}
}

// The profiles (IMP-233): a profile note that does not read, a role whose
// profile is missing; a good one takes no issue, and its team neither.
func TestLint_TeamInvalid_Profiles(t *testing.T) {
	l, v, idx := newTestLinter(t)
	seed(t, v, idx, "p/profiles/analista.md", "---\ntitle: Analista\ntype: profile\n---\n\n```profile\nrun: claude.run\naccess: read\n```\n")
	seed(t, v, idx, "p/profiles/rotto.md", "---\ntitle: Rotto\ntype: profile\n---\n\n# Rotto\n\n```profile\nrun: claude.run\nrepo: x\n```\n")
	seed(t, v, idx, "p/profiles/vuoto.md", "---\ntitle: Vuoto\ntype: profile\n---\n\nNo block.\n")
	seed(t, v, idx, "p/team.md", "---\ntitle: Team\ntype: team\n---\n\n```team\nroles:\n  a: {profile: analista}\n  b: {profile: nessuno, run: pi.run}\n```\n")
	issues, err := l.Run(context.Background(), "p", []string{"team-invalid"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, is := range issues {
		got = append(got, fmt.Sprintf("%s:%d %s", is.File, is.Line, is.Message))
	}
	want := []string{
		"p/profiles/rotto.md:8 the profile at line 8: repo is no field of a profile: " + "agent, run, model, effort, max_turns, max_seconds, max_tokens, access, review, guide, skills, tools",
		"p/profiles/vuoto.md:0 a note type: profile holds one ```profile block, this one 0",
		"p/team.md:9 the team at line 6: role b: profile p/profiles/nessuno.md: no note of the project",
	}
	slices.Sort(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("issues:\n%s", strings.Join(got, "\n"))
	}
}

// The rooms (IMP-233): a team that reads, with a coordinator and no rooms,
// one team's.
func TestLint_TeamInvalid_Rooms(t *testing.T) {
	l, v, idx := newTestLinter(t)
	team := func(block string) string { return "---\ntitle: T\ntype: team\n---\n\n```team\n" + block + "```\n" }
	seed(t, v, idx, "p/front.md", team("coordinator: lead\nroles:\n  lead: {run: pi.run, delegates_to: [ui]}\n  ui: {run: pi.run}\n"))
	seed(t, v, idx, "p/senza.md", team("roles:\n  ui: {run: pi.run}\n"))
	seed(t, v, idx, "p/grande.md", team("coordinator: a\nrooms:\n  x: front\nroles:\n  a: {run: pi.run, delegates_to: [x]}\n"))
	seed(t, v, idx, "p/squadra.md", team("coordinator: capo\nrooms:\n  front: front\n  senza: senza\n  grande: grande\n  manca: manca\nroles:\n  capo: {run: pi.run, delegates_to: [front, senza, grande, manca]}\n"))
	issues, err := l.Run(context.Background(), "p", []string{"team-invalid"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, is := range issues {
		got = append(got, fmt.Sprintf("%s %s", is.File, is.Message))
	}
	slices.Sort(got)
	want := []string{
		"p/squadra.md the team at line 6: room front: p/front.md is a room of p/grande.md too: a room is one team's",
		"p/squadra.md the team at line 6: room grande: p/grande.md has rooms of its own: a room has none",
		"p/squadra.md the team at line 6: room manca: p/manca.md is no note of the project",
		"p/squadra.md the team at line 6: room senza: p/senza.md has no coordinator to receive the objectives",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("issues:\n%s", strings.Join(got, "\n"))
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
