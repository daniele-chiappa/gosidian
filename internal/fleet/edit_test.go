package fleet

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

// The block of a team as a person writes it: comments, blank lines, a
// field the map does not show.
const editBlock = `# The team of the app.
coordinator: capo
budget: {day_usd: 10, task_usd: 2}

roles:
  # Who splits the work.
  capo:
    agent: "[[fitness/agents/capo]]"
    run: pi.run
    delegates_to: [dev, rev]

  dev:
    agent: "[[fitness/agents/dev]]"
    run: pi.run
    model: qwen3 # the cheap one
    writes: [fitness/app/]
    max_turns: 30

  rev:
    run: pi.run
    review: true
    access: read
# the end
`

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func editEnv() EditEnv {
	return EditEnv{
		Profiles: func(p string) (string, error) {
			switch p {
			case "fitness/profiles/reviewer.md":
				return "run: claude.run\naccess: read\nreview: true\n", nil
			case "fitness/profiles/writer.md":
				return "agent: \"[[fitness/agents/writer]]\"\nmodel: big\n", nil
			}
			return "", errors.New("no note of the project")
		},
		OpenTasks: func(role string) []string {
			if role == "rev" {
				return []string{"fitness/tasks/a.md", "fitness/tasks/b.md"}
			}
			return nil
		},
		Exists: func(p string) bool {
			return slices.Contains([]string{"fitness/front.md", "fitness/notes.md", "fitness/agents/dev.md", "fitness/tasks.md"}, p)
		},
		IsTeam: func(p string) bool { return p == "fitness/front.md" },
	}
}

func mustEdit(t *testing.T, ops ...Op) (string, []NewNote) {
	t.Helper()
	out, staged, err := Edit(editBlock, "fitness/squadra.md", ops, editEnv())
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if _, problems := ParseWith(out, "fitness/squadra.md", editEnv().Profiles); len(problems) > 0 {
		t.Fatalf("the block does not read: %v\n%s", problems, out)
	}
	return out, staged
}

// A change writes again the entry it changes; every other line stays as
// it was, the comments and blank lines too.
func TestEditKeepsTheLines(t *testing.T) {
	out, _ := mustEdit(t, Op{Op: OpSet, Role: "dev", Field: "model", Value: raw("big")})
	if want := strings.Replace(editBlock, "model: qwen3 # the cheap one", "model: big # the cheap one", 1); out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	// Nothing changed is the block as it was.
	if out, _ := mustEdit(t); out != editBlock {
		t.Fatalf("no change:\n%s", out)
	}
	// A field taken away, one added at the role's end.
	out, _ = mustEdit(t, Op{Op: OpSet, Role: "dev", Field: "max_turns", Value: raw(nil)},
		Op{Op: OpSet, Role: "dev", Field: "tools", Value: raw([]string{})})
	if want := strings.Replace(editBlock, "    max_turns: 30\n", "    tools: []\n", 1); out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestEditTeamFields(t *testing.T) {
	out, _ := mustEdit(t, Op{Op: OpSet, Field: "budget.day_usd", Value: raw(5)},
		Op{Op: OpSet, Field: "max_agents", Value: raw(3)}, Op{Op: OpSet, Field: "coordinator", Value: raw("dev")})
	for _, want := range []string{"coordinator: dev\n", "budget: {day_usd: 5, task_usd: 2}\nmax_agents: 3\n\nroles:\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("no %q in:\n%s", want, out)
		}
	}
	if !strings.HasPrefix(out, "# The team of the app.\n") || !strings.HasSuffix(out, "# the end\n") {
		t.Fatalf("the comments:\n%s", out)
	}
	for _, op := range []Op{
		{Op: OpSet, Field: "coordinator", Value: raw("nessuno")},
		{Op: OpSet, Field: "roles", Value: raw("x")},
		{Op: OpSet, Field: "max_agents", Value: raw(-1)},
		{Op: OpSet, Field: "max_agents", Value: raw(1.5)},
		{Op: OpSet, Role: "dev", Field: "colour", Value: raw("red")},
		{Op: OpSet, Role: "dev", Field: "review", Value: raw("yes")},
		{Op: OpSet, Role: "dev", Field: "writes", Value: raw("fitness/")},
		{Op: OpSet, Role: "nessuno", Field: "model", Value: raw("x")},
		{Op: "move"},
	} {
		if _, _, err := Edit(editBlock, "fitness/squadra.md", []Op{{Op: OpSet, Role: "dev", Field: "model", Value: raw("x")}, op}, editEnv()); err == nil {
			t.Errorf("%+v: no error", op)
		} else if oe := (*OpError)(nil); !errors.As(err, &oe) || oe.Index != 1 {
			t.Errorf("%+v: %v", op, err)
		}
	}
}

// A role added: its profile's executor and agent, or the team's first
// role's executor and an agent note of its name, written with the block
// when missing.
func TestEditAddRole(t *testing.T) {
	out, staged := mustEdit(t, Op{Op: OpAddRole, Role: "verifica", Profile: "reviewer"})
	if !strings.HasSuffix(out, "    access: read\n  verifica:\n    profile: reviewer\n    agent: \"[[fitness/agents/verifica]]\"\n# the end\n") {
		t.Fatalf("got:\n%s", out)
	}
	if len(staged) != 1 || staged[0].Path != "fitness/agents/verifica.md" || !staged[0].IfMissing {
		t.Fatalf("staged: %+v", staged)
	}
	team, _ := ParseWith(out, "fitness/squadra.md", editEnv().Profiles)
	if r, _ := team.Role("verifica"); r.Run != "claude.run" || !r.Review {
		t.Fatalf("role: %+v", r)
	}
	// Without a profile, the first role's executor; the agent of a
	// profile is the profile's.
	out, staged = mustEdit(t, Op{Op: OpAddRole, Role: "nuovo"}, Op{Op: OpAddRole, Role: "scrive", Profile: "writer"})
	if !strings.Contains(out, "  nuovo:\n    agent: \"[[fitness/agents/nuovo]]\"\n    run: pi.run\n  scrive:\n    profile: writer\n    run: pi.run\n") {
		t.Fatalf("got:\n%s", out)
	}
	if len(staged) != 1 || staged[0].Path != "fitness/agents/nuovo.md" {
		t.Fatalf("staged: %+v", staged)
	}
	// An agent note that exists is not written; a role renamed in the
	// draft takes the agent of its new name.
	_, staged = mustEdit(t, Op{Op: OpAddRole, Role: "tmp"}, Op{Op: OpRemoveRole, Role: "dev"}, Op{Op: OpRenameRole, Role: "tmp", To: "dev"})
	if len(staged) != 0 {
		t.Fatalf("staged: %+v", staged)
	}
	_, staged = mustEdit(t, Op{Op: OpAddRole, Role: "tmp"}, Op{Op: OpRenameRole, Role: "tmp", To: "altro"})
	if len(staged) != 1 || staged[0].Path != "fitness/agents/altro.md" {
		t.Fatalf("staged: %+v", staged)
	}
	for _, op := range []Op{
		{Op: OpAddRole, Role: "dev"},
		{Op: OpAddRole, Role: "Dev"},
		{Op: OpAddRole, Role: "x", Profile: "nessuno"},
	} {
		if _, _, err := Edit(editBlock, "fitness/squadra.md", []Op{op}, editEnv()); err == nil {
			t.Errorf("%+v: no error", op)
		}
	}
}

func TestEditRenameAndRemove(t *testing.T) {
	out, _ := mustEdit(t, Op{Op: OpRenameRole, Role: "capo", To: "guida"}, Op{Op: OpRenameRole, Role: "dev", To: "sviluppa"})
	want := strings.NewReplacer("coordinator: capo", "coordinator: guida", "  capo:\n", "  guida:\n",
		"delegates_to: [dev, rev]", "delegates_to: [sviluppa, rev]", "  dev:\n", "  sviluppa:\n").Replace(editBlock)
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	out, _ = mustEdit(t, Op{Op: OpRemoveRole, Role: "dev"})
	if strings.Contains(out, "dev") || !strings.Contains(out, "delegates_to: [rev]\n\n  rev:\n") {
		t.Fatalf("got:\n%s", out)
	}
	for _, c := range []struct {
		op   Op
		want string
	}{
		{Op{Op: OpRenameRole, Role: "rev", To: "giudica"}, "role rev has 2 open tasks, fitness/tasks/a.md, fitness/tasks/b.md: finish them or give them to another role before you rename it"},
		{Op{Op: OpRemoveRole, Role: "rev"}, "before you take away it"},
		{Op{Op: OpRemoveRole, Role: "capo"}, "role capo is the coordinator: give the team another one first"},
		{Op{Op: OpRenameRole, Role: "dev", To: "rev"}, "the team has a role rev already"},
	} {
		_, _, err := Edit(editBlock, "fitness/squadra.md", []Op{c.op}, editEnv())
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v", c.op, err)
		}
	}
	// The last role stays.
	if _, _, err := Edit("roles:\n  solo: {run: pi.run}\n", "fitness/s.md", []Op{{Op: OpRemoveRole, Role: "solo"}}, editEnv()); err == nil {
		t.Error("the last role went")
	}
}

func TestEditRooms(t *testing.T) {
	out, staged := mustEdit(t, Op{Op: OpAddRoom, Room: "front", Path: "front"},
		Op{Op: OpSet, Role: "capo", Field: "delegates_to", Value: raw([]string{"dev", "rev", "front"})})
	if !strings.Contains(out, "budget: {day_usd: 10, task_usd: 2}\nrooms:\n  front: \"[[fitness/front]]\"\n\nroles:\n") ||
		!strings.Contains(out, "delegates_to: [dev, rev, front]") || len(staged) != 0 {
		t.Fatalf("got:\n%s\n%+v", out, staged)
	}
	out, _, err := Edit(out, "fitness/squadra.md", []Op{{Op: OpRemoveRoom, Room: "front"}}, editEnv())
	if err != nil || out != editBlock {
		t.Fatalf("removed: %v\n%s", err, out)
	}
	// A room created: the notes of a new team, as memory_create_team
	// writes them; taken away in the same draft, none.
	out, staged = mustEdit(t, Op{Op: OpCreateRoom, Room: "retro", Coordinator: "guida", Roles: []string{"guida", "api"}})
	var paths []string
	for _, n := range staged {
		paths = append(paths, n.Path)
	}
	if !strings.Contains(out, "  retro: \"[[fitness/retro]]\"\n") || strings.Join(paths, " ") !=
		"fitness/retro.md fitness/retro-tasks.md fitness/pipelines/retro-work.md fitness/pipelines/retro-alert.md fitness/pipelines/retro-coordinate.md fitness/agents/guida.md fitness/agents/api.md fitness/profiles/developer.md fitness/profiles/reviewer.md fitness/profiles/analyst.md" {
		t.Fatalf("got:\n%s\n%v", out, paths)
	}
	room, problems := Parse(string(Find(staged[0].Content)[0].Src), "fitness/retro.md")
	if len(problems) > 0 || room.Coordinator != "guida" || room.Roles[0].Run != "pi.run" {
		t.Fatalf("room: %+v %v", room, problems)
	}
	if _, staged = mustEdit(t, Op{Op: OpCreateRoom, Room: "retro"}, Op{Op: OpRemoveRoom, Room: "retro"}); len(staged) != 0 {
		t.Fatalf("staged: %+v", staged)
	}
	for _, c := range []struct {
		op   Op
		want string
	}{
		{Op{Op: OpAddRoom, Room: "back", Path: "back"}, "fitness/back.md is no note of the project"},
		{Op{Op: OpAddRoom, Room: "note", Path: "notes"}, "fitness/notes.md is no note type: team"},
		{Op{Op: OpAddRoom, Room: "me", Path: "squadra"}, "the team itself"},
		{Op{Op: OpAddRoom, Room: "dev", Path: "front"}, "the team has a role dev already"},
		{Op{Op: OpCreateRoom, Room: "front"}, "fitness/front.md exists already"},
		{Op{Op: OpRemoveRoom, Room: "front"}, "the team has no room front"},
	} {
		_, _, err := Edit(editBlock, "fitness/squadra.md", []Op{c.op}, editEnv())
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v", c.op, err)
		}
	}
}

// A block the lines cannot keep is written whole, and reads the same.
func TestEditWritesWhole(t *testing.T) {
	src := "roles: {capo: {run: pi.run, delegates_to: [dev]}, dev: {run: pi.run}}\n"
	out, _, err := Edit(src, "fitness/s.md", []Op{{Op: OpRenameRole, Role: "dev", To: "api"}}, editEnv())
	if err != nil {
		t.Fatal(err)
	}
	team, problems := Parse(out, "fitness/s.md")
	if len(problems) > 0 || len(team.Roles) != 2 || team.Roles[1].Name != "api" || team.Roles[0].DelegatesTo[0] != "api" {
		t.Fatalf("got:\n%s\n%v", out, problems)
	}
	// Windows lines stay Windows lines.
	out, _, err = Edit(strings.ReplaceAll(editBlock, "\n", "\r\n"), "fitness/squadra.md", []Op{{Op: OpSet, Role: "dev", Field: "model", Value: raw("big")}}, editEnv())
	if err != nil || !strings.Contains(out, "model: big # the cheap one\r\n") || strings.Count(out, "\r\n") != strings.Count(out, "\n") {
		t.Fatalf("crlf: %v\n%q", err, out)
	}
	if _, _, err := Edit("roles: [a\n", "fitness/s.md", nil, editEnv()); err == nil {
		t.Fatal("a block that does not read")
	}
}

func TestEditNote(t *testing.T) {
	note := "---\ntype: team\n---\n\n# La squadra\n\n```team\n" + editBlock + "```\n\nAfter.\n"
	out, block, line, _, err := EditNote([]byte(note), "fitness/squadra.md", []Op{{Op: OpSet, Role: "dev", Field: "model", Value: raw("big")}}, editEnv())
	if err != nil || line != 7 || !strings.Contains(block, "model: big") {
		t.Fatalf("%v %d\n%s", err, line, block)
	}
	if want := strings.Replace(note, "model: qwen3", "model: big", 1); string(out) != want {
		t.Fatalf("got:\n%s", out)
	}
	if _, _, _, _, err := EditNote([]byte("no block\n"), "fitness/s.md", nil, editEnv()); err == nil {
		t.Fatal("a note without a block")
	}
}

// The problems of a role and of a room say which, for the map.
func TestProblemsSayTheRole(t *testing.T) {
	_, problems := Parse("rooms:\n  dev: front.md\nroles:\n  capo: {run: pi.run, delegates_to: [capo]}\n  dev: {run: x}\n", "fitness/s.md")
	var got []string
	for _, p := range problems {
		got = append(got, p.Role+"/"+p.Room+"/"+p.String())
	}
	want := []string{
		"dev//line 5: role dev: run \"x\": the node that runs it, a plugin's <name>.run (claude.run, pi.run)",
		"/dev/line 2: room dev: a role has its name",
		"capo//line 4: role capo: delegates_to itself",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s", strings.Join(got, "\n"))
	}
}
