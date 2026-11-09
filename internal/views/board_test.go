package views

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/dbschema"
	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/parser"
)

const testBoardBlock = "columns:\n  - {name: Idee, status: backlog}\n  - {name: Pronto, status: submitted}\n" +
	"  - {name: Analisi, status: working, wip: 1}\n  - {name: Fatto, status: completed}\n"

// boardIndex holds the board p/board.md, the database of p's tasks and
// their cards, a team's task among them.
func boardIndex(t *testing.T) (*index.Index, Context) {
	t.Helper()
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	db, _, err := fleet.BoardSchema(fleet.DatabaseNote("p"))
	if err != nil {
		t.Fatal(err)
	}
	card := func(title, column, status, rank, extra string) string {
		return "---\ntitle: " + title + "\ntype: task\nstatus: " + status + "\nboard: \"[[p/board]]\"\ncolumn: " + column + "\n" + rank + extra + "---\n"
	}
	notes := map[string]string{
		"p/board.md":      "---\ntitle: Board\ntype: board\n---\n\n```board\n" + testBoardBlock + "```\n",
		"p/tasks.md":      string(db),
		"p/tasks/a.md":    card("Seconda", "Pronto", "submitted", "rank: 2\n", ""),
		"p/tasks/b.md":    card("Prima", "Pronto", "submitted", "rank: 1\n", ""),
		"p/tasks/c.md":    card("Senza rango", "Pronto", "submitted", "", ""),
		"p/tasks/d.md":    card("Al lavoro", "analisi", "working", "rank: 1\n", "claimed_by: claude@1\n"),
		"p/tasks/e.md":    card("Fallita", "", "failed", "", ""),
		"p/tasks/f.md":    "---\ntitle: Altra\ntype: task\nstatus: submitted\nboard: \"[[p/altra]]\"\ncolumn: Pronto\nrank: 0.5\n---\n",
		"p/tasks/team.md": "---\ntitle: Squadra\ntype: task\nstatus: submitted\nteam: \"[[p/squadra]]\"\nassignee: dev\n---\n",
		"p/docs/IMP-1.md": "---\ntitle: IMP-1\n---\n",
		"p/tasks/g.md":    card("Per IMP-1", "Idee", "backlog", "rank: 1\n", "for:\n  - \"[[p/docs/IMP-1]]\"\n"),
	}
	for p, body := range notes {
		if err := idx.Upsert(index.NoteDoc{Path: p, Title: strings.TrimSuffix(filepath.Base(p), ".md"), Body: body, ModTime: 1, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := dbschema.Parse("p/tasks.md", parser.ExtractFrontmatterRaw(db))
	if err != nil {
		t.Fatal(err)
	}
	c := Context{
		This: ThisFields("p/board.md", map[string]any{"title": "Board", "type": "board"}),
		Schema: func(folder string) *dbschema.Schema {
			return map[bool]*dbschema.Schema{true: schema}[folder == "p/tasks"]
		},
		Resolve: func(target string) string {
			if _, err := idx.Note(target + ".md"); err == nil {
				return target + ".md"
			}
			return ""
		},
		CanWrite: func(string) bool { return true },
	}
	return idx, c
}

// A ```board block shows its board's cards: in its columns, in their order,
// empty ones too, each by rank; the closed cards without a column last.
func TestBoardBlock(t *testing.T) {
	idx, c := boardIndex(t)
	r, err := computeBlock(testBoardBlock, true, c, idx.Query)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := r.Groups()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, g := range groups {
		var titles []string
		for _, h := range g.Hits {
			titles = append(titles, rowTitle(h))
		}
		got = append(got, g.Value+"="+strings.Join(titles, "+"))
	}
	if want := "Idee=Per IMP-1,Pronto=Prima+Seconda+Senza rango,Analisi=Al lavoro,Fatto=,=Fallita"; strings.Join(got, ",") != want {
		t.Fatalf("groups %v\nwant %s", got, want)
	}
	want := "_Flow: cycle time too few cards (0); closed per week 0 · 0 · 0 · 0; stale 0 (over 3 days)._\n\n" +
		"**Idee** (1)\n\n- [[p/tasks/g\\|Per IMP-1]] · for: [[p/docs/IMP-1]]\n" +
		"\n**Pronto** (3)\n\n- [[p/tasks/b\\|Prima]]\n- [[p/tasks/a\\|Seconda]]\n- [[p/tasks/c\\|Senza rango]]\n" +
		"\n**Analisi** (1, wip 1)\n\n- [[p/tasks/d\\|Al lavoro]] · claimed_by: claude@1\n" +
		"\n**(closed)** (1)\n\n- [[p/tasks/e\\|Fallita]]\n"
	if md := r.Markdown(); md != want {
		t.Fatalf("markdown:\n%s\nwant:\n%s", md, want)
	}
	d := r.Data(c)
	if d.Group == nil || d.Group.Name != fleet.KeyColumn || strings.Join(d.Groups, ",") != "Idee,Pronto,Analisi,Fatto," || len(d.Lanes) != 4 ||
		d.Lanes[2] != (fleet.Column{Name: "Analisi", Status: fleet.Working, WIP: 1}) || d.Database != "p/tasks.md" || d.Defaults["board"] != "[[p/board]]" {
		t.Fatalf("data: %+v", d)
	}
	// The rows come in the board's order, with their rank.
	var rows []string
	for _, row := range d.Rows {
		rows = append(rows, strings.TrimSuffix(strings.TrimPrefix(row.Path, "p/tasks/"), ".md"))
		if row.Path == "p/tasks/a.md" && row.Fields["rank"] != "2" {
			t.Fatalf("row %+v", row)
		}
	}
	if strings.Join(rows, ",") != "g,b,a,c,d,e" {
		t.Fatalf("rows %v", rows)
	}
	// A block that does not read says why.
	if _, err := computeBlock("columns: []\n", true, c, idx.Query); err == nil || !strings.Contains(err.Error(), "board") {
		t.Fatalf("a bad block: %v", err)
	}
}

// A note's board blocks render with its views, in order, each with its data
// at its data-view index; for agents the block stays and its cards follow.
func TestBoardBlockInANote(t *testing.T) {
	idx, c := boardIndex(t)
	body := "# Board\n\n```board\n" + testBoardBlock + "```\n\n```view\nfrom: p/docs\n```\n"
	html, data := RenderNoteData([]byte(body), c, idx.Query)
	if len(data) != 2 || data[0].As != "board" || len(data[0].Lanes) != 4 || data[1].As != "table" ||
		!strings.Contains(string(html), `data-view="0"`) || !strings.Contains(string(html), `data-view="1"`) || strings.Contains(string(html), "```board") {
		t.Fatalf("data %+v\n%s", data, html)
	}
	out, hash := RenderNote([]byte(body), true, c, idx.Query)
	if hash == "" || !strings.Contains(string(out), "```board\n"+testBoardBlock+"```\n"+resultOpen) || !strings.Contains(string(out), "**Pronto** (3)") {
		t.Fatalf("for agents:\n%s", out)
	}
	if n := len(FindBlocks([]byte(body))); n != 2 {
		t.Fatalf("FindBlocks: %d", n)
	}
}

// A board's state: its flow from its cards' dates, its iteration in
// progress, the iterations whose retrospective waits; for agents, lines
// above the columns.
func TestBoardBlockFlow(t *testing.T) {
	idx, c := boardIndex(t)
	now := time.Now()
	c.Today = now
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	day := func(d int) string { return now.AddDate(0, 0, d).Format(time.DateOnly) }
	for p, body := range map[string]string{
		"p/tasks/h.md": "---\ntitle: Ferma\ntype: task\nstatus: working\nboard: \"[[p/board]]\"\ncolumn: Analisi\ncolumn_since: " + at(100*time.Hour) +
			"\niteration: \"[[p/iterations/1]]\"\n---\n",
		"p/tasks/i.md": "---\ntitle: Chiusa\ntype: task\nstatus: completed\nboard: \"[[p/board]]\"\ncolumn: Fatto\nstarted: " + at(50*time.Hour) +
			"\nfinished: " + at(2*time.Hour) + "\niteration: \"[[p/iterations/1]]\"\n---\n",
		"p/iterations/1.md": "---\ntitle: Iterazione 1\ntype: iteration\nboard: \"[[p/board]]\"\ngoal: Il primo passo\nstart: " + day(-3) + "\nend: " + day(4) + "\n---\n",
		"p/iterations/0.md": "---\ntitle: Iterazione 0\ntype: iteration\nboard: \"[[p/board]]\"\nstart: " + day(-20) + "\nend: " + day(-4) +
			"\nreviewed: " + at(72*time.Hour) + "\nretro: pending\n---\n",
	} {
		if err := idx.Upsert(index.NoteDoc{Path: p, Title: strings.TrimSuffix(filepath.Base(p), ".md"), Body: body, ModTime: 1, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := computeBlock(testBoardBlock, true, c, idx.Query)
	if err != nil {
		t.Fatal(err)
	}
	st := r.Data(c).Board
	if st == nil || st.StaleDays != fleet.DefaultStale || len(st.Flow.Stale) != 1 || st.Flow.Stale[0].Path != "p/tasks/h.md" || st.Flow.Stale[0].Days != 4 ||
		st.Flow.Cycle.N != 1 || st.Flow.Closed[2]+st.Flow.Closed[3] != 1 {
		t.Fatalf("flow: %+v", st)
	}
	if it := st.Iteration; it == nil || it.Path != "p/iterations/1.md" || it.Goal != "Il primo passo" || it.Cards.Total != 2 || it.Cards.Done != 1 || it.DaysLeft != 5 {
		t.Fatalf("iteration: %+v", st.Iteration)
	}
	if len(st.Retro) != 1 || st.Retro[0].Path != "p/iterations/0.md" {
		t.Fatalf("retro: %+v", st.Retro)
	}
	md := r.Markdown()
	for _, want := range []string{
		"_Flow: cycle time too few cards (1); closed per week ", "; stale 1 (over 3 days)._\n",
		"_Iteration [[p/iterations/1\\|Iterazione 1]]: Il primo passo, " + day(-3) + " to " + day(4) + ", 5 days left, 1 of 2 cards done._\n",
		"_Retrospective to write: [[p/iterations/0\\|Iterazione 0]] (retro: done once written)._\n\n**Idee**",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
}
