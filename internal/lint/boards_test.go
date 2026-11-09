package lint

import (
	"context"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/fleet"
)

const lintBoard = "---\ntitle: Board\ntype: board\n---\n\n```board\ncolumns:\n  - {name: Pronto, status: submitted}\n" +
	"  - {name: In corso, status: working, wip: 2}\n  - {name: Fatto, status: completed}\n```\n"

func lintCard(column, status string) string {
	return "---\ntitle: Scheda\ntype: task\nstatus: " + status + "\nboard: \"[[p/board]]\"\ncolumn: " + column + "\n---\n\n# Scheda\n"
}

func TestLint_BoardInvalid(t *testing.T) {
	l, v, idx := newTestLinter(t)
	db, _, err := fleet.BoardSchema(fleet.DatabaseNote("p"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, v, idx, "p/tasks.md", string(db))
	seed(t, v, idx, "p/board.md", lintBoard)
	seed(t, v, idx, "p/tasks/ok.md", lintCard("Pronto", "submitted"))
	seed(t, v, idx, "p/tasks/case.md", lintCard("in corso", "working"))
	seed(t, v, idx, "p/tasks/closed.md", lintCard("", "failed"))
	seed(t, v, idx, "p/tasks/drift.md", lintCard("Pronto", "working"))
	seed(t, v, idx, "p/tasks/gone.md", lintCard("Rilascio", "submitted"))
	seed(t, v, idx, "p/tasks/team.md", "---\ntitle: T\ntype: task\nstatus: submitted\nteam: \"[[p/squadra]]\"\nassignee: dev\n---\n")
	seed(t, v, idx, "p/tasks/noboard.md", strings.Replace(lintCard("Pronto", "submitted"), "[[p/board]]", "[[p/nessuna]]", 1))
	seed(t, v, idx, "p/empty-type.md", "---\ntitle: B\ntype: board\n---\n\nNo block.\n")
	// The unknown state on line 3 of the block, which starts on line 3.
	seed(t, v, idx, "p/bad.md", "# Bad\n\n```board\ncolumns:\n  - {name: Pronto, status: ready}\n```\n")
	issues, err := l.Run(context.Background(), "p", []string{"board-invalid"}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Issue{}
	for _, is := range issues {
		got[is.File] = is
	}
	if is := got["p/empty-type.md"]; !strings.Contains(is.Message, "one ```board block, this one 0") {
		t.Errorf("empty type: %+v", is)
	}
	if is := got["p/bad.md"]; !strings.Contains(is.Message, `status "ready"`) || is.Line != 5 {
		t.Errorf("bad: %+v", is)
	}
	if is := got["p/tasks/drift.md"]; !strings.Contains(is.Message, "column Pronto is submitted, the card working") || !strings.Contains(is.FixHint, "In corso") {
		t.Errorf("drift: %+v", is)
	}
	if is := got["p/tasks/gone.md"]; !strings.Contains(is.Message, "no column Rilascio") || !strings.Contains(is.FixHint, "Pronto") {
		t.Errorf("gone: %+v", is)
	}
	if is := got["p/tasks/noboard.md"]; !strings.Contains(is.Message, "p/nessuna.md is no board") {
		t.Errorf("no board: %+v", is)
	}
	if len(got) != 5 || len(issues) != 5 {
		t.Errorf("issues: %+v", issues)
	}
}

// A project with cards whose database of the tasks lost the fields of the
// cards, edited by hand after the server added them.
func TestLint_BoardInvalidDatabase(t *testing.T) {
	l, v, idx := newTestLinter(t)
	seed(t, v, idx, "p/tasks.md", string(fleet.DatabaseNote("p")))
	seed(t, v, idx, "p/board.md", lintBoard)
	seed(t, v, idx, "p/tasks/ok.md", lintCard("Pronto", "submitted"))
	// A project with no card keeps its database as it is.
	seed(t, v, idx, "q/tasks.md", string(fleet.DatabaseNote("q")))
	seed(t, v, idx, "q/board.md", strings.ReplaceAll(lintBoard, "p/", "q/"))
	issues, err := l.Run(context.Background(), "p", []string{"board-invalid"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].File != "p/tasks.md" || !strings.Contains(issues[0].Message, "backlog") || !strings.Contains(issues[0].Message, "for") {
		t.Fatalf("issues: %+v", issues)
	}
	if issues, _ = l.Run(context.Background(), "q", []string{"board-invalid"}, ""); len(issues) != 0 {
		t.Fatalf("a project without cards: %+v", issues)
	}
}

// The iterations of a board and the dates of its cards (phase 2).
func TestLint_BoardInvalidIterations(t *testing.T) {
	l, v, idx := newTestLinter(t)
	db, _, err := fleet.BoardSchema(fleet.DatabaseNote("p"))
	if err != nil {
		t.Fatal(err)
	}
	iteration := func(board, start, end string) string {
		return "---\ntitle: Iterazione\ntype: iteration\nboard: \"" + board + "\"\nstart: " + start + "\nend: " + end + "\n---\n"
	}
	card := func(extra string) string {
		return strings.Replace(lintCard("In corso", "working"), "---\n\n#", extra+"---\n\n#", 1)
	}
	seed(t, v, idx, "p/tasks.md", string(db))
	seed(t, v, idx, "p/board.md", lintBoard)
	seed(t, v, idx, "p/iterations/1.md", iteration("[[p/board]]", "2026-11-09", "2026-11-22"))
	seed(t, v, idx, "p/iterations/2.md", iteration("[[p/board]]", "2026-11-23", "2026-12-06"))
	seed(t, v, idx, "p/iterations/clash.md", iteration("[[p/board]]", "2026-12-06", "2026-12-10"))
	seed(t, v, idx, "p/iterations/back.md", iteration("[[p/board]]", "2027-01-10", "2027-01-01"))
	seed(t, v, idx, "p/iterations/noboard.md", iteration("[[p/nessuna]]", "2027-02-01", "2027-02-10"))
	seed(t, v, idx, "p/tasks/ok.md", card("iteration: \"[[p/iterations/1]]\"\nstarted: 2026-11-09T10:00:00+01:00\n"))
	seed(t, v, idx, "p/tasks/noit.md", card("iteration: \"[[p/docs/x]]\"\n"))
	seed(t, v, idx, "p/tasks/open.md", card("finished: 2026-11-10T10:00:00+01:00\n"))
	seed(t, v, idx, "p/tasks/notime.md", card("started: ieri\n"))
	issues, err := l.Run(context.Background(), "p", []string{"board-invalid"}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, is := range issues {
		got[is.File] += is.Message + "\n"
	}
	for file, want := range map[string]string{
		"p/iterations/clash.md":   "meet those of p/iterations/2.md",
		"p/iterations/back.md":    "before its start",
		"p/iterations/noboard.md": "p/nessuna.md is no board",
		"p/tasks/noit.md":         "p/docs/x.md is no iteration",
		"p/tasks/open.md":         "working and has finished",
		"p/tasks/notime.md":       `started "ieri" is no time`,
	} {
		if !strings.Contains(got[file], want) {
			t.Errorf("%s: %q, want %q", file, got[file], want)
		}
	}
	if len(got) != 6 {
		t.Errorf("issues: %+v", issues)
	}
}
