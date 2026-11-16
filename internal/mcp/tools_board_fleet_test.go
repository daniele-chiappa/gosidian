package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/clock"
	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/projects"
)

// boardFleet is a server whose project p has the board of testBoard, the
// team squadra (capo gives tasks to dev) and a pipeline that takes the
// ready tasks of both roles and waits (IMP-216 phase 3).
func boardFleet(t *testing.T, extra string) (*Server, *pipeline.Engine, *jobs.Store) {
	t.Helper()
	s, _, _ := newTestServer(t)
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pstore.Set("p", projects.Flags{UsePipelines: true}); err != nil {
		t.Fatal(err)
	}
	s.SetProjects(pstore)
	s.SetPipelinesSwitch(true)
	ctx := context.Background()
	seedBoard(t, s, extra)
	lavora := "---\ntype: pipeline\n---\n\n```pipeline\ntrigger: {on: task}\nnodes:\n  - {id: w, use: wait.call}\n```\n"
	for rel, body := range map[string]string{"p/squadra.md": testFleetTeam, "p/pipelines/lavora.md": lavora} {
		if res, _ := s.handleCreate(ctx, call(map[string]any{"path": rel, "content": body})); res.IsError {
			t.Fatalf("seed %s: %s", rel, expectError(t, res))
		}
	}
	store, err := jobs.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	runner := jobs.NewRunner(store, jobs.Options{Workers: 1, Poll: 10 * time.Millisecond, Lease: time.Minute})
	e := pipeline.New(s.PipelineHost(), runner, pipeline.Config{Location: time.UTC}, nil, nil)
	s.SetPipelines(e)
	return s, e, store
}

func teamCard(t *testing.T, s *Server, title, role string, args map[string]any) fleet.Task {
	t.Helper()
	a := map[string]any{"column": "Pronto", "team": "squadra", "assignee": role}
	for k, v := range args {
		a[k] = v
	}
	return newCard(t, s, title, a)
}

// A team's card starts in the board's order, into the first column of
// working within its limit: the next one waits for the place.
func TestBoardFleet_ATeamsCardStartsWithinTheLimit(t *testing.T) {
	s, e, _ := boardFleet(t, "")
	a := teamCard(t, s, "Prima", "dev", nil)
	b := teamCard(t, s, "Seconda", "dev", nil)
	if a.Team != "p/squadra.md" || a.Assignee != "dev" || a.Column != "Pronto" || a.Rank != 1 || b.Rank != 2 {
		t.Fatalf("cards: %+v %+v", a, b)
	}
	// The second goes first: a person put it on top.
	top := 0
	if _, err := s.MoveCard(context.Background(), "daniele", b.Path, fleet.CardMove{Column: "Pronto", At: &top}); err != nil {
		t.Fatal(err)
	}
	e.TickTasks()
	first, second := taskOf(t, s, b.Path), taskOf(t, s, a.Path)
	if first.Status != fleet.Working || first.Column != "Analisi" || !strings.HasPrefix(first.ClaimedBy, "run-") || first.Started == "" || first.Attempts != 1 {
		t.Fatalf("the card on top did not start in Analisi: %+v", first)
	}
	if second.Status != fleet.Submitted || second.Column != "Pronto" || second.ClaimedBy != "" || second.Attempts != 0 {
		t.Fatalf("the next card started past the limit: %+v", second)
	}
	if why := e.TaskWaits("p")[a.Path]; !strings.Contains(why, "Analisi") || !strings.Contains(why, "wip") {
		t.Fatalf("why it waits: %q", why)
	}
	// An agent in a session takes no card of a team.
	if msg := expectError(t, mustCall(s.handleTaskClaim(context.Background(), call(map[string]any{"board": "[[p/board]]"})))); !strings.Contains(msg, "no card") {
		t.Fatalf("the board's next: %s", msg)
	}
}

// A person who takes back a card a run holds cancels the run.
func TestBoardFleet_APersonTakesBackARunsCard(t *testing.T) {
	s, e, store := boardFleet(t, "")
	a := teamCard(t, s, "Prima", "dev", nil)
	e.TickTasks()
	runID, ok := strings.CutPrefix(taskOf(t, s, a.Path).ClaimedBy, "run-")
	if !ok {
		t.Fatalf("not started: %+v", taskOf(t, s, a.Path))
	}
	if _, err := s.MoveCard(context.Background(), "daniele", a.Path, fleet.CardMove{Column: "Idee"}); err != nil {
		t.Fatal(err)
	}
	if got := taskOf(t, s, a.Path); got.Status != fleet.Backlog || got.Column != "Idee" || got.ClaimedBy != "" {
		t.Fatalf("taken back: %+v", got)
	}
	if run, err := store.Run(runID); err != nil || run.Status != jobs.Canceled {
		t.Fatalf("its run: %+v %v", run.Status, err)
	}
	// The end of the run leaves the card where the person put it.
	e.TickTasks()
	if got := taskOf(t, s, a.Path); got.Status != fleet.Backlog || got.Column != "Idee" {
		t.Fatalf("after the run ended: %+v", got)
	}
}

// An objective's sub-tasks are cards at the bottom of the board's first
// column of submitted, and the objective that waits for them takes no
// place of its column.
func TestBoardFleet_AnObjectivesSubTasksAreCards(t *testing.T) {
	s, e, store := boardFleet(t, "")
	other := teamCard(t, s, "Altra", "dev", map[string]any{"column": "Idee"})
	obj := teamCard(t, s, "Obiettivo", "capo", nil)
	e.TickTasks()
	o := taskOf(t, s, obj.Path)
	runID, ok := strings.CutPrefix(o.ClaimedBy, "run-")
	if !ok || o.Column != "Analisi" {
		t.Fatalf("objective: %+v", o)
	}
	run := ctxWithToken(&auth.Token{ID: "r1", Name: "run-" + runID + "-w", Kind: auth.KindRun, RunID: runID, Projects: []string{"p"}, Scopes: []string{auth.ScopeRead}})
	sub := taskCall(t, func(_ context.Context, r mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return s.handleTaskCreate(run, r)
	},
		map[string]any{"project": "p", "team": "squadra", "assignee": "dev", "title": "La pagina"}).Task
	if sub.Parent != obj.Path || sub.Board != "p/board.md" || sub.Column != "Pronto" || sub.Status != fleet.Submitted || sub.Rank != 1 {
		t.Fatalf("sub-task: %+v", sub)
	}
	// The host's CreateTask, task.plan's, does the same; one that waits
	// for a person has no column of its state on this board, and stays a
	// team's task.
	fl := s.PipelineHost().(pipeline.Fleet)
	second, err := fl.CreateTask("p", fleet.NewTask{Title: "Il test", Team: "p/squadra.md", Assignee: "dev", Parent: obj.Path}, "task")
	if err != nil || second.Board != "p/board.md" || second.Column != "Pronto" || second.Rank != 2 {
		t.Fatalf("task.plan's sub-task: %+v %v", second, err)
	}
	held, err := fl.CreateTask("p", fleet.NewTask{Title: "Chiedi", Team: "p/squadra.md", Assignee: "dev", Parent: obj.Path, Status: fleet.InputRequired}, "task")
	if err != nil || held.Board != "" {
		t.Fatalf("a held sub-task without a column: %+v %v", held, err)
	}
	// The coordinator's run ends: the objective waits in Analisi, whose
	// one place its sub-task takes.
	if err := e.Runner().Cancel(runID); err != nil {
		t.Fatal(err)
	}
	_ = store
	e.TickTasks()
	if o = taskOf(t, s, obj.Path); o.Status != fleet.Working || o.ClaimedBy != "" || o.Column != "Analisi" {
		t.Fatalf("the waiting objective: %+v", o)
	}
	e.TickTasks()
	if got := taskOf(t, s, sub.Path); got.Status != fleet.Working || got.Column != "Analisi" || !strings.HasPrefix(got.ClaimedBy, "run-") {
		t.Fatalf("the sub-task did not take Analisi's place: %+v (%s)", got, e.TaskWaits("p")[sub.Path])
	}
	if got := taskOf(t, s, second.Path); got.Status != fleet.Submitted {
		t.Fatalf("the second sub-task went past the limit: %+v", got)
	}
	_ = other
}

// The writes of a run keep the card's column with its state: given back,
// it goes to the top of Pronto; closed, to Fatto with its end; and a card
// a person closes goes to review after its run.
func TestBoardFleet_ARunsWritesMoveTheCard(t *testing.T) {
	s, e, _ := boardFleet(t, "done: person\n")
	a := teamCard(t, s, "Prima", "dev", map[string]any{"done_by": "agent"})
	b := teamCard(t, s, "Seconda", "dev", nil)
	waiting := teamCard(t, s, "Terza", "dev", nil)
	fl := s.PipelineHost().(pipeline.Fleet)
	bs := s.PipelineHost().(pipeline.Boards)
	if bs.DoneByPerson(taskOf(t, s, a.Path)) || !bs.DoneByPerson(taskOf(t, s, b.Path)) {
		t.Fatal("done_by: the card's wins over the board's")
	}
	e.TickTasks()
	holder := taskOf(t, s, a.Path).ClaimedBy
	now := clock.Now()
	// Sent back for another attempt: the top of Pronto.
	back, err := fl.UpdateTask(a.Path, func(cur fleet.Task, content []byte) ([]byte, error) {
		return fleet.Update(cur, content, holder, fleet.Submitted, "Rifai.", now)
	}, "task")
	if err != nil || back.Column != "Pronto" || back.Status != fleet.Submitted || back.ClaimedBy != "" {
		t.Fatalf("given back: %+v %v", back, err)
	}
	if w := taskOf(t, s, waiting.Path); !(back.Rank < taskOf(t, s, b.Path).Rank && back.Rank < w.Rank) {
		t.Fatalf("given back to the top: rank %v", back.Rank)
	}
	// It starts again, and closes: Fatto.
	e.TickTasks()
	holder = taskOf(t, s, a.Path).ClaimedBy
	done, err := fl.UpdateTask(a.Path, func(cur fleet.Task, content []byte) ([]byte, error) {
		return fleet.Complete(cur, content, holder, fleet.Completed, "Fatto.", now)
	}, "task")
	if err != nil || done.Column != "Fatto" || done.Status != fleet.Completed || done.Finished == "" || done.Started == "" {
		t.Fatalf("closed: %+v %v", done, err)
	}
	// The next card starts; its run's completion sends it to review.
	e.TickTasks()
	next := taskOf(t, s, b.Path)
	if next.Column != "Analisi" {
		t.Fatalf("the next card: %+v", next)
	}
	rev, err := fl.UpdateTask(b.Path, func(cur fleet.Task, content []byte) ([]byte, error) {
		return fleet.ToReview(cur, content, next.ClaimedBy, "Guardala.", now)
	}, "task")
	if err != nil || rev.Column != "Verifica" || rev.Status != fleet.Review || rev.ClaimedBy != "" || rev.Finished != "" {
		t.Fatalf("to review: %+v %v", rev, err)
	}
}
