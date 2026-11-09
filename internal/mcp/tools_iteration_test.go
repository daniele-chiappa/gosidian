package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/i18n"
)

// A card gets started the first time it goes to work, finished when it
// closes, and loses finished when it opens again: by an agent, by a
// person, by an ended lease.
func TestBoardTools_TheDatesOfACard(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	seedBoard(t, s, "")
	a := newCard(t, s, "Prima", map[string]any{"column": "Pronto"})
	if a.Started != "" || a.Finished != "" {
		t.Fatalf("a new card has dates: %+v", a)
	}
	got := taskCall(t, s.handleTaskClaim, map[string]any{"path": a.Path}).Task
	if got.Started == "" || got.Finished != "" {
		t.Fatalf("taken: %+v", got)
	}
	started := got.Started
	if got = taskCall(t, s.handleTaskUpdate, map[string]any{"path": a.Path, "status": "review"}).Task; got.Started != started {
		t.Fatalf("to review: %+v", got)
	}
	if got = taskCall(t, s.handleTaskComplete, map[string]any{"path": a.Path, "report": "Fatto."}).Task; got.Finished == "" || got.Started != started {
		t.Fatalf("closed: %+v", got)
	}
	if _, err := s.MoveCard(ctx, "daniele", a.Path, fleet.CardMove{Column: "Sviluppo"}); err != nil {
		t.Fatal(err)
	}
	if got = taskOf(t, s, a.Path); got.Finished != "" || got.Started != started {
		t.Fatalf("opened again: %+v", got)
	}
	// The database of the tasks has the fields of the dates.
	db, _ := s.vault.Load("p/tasks.md")
	for _, want := range []string{"started: {type: date}", "finished: {type: date}", "iteration: {type: relation}"} {
		if !strings.Contains(string(db.Content), want) {
			t.Errorf("tasks.md lacks %s:\n%s", want, db.Content)
		}
	}
	// Failed at the last attempt of its lease, it finished.
	b := newCard(t, s, "Seconda", map[string]any{"column": "Pronto"})
	taskCall(t, s.handleTaskClaim, map[string]any{"path": b.Path})
	pastLease(t, s, b.Path, 3)
	newCard(t, s, "Terza", nil)
	if got = taskOf(t, s, b.Path); got.Status != fleet.Failed || got.Finished == "" {
		t.Fatalf("failed: %+v", got)
	}
	// The server's dates are not written by hand.
	for _, k := range []string{"started", "finished"} {
		if msg := expectError(t, mustCall(s.handleTaskUpdate(ctx, call(map[string]any{"path": a.Path, "status": "review", k: "2026-11-01"})))); !strings.Contains(msg, "server") {
			t.Errorf("%s by hand: %s", k, msg)
		}
	}
}

// iterationNote is an iteration of p/board.md from start to end.
func iterationNote(start, end string) string {
	return "---\ntitle: Iterazione\ntype: iteration\nboard: \"[[p/board]]\"\ngoal: Il primo passo\nstart: " + start + "\nend: " + end + "\ntags: [p]\n---\n\n# Iterazione\n"
}

func createNote(t *testing.T, s *Server, rel, body string) {
	t.Helper()
	if res, _ := s.handleCreate(context.Background(), call(map[string]any{"path": rel, "content": body})); res.IsError {
		t.Fatalf("seed %s: %s", rel, expectError(t, res))
	}
}

// boardList is what memory_tasks says of a board beside its columns.
type boardList struct {
	Board struct {
		Flow      fleet.Flow           `json:"flow"`
		Iteration *fleet.IterationInfo `json:"iteration"`
		Retro     []string             `json:"retro"`
	} `json:"board"`
}

func listBoard(t *testing.T, s *Server) boardList {
	t.Helper()
	var out boardList
	if err := json.Unmarshal([]byte(resultText(t, mustCall(s.handleTasks(context.Background(), call(map[string]any{"project": "p", "board": "[[p/board]]"}))))), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBoardTools_FlowAndIteration(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	seedBoard(t, s, "stale: 2\n")
	day := func(d int) string { return time.Now().AddDate(0, 0, d).Format(time.DateOnly) }
	createNote(t, s, "p/iterations/1.md", iterationNote(day(-1), day(5)))
	createNote(t, s, "p/iterations/old.md", iterationNote(day(-20), day(-10)))
	createNote(t, s, "p/iterations/other.md", strings.Replace(iterationNote(day(0), day(3)), "[[p/board]]", "[[p/altra]]", 1))
	a := newCard(t, s, "Prima", map[string]any{"column": "Pronto", "iteration": "[[p/iterations/1]]"})
	if a.Iteration != "p/iterations/1.md" {
		t.Fatalf("a card in the iteration: %+v", a)
	}
	b := newCard(t, s, "Seconda", map[string]any{"column": "Pronto"})
	for name, it := range map[string]string{"no iteration": "[[p/docs/IMP-1]]", "another board's": "[[p/iterations/other]]"} {
		if msg := expectError(t, mustCall(s.handleTaskCreate(ctx, call(map[string]any{"project": "p", "board": "board", "title": "x", "iteration": it})))); !strings.Contains(msg, "iteration") {
			t.Errorf("%s: %s", name, msg)
		}
	}
	// Into the iteration, a card nobody holds; and out of it.
	if got := taskCall(t, s.handleTaskUpdate, map[string]any{"path": b.Path, "iteration": "[[p/iterations/1]]"}).Task; got.Iteration != "p/iterations/1.md" || got.Status != fleet.Submitted {
		t.Fatalf("linked: %+v", got)
	}
	if got := taskCall(t, s.handleTaskUpdate, map[string]any{"path": b.Path, "iteration": ""}).Task; got.Iteration != "" {
		t.Fatalf("unlinked: %+v", got)
	}
	taskCall(t, s.handleTaskUpdate, map[string]any{"path": b.Path, "iteration": "[[p/iterations/1]]"})

	// a at work for three days: stale, on a board that says 2.
	taskCall(t, s.handleTaskClaim, map[string]any{"path": a.Path})
	note, _ := s.vault.Load(a.Path)
	out, _ := fleet.Set(note.Content, map[string]any{fleet.KeyColumnSince: time.Now().Add(-73 * time.Hour).Format(time.RFC3339)})
	resultText(t, mustCall(s.handleUpdate(ctx, call(map[string]any{"path": a.Path, "content": string(out)}))))
	got := listBoard(t, s)
	if len(got.Board.Flow.Stale) != 1 || got.Board.Flow.Stale[0].Path != a.Path || got.Board.Flow.Stale[0].Days != 3 || got.Board.Flow.Stale[0].Column != "Analisi" {
		t.Fatalf("stale: %+v", got.Board.Flow)
	}
	if len(got.Board.Flow.Closed) != 4 || got.Board.Flow.Cycle.N != 0 || got.Board.Flow.Cycle.P50 != nil {
		t.Fatalf("flow: %+v", got.Board.Flow)
	}
	it := got.Board.Iteration
	if it == nil || it.Path != "p/iterations/1.md" || it.Goal != "Il primo passo" || it.DaysLeft != 6 || it.Cards.Total != 2 || it.Cards.Done != 0 {
		t.Fatalf("iteration: %+v", it)
	}
	// The answer on a card says the board's stale ones: a stays in its
	// column, then leaves it.
	stale := func(args map[string]any) []fleet.StaleCard {
		var reply struct {
			Board struct {
				Stale []fleet.StaleCard `json:"stale"`
			} `json:"board"`
		}
		if err := json.Unmarshal([]byte(resultText(t, mustCall(s.handleTaskUpdate(ctx, call(args))))), &reply); err != nil {
			t.Fatal(err)
		}
		return reply.Board.Stale
	}
	if got := stale(map[string]any{"path": a.Path, "status": "working", "message": "Ancora qui."}); len(got) != 1 || got[0].Path != a.Path {
		t.Fatalf("stale in the answer: %+v", got)
	}
	if got := stale(map[string]any{"path": a.Path, "column": "Sviluppo"}); len(got) != 0 {
		t.Fatalf("a moved card is still stale: %+v", got)
	}
}

func TestCloseIterations(t *testing.T) {
	defer i18n.SetLang(i18n.Lang())
	i18n.SetLang("it")
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	seedBoard(t, s, "")
	day := func(d int) string { return time.Now().AddDate(0, 0, d).Format(time.DateOnly) }
	createNote(t, s, "p/iterations/1.md", iterationNote(day(-14), day(-1)))
	createNote(t, s, "p/iterations/2.md", iterationNote(day(0), day(13)))
	done := newCard(t, s, "Fatta", map[string]any{"column": "Pronto", "iteration": "[[p/iterations/1]]"})
	taskCall(t, s.handleTaskClaim, map[string]any{"path": done.Path})
	taskCall(t, s.handleTaskComplete, map[string]any{"path": done.Path, "report": "Fatto."})
	left := newCard(t, s, "Rimasta", map[string]any{"column": "Pronto", "iteration": "[[p/iterations/1]]"})
	if n, err := s.CloseIterations(ctx, time.Now()); err != nil || n != 1 {
		t.Fatalf("closed %d %v", n, err)
	}
	note, _ := s.vault.Load("p/iterations/1.md")
	body := string(note.Content)
	for _, want := range []string{
		"# Iterazione\n\n## Revisione\n\nChiusa il " + time.Now().Format(time.DateOnly) + ". Obiettivo: Il primo passo.\n",
		"**Fatte** (1)\n\n- [[" + strings.TrimSuffix(done.Path, ".md") + "|Fatta]]\n",
		"**Rimandate, nella loro colonna** (1)\n\n- [[" + strings.TrimSuffix(left.Path, ".md") + "|Rimasta]] · Pronto\n",
		"- **Tempo di attraversamento**: pochi dati (1 schede, ne servono 3)\n",
		"## Retrospettiva\n\n- Cosa è andato bene?\n- Cosa cambiare?\n",
		"retro: pending", "reviewed: ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the iteration lacks %q:\n%s", want, body)
		}
	}
	if got := taskOf(t, s, left.Path); got.Iteration != "" || got.Column != "Pronto" {
		t.Fatalf("the card left: %+v", got)
	}
	if got := taskOf(t, s, done.Path); got.Iteration != "p/iterations/1.md" {
		t.Fatalf("the card done: %+v", got)
	}
	// Once only; the retrospective waits on the board.
	if n, err := s.CloseIterations(ctx, time.Now().Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("closed again %d %v", n, err)
	}
	if again, _ := s.vault.Load("p/iterations/1.md"); again.ETag() != note.ETag() {
		t.Fatal("the review written again")
	}
	got := listBoard(t, s)
	if len(got.Board.Retro) != 1 || got.Board.Retro[0] != "p/iterations/1.md" || got.Board.Iteration == nil || got.Board.Iteration.Path != "p/iterations/2.md" {
		t.Fatalf("the board: %+v", got.Board)
	}
	// Written, it is done.
	out, _ := fleet.Set(note.Content, map[string]any{fleet.KeyRetro: fleet.RetroDone})
	resultText(t, mustCall(s.handleUpdate(ctx, call(map[string]any{"path": "p/iterations/1.md", "content": string(out)}))))
	if got := listBoard(t, s); len(got.Board.Retro) != 0 {
		t.Fatalf("a retrospective written still waits: %+v", got.Board.Retro)
	}
	// A card does not join a closed iteration.
	if msg := expectError(t, mustCall(s.handleTaskUpdate(ctx, call(map[string]any{"path": left.Path, "iteration": "[[p/iterations/1]]"})))); !strings.Contains(msg, "closed") {
		t.Fatalf("a closed iteration: %s", msg)
	}
}
