package v1

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/fleet"
	gmcp "github.com/gosidian/gosidian/internal/mcp"
	"github.com/gosidian/gosidian/internal/vault"
)

// fakeCards records the moves of cards the PATCH hands over, and answers
// with err or warning; it runs the move's Check on the card as it is.
type fakeCards struct {
	root    string
	moves   []fleet.CardMove
	err     error
	warning string
}

func (f *fakeCards) MoveCard(_ context.Context, actor, rel string, m fleet.CardMove) (string, error) {
	f.moves = append(f.moves, m)
	if m.Check != nil {
		content, err := os.ReadFile(filepath.Join(f.root, rel))
		if err != nil {
			return "", err
		}
		if err := m.Check(content); err != nil {
			return "", err
		}
	}
	return f.warning, f.err
}

const testCard = "---\ntitle: Una scheda\ntype: task\nstatus: submitted\nboard: \"[[p/board]]\"\ncolumn: Pronto\nrank: 1\npriority: high\ntags: [p, type:task]\n---\n\n# Una scheda\n"

const cardURL = "/api/v1/notes/p/tasks/a.md/frontmatter"

func newCardFixture(t *testing.T) (*notesFixture, *fakeCards) {
	t.Helper()
	f := newNotesFixture(t)
	f.seedNote(t, "p/tasks/a.md", testCard)
	f.seedNote(t, "p/tasks/team.md", "---\ntitle: Di una squadra\ntype: task\nstatus: submitted\nteam: \"[[p/squadra]]\"\nassignee: dev\n---\n")
	cards := &fakeCards{root: f.vaultRoot}
	f.router.deps.Cards = cards
	return f, cards
}

// The edit of a card's place goes to the board's rules; the rest of its
// fields, and the fields of a team's task, are edited as any note's.
func TestFrontmatterPatch_ACardsPlace(t *testing.T) {
	f, cards := newCardFixture(t)
	r := f.doAuthRecorder(http.MethodPatch, cardURL, `{"set":{"column":"Analisi"},"expect":{"column":"Pronto"}}`, nil)
	if r.code != http.StatusOK || len(cards.moves) != 1 || cards.moves[0].Column != "Analisi" || cards.moves[0].At != nil {
		t.Fatalf("a move: %d %s %+v", r.code, r.body, cards.moves)
	}
	r = f.doAuthRecorder(http.MethodPatch, cardURL, `{"set":{"status":"review"},"at":0}`, nil)
	if r.code != http.StatusOK || len(cards.moves) != 2 || cards.moves[1].Status != "review" || cards.moves[1].At == nil || *cards.moves[1].At != 0 {
		t.Fatalf("a state and a place: %d %s %+v", r.code, r.body, cards.moves)
	}
	if r = f.doAuthRecorder(http.MethodPatch, cardURL, `{"at":2}`, nil); r.code != http.StatusOK || len(cards.moves) != 3 || *cards.moves[2].At != 2 {
		t.Fatalf("a place alone: %d %s", r.code, r.body)
	}
	// Another field of the card, and a team's task, as any note.
	if r = f.doAuthRecorder(http.MethodPatch, cardURL, `{"set":{"priority":"low"}}`, nil); r.code != http.StatusOK || len(cards.moves) != 3 ||
		!strings.Contains(f.content(t, "p/tasks/a.md"), "priority: low") {
		t.Fatalf("another field: %d %s", r.code, r.body)
	}
	if r = f.doAuthRecorder(http.MethodPatch, "/api/v1/notes/p/tasks/team.md/frontmatter", `{"set":{"status":"canceled"}}`, nil); r.code != http.StatusOK || len(cards.moves) != 3 {
		t.Fatalf("a team's task: %d %s", r.code, r.body)
	}
	for name, body := range map[string]string{
		"with another field": `{"set":{"column":"Analisi","priority":"low"}}`,
		"the rank":           `{"set":{"rank":3}}`,
		"no column":          `{"unset":["column"]}`,
		"a column not text":  `{"set":{"column":3}}`,
		"a place below zero": `{"at":-1}`,
		"its start":          `{"set":{"started":"2026-11-09T10:00:00Z"}}`,
		"its end taken away": `{"unset":["finished"]}`,
	} {
		if r = f.doAuthRecorder(http.MethodPatch, cardURL, body, nil); r.code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, r.code, r.body)
		}
	}
	if r = f.doAuthRecorder(http.MethodPatch, "/api/v1/notes/p/tasks/team.md/frontmatter", `{"at":0}`, nil); r.code != http.StatusBadRequest {
		t.Errorf("a place for a team's task: %d %s", r.code, r.body)
	}
	if len(cards.moves) != 3 {
		t.Fatalf("a refused edit moved a card: %+v", cards.moves)
	}
}

func TestFrontmatterPatch_ACardsMoveRefused(t *testing.T) {
	f, cards := newCardFixture(t)
	// A field the person saw has changed: 409 with its value now.
	r := f.doAuthRecorder(http.MethodPatch, cardURL, `{"set":{"column":"Analisi"},"expect":{"column":"Idee"}}`, nil)
	if r.code != http.StatusConflict || !strings.Contains(r.body, `"column":"Pronto"`) {
		t.Fatalf("a conflict: %d %s", r.code, r.body)
	}
	cards.err = &fleet.MoveError{Msg: "the column Analisi of p/board.md holds 1 cards, its wip"}
	if r = f.doAuthRecorder(http.MethodPatch, cardURL, `{"set":{"column":"Analisi"}}`, nil); r.code != http.StatusUnprocessableEntity || !strings.Contains(r.body, "its wip") {
		t.Fatalf("a full column: %d %s", r.code, r.body)
	}
	cards.err = &fleet.MoveError{Msg: "changed", Stale: true}
	if r = f.doAuthRecorder(http.MethodPatch, cardURL, `{"set":{"column":"Analisi"}}`, nil); r.code != http.StatusPreconditionFailed {
		t.Fatalf("a stale etag: %d %s", r.code, r.body)
	}
	// A column past its limit for a board that warns people: moved, and said.
	cards.err, cards.warning = nil, "the column Analisi of p/board.md holds 1 cards, its wip"
	if r = f.doAuthRecorder(http.MethodPatch, cardURL, `{"set":{"column":"Analisi"}}`, nil); r.code != http.StatusOK || !strings.Contains(r.body, `"warning":"the column Analisi`) {
		t.Fatalf("a warning: %d %s", r.code, r.body)
	}
}

// With the MCP server behind it, a person's move goes through the board:
// the card takes the column's state, and a full column warns.
func TestFrontmatterPatch_ACardThroughTheBoard(t *testing.T) {
	f := newNotesFixture(t)
	f.router.deps.Cards = gmcp.New(vault.New(f.vaultRoot), f.idx, nil)
	f.seedNote(t, "p/board.md", "---\ntitle: Board\ntype: board\n---\n\n```board\ncolumns:\n  - {name: Pronto, status: submitted}\n"+
		"  - {name: Analisi, status: working, wip: 1}\n  - {name: Fatto, status: completed}\n```\n")
	f.seedNote(t, "p/tasks/a.md", testCard)
	f.seedNote(t, "p/tasks/b.md", strings.Replace(testCard, "rank: 1", "rank: 2", 1))
	r := f.doAuthRecorder(http.MethodPatch, cardURL, `{"set":{"column":"Analisi"},"expect":{"column":"Pronto"}}`, nil)
	if got := f.content(t, "p/tasks/a.md"); r.code != http.StatusOK || !strings.Contains(got, "status: working\n") || !strings.Contains(got, "column: Analisi\n") ||
		strings.Contains(r.body, "warning") {
		t.Fatalf("moved: %d %s\n%s", r.code, r.body, got)
	}
	r = f.doAuthRecorder(http.MethodPatch, "/api/v1/notes/p/tasks/b.md/frontmatter", `{"set":{"column":"Analisi"}}`, nil)
	if r.code != http.StatusOK || !strings.Contains(r.body, `"warning":"the column Analisi of p/board.md holds 1 cards, its wip"`) {
		t.Fatalf("past the limit: %d %s", r.code, r.body)
	}
	if r = f.doAuthRecorder(http.MethodPatch, cardURL, `{"set":{"column":"Rilascio"}}`, nil); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("no such column: %d %s", r.code, r.body)
	}
}
