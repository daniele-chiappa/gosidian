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
