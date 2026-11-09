package mcp

// The iterations of the boards (IMP-216 phase 2): notes type: iteration
// that a board's cards link. memory_tasks shows a board's flow, its
// iteration in progress and those whose retrospective waits; the server
// closes an iteration when its last day is over (CloseIterations), with
// pipelines or without.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/i18n"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/vault"
)

// iterationActor is who the audit says closed an iteration.
const iterationActor = "iteration"

// fleetIterations are the iterations of project, of every project for "";
// with open, only those not reviewed yet.
func (s *Server) fleetIterations(project string, open bool) ([]fleet.Iteration, error) {
	var conds []index.FieldCond
	if open {
		conds = append(conds, index.FieldCond{Field: fleet.KeyReviewed, Op: index.OpExists, Values: []string{"false"}})
	}
	paths, err := s.notesOfType(project, fleet.IterationType, conds...)
	if err != nil {
		return nil, err
	}
	out := make([]fleet.Iteration, 0, len(paths))
	for _, p := range paths {
		if note, err := s.vault.Load(p); err == nil {
			out = append(out, fleet.ReadIteration(p, note.Content))
		}
	}
	return out, nil
}

// cardIteration checks that the iteration at rel, which a card of board b
// is to link, is one of b's, and not closed.
func (s *Server) cardIteration(project string, b fleet.Board, rel string) error {
	its, err := s.fleetIterations(project, false)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(its, func(it fleet.Iteration) bool { return it.Path == rel })
	switch {
	case i < 0:
		return fmt.Errorf("iteration %s is no iteration of project %s (a note type: iteration, docs/board.md)", rel, project)
	case its[i].Board != b.Path:
		return fmt.Errorf("iteration %s is of board %s, not of %s", rel, its[i].Board, b.Path)
	case its[i].Reviewed != "":
		return fmt.Errorf("iteration %s closed on %s: link the card to one in progress or to come", rel, its[i].Reviewed)
	}
	return nil
}

// boardState is what a board shows above its columns: its flow, its
// iteration in progress, and the iterations whose retrospective waits.
func (s *Server) boardState(b fleet.Board, cards []fleet.Task, now time.Time) map[string]any {
	out := map[string]any{"flow": b.Flow(cards, now)}
	project, _, _ := strings.Cut(b.Path, "/")
	its, err := s.fleetIterations(project, false)
	if err != nil {
		return out
	}
	if it, ok := fleet.Current(its, b.Path, now); ok {
		out["iteration"] = b.Info(it, cards, now)
	}
	if pending := fleet.WaitingRetro(its, b.Path); len(pending) > 0 {
		retro := make([]string, len(pending))
		for i, it := range pending {
			retro[i] = it.Path
		}
		out["retro"] = retro
	}
	return out
}

// CloseIterations closes the iterations of every project whose last day
// is over at now (fleet.Iteration.Due): it writes each one's review in its
// note, with reviewed and retro: pending, then takes the iteration away
// from its cards that are not closed, which stay in their column, and
// starts the project's pipelines on iteration. A note with reviewed is not
// written again. It returns how many it closed.
func (s *Server) CloseIterations(ctx context.Context, now time.Time) (int, error) {
	its, err := s.fleetIterations("", true)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range its {
		if !it.Due(now) {
			continue
		}
		closed, err := s.closeIteration(ctx, it, now)
		if err != nil {
			slog.Warn("iterations: closing one", "iteration", it.Path, "err", err)
			continue
		}
		if closed {
			n++
		}
	}
	return n, nil
}

// closeIteration closes it at now, under its board's lock. A board that
// does not read still gets the review, without columns.
func (s *Server) closeIteration(ctx context.Context, it fleet.Iteration, now time.Time) (bool, error) {
	b, _, err := s.fleetBoard(it.Board)
	if err != nil {
		b = fleet.Board{Path: it.Board}
	}
	unlock := s.lockBoard(b.Path)
	defer unlock()
	project, _, _ := strings.Cut(it.Path, "/")
	cards, err := s.fleetTasks(project)
	if err != nil {
		return false, err
	}
	lang := i18n.Lang()
	review := it.Review(b, cards, now,
		func(key string, args map[string]string) string { return i18n.Text("board", lang, key, args) },
		func(f float64) string { return i18n.Decimal(lang, f) })
	skipped := false
	_, werr := s.writeNote(ctx, nil, noteWrite{rel: it.Path, mode: writeReplace, action: audit.ActionUpdate,
		automation: true, source: audit.SourceAutomation, actor: iterationActor,
		content: func(note *vault.Note) ([]byte, error) {
			if cur := fleet.ReadIteration(it.Path, note.Content); cur.Reviewed != "" || !cur.Due(now) {
				skipped = true
				return nil, errSkipWrite
			}
			out := slices.Concat(bytes.TrimRight(note.Content, "\n"), []byte("\n\n"+review))
			return fleet.Set(out, map[string]any{fleet.KeyReviewed: now.Format(time.RFC3339), fleet.KeyRetro: fleet.RetroPending})
		}})
	switch {
	case werr != nil:
		return false, werr
	case skipped:
		return false, nil
	}
	w := taskWrite{actor: iterationActor, source: audit.SourceAutomation}
	var deferred []string
	for _, c := range it.Unfinished(cards) {
		_, err := s.fleetUpdate(ctx, w, c.Path, func(cur fleet.Task, content []byte) ([]byte, error) {
			if cur.Iteration != it.Path || fleet.Ended(cur.Status) {
				return nil, pipeline.ErrTaskSkip
			}
			return fleet.Set(content, nil, fleet.KeyIteration)
		})
		switch {
		case errors.Is(err, pipeline.ErrTaskSkip):
		case err != nil:
			slog.Warn("iterations: a card not done keeps its iteration", "iteration", it.Path, "card", c.Path, "err", err)
		default:
			deferred = append(deferred, c.Path)
		}
	}
	if s.pipelines != nil {
		s.pipelines.IterationClosed(pipeline.IterationClose{Project: project, Iteration: it.Path, Board: it.Board, Goal: it.Goal,
			Start: it.Start, End: it.End, Review: review, Deferred: deferred})
	}
	return true, nil
}

// RunIterations closes the iterations that are due every interval until
// ctx ends (CloseIterations), and once at once.
func (s *Server) RunIterations(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if n, err := s.CloseIterations(ctx, time.Now()); err != nil {
			slog.Warn("iterations: listing them", "err", err)
		} else if n > 0 {
			slog.Info("iterations: closed", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
