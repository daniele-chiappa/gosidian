package v1

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/projects"
)

// The office's map changes a team through POST /api/v1/fleet/teams/<path>
// (IMP-233, phase C): the draft's changes checked on the note as it is, and
// written when the draft is saved on the version it began from.

func readEdit(t *testing.T, w *recorder) officeEditResponse {
	t.Helper()
	var out officeEditResponse
	if err := json.Unmarshal([]byte(w.body), &out); err != nil {
		t.Fatalf("%d %v: %s", w.code, err, w.body)
	}
	return out
}

func editBody(t *testing.T, etag string, save bool, ops ...fleet.Op) string {
	t.Helper()
	return mustJSON(t, map[string]any{"etag": etag, "save": save, "ops": ops})
}

func jsonValue(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// seedEditable is the office's team with every agent note there, and a
// profile.
func seedEditable(t *testing.T) (*notesFixture, string) {
	t.Helper()
	f := seedOffice(t)
	f.createNote(t, "Alpha/agents/dev.md", "---\ntitle: dev\n---\n\n# dev\n")
	f.createNote(t, "Alpha/profiles/reviewer.md", "---\ntitle: Chi rivede\ndescription: Legge e giudica.\ntype: profile\n---\n\n```profile\nrun: claude.run\naccess: read\nreview: true\n```\n")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/fleet/teams/Alpha/squadra.md", "", nil)
	out, _ := readOffice(t, w.body)
	if out.ETag == "" || !out.Editable {
		t.Fatalf("etag %q editable %v", out.ETag, out.Editable)
	}
	return f, out.ETag
}

func noteContent(t *testing.T, f *notesFixture, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.vaultRoot, path))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return string(b)
}

func TestFleetTeamEdit_ACheckWritesNothing(t *testing.T) {
	f, etag := seedEditable(t)
	before := noteContent(t, f, "Alpha/squadra.md")
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/fleet/teams/Alpha/squadra.md",
		editBody(t, etag, false, fleet.Op{Op: fleet.OpSet, Role: "dev", Field: "model", Value: jsonValue("big")},
			fleet.Op{Op: fleet.OpAddRole, Role: "verifica", Profile: "reviewer"}), nil)
	if w.code != http.StatusOK {
		t.Fatalf("%d %s", w.code, w.body)
	}
	out := readEdit(t, w)
	if out.Saved || out.Changed || out.ETag != etag || out.Line != 8 {
		t.Fatalf("saved %v changed %v etag %q line %d", out.Saved, out.Changed, out.ETag, out.Line)
	}
	if dev, _ := out.View.Team.Role("dev"); dev.Model != "big" || !strings.Contains(out.Block, "    model: big\n") {
		t.Fatalf("dev %+v\n%s", dev, out.Block)
	}
	if v, _ := out.View.Team.Role("verifica"); v.Run != "claude.run" || !v.Review {
		t.Fatalf("verifica: %+v", v)
	}
	// Its agent's note comes with the save: the draft reads it there.
	if strings.Join(out.Creates, " ") != "Alpha/agents/verifica.md" || out.View.Roles["verifica"].Agent.Missing {
		t.Fatalf("creates %v agent %+v", out.Creates, out.View.Roles["verifica"].Agent)
	}
	if strings.Join(out.View.Roles["verifica"].Fields, ",") != "profile,agent" {
		t.Fatalf("the fields it writes: %v", out.View.Roles["verifica"].Fields)
	}
	if len(out.Problems) != 0 {
		t.Fatalf("problems: %+v", out.Problems)
	}
	if p := out.Palette; p == nil || len(p.Profiles) != 1 || p.Profiles[0].Name != "reviewer" || p.Profiles[0].Fields["access"] != "read" ||
		p.Profiles[0].Title != "Chi rivede" || p.Profiles[0].Description != "Legge e giudica." || !strings.Contains(strings.Join(p.Runs, " "), "claude.run pi.run") {
		t.Fatalf("palette: %+v", out.Palette)
	}
	if noteContent(t, f, "Alpha/squadra.md") != before {
		t.Fatal("a check wrote the note")
	}
	if _, err := os.Stat(filepath.Join(f.vaultRoot, "Alpha/agents/verifica.md")); err == nil {
		t.Fatal("a check wrote the agent's note")
	}
}

func TestFleetTeamEdit_ASaveWritesTheTeamAndItsNotes(t *testing.T) {
	f, etag := seedEditable(t)
	ops := []fleet.Op{{Op: fleet.OpAddRole, Role: "verifica", Profile: "reviewer"},
		{Op: fleet.OpSet, Role: "capo", Field: "delegates_to", Value: jsonValue([]string{"dev", "rev", "verifica"})}}
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/fleet/teams/Alpha/squadra.md", editBody(t, etag, true, ops...), nil)
	if w.code != http.StatusOK {
		t.Fatalf("%d %s", w.code, w.body)
	}
	out := readEdit(t, w)
	if !out.Saved || out.ETag == etag || strings.Join(out.Creates, " ") != "Alpha/agents/verifica.md" || out.View.ETag != out.ETag {
		t.Fatalf("saved %v etag %q creates %v", out.Saved, out.ETag, out.Creates)
	}
	note := noteContent(t, f, "Alpha/squadra.md")
	if want := strings.Replace(strings.Replace(officeTeamNote, "delegates_to: [dev, rev]", "delegates_to: [dev, rev, verifica]", 1),
		"  doc:\n    run: pi.run\n", "  doc:\n    run: pi.run\n  verifica:\n    profile: reviewer\n    agent: \"[[Alpha/agents/verifica]]\"\n", 1); note != want {
		t.Fatalf("the note:\n%s", note)
	}
	if !strings.Contains(noteContent(t, f, "Alpha/agents/verifica.md"), "You are the agent verifica") {
		t.Fatal("the agent's note")
	}
	// The draft's etag is old now: the save stops, and a check says so.
	w = f.doAuthRecorder(http.MethodPost, "/api/v1/fleet/teams/Alpha/squadra.md", editBody(t, etag, true, ops[1]), nil)
	if w.code != http.StatusPreconditionFailed || !strings.Contains(w.body, out.ETag) {
		t.Fatalf("an old etag: %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/fleet/teams/Alpha/squadra.md", editBody(t, "", true, ops[1]), nil); w.code != http.StatusPreconditionFailed {
		t.Fatalf("no etag: %d %s", w.code, w.body)
	}
	if c := readEdit(t, f.doAuthRecorder(http.MethodPost, "/api/v1/fleet/teams/Alpha/squadra.md", editBody(t, etag, false), nil)); !c.Changed || c.ETag != out.ETag {
		t.Fatalf("a check on an old etag: changed %v etag %q", c.Changed, c.ETag)
	}
}

func TestFleetTeamEdit_WhatTheServerRefuses(t *testing.T) {
	f, etag := seedEditable(t)
	post := func(save bool, ops ...fleet.Op) *recorder {
		return f.doAuthRecorder(http.MethodPost, "/api/v1/fleet/teams/Alpha/squadra.md", editBody(t, etag, save, ops...), nil)
	}
	// A role with open tasks keeps its name: the second change, the reason.
	w := post(false, fleet.Op{Op: fleet.OpSet, Role: "doc", Field: "model", Value: jsonValue("x")}, fleet.Op{Op: fleet.OpRenameRole, Role: "dev", To: "api"})
	if w.code != http.StatusUnprocessableEntity || !strings.Contains(w.body, `"op":1`) || !strings.Contains(w.body, "role dev has 1 open tasks, Alpha/tasks/a.md") {
		t.Fatalf("rename: %d %s", w.code, w.body)
	}
	// A draft with problems is checked, not saved.
	bad := fleet.Op{Op: fleet.OpSet, Role: "doc", Field: "run", Value: jsonValue("nessuno")}
	c := readEdit(t, post(false, bad))
	if len(c.Problems) != 1 || c.Problems[0].Role != "doc" || c.Problems[0].Line != 8+17 {
		t.Fatalf("problems: %+v", c.Problems)
	}
	if w := post(true, bad); w.code != http.StatusUnprocessableEntity || !strings.Contains(w.body, `run \"nessuno\"`) {
		t.Fatalf("save with problems: %d %s", w.code, w.body)
	}
	// An agent's note missing, a skill of another type.
	c = readEdit(t, post(false, fleet.Op{Op: fleet.OpSet, Role: "rev", Field: "agent", Value: jsonValue("[[Alpha/agents/manca]]")},
		fleet.Op{Op: fleet.OpSet, Role: "rev", Field: "skills", Value: jsonValue([]string{"agents/capo"})}))
	var got []string
	for _, p := range c.Problems {
		got = append(got, p.Role+": "+p.Msg)
	}
	if strings.Join(got, "\n") != "rev: role rev: agent Alpha/agents/manca.md is no note of the project\nrev: role rev: skill Alpha/agents/capo.md is no note type: skill" {
		t.Fatalf("problems:\n%s", strings.Join(got, "\n"))
	}
	if w := post(false, fleet.Op{Op: "move"}); w.code != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown change: %d", w.code)
	}
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/fleet/teams/Alpha/nessuna.md", editBody(t, etag, false), nil); w.code != http.StatusNotFound {
		t.Fatalf("no note: %d", w.code)
	}
	// A member who reads the project sees the team, and changes nothing.
	_, member := f.memberUser(t, "bruno")
	f.setVisibility(t, "Alpha", projects.VisibilityInternal)
	r := f.req(t, http.MethodGet, "/api/v1/fleet/teams/Alpha/squadra.md", "", member)
	if out, _ := readOffice(t, r.body); r.code != http.StatusOK || out.Editable {
		t.Fatalf("reader: %d editable %v", r.code, out.Editable)
	}
	if w := f.req(t, http.MethodPost, "/api/v1/fleet/teams/Alpha/squadra.md", editBody(t, etag, false), member); w.code != http.StatusForbidden {
		t.Fatalf("reader's draft: %d %s", w.code, w.body)
	}
}

// A room created with the draft: the notes of its team come with the
// save, and the draft reads them there before.
func TestFleetTeamEdit_ARoomCreatedAndOneLinked(t *testing.T) {
	f, etag := seedEditable(t)
	f.createNote(t, "Alpha/front.md", "---\ntitle: Il front\ntype: team\n---\n\n```team\ncoordinator: lead\nroles:\n  lead: {run: pi.run, delegates_to: [ui]}\n  ui: {run: pi.run}\n```\n")
	c := readEdit(t, f.doAuthRecorder(http.MethodPost, "/api/v1/fleet/teams/Alpha/squadra.md", editBody(t, etag, false), nil))
	if c.Palette == nil || len(c.Palette.Teams) != 1 || c.Palette.Teams[0].Path != "Alpha/front.md" || c.Palette.Teams[0].Title != "Il front" {
		t.Fatalf("the teams to link: %+v", c.Palette)
	}
	ops := []fleet.Op{{Op: fleet.OpCreateRoom, Room: "retro", Coordinator: "guida"}, {Op: fleet.OpAddRoom, Room: "front", Path: "front"},
		{Op: fleet.OpSet, Role: "capo", Field: "delegates_to", Value: jsonValue([]string{"dev", "retro", "front"})}}
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/fleet/teams/Alpha/squadra.md", editBody(t, etag, false, ops...), nil)
	c = readEdit(t, w)
	if w.code != http.StatusOK || len(c.Problems) != 0 || len(c.View.Rooms) != 2 || c.View.Rooms[0].View == nil || c.View.Rooms[0].View.Team.Coordinator != "guida" {
		t.Fatalf("%d problems %+v rooms %+v", w.code, c.Problems, c.View.Rooms)
	}
	// The team's notes, and those of the project it lacks: the database of
	// the tasks, the agent of guida, two profiles (dev's agent and the
	// reviewer are there).
	if strings.Join(c.Creates, " ") != "Alpha/retro.md Alpha/tasks.md Alpha/retro-tasks.md Alpha/pipelines/retro-work.md Alpha/pipelines/retro-alert.md Alpha/pipelines/retro-coordinate.md Alpha/agents/guida.md Alpha/profiles/developer.md Alpha/profiles/analyst.md" {
		t.Fatalf("creates: %v", c.Creates)
	}
	w = f.doAuthRecorder(http.MethodPost, "/api/v1/fleet/teams/Alpha/squadra.md", editBody(t, etag, true, ops...), nil)
	if out := readEdit(t, w); w.code != http.StatusOK || !out.Saved || out.View.Rooms[0].View == nil {
		t.Fatalf("save: %d %s", w.code, w.body)
	}
	if !strings.Contains(noteContent(t, f, "Alpha/retro.md"), "coordinator: guida") || !strings.Contains(noteContent(t, f, "Alpha/squadra.md"), "  retro: \"[[Alpha/retro]]\"\n  front: \"[[Alpha/front]]\"\n") {
		t.Fatal("the room's note or the team's rooms")
	}
	// A room that cannot be one: it has no coordinator.
	f.createNote(t, "Alpha/sola.md", "---\ntype: team\n---\n\n```team\nroles:\n  a: {run: pi.run}\n```\n")
	etag = readEdit(t, f.doAuthRecorder(http.MethodPost, "/api/v1/fleet/teams/Alpha/squadra.md", editBody(t, "", false), nil)).ETag
	c = readEdit(t, f.doAuthRecorder(http.MethodPost, "/api/v1/fleet/teams/Alpha/squadra.md", editBody(t, etag, false, fleet.Op{Op: fleet.OpAddRoom, Room: "sola", Path: "sola"}), nil))
	if len(c.Problems) != 1 || c.Problems[0].Room != "sola" || !strings.Contains(c.Problems[0].Msg, "has no coordinator") {
		t.Fatalf("problems: %+v", c.Problems)
	}
}
