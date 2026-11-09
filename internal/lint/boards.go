package lint

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/gosidian/gosidian/internal/dbschema"
	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/parser"
)

// checkBoards reports the ```board blocks (IMP-216) that do not read as a
// board, on the line of their problem, and the notes type: board without
// exactly one block; the cards whose board is no board of the project, or
// whose column is not their state's, as a note edited by hand leaves them;
// in a project with cards, a database of the tasks that lost what the
// cards need. And (phase 2) the iterations that do not read, or whose days
// meet another's of their board; the cards whose iteration is none of
// their board's, and whose dates the server did not write as they are.
func checkBoards(_ context.Context, l *Linter, project string) ([]Issue, error) {
	notes, err := l.notesInProject(project)
	if err != nil {
		return nil, err
	}
	const hint = "the format: docs/board.md; the problem's line is in the note"
	var issues []Issue
	add := func(file string, line int, msg, fix string) {
		issues = append(issues, Issue{Severity: SeverityWarning, File: file, Line: line, Rule: "board-invalid", Message: msg, FixHint: fix})
	}
	// The boards of the project by path: nil for a board note that does
	// not read, whose cards are not checked.
	boards := map[string]*fleet.Board{}
	var cards []fleet.Task
	var its []fleet.Iteration
	var db *projectNote
	for i, n := range notes {
		if !strings.HasSuffix(strings.ToLower(n.Path), ".md") {
			continue
		}
		fm := parser.ParseFrontmatterFields(rawFrontmatter(n))
		blocks := fleet.FindBoard(n.Content)
		isBoard := noteHasType(fm, fleet.BoardType)
		if isBoard && len(blocks) != 1 {
			add(n.Path, 0, fmt.Sprintf("a note type: board holds one ```board block, this one %d", len(blocks)), hint)
		}
		readable := len(blocks) == 1
		for _, b := range blocks {
			board, problems := fleet.ParseBoard(b.Src, n.Path)
			for _, p := range problems {
				line := b.Line
				if p.Line > 0 {
					line += p.Line
				}
				add(n.Path, line, fmt.Sprintf("the board at line %d: %s", b.Line, p.Msg), hint)
			}
			if readable = readable && len(problems) == 0; readable {
				boards[n.Path] = &board
			}
		}
		if isBoard && !readable {
			boards[n.Path] = nil
		}
		if noteHasType(fm, fleet.TaskType) {
			if t := fleet.ReadTask(n.Path, n.Content); t.Board != "" {
				cards = append(cards, t)
			}
		}
		if noteHasType(fm, fleet.IterationType) {
			its = append(its, fleet.ReadIteration(n.Path, n.Content))
		}
		if n.Path == project+"/"+fleet.Database {
			db = &notes[i]
		}
	}
	const itHint = "an iteration: a note type: iteration with board, start and end (docs/board.md)"
	byPath := map[string]fleet.Iteration{}
	for _, it := range its {
		byPath[it.Path] = it
		for _, p := range it.Problems() {
			add(it.Path, 0, "the iteration: "+p, itHint)
		}
		if _, ok := boards[it.Board]; it.Board != "" && !ok {
			add(it.Path, 0, fmt.Sprintf("the iteration's board %s is no board of the project", it.Board), itHint)
		}
	}
	for _, pair := range fleet.Overlapping(its) {
		add(pair[1].Path, 0, fmt.Sprintf("the iteration's days meet those of %s, of the same board: one iteration at a time", pair[0].Path), itHint)
	}
	for _, t := range cards {
		for _, msg := range cardDates(t) {
			add(t.Path, 0, msg, "the server writes started and finished as the card moves: take away what was written by hand")
		}
		switch it, ok := byPath[t.Iteration]; {
		case t.Iteration == "":
		case !ok:
			add(t.Path, 0, fmt.Sprintf("the card's iteration %s is no iteration of the project", t.Iteration), itHint)
		case it.Board != t.Board:
			add(t.Path, 0, fmt.Sprintf("the card's iteration %s is of board %s, the card of %s", t.Iteration, it.Board, t.Board), itHint)
		}
		b, ok := boards[t.Board]
		switch {
		case !ok:
			add(t.Path, 0, fmt.Sprintf("the card's board %s is no board of the project (a note type: board with a ```board block)", t.Board), hint)
			continue
		case b == nil:
			continue // its board is reported
		}
		where := "the server's next move of the card puts it in " + b.ColumnOf(t)
		if b.ColumnOf(t) == "" {
			where = "its state has no column on the board: give it a column of " + b.Path
		}
		c, ok := b.Column(t.Column)
		switch {
		case t.Column == "" && fleet.Ended(t.Status):
		case t.Column == "":
			add(t.Path, 0, fmt.Sprintf("the card is %s, in no column of %s", t.Status, b.Path), where)
		case !ok:
			add(t.Path, 0, fmt.Sprintf("the card's column %s: %s has no column %s", t.Column, b.Path, t.Column), where)
		case c.Status != t.Status:
			add(t.Path, 0, fmt.Sprintf("the card's column %s is %s, the card %s", c.Name, c.Status, t.Status), where)
		}
	}
	if len(cards) > 0 && db != nil {
		if missing := cardsMissing(*db); len(missing) > 0 {
			add(db.Path, 0, fmt.Sprintf("%s has cards of boards, and its database of the tasks lacks %s", project, strings.Join(missing, "; ")),
				"the server writes them with the project's next card (memory_task_create with board), or write them in fields (docs/board.md)")
		}
	}
	return issues, nil
}

// cardDates are the problems of the dates of card t: one that is no time,
// a finished on a card not closed, or before its start.
func cardDates(t fleet.Task) []string {
	var out []string
	start, okStart := fleet.When(t.Started)
	end, okEnd := fleet.When(t.Finished)
	switch {
	case t.Started != "" && !okStart:
		out = append(out, fmt.Sprintf("the card's started %q is no time", t.Started))
	case t.Finished != "" && !okEnd:
		out = append(out, fmt.Sprintf("the card's finished %q is no time", t.Finished))
	case t.Finished != "" && !fleet.Ended(t.Status):
		out = append(out, fmt.Sprintf("the card is %s and has finished: only a closed card has it", t.Status))
	case okStart && okEnd && end.Before(start):
		out = append(out, fmt.Sprintf("the card finished (%s) before it started (%s)", t.Finished, t.Started))
	}
	return out
}

// cardsMissing is what the database note of the tasks lacks to hold cards
// (fleet.BoardSchema): nil for one whose schema does not read, which
// database-field-invalid reports.
func cardsMissing(db projectNote) []string {
	s, err := dbschema.Parse(db.Path, rawFrontmatter(db))
	if err != nil {
		return nil
	}
	var out, fields []string
	if f, ok := s.Field(fleet.KeyStatus); !ok || !slices.Contains(f.Options, fleet.Backlog) {
		out = append(out, "backlog among the options of status")
	}
	for _, name := range []string{"team", "assignee"} {
		if f, ok := s.Field(name); ok && f.Required {
			out = append(out, name+" not required, which a card has not")
		}
	}
	for _, name := range fleet.CardFields() {
		if _, ok := s.Field(name); !ok {
			fields = append(fields, name)
		}
	}
	if len(fields) > 0 {
		out = append(out, "the fields "+strings.Join(fields, ", "))
	}
	return out
}
