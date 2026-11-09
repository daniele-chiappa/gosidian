package fleet

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/frontmatter"
	"github.com/gosidian/gosidian/internal/parser"
)

const boardSrc = `columns:
  - {name: Idee,     status: backlog}
  - {name: Pronto,   status: submitted}
  - {name: Analisi,  status: working, wip: 2}
  - {name: Sviluppo, status: working, wip: 3}
  - {name: Verifica, status: review,  wip: 4}
  - {name: Fatto,    status: completed}
wip: {agents: hard, people: warn}
done: agent
`

func TestParseBoard(t *testing.T) {
	b, problems := ParseBoard(boardSrc, "fitness/board.md")
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	var names []string
	for _, c := range b.Columns {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "Idee,Pronto,Analisi,Sviluppo,Verifica,Fatto" {
		t.Fatalf("columns out of order: %v", names)
	}
	if c, ok := b.Column("sviluppo"); !ok || c.Status != Working || c.WIP != 3 {
		t.Fatalf("Sviluppo: %+v %v", c, ok)
	}
	if c, _ := b.Column("Pronto"); c.WIP != 0 {
		t.Fatalf("a column without wip has a limit: %+v", c)
	}
	if b.WIP != (Rigidity{Agents: WIPHard, People: WIPWarn}) || b.Done != DoneAgent || !b.Hard(false) || b.Hard(true) {
		t.Fatalf("board: %+v", b)
	}
	// The defaults: hard for agents, a warning for people, the agent closes.
	b, problems = ParseBoard("columns:\n  - {name: Pronto, status: submitted}\n  - {name: In corso, status: working}\n", "fitness/b.md")
	if len(problems) > 0 || b.WIP != (Rigidity{Agents: WIPHard, People: WIPWarn}) || b.Done != DoneAgent {
		t.Fatalf("defaults: %+v %v", b, problems)
	}
}

func TestParseBoardProblems(t *testing.T) {
	ok := "  - {name: Pronto, status: submitted}\n  - {name: In corso, status: working}\n"
	cases := map[string]string{
		"unknown field":        "columns:\n" + ok + "colour: red\n",
		"unknown column field": "columns:\n" + ok + "  - {name: X, status: review, colour: red}\n",
		"unknown state":        "columns:\n" + ok + "  - {name: X, status: doing}\n",
		"no state":             "columns:\n" + ok + "  - {name: X}\n",
		"no name":              "columns:\n" + ok + "  - {status: review}\n",
		"same name":            "columns:\n" + ok + "  - {name: pronto, status: review}\n",
		"wip zero":             "columns:\n" + ok + "  - {name: X, status: review, wip: 0}\n",
		"wip negative":         "columns:\n" + ok + "  - {name: X, status: review, wip: -1}\n",
		"wip not a number":     "columns:\n" + ok + "  - {name: X, status: review, wip: many}\n",
		"wip a fraction":       "columns:\n" + ok + "  - {name: X, status: review, wip: 1.5}\n",
		"no submitted column":  "columns:\n  - {name: In corso, status: working}\n",
		"no working column":    "columns:\n  - {name: Pronto, status: submitted}\n",
		"no columns":           "done: agent\n",
		"columns not a list":   "columns: {name: Pronto}\n",
		"bad rigidity":         "columns:\n" + ok + "wip: {agents: soft}\n",
		"bad done":             "columns:\n" + ok + "done: robot\n",
		"yaml":                 "columns: [\n",
		"too many columns":     "columns:\n" + ok + manyColumns(maxColumns-1),
		"stale negative":       "columns:\n" + ok + "stale: -1\n",
		"stale a fraction":     "columns:\n" + ok + "stale: 1.5\n",
		"stale too long":       "columns:\n" + ok + "stale: 366\n",
		"stale not a number":   "columns:\n" + ok + "stale: soon\n",
	}
	for name, src := range cases {
		if _, problems := ParseBoard(src, "fitness/board.md"); len(problems) == 0 {
			t.Errorf("%s: no problem", name)
		}
	}
	// A problem says the line of the column, counted from the block's first.
	_, problems := ParseBoard("columns:\n"+ok+"  - {name: X, status: doing}\n", "fitness/board.md")
	if len(problems) != 1 || problems[0].Line != 4 || !strings.Contains(problems[0].Msg, "doing") {
		t.Fatalf("the problem of a column: %v", problems)
	}
}

func manyColumns(n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString("  - {name: C" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ", status: review}\n")
	}
	return b.String()
}

func TestFindBoard(t *testing.T) {
	body := []byte("# Board\n\n```team\nroles: {}\n```\n\n```board\n" + boardSrc + "```\n\nTesto.\n")
	blocks := FindBoard(body)
	if len(blocks) != 1 || blocks[0].Line != 7 || blocks[0].Src != boardSrc {
		t.Fatalf("blocks: %+v", blocks)
	}
	if len(Find(body)) != 1 {
		t.Fatal("the team's block is no longer found")
	}
}

func board(t *testing.T) Board {
	t.Helper()
	b, problems := ParseBoard(boardSrc, "fitness/board.md")
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	return b
}

func TestBoardTarget(t *testing.T) {
	b := board(t)
	cases := []struct {
		name           string
		cur            Task
		column, status string
		want           string // the column's name, then its state
		err            string
	}{
		{name: "a column gives its state", cur: Task{Status: Submitted, Column: "Pronto"}, column: "sviluppo", want: "Sviluppo/working"},
		{name: "the same column and its state", cur: Task{Status: Working, Column: "Analisi"}, column: "Verifica", status: Review, want: "Verifica/review"},
		{name: "a column and a state that disagree", cur: Task{Status: Working, Column: "Analisi"}, column: "Verifica", status: Completed, err: "column Verifica is review"},
		{name: "a column the board has not", cur: Task{Status: Working}, column: "Rilascio", err: "no column Rilascio"},
		{name: "a state keeps its column", cur: Task{Status: Working, Column: "Sviluppo"}, status: Working, want: "Sviluppo/working"},
		{name: "a state goes to its first column", cur: Task{Status: Submitted, Column: "Pronto"}, status: Working, want: "Analisi/working"},
		{name: "an open state without a column", cur: Task{Status: Working, Column: "Analisi"}, status: InputRequired, err: "no column for input-required"},
		{name: "a closed state without a column", cur: Task{Status: Working, Column: "Analisi"}, status: Failed, want: "/failed"},
		{name: "a card changed by hand", cur: Task{Status: Review, Column: "Analisi"}, want: "Verifica/review"},
		{name: "a card without a column", cur: Task{Status: Backlog}, want: "Idee/backlog"},
		{name: "a card where it should be", cur: Task{Status: Working, Column: "sviluppo"}, want: "Sviluppo/working"},
	}
	for _, c := range cases {
		got, err := b.Target(c.cur, c.column, c.status)
		switch {
		case c.err != "":
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: err %v, want %q", c.name, err, c.err)
			}
		case err != nil:
			t.Errorf("%s: %v", c.name, err)
		case got.Name+"/"+got.Status != c.want:
			t.Errorf("%s: %s/%s, want %s", c.name, got.Name, got.Status, c.want)
		}
	}
	if b.ColumnOf(Task{Status: Review, Column: "Analisi"}) != "Verifica" || b.ColumnOf(Task{Status: Canceled}) != "" {
		t.Fatal("ColumnOf")
	}
}

func TestBoardCountAndFull(t *testing.T) {
	b := board(t)
	cards := []Task{
		{Path: "fitness/tasks/a.md", Board: "fitness/board.md", Status: Working, Column: "Analisi"},
		{Path: "fitness/tasks/b.md", Board: "fitness/board.md", Status: Working, Column: "Analisi"},
		{Path: "fitness/tasks/c.md", Board: "fitness/board.md", Status: Review, Column: "Analisi"}, // changed by hand: Verifica
		{Path: "fitness/tasks/d.md", Board: "fitness/other.md", Status: Working, Column: "Analisi"},
		{Path: "fitness/tasks/e.md", Status: Working},
	}
	if n := b.Count(cards, "analisi", ""); n != 2 {
		t.Fatalf("Analisi holds %d", n)
	}
	if n := b.Count(cards, "Analisi", "fitness/tasks/a.md"); n != 1 {
		t.Fatalf("Analisi without a holds %d", n)
	}
	if n := b.Count(cards, "Verifica", ""); n != 1 {
		t.Fatalf("Verifica holds %d", n)
	}
	analisi, _ := b.Column("Analisi")
	pronto, _ := b.Column("Pronto")
	if !analisi.Full(2) || analisi.Full(1) || pronto.Full(1000) {
		t.Fatal("Full")
	}
}

func TestOrder(t *testing.T) {
	cards := []Task{
		{Path: "p/tasks/4.md"},
		{Path: "p/tasks/3.md", Rank: 2},
		{Path: "p/tasks/1.md"},
		{Path: "p/tasks/2.md", Rank: 0.5},
		{Path: "p/tasks/5.md", Rank: 2},
	}
	slices.SortFunc(cards, Order)
	var got []string
	for _, c := range cards {
		got = append(got, strings.TrimSuffix(c.Path[len("p/tasks/"):], ".md"))
	}
	if strings.Join(got, ",") != "2,3,5,1,4" {
		t.Fatalf("order %v: by rank, then the cards without one, each by path", got)
	}
}

func TestPlace(t *testing.T) {
	cases := []struct {
		name   string
		ranks  []float64
		at     int
		rank   float64
		others []float64
	}{
		{name: "an empty column", at: 0, rank: 1},
		{name: "the bottom", ranks: []float64{1, 2, 3}, at: 3, rank: 4},
		{name: "past the bottom", ranks: []float64{1, 2.5}, at: 9, rank: 3.5},
		{name: "the top", ranks: []float64{1, 2}, at: 0, rank: 0.5},
		{name: "between two", ranks: []float64{1, 2, 3}, at: 1, rank: 1.5},
		{name: "below a card without a rank", ranks: []float64{1, 0}, at: 2, rank: 3, others: []float64{1, 2}},
		{name: "no room left", ranks: []float64{1, 1 + 1.0/2048, 2}, at: 1, rank: 2, others: []float64{1, 3, 4}},
		{name: "two of the same rank", ranks: []float64{1, 1}, at: 1, rank: 2, others: []float64{1, 3}},
		{name: "no room at the top", ranks: []float64{1.0 / 2048, 1}, at: 0, rank: 1, others: []float64{2, 3}},
	}
	for _, c := range cases {
		rank, others := Place(c.ranks, c.at)
		if rank != c.rank || !slices.Equal(others, c.others) {
			t.Errorf("%s: %v %v, want %v %v", c.name, rank, others, c.rank, c.others)
		}
	}
	// Ten cards put one after the other at the top of the same column keep
	// an order of their own, then the column is renumbered.
	ranks := []float64{1}
	renumbered := 0
	for i := range 20 {
		rank, others := Place(ranks, 0)
		if others != nil {
			if i < 10 {
				t.Fatalf("renumbered after %d", i)
			}
			ranks = others
			renumbered++
		}
		ranks = append([]float64{rank}, ranks...)
		if !slices.IsSorted(ranks) || ranks[0] <= 0 || len(slices.Compact(slices.Clone(ranks))) != len(ranks) {
			t.Fatalf("after %d: %v", i, ranks)
		}
	}
	if renumbered != 1 {
		t.Fatalf("renumbered %d times in 20", renumbered)
	}
}

func TestCardRenderAndRead(t *testing.T) {
	at := time.Date(2026, 10, 30, 21, 0, 0, 0, time.UTC)
	n := NewTask{Title: "La board", Body: "Fai la board.", Board: "fitness/board.md", Column: "Idee", Status: Backlog, Rank: 1.5,
		DoneBy: DonePerson, For: []string{"fitness/docs/improvements/IMP-1.md", "fitness/plans/piano.md"}, Iteration: "fitness/iterations/1.md"}
	p := TaskPath("fitness", n.Title, at)
	content := n.Render("fitness", at)
	fm := parser.ExtractFrontmatterRaw(content)
	if err := frontmatter.YAMLError(fm); err != nil {
		t.Fatalf("%v\n%s", err, content)
	}
	if strings.Contains(fm, "team:") || strings.Contains(fm, "assignee:") {
		t.Fatalf("a card without a team writes one:\n%s", content)
	}
	task := ReadTask(p, content)
	if task.Board != "fitness/board.md" || task.Column != "Idee" || task.Status != Backlog || task.Rank != 1.5 || task.DoneBy != DonePerson ||
		strings.Join(task.For, ",") != "fitness/docs/improvements/IMP-1.md,fitness/plans/piano.md" || task.ColumnSince != at.Format(time.RFC3339) ||
		task.Team != "" || task.Assignee != "" || task.Iteration != "fitness/iterations/1.md" || task.Started != "" || task.Finished != "" {
		t.Fatalf("card: %+v\n%s", task, content)
	}
	if !strings.Contains(fm, `board: "[[fitness/board]]"`) || !strings.Contains(fm, `  - "[[fitness/plans/piano]]"`) {
		t.Fatalf("links:\n%s", content)
	}
	// A task of a team stays as it was written.
	team := NewTask{Title: "x", Team: "fitness/squadra.md", Assignee: "dev"}.Render("fitness", at)
	if f := parser.ExtractFrontmatterRaw(team); strings.Contains(f, "board") || strings.Contains(f, "column") || strings.Contains(f, "rank") {
		t.Fatalf("a team's task writes the board's keys:\n%s", team)
	}
}

func TestBacklogMoves(t *testing.T) {
	if !CanMove(Backlog, Submitted) || !CanMove(Backlog, Canceled) || !CanMove(Submitted, Backlog) {
		t.Fatal("a move of the backlog refused")
	}
	if CanMove(Backlog, Working) || CanMove(Backlog, Completed) || CanMove(Working, Backlog) {
		t.Fatal("a card leaves the backlog without being ready")
	}
	if !Known(Backlog) || !Known(Review) || Known("doing") || slices.Contains(States, Backlog) {
		t.Fatal("Known, or the backlog among the states of a team's task")
	}
	if w := (Task{Status: Backlog}).Waits(nil, time.Now()); w == "" {
		t.Fatal("a card in the backlog starts")
	}
}

func TestParseBoardDoneByAPersonNeedsReview(t *testing.T) {
	src := "columns:\n  - {name: Pronto, status: submitted}\n  - {name: In corso, status: working}\ndone: person\n"
	if _, problems := ParseBoard(src, "fitness/board.md"); len(problems) == 0 || !strings.Contains(problems[0].Msg, "review") {
		t.Fatalf("a person closes the cards of a board without review: %v", problems)
	}
	ok := strings.Replace(src, "\ndone:", "\n  - {name: Verifica, status: review}\ndone:", 1)
	if _, problems := ParseBoard(ok, "fitness/board.md"); len(problems) > 0 {
		t.Fatalf("with review: %v", problems)
	}
}

func TestBoardText(t *testing.T) {
	note := "---\ntitle: Board\ntype: board\n---\n\n# Board\n\n```board\n" + boardSrc + "```\n\n## Definizione di fatto\n\nI test passano.\n\n" +
		"```view\nfrom: fitness/tasks\nas: board\ngroup_by: status\n```\n\n```sh\nnpm test\n```\n"
	want := "# Board\n\n## Definizione di fatto\n\nI test passano.\n\n```sh\nnpm test\n```"
	if got := BoardText([]byte(note)); got != want {
		t.Fatalf("text:\n%q\nwant\n%q", got, want)
	}
	long := "---\ntype: board\n---\n\n" + strings.Repeat("parola ", 3000)
	if got := BoardText([]byte(long)); len(got) > maxBoardText+64 || !strings.HasSuffix(got, "(cut: read the board's note)") {
		t.Fatalf("a long note: %d bytes, %q", len(got), got[len(got)-40:])
	}
}

func TestBoardSchema(t *testing.T) {
	db := DatabaseNote("fitness")
	out, changed, err := BoardSchema(db)
	if err != nil || !changed {
		t.Fatalf("changed %v err %v", changed, err)
	}
	fm := parser.ExtractFrontmatterRaw(out)
	if err := frontmatter.YAMLError(fm); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"status: {type: select, required: true, options: [backlog, submitted, working",
		"team: {type: relation}\n", "assignee: {type: text}\n",
		"board: {type: relation}\n", "column: {type: text}\n", "rank: {type: number}\n", "column_since: {type: date}\n",
		"done_by: {type: select, options: [agent, person]}\n", "for: {type: relation}\n",
		"started: {type: date}\n", "finished: {type: date}\n", "iteration: {type: relation}\n",
		"row_views:\n  - title: Sub-tasks\n", "description: \"The tasks of the project's teams",
	} {
		if !strings.Contains(fm, want) {
			t.Errorf("lacks %q:\n%s", want, fm)
		}
	}
	// What is not the fields stays as written, the body too.
	if !strings.HasSuffix(string(out), string(db[strings.Index(string(db), "\n---\n"):])) ||
		strings.Count(string(out), "\n") != strings.Count(string(db), "\n")+9 {
		t.Fatalf("more than the fields changed:\n%s", out)
	}
	again, changed, err := BoardSchema(out)
	if err != nil || changed || string(again) != string(out) {
		t.Fatalf("a second time: changed %v err %v", changed, err)
	}
	// A database written by hand, in block style, with a field of its own.
	hand := "---\ntitle: Compiti\ntype: database\nsource: fitness/tasks\nfields:\n  title:\n    type: text\n  status:\n    type: select\n    options:\n      - submitted\n      - working\n  team:\n    type: relation\n    required: true\n  stima:\n    type: number   # ore\ntags: [fitness]\n---\n\n# Compiti\n"
	out, changed, err = BoardSchema([]byte(hand))
	if err != nil || !changed {
		t.Fatalf("by hand: changed %v err %v", changed, err)
	}
	fm = parser.ExtractFrontmatterRaw(out)
	if err := frontmatter.YAMLError(fm); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(fm, "      - backlog\n      - submitted\n") || strings.Contains(fm, "required") || !strings.Contains(fm, "# ore") ||
		!strings.Contains(fm, "  for: {type: relation}\n") || !strings.HasSuffix(fm, "tags: [fitness]") {
		t.Fatalf("by hand:\n%s", out)
	}
	for name, bad := range map[string]string{
		"no fields":      "---\ntitle: x\ntype: database\n---\n",
		"fields no map":  "---\ntype: database\nfields: [a]\n---\n",
		"no frontmatter": "# x\n",
		"status no list": "---\ntype: database\nfields:\n  status: {type: select, options: submitted}\n---\n",
	} {
		if _, _, err := BoardSchema([]byte(bad)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestToReview(t *testing.T) {
	at := time.Date(2026, 10, 30, 22, 0, 0, 0, time.UTC)
	content := NewTask{Title: "La board", Board: "fitness/board.md", Column: "Sviluppo", Status: Working}.Render("fitness", at)
	content, err := Set(content, map[string]any{KeyClaimedBy: "claude@1", KeyLeaseUntil: at.Add(time.Hour).Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	task := ReadTask("fitness/tasks/x.md", content)
	if _, err := ToReview(task, content, "other@2", "Fatto.", at); err == nil {
		t.Fatal("another agent sends the card to review")
	}
	out, err := ToReview(task, content, "claude@1", "Fatto, i test passano.", at)
	if err != nil {
		t.Fatal(err)
	}
	task = ReadTask("fitness/tasks/x.md", out)
	if task.Status != Review || task.ClaimedBy != "" || !task.LeaseUntil.IsZero() {
		t.Fatalf("to review: %+v\n%s", task, out)
	}
	if r := LastSection(out, "Report"); !strings.HasPrefix(r, "Fatto, i test passano.") || !strings.Contains(r, "a person closes it") {
		t.Fatalf("report %q", r)
	}
}

func TestBoardNext(t *testing.T) {
	b, problems := ParseBoard("columns:\n  - {name: Idee, status: backlog}\n  - {name: Urgente, status: submitted}\n  - {name: Pronto, status: submitted}\n  - {name: In corso, status: working, wip: 1}\n", "p/board.md")
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	now := time.Date(2026, 10, 30, 23, 0, 0, 0, time.UTC)
	card := func(name, column, status string, rank float64) Task {
		return Task{Path: "p/tasks/" + name + ".md", Board: "p/board.md", Column: column, Status: status, Rank: rank}
	}
	cards := []Task{
		card("idea", "Idee", Backlog, 1),
		card("p1", "Pronto", Submitted, 1),
		card("u2", "Urgente", Submitted, 2),
		card("u1", "Urgente", Submitted, 1),
		card("other", "Urgente", Submitted, 0.5),
		card("work", "In corso", Working, 1),
		{Path: "p/tasks/team.md", Board: "p/board.md", Column: "Urgente", Status: Submitted, Rank: 0.1, Team: "p/squadra.md"},
	}
	cards[4].Board = "p/other.md"
	cards[3].DependsOn = []string{"p/tasks/work.md"}
	got, ok := b.Next(cards, now)
	if !ok || got.Path != "p/tasks/u2.md" {
		t.Fatalf("next %s %v: the first ready by rank of the first column, but a team's, another board's and one that waits", got.Path, ok)
	}
	cards[5].Status = Completed
	if got, _ = b.Next(cards, now); got.Path != "p/tasks/u1.md" {
		t.Fatalf("next %s once its dependency is completed", got.Path)
	}
	cards[2].Status, cards[3].Status = Working, Working
	if got, _ = b.Next(cards, now); got.Path != "p/tasks/p1.md" {
		t.Fatalf("next %s from the second column", got.Path)
	}
	cards[1].LeaseUntil, cards[1].ClaimedBy = now.Add(time.Minute), "x@1"
	if got, ok = b.Next(cards, now); ok {
		t.Fatalf("next %s while it is held", got.Path)
	}
}
