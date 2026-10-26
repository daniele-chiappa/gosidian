package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/clock"
	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/vault"
)

// The teams of agents and their tasks in the vault (IMP-195): what the
// task tools, the task trigger and the task nodes read and write. Every
// write of a task goes through writeNote, under the note's lock.

// notesOfType lists the notes of project whose frontmatter says type typ.
func (s *Server) notesOfType(project, typ string) ([]string, error) {
	var out []string
	for offset := 0; ; offset += index.QueryMaxLimit {
		hits, total, err := s.index.Query(index.QueryOptions{Projects: []string{project},
			Where: []index.FieldCond{{Field: "type", Op: index.OpEq, Values: []string{typ}}},
			Sort:  "path", Limit: index.QueryMaxLimit, Offset: offset})
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
			out = append(out, h.Path)
		}
		if len(hits) == 0 || offset+len(hits) >= total {
			return out, nil
		}
	}
}

// fleetTeams are the teams of project whose block reads, by path.
func (s *Server) fleetTeams(project string) (map[string]fleet.Team, error) {
	paths, err := s.notesOfType(project, fleet.TeamType)
	if err != nil {
		return nil, err
	}
	out := map[string]fleet.Team{}
	for _, p := range paths {
		note, err := s.vault.Load(p)
		if err != nil {
			continue
		}
		blocks := fleet.Find(note.Content)
		if len(blocks) != 1 {
			continue
		}
		if t, problems := fleet.Parse(blocks[0].Src, p); len(problems) == 0 {
			out[p] = t
		}
	}
	return out, nil
}

// fleetTasks are the tasks of project.
func (s *Server) fleetTasks(project string) ([]fleet.Task, error) {
	paths, err := s.notesOfType(project, fleet.TaskType)
	if err != nil {
		return nil, err
	}
	out := make([]fleet.Task, 0, len(paths))
	for _, p := range paths {
		note, err := s.vault.Load(p)
		if err != nil {
			continue
		}
		out = append(out, fleet.ReadTask(p, note.Content))
	}
	return out, nil
}

// fleetRead reads one task.
func (s *Server) fleetRead(rel string) (fleet.Task, []byte, error) {
	note, err := s.vault.Load(rel)
	if err != nil {
		return fleet.Task{}, nil, err
	}
	if !fleet.IsTask(parser.FrontmatterRawForPath(rel, note.Content)) {
		return fleet.Task{}, nil, fmt.Errorf("%s is no task (type: task)", rel)
	}
	return fleet.ReadTask(rel, note.Content), note.Content, nil
}

// taskWrite is how a task is written: by a token (the limiter counts it)
// or by the server for a run (actor).
type taskWrite struct {
	tok   *auth.Token
	actor string
}

func (w taskWrite) noteWrite(nw noteWrite) noteWrite {
	if w.tok == nil {
		nw.automation, nw.source, nw.actor, nw.batched = true, audit.SourcePipeline, w.actor, true
	}
	return nw
}

// fleetUpdate rewrites a task under its note's lock. build's
// pipeline.ErrTaskSkip writes nothing and comes back as it is; its other
// errors refuse, as a *writeError of kind writeContent.
func (s *Server) fleetUpdate(ctx context.Context, w taskWrite, rel string, build func(fleet.Task, []byte) ([]byte, error)) (fleet.Task, error) {
	var out fleet.Task
	skipped := false
	_, werr := s.writeNote(ctx, w.tok, w.noteWrite(noteWrite{rel: rel, mode: writeReplace, action: audit.ActionUpdate,
		content: func(note *vault.Note) ([]byte, error) {
			if !fleet.IsTask(parser.FrontmatterRawForPath(rel, note.Content)) {
				return nil, contentError("%s is no task (type: task)", rel)
			}
			out = fleet.ReadTask(rel, note.Content)
			b, err := build(out, note.Content)
			switch {
			case errors.Is(err, pipeline.ErrTaskSkip):
				skipped = true
				return nil, errSkipWrite
			case err != nil:
				return nil, contentError("%s", err.Error())
			}
			out = fleet.ReadTask(rel, b)
			return b, nil
		}}))
	switch {
	case werr != nil:
		return fleet.Task{}, werr
	case skipped:
		return out, pipeline.ErrTaskSkip
	}
	s.kickTasks()
	return out, nil
}

// fleetCreate writes a new task of project.
func (s *Server) fleetCreate(ctx context.Context, w taskWrite, project string, n fleet.NewTask) (fleet.Task, error) {
	at := clock.Now()
	rel := fleet.TaskPath(project, n.Title, at)
	for i := 2; ; i++ {
		if _, err := s.vault.Load(rel); err != nil {
			break
		}
		rel = strings.TrimSuffix(fleet.TaskPath(project, n.Title, at), ".md") + fmt.Sprintf("-%d.md", i)
	}
	content := n.Render(project, at)
	if _, werr := s.writeNote(ctx, w.tok, w.noteWrite(noteWrite{rel: rel, mode: writeCreate, action: audit.ActionCreate,
		content: fixedContent(content), schemaCheck: true})); werr != nil {
		return fleet.Task{}, werr
	}
	s.kickTasks()
	return fleet.ReadTask(rel, content), nil
}

// kickTasks tells the task trigger a task was written.
func (s *Server) kickTasks() {
	if s.pipelines != nil {
		s.pipelines.KickTasks()
	}
}

// taskLease is how long an outside agent holds a task without a heartbeat.
func (s *Server) taskLease() time.Duration {
	if s.pipelines != nil {
		if l := s.pipelines.Config().Tasks.Lease; l > 0 {
			return l
		}
	}
	return 15 * time.Minute
}

// The fleet of the pipelines' host (pipeline.Fleet).

func (h pipelineHost) Teams(project string) (map[string]fleet.Team, error) {
	return h.s.fleetTeams(project)
}

func (h pipelineHost) Tasks(project string) ([]fleet.Task, error) { return h.s.fleetTasks(project) }

func (h pipelineHost) ReadTask(rel string) (fleet.Task, []byte, error) { return h.s.fleetRead(rel) }

func (h pipelineHost) UpdateTask(rel string, build func(fleet.Task, []byte) ([]byte, error), actor string) (fleet.Task, error) {
	t, err := h.s.fleetUpdate(context.Background(), taskWrite{actor: actor}, rel, build)
	var werr *writeError
	if errors.As(err, &werr) {
		return t, errors.New(werr.msg)
	}
	return t, err
}

func (h pipelineHost) CreateTask(project string, n fleet.NewTask, actor string) (fleet.Task, error) {
	t, err := h.s.fleetCreate(context.Background(), taskWrite{actor: actor}, project, n)
	var werr *writeError
	if errors.As(err, &werr) {
		return t, errors.New(werr.msg)
	}
	return t, err
}
