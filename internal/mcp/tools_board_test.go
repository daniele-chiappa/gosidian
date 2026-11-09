package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/fleet"
)

// testBoard is a board of project p: Analisi takes one card at work.
func testBoard(extra string) string {
	return "---\ntitle: Board\ntype: board\ntags: [p]\n---\n\n# Board\n\n```board\ncolumns:\n" +
		"  - {name: Idee, status: backlog}\n  - {name: Pronto, status: submitted}\n  - {name: Analisi, status: working, wip: 1}\n" +
		"  - {name: Sviluppo, status: working}\n  - {name: Verifica, status: review}\n  - {name: Fatto, status: completed}\n" + extra +
		"```\n\n## Definizione di fatto\n\nI test passano.\n\n```view\nfrom: p/tasks\nas: board\ngroup_by: status\n```\n"
}

func seedBoard(t *testing.T, s *Server, extra string) {
	t.Helper()
	for rel, body := range map[string]string{"p/board.md": testBoard(extra), "p/docs/IMP-1.md": "---\ntitle: IMP-1\n---\n\n# IMP-1\n"} {
		if res, _ := s.handleCreate(context.Background(), call(map[string]any{"path": rel, "content": body})); res.IsError {
			t.Fatalf("seed %s: %s", rel, expectError(t, res))
		}
	}
}

// taskReply is a task tool's answer.
type taskReply struct {
	Task    fleet.Task `json:"task"`
	Warning string     `json:"warning"`
	Board   struct {
		Path string `json:"path"`
		Text string `json:"text"`
	} `json:"board"`
}

func taskCall(t *testing.T, h toolFn, args map[string]any) taskReply {
	t.Helper()
	var out taskReply
	if err := json.Unmarshal([]byte(resultText(t, mustCall(h(context.Background(), call(args))))), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func newCard(t *testing.T, s *Server, title string, args map[string]any) fleet.Task {
	t.Helper()
	a := map[string]any{"project": "p", "board": "[[p/board]]", "title": title}
	for k, v := range args {
		a[k] = v
	}
	return taskCall(t, s.handleTaskCreate, a).Task
}

func TestBoardTools_CreateACard(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	seedBoard(t, s, "")
	a := newCard(t, s, "Una idea", map[string]any{"body": "Pensaci.", "for": []any{"[[p/docs/IMP-1]]"}})
	if a.Board != "p/board.md" || a.Column != "Idee" || a.Status != fleet.Backlog || a.Rank != 1 || a.Team != "" ||
		len(a.For) != 1 || a.For[0] != "p/docs/IMP-1.md" || a.ColumnSince == "" {
		t.Fatalf("card: %+v", a)
	}
	b := newCard(t, s, "Pronta", map[string]any{"column": "pronto", "done_by": "person"})
	if b.Column != "Pronto" || b.Status != fleet.Submitted || b.Rank != 1 || b.DoneBy != fleet.DonePerson {
		t.Fatalf("card in Pronto: %+v", b)
	}
	if c := newCard(t, s, "Seconda idea", nil); c.Rank != 2 {
		t.Fatalf("the second card of Idee: rank %v", c.Rank)
	}
	// The project had no database of its tasks: the first card wrote one
	// that holds cards.
	db, err := s.vault.Load("p/tasks.md")
	if err != nil || !strings.Contains(string(db.Content), "options: [backlog, submitted") || !strings.Contains(string(db.Content), "for: {type: relation}") {
		t.Fatalf("tasks.md: %v\n%s", err, db.Content)
	}
	seedFleet(t, s, ctx, fleetTask("submitted", ""))
	for name, args := range map[string]map[string]any{
		"a team on a board": {"project": "p", "board": "board", "team": "squadra", "assignee": "dev", "title": "x"},
		"neither":           {"project": "p", "title": "x"},
		"a team, no role":   {"project": "p", "team": "squadra", "title": "x"},
		"no board":          {"project": "p", "board": "[[p/nessuna]]", "title": "x"},
		"not a board":       {"project": "p", "board": "[[p/docs/IMP-1]]", "title": "x"},
		"no column":         {"project": "p", "board": "board", "column": "Rilascio", "title": "x"},
		"a column at work":  {"project": "p", "board": "board", "column": "Sviluppo", "title": "x"},
		"bad done_by":       {"project": "p", "board": "board", "done_by": "robot", "title": "x"},
		"for no note":       {"project": "p", "board": "board", "for": []any{"[[p/docs/IMP-9]]"}, "title": "x"},
		"hold":              {"project": "p", "board": "board", "hold": true, "title": "x"},
		"parent":            {"project": "p", "board": "board", "parent": "p/tasks/a.md", "title": "x"},
	} {
		if res := mustCall(s.handleTaskCreate(ctx, call(args))); !res.IsError {
			t.Errorf("%s: created", name)
		}
	}
}

func TestBoardTools_TheDatabaseOfATeamTakesCards(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	resultText(t, mustCall(s.handleCreateTeam(ctx, call(map[string]any{"project": "p", "name": "squadra"}))))
	before, _ := s.vault.Load("p/tasks.md")
	if strings.Contains(string(before.Content), "backlog") {
		t.Fatal("a team's database holds cards before a board")
	}
	seedBoard(t, s, "")
	newCard(t, s, "Una idea", nil)
	after, _ := s.vault.Load("p/tasks.md")
	if !strings.Contains(string(after.Content), "options: [backlog, submitted") || !strings.Contains(string(after.Content), "  team: {type: relation}\n") {
		t.Fatalf("tasks.md:\n%s", after.Content)
	}
	newCard(t, s, "Altra idea", nil)
	if again, _ := s.vault.Load("p/tasks.md"); string(again.Content) != string(after.Content) {
		t.Fatal("tasks.md written again")
	}
}

func TestBoardTools_TakeMoveAndClose(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedBoard(t, s, "")
	first := newCard(t, s, "Prima", map[string]any{"column": "Pronto"})
	second := newCard(t, s, "Seconda", map[string]any{"column": "Pronto", "done_by": "person"})

	got := taskCall(t, s.handleTaskClaim, map[string]any{"board": "[[p/board]]"})
	if got.Task.Path != first.Path || got.Task.Column != "Analisi" || got.Task.Status != fleet.Working || got.Task.ClaimedBy == "" ||
		got.Task.Rank != 1 || got.Task.ColumnSince == "" {
		t.Fatalf("claimed: %+v", got.Task)
	}
	if got.Board.Path != "p/board.md" || !strings.Contains(got.Board.Text, "## Definizione di fatto\n\nI test passano.") ||
		strings.Contains(got.Board.Text, "```") {
		t.Fatalf("the board's text: %+v", got.Board)
	}
	// Analisi holds one card: the next one waits.
	msg := expectError(t, mustCall(s.handleTaskClaim(context.Background(), call(map[string]any{"board": "[[p/board]]"}))))
	if !strings.Contains(msg, "Analisi") || !strings.Contains(msg, "wip") {
		t.Fatalf("a claim past the limit: %s", msg)
	}
	// To another column of the same state, then to review.
	moved := taskCall(t, s.handleTaskUpdate, map[string]any{"path": first.Path, "column": "sviluppo", "message": "Analisi fatta."}).Task
	if moved.Column != "Sviluppo" || moved.Status != fleet.Working || moved.ClaimedBy == "" {
		t.Fatalf("moved: %+v", moved)
	}
	if msg := expectError(t, mustCall(s.handleTaskUpdate(context.Background(), call(map[string]any{"path": first.Path, "column": "Fatto"})))); !strings.Contains(msg, "completed") {
		t.Fatalf("an update to the column of completed: %s", msg)
	}
	if msg := expectError(t, mustCall(s.handleTaskUpdate(context.Background(), call(map[string]any{"path": first.Path})))); !strings.Contains(msg, "status, column") {
		t.Fatalf("an update without status or column: %s", msg)
	}
	// Analisi has room again: the second card.
	got = taskCall(t, s.handleTaskClaim, map[string]any{"board": "[[p/board]]"})
	if got.Task.Path != second.Path || got.Task.Column != "Analisi" {
		t.Fatalf("the second claim: %+v", got.Task)
	}
	// The agent closes the first; a person closes the second, from review.
	done := taskCall(t, s.handleTaskComplete, map[string]any{"path": first.Path, "report": "Fatto."}).Task
	if done.Status != fleet.Completed || done.Column != "Fatto" || done.ClaimedBy != "" {
		t.Fatalf("closed: %+v", done)
	}
	review := taskCall(t, s.handleTaskComplete, map[string]any{"path": second.Path, "report": "Fatto, guardalo."}).Task
	if review.Status != fleet.Review || review.Column != "Verifica" || review.ClaimedBy != "" || !review.LeaseUntil.IsZero() {
		t.Fatalf("to review: %+v", review)
	}
	note, _ := s.vault.Load(second.Path)
	if !strings.Contains(string(note.Content), "Fatto, guardalo.") || !strings.Contains(string(note.Content), "a person closes it") {
		t.Fatalf("the report:\n%s", note.Content)
	}
	// Nothing is ready any more.
	if msg := expectError(t, mustCall(s.handleTaskClaim(context.Background(), call(map[string]any{"board": "[[p/board]]"})))); !strings.Contains(msg, "no card") {
		t.Fatalf("a claim of an empty Pronto: %s", msg)
	}
}

func TestBoardTools_ClaimAPathAndAWarning(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedBoard(t, s, "wip: {agents: warn}\n")
	a := newCard(t, s, "Prima", map[string]any{"column": "Pronto"})
	b := newCard(t, s, "Seconda", map[string]any{"column": "Pronto"})
	if got := taskCall(t, s.handleTaskClaim, map[string]any{"path": b.Path}); got.Task.Column != "Analisi" || got.Warning != "" || got.Board.Path != "p/board.md" {
		t.Fatalf("a card claimed by its path: %+v", got)
	}
	got := taskCall(t, s.handleTaskClaim, map[string]any{"path": a.Path})
	if got.Task.Column != "Analisi" || !strings.Contains(got.Warning, "wip") {
		t.Fatalf("past the limit with warn: %+v", got)
	}
	if msg := expectError(t, mustCall(s.handleTaskClaim(context.Background(), call(map[string]any{})))); !strings.Contains(msg, "path or board") {
		t.Fatalf("a claim of nothing: %s", msg)
	}
}

// pastLease makes the card at rel held past its lease, at its attempts-th
// attempt.
func pastLease(t *testing.T, s *Server, rel string, attempts int) {
	t.Helper()
	note, err := s.vault.Load(rel)
	if err != nil {
		t.Fatal(err)
	}
	out, err := fleet.Set(note.Content, map[string]any{fleet.KeyLeaseUntil: time.Now().Add(-time.Minute).Format(time.RFC3339), fleet.KeyAttempts: attempts})
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, mustCall(s.handleUpdate(context.Background(), call(map[string]any{"path": rel, "content": string(out)}))))
}

// A card whose agent held it past its lease goes back to the top of the
// first column of submitted with the next move on its board, an agent's or
// a person's; at the last attempt it fails.
func TestBoardTools_AnEndedLeaseGivesTheCardBack(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedBoard(t, s, "")
	a := newCard(t, s, "Prima", map[string]any{"column": "Pronto"})
	b := newCard(t, s, "Seconda", map[string]any{"column": "Pronto"})
	taskCall(t, s.handleTaskClaim, map[string]any{"board": "[[p/board]]"})
	// Analisi is full of a card nobody works on: the claim gives it back
	// first, to the top of Pronto, and takes it again.
	pastLease(t, s, a.Path, 1)
	got := taskCall(t, s.handleTaskClaim, map[string]any{"board": "[[p/board]]"})
	if got.Task.Path != a.Path || got.Task.Attempts != 2 || got.Task.Column != "Analisi" || got.Warning != "" {
		t.Fatalf("claimed again: %+v", got)
	}
	if note, _ := s.vault.Load(a.Path); !strings.Contains(string(note.Content), "without a heartbeat (attempt 1 of 3)") {
		t.Fatalf("a:\n%s", note.Content)
	}
	// A person's move gives it back too, before it counts the column.
	pastLease(t, s, a.Path, 2)
	if warning, err := s.MoveCard(context.Background(), "alice", b.Path, fleet.CardMove{Column: "Analisi"}); err != nil || warning != "" {
		t.Fatalf("a person's move: %q %v", warning, err)
	}
	back, _, _ := s.fleetRead(a.Path)
	if back.Status != fleet.Submitted || back.Column != "Pronto" || back.ClaimedBy != "" || !back.LeaseUntil.IsZero() {
		t.Fatalf("given back: %+v", back)
	}
	// At the last attempt it fails, and leaves the board for the closed.
	if _, err := s.MoveCard(context.Background(), "alice", b.Path, fleet.CardMove{Column: "Sviluppo"}); err != nil {
		t.Fatal(err)
	}
	taskCall(t, s.handleTaskClaim, map[string]any{"path": a.Path})
	pastLease(t, s, a.Path, 3)
	c := newCard(t, s, "Terza", map[string]any{"column": "Pronto"})
	if got := taskCall(t, s.handleTaskClaim, map[string]any{"board": "[[p/board]]"}); got.Task.Path != c.Path {
		t.Fatalf("the next card: %+v", got.Task)
	}
	failed, _, _ := s.fleetRead(a.Path)
	if failed.Status != fleet.Failed || failed.Column != "" || failed.Rank != 0 || failed.ClaimedBy != "" {
		t.Fatalf("failed: %+v", failed)
	}
}

// A token that writes but does not read takes, moves and closes a card
// without the board's text, which only a token that reads receives.
func TestBoardTools_AWriteTokenGetsNoBoardText(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedBoard(t, s, "")
	a := newCard(t, s, "Prima", map[string]any{"column": "Pronto"})
	ctx := context.WithValue(context.Background(), tokenCtxKey, &auth.Token{ID: "w", Name: "writer", Scopes: []string{auth.ScopeWrite}})
	for _, step := range []struct {
		h    toolFn
		args map[string]any
	}{
		{s.handleTaskClaim, map[string]any{"board": "[[p/board]]"}},
		{s.handleTaskUpdate, map[string]any{"path": a.Path, "column": "Sviluppo"}},
		{s.handleTaskComplete, map[string]any{"path": a.Path, "report": "Fatto."}},
	} {
		var got taskReply
		if err := json.Unmarshal([]byte(resultText(t, mustCall(step.h(ctx, call(step.args))))), &got); err != nil {
			t.Fatal(err)
		}
		if got.Task.Path != a.Path || got.Board.Path != "p/board.md" || got.Board.Text != "" {
			t.Fatalf("%v: %+v", step.args, got)
		}
	}
}

func TestBoardTools_TheListOfABoard(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	seedFleet(t, s, ctx, fleetTask("submitted", ""))
	seedBoard(t, s, "")
	newCard(t, s, "Una idea", nil)
	newCard(t, s, "Pronta", map[string]any{"column": "Pronto"})
	var out struct {
		Tasks []fleet.Task `json:"tasks"`
		Board struct {
			Columns []struct {
				Name  string `json:"name"`
				Cards int    `json:"cards"`
				WIP   int    `json:"wip"`
			} `json:"columns"`
		} `json:"board"`
	}
	if err := json.Unmarshal([]byte(resultText(t, mustCall(s.handleTasks(ctx, call(map[string]any{"project": "p", "board": "[[p/board]]"}))))), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Tasks) != 2 || len(out.Board.Columns) != 6 || out.Board.Columns[0].Cards != 1 || out.Board.Columns[1].Cards != 1 || out.Board.Columns[2].WIP != 1 {
		t.Fatalf("the board's list: %+v", out)
	}
	if err := json.Unmarshal([]byte(resultText(t, mustCall(s.handleTasks(ctx, call(map[string]any{"project": "p", "status": "backlog"}))))), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Tasks) != 1 || out.Tasks[0].Status != fleet.Backlog {
		t.Fatalf("the backlog: %+v", out.Tasks)
	}
}

// Ten agents that take ten cards at once from a board whose column at work
// holds one: one takes its card, nine are refused. Each takes another card,
// so only the board's limit, counted and written under its lock, stops
// them; the next card, which they would all pick, is refused by the lock of
// its note alone.
func TestBoardTools_TheLimitHoldsAgainstARace(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedBoard(t, s, "")
	var paths []string
	for i := range 10 {
		paths = append(paths, newCard(t, s, fmt.Sprintf("Scheda %d", i), map[string]any{"column": "Pronto"}).Path)
	}
	for _, args := range []func(i int) map[string]any{
		func(i int) map[string]any { return map[string]any{"path": paths[i]} },
		func(int) map[string]any { return map[string]any{"board": "[[p/board]]"} },
	} {
		var wg sync.WaitGroup
		var mu sync.Mutex
		took, refused := 0, 0
		for i := range 10 {
			wg.Go(func() {
				res := mustCall(s.handleTaskClaim(context.Background(), call(args(i))))
				mu.Lock()
				defer mu.Unlock()
				if res.IsError {
					refused++
				} else {
					took++
				}
			})
		}
		wg.Wait()
		working := 0
		cards, _ := s.fleetTasks("p")
		for _, c := range cards {
			if c.Status == fleet.Working {
				working++
			}
		}
		if took != 1 || refused != 9 || working != 1 {
			t.Fatalf("%v: took %d, refused %d, %d cards at work", args(0), took, refused, working)
		}
		// The card goes back for the next round.
		for _, c := range cards {
			if c.Status == fleet.Working {
				resultText(t, mustCall(s.handleTaskUpdate(context.Background(), call(map[string]any{"path": c.Path, "status": "submitted"}))))
			}
		}
	}
}

// An agent that reads a board with its views sees the cards in the
// board's columns, after the block.
func TestBoardTools_TheBoardNoteShowsItsCards(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedBoard(t, s, "")
	newCard(t, s, "Una idea", nil)
	newCard(t, s, "Pronta", map[string]any{"column": "Pronto"})
	taskCall(t, s.handleTaskClaim, map[string]any{"board": "[[p/board]]"})
	text := resultText(t, mustCall(s.handleGet(context.Background(), call(map[string]any{"path": "p/board.md", "render_views": true}))))
	for _, want := range []string{`**Idee** (1)`, `Una idea`, `**Analisi** (1, wip 1)`, `Pronta`, `claimed_by: `} {
		if !strings.Contains(text, want) {
			t.Fatalf("the board lacks %q:\n%s", want, text)
		}
	}
}

// A person moves a card from the web UI (MoveCard): to any column, within
// the limits the board sets for people, to a place in it.
func TestBoardTools_APersonMovesACard(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	seedBoard(t, s, "")
	a := newCard(t, s, "Prima", map[string]any{"column": "Pronto"})
	b := newCard(t, s, "Seconda", map[string]any{"column": "Pronto"})
	c := newCard(t, s, "Terza", map[string]any{"column": "Pronto"})
	taskCall(t, s.handleTaskClaim, map[string]any{"path": a.Path})
	at := func(i int) *int { return &i }

	// Past the limit of Analisi: a warning, for people by default.
	warning, err := s.MoveCard(ctx, "daniele", b.Path, fleet.CardMove{Column: "Analisi"})
	if err != nil || !strings.Contains(warning, "wip") {
		t.Fatalf("past the limit: %q %v", warning, err)
	}
	if got := taskOf(t, s, b.Path); got.Status != fleet.Working || got.Column != "Analisi" || got.ClaimedBy != "" {
		t.Fatalf("moved: %+v", got)
	}
	// Back to Pronto, at its top: the agent that held it lets it go.
	if _, err := s.MoveCard(ctx, "daniele", a.Path, fleet.CardMove{Column: "Pronto", At: at(0)}); err != nil {
		t.Fatal(err)
	}
	cards, _ := s.fleetTasks("p")
	b0, _, _ := s.fleetBoard("p/board.md")
	var order []string
	for _, t := range columnCards(b0, cards, "Pronto", "") {
		order = append(order, t.Title)
	}
	if got := taskOf(t, s, a.Path); got.Status != fleet.Submitted || got.ClaimedBy != "" || !got.LeaseUntil.IsZero() || strings.Join(order, ",") != "Prima,Terza" {
		t.Fatalf("back: %+v, Pronto %v", got, order)
	}
	// Within its column, to the bottom.
	if _, err := s.MoveCard(ctx, "daniele", a.Path, fleet.CardMove{At: at(9)}); err != nil {
		t.Fatal(err)
	}
	cards, _ = s.fleetTasks("p")
	order = order[:0]
	for _, t := range columnCards(b0, cards, "Pronto", "") {
		order = append(order, t.Title)
	}
	if strings.Join(order, ",") != "Terza,Prima" {
		t.Fatalf("Pronto %v", order)
	}
	// Straight to Fatto, and back to work: a person moves a card anywhere.
	if _, err := s.MoveCard(ctx, "daniele", c.Path, fleet.CardMove{Column: "Fatto"}); err != nil {
		t.Fatal(err)
	}
	if got := taskOf(t, s, c.Path); got.Status != fleet.Completed || got.Column != "Fatto" {
		t.Fatalf("done: %+v", got)
	}
	if _, err := s.MoveCard(ctx, "daniele", c.Path, fleet.CardMove{Status: fleet.Review}); err != nil {
		t.Fatal(err)
	}
	if got := taskOf(t, s, c.Path); got.Status != fleet.Review || got.Column != "Verifica" {
		t.Fatalf("to review by its state: %+v", got)
	}
	// A move that changes nothing writes nothing.
	before, _ := s.vault.Load(c.Path)
	if _, err := s.MoveCard(ctx, "daniele", c.Path, fleet.CardMove{Column: "verifica"}); err != nil {
		t.Fatal(err)
	}
	if after, _ := s.vault.Load(c.Path); after.ETag() != before.ETag() {
		t.Fatal("a move to where the card is wrote it")
	}
	// Refusals: a Check that fails, a stale etag, a column the board has
	// not, a team's task.
	var me *fleet.MoveError
	if _, err := s.MoveCard(ctx, "daniele", c.Path, fleet.CardMove{Column: "Pronto", Check: func([]byte) error { return errors.New("seen otherwise") }}); err == nil {
		t.Fatal("a failed check moved the card")
	}
	if _, err := s.MoveCard(ctx, "daniele", c.Path, fleet.CardMove{Column: "Pronto", IfMatch: "stale"}); !errors.As(err, &me) || !me.Stale {
		t.Fatalf("a stale etag: %v", err)
	}
	if _, err := s.MoveCard(ctx, "daniele", c.Path, fleet.CardMove{Column: "Rilascio"}); !errors.As(err, &me) || me.Stale || !strings.Contains(err.Error(), "no column") {
		t.Fatalf("no such column: %v", err)
	}
	if got := taskOf(t, s, c.Path); got.Status != fleet.Review {
		t.Fatalf("a refused move moved the card: %+v", got)
	}
	seedFleet(t, s, ctx, fleetTask("submitted", ""))
	if _, err := s.MoveCard(ctx, "daniele", "p/tasks/a.md", fleet.CardMove{Column: "Pronto"}); !errors.As(err, &me) {
		t.Fatalf("a team's task: %v", err)
	}
}

// A board hard for people refuses a person's card past its limit.
func TestBoardTools_AHardBoardRefusesAPerson(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedBoard(t, s, "wip: {people: hard}\n")
	a := newCard(t, s, "Prima", map[string]any{"column": "Pronto"})
	b := newCard(t, s, "Seconda", map[string]any{"column": "Pronto"})
	if _, err := s.MoveCard(context.Background(), "daniele", a.Path, fleet.CardMove{Column: "Analisi"}); err != nil {
		t.Fatal(err)
	}
	var me *fleet.MoveError
	if _, err := s.MoveCard(context.Background(), "daniele", b.Path, fleet.CardMove{Column: "Analisi"}); !errors.As(err, &me) || !strings.Contains(err.Error(), "wip") {
		t.Fatalf("past a hard limit: %v", err)
	}
}
