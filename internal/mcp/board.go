package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/clock"
	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/vault"
)

// The boards of a project's cards (IMP-216): every write that moves a card
// into a column counts the column and writes the card under its board's
// lock, so two agents that ask for the last place at once do not both get
// it. The lock is in the process: one server writes a vault.

// lockBoard takes the lock of the board at rel; the function it returns
// lets it go.
func (s *Server) lockBoard(rel string) func() {
	m, _ := s.boardLocks.LoadOrStore(rel, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// fleetBoard reads the board at rel and its note.
func (s *Server) fleetBoard(rel string) (fleet.Board, []byte, error) {
	note, err := s.vault.Load(rel)
	if err != nil {
		return fleet.Board{}, nil, fmt.Errorf("board %s: no such note", rel)
	}
	if t := strings.TrimSpace(fmt.Sprint(parser.ParseFrontmatterFields(parser.FrontmatterRawForPath(rel, note.Content))["type"])); t != fleet.BoardType {
		return fleet.Board{}, nil, fmt.Errorf("%s is no board (type: board, with a ```board block: docs/board.md)", rel)
	}
	blocks := fleet.FindBoard(note.Content)
	if len(blocks) != 1 {
		return fleet.Board{}, nil, fmt.Errorf("the board %s has %d ```board blocks: one", rel, len(blocks))
	}
	b, problems := fleet.ParseBoard(blocks[0].Src, rel)
	if len(problems) > 0 {
		return fleet.Board{}, nil, fmt.Errorf("the board %s does not read: %s (memory_lint, rule board-invalid)", rel, problems[0])
	}
	return b, note.Content, nil
}

// cardsDatabase makes the database of project's tasks hold cards, once:
// it writes one when the project has none (fleet.BoardSchema). The server
// writes it as itself, whoever made the card.
func (s *Server) cardsDatabase(ctx context.Context, project string) error {
	rel := project + "/" + fleet.Database
	_, werr := s.writeNote(ctx, nil, noteWrite{rel: rel, mode: writeUpsert, automation: true,
		content: func(note *vault.Note) ([]byte, error) {
			content := fleet.DatabaseNote(project)
			if note != nil {
				content = note.Content
			}
			out, changed, err := fleet.BoardSchema(content)
			switch {
			case err != nil:
				return nil, contentError("%s: %v, so it cannot hold the cards of a board (docs/board.md)", rel, err)
			case !changed && note != nil:
				return nil, errSkipWrite
			}
			return out, nil
		}})
	if werr != nil {
		return werr
	}
	return nil
}

// moveCard writes the card cur of board b, whose lock the caller holds,
// in the column of column or status (fleet.Board.Target). move changes the
// rest, checking what its tool checks (its state, its holder) first; then
// the card enters its column, within its limit for a person (w.person) or
// for an agent, at place at, or at the bottom when at is nil; at moves it
// within its own column too. A column at its limit refuses the card when
// the board is hard for who writes, and otherwise takes it with a
// warning.
func (s *Server) moveCard(ctx context.Context, w taskWrite, b fleet.Board, cards []fleet.Task, cur fleet.Task, column, status string,
	at *int, move func(cur fleet.Task, content []byte, status string) ([]byte, error)) (fleet.Task, string, error) {
	target, err := b.Target(cur, column, status)
	if err != nil {
		return fleet.Task{}, "", err
	}
	warning := ""
	var in []fleet.Task
	var ranks []float64
	t, err := s.fleetUpdate(ctx, w, cur.Path, func(c fleet.Task, content []byte) ([]byte, error) {
		out, err := move(c, content, target.Status)
		if err != nil {
			return nil, err
		}
		set := map[string]any{}
		var unset []string
		switch from := b.ColumnOf(c); {
		case target.Name == "":
			unset = []string{fleet.KeyColumn, fleet.KeyRank}
		case !strings.EqualFold(from, target.Name):
			if warning, err = wip(b, cards, target, c.Path, w.person); err != nil {
				return nil, err
			}
			var rank float64
			in = columnCards(b, cards, target.Name, c.Path)
			rank, ranks = place(in, at)
			set[fleet.KeyColumn], set[fleet.KeyRank], set[fleet.KeyColumnSince] = target.Name, rank, time.Now().Format(time.RFC3339)
		case at != nil:
			var rank float64
			in = columnCards(b, cards, target.Name, c.Path)
			rank, ranks = place(in, at)
			set[fleet.KeyColumn], set[fleet.KeyRank] = target.Name, rank
		case c.Column != target.Name:
			set[fleet.KeyColumn] = target.Name // a card written by hand, or in another case
		}
		if len(set) == 0 && len(unset) == 0 {
			return out, nil
		}
		return fleet.Set(out, set, unset...)
	})
	if err != nil {
		return fleet.Task{}, "", err
	}
	return t, warning, s.renumber(ctx, w, in, ranks)
}

// MoveCard moves the card at rel for the person actor, from the web UI
// (IMP-216): under its board's lock and within its limits for people, as
// the task tools move a card for an agent. A person moves a card to any
// column: back to backlog or submitted it lets its holder go, and closed
// it ends its hold. A move to where the card is writes nothing. A refusal
// is a *fleet.MoveError.
func (s *Server) MoveCard(ctx context.Context, actor, rel string, m fleet.CardMove) (string, error) {
	cur, _, err := s.fleetRead(rel)
	if err != nil || cur.Board == "" {
		return "", &fleet.MoveError{Msg: rel + " is no card of a board"}
	}
	b, _, err := s.fleetBoard(cur.Board)
	if err != nil {
		return "", &fleet.MoveError{Msg: err.Error()}
	}
	unlock := s.lockBoard(b.Path)
	defer unlock()
	project, _, _ := strings.Cut(rel, "/")
	cards, err := s.boardCards(ctx, b, project)
	if err != nil {
		return "", err
	}
	if cur, _ = taskIn(cards, rel); cur.Path == "" {
		return "", &fleet.MoveError{Msg: rel + " is no task of the project"}
	}
	target, err := b.Target(cur, m.Column, m.Status)
	switch {
	case err != nil:
		return "", &fleet.MoveError{Msg: err.Error()}
	case m.At == nil && target.Status == cur.Status && target.Name == cur.Column:
		return "", nil
	}
	now := clock.NoteTime(time.Now())
	_, warning, err := s.moveCard(ctx, taskWrite{actor: actor, person: true, ifMatch: m.IfMatch}, b, cards, cur, m.Column, m.Status, m.At,
		func(c fleet.Task, content []byte, status string) ([]byte, error) {
			if m.Check != nil {
				if err := m.Check(content); err != nil {
					return nil, err
				}
			}
			var unset []string
			switch {
			case fleet.Ended(status):
				unset = []string{fleet.KeyClaimedBy, fleet.KeyLeaseUntil, fleet.KeyBranch}
			case status == fleet.Backlog || status == fleet.Submitted:
				unset = []string{fleet.KeyClaimedBy, fleet.KeyLeaseUntil}
			}
			return fleet.Set(content, map[string]any{fleet.KeyStatus: status, fleet.KeyUpdated: now}, unset...)
		})
	var werr *writeError
	switch {
	case errors.As(err, &werr) && werr.kind == writePrecondition:
		return "", &fleet.MoveError{Msg: werr.msg, Stale: true}
	case errors.As(err, &werr) && werr.kind == writeFailed:
		return "", err
	case err != nil:
		return "", &fleet.MoveError{Msg: err.Error()}
	}
	return warning, nil
}

// boardCards reads the tasks of project, the cards of b among them, after
// giving back the cards an agent held past their lease, before b counts
// its columns: to the top of b's first column of submitted, or failed at
// the last attempt (closed, in its column if b has one), as the pipelines
// give back a team's task. The caller holds b's lock.
func (s *Server) boardCards(ctx context.Context, b fleet.Board, project string) ([]fleet.Task, error) {
	cards, err := s.fleetTasks(project)
	if err != nil {
		return nil, err
	}
	now, max := time.Now(), s.taskMaxAttempts()
	w := taskWrite{actor: "task"}
	ended := false
	for _, t := range cards {
		if t.Board != b.Path || !t.LeaseOver(now) {
			continue
		}
		target, err := b.Target(t, "", fleet.AfterLease(t, max))
		if err != nil {
			continue // the board lost its columns of submitted: memory_lint says so
		}
		var in []fleet.Task
		var ranks []float64
		_, err = s.fleetUpdate(ctx, w, t.Path, func(c fleet.Task, content []byte) ([]byte, error) {
			if c.ClaimedBy != t.ClaimedBy || !c.LeaseOver(now) || fleet.AfterLease(c, max) != target.Status {
				return nil, pipeline.ErrTaskSkip
			}
			out, err := fleet.GiveBack(c, content, max, clock.Now())
			switch {
			case err != nil:
				return nil, err
			case target.Name == "":
				return fleet.Set(out, nil, fleet.KeyColumn, fleet.KeyRank)
			}
			top := 0
			in = columnCards(b, cards, target.Name, c.Path)
			rank, others := place(in, &top)
			ranks = others
			return fleet.Set(out, map[string]any{fleet.KeyColumn: target.Name, fleet.KeyRank: rank, fleet.KeyColumnSince: time.Now().Format(time.RFC3339)})
		})
		switch {
		case errors.Is(err, pipeline.ErrTaskSkip):
			continue
		case err != nil:
			return nil, err
		}
		if err := s.renumber(ctx, w, in, ranks); err != nil {
			return nil, err
		}
		ended = true
	}
	if !ended {
		return cards, nil
	}
	return s.fleetTasks(project)
}

// wip checks that column c of b takes one more card than the cards of it
// among cards but the one at except: a warning when it is full and the
// board only warns who writes, an error when it refuses.
func wip(b fleet.Board, cards []fleet.Task, c fleet.Column, except string, person bool) (string, error) {
	n := b.Count(cards, c.Name, except)
	if !c.Full(n) {
		return "", nil
	}
	msg := fmt.Sprintf("the column %s of %s holds %d cards, its wip", c.Name, b.Path, n)
	if b.Hard(person) {
		return "", fmt.Errorf("%s: finish or move one of them first", msg)
	}
	return msg, nil
}

// columnCards are the cards of b among cards in column, but the one at
// except, in their order.
func columnCards(b fleet.Board, cards []fleet.Task, column, except string) []fleet.Task {
	var in []fleet.Task
	for _, t := range cards {
		if t.Board == b.Path && t.Path != except && strings.EqualFold(b.ColumnOf(t), column) {
			in = append(in, t)
		}
	}
	slices.SortFunc(in, fleet.Order)
	return in
}

// place is the rank of a card put at place at of a column of cards in (0
// the top, nil the bottom), and the new ranks of those when the column is
// renumbered (fleet.Place), nil when they keep theirs.
func place(in []fleet.Task, at *int) (float64, []float64) {
	ranks := make([]float64, len(in))
	for i, t := range in {
		ranks[i] = t.Rank
	}
	i := len(ranks)
	if at != nil {
		i = *at
	}
	return fleet.Place(ranks, i)
}

// renumber writes the new ranks of the cards in, after a card entered
// their column.
func (s *Server) renumber(ctx context.Context, w taskWrite, in []fleet.Task, ranks []float64) error {
	for i, r := range ranks {
		if in[i].Rank == r {
			continue
		}
		if _, err := s.fleetUpdate(ctx, w, in[i].Path, func(_ fleet.Task, content []byte) ([]byte, error) {
			return fleet.Set(content, map[string]any{fleet.KeyRank: r})
		}); err != nil {
			return err
		}
	}
	return nil
}

// taskIn is the task at rel among tasks.
func taskIn(tasks []fleet.Task, rel string) (fleet.Task, bool) {
	i := slices.IndexFunc(tasks, func(t fleet.Task) bool { return t.Path == rel })
	if i < 0 {
		return fleet.Task{}, false
	}
	return tasks[i], true
}
