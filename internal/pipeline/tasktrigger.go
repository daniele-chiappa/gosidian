package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/clock"
	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/jobs"
)

// The task trigger (IMP-195): the tasks of a project's teams start its
// pipelines on task.
//
//   - ready: a task submitted, whose dependencies are completed, starts the
//     pipeline that takes its team and role. The trigger takes the task for
//     the run as it starts it (working, held by run-<id>, one attempt
//     more), so two ticks never start it twice; the run's end gives it back
//     (taskRunEnded).
//   - children_done: an objective whose sub-tasks are all closed, or one
//     failed or asks for input, starts its coordinator's pipeline again,
//     once for each state of those that call it (childrenState), at most
//     max_rounds times.
//   - budget: the team spent its budget of the day; once a day, for a
//     pipeline that tells someone.
//
// A team's spend is the cost of its runs' steps in the day of the notes'
// zone; a ready task does not start while it is spent. The loop ticks every
// minute and when a task is written (KickTasks).

// Fleet is the host's side of the teams and tasks: the task trigger and
// the task nodes read and write them through it.
type Fleet interface {
	// Teams are the team notes of a project whose block reads, by path.
	Teams(project string) (map[string]fleet.Team, error)
	// Tasks are the task notes of a project.
	Tasks(project string) ([]fleet.Task, error)
	// ReadTask reads one task with its note's content.
	ReadTask(path string) (fleet.Task, []byte, error)
	// UpdateTask rewrites a task under its note's lock: build gets the
	// task and its content as they are; ErrTaskSkip leaves the note alone.
	UpdateTask(path string, build func(t fleet.Task, content []byte) ([]byte, error), actor string) (fleet.Task, error)
	// CreateTask writes a new task note of project.
	CreateTask(project string, n fleet.NewTask, actor string) (fleet.Task, error)
}

// ErrTaskSkip, returned by an UpdateTask's build, writes nothing.
var ErrTaskSkip = errors.New("the task stays as it is")

// The events of the task trigger.
const (
	EventReady        = "ready"
	EventChildrenDone = "children_done"
	EventBudget       = "budget"
)

// taskEvents are the events a task trigger may take.
var taskEvents = []string{EventReady, EventChildrenDone, EventBudget}

// The resources of the agents of the teams (IMP-195): the steps of a task's
// run that run an agent take a place in their team's and in the
// installation's.
const (
	ResourceTeam   = "team:"
	ResourceAgents = "agents"
)

// AgentContract is the contract of the nodes that run an agent.
const AgentContract = "agent-run/1"

// TaskConfig are the limits of the tasks (the [agents] section).
type TaskConfig struct {
	// Lease is how long an outside agent holds a task without a
	// heartbeat; MaxAttempts the attempts before it fails.
	Lease       time.Duration
	MaxAttempts int
	MaxTasks    int
}

// taskLoop is the state of the task trigger.
type taskLoop struct {
	mu   sync.Mutex
	kick chan struct{}
	// teams are the teams last read, by name (Team.Name), for their limits.
	teams map[string]fleet.Team
	// waits say why a submitted task did not start at the last tick.
	waits map[string]string
	// budget is the day each team's budget event was sent.
	budget map[string]string
	// wrote is what the MCP tokens of each run at work wrote, by run id:
	// the notes task.gate looks for. Kept in memory until the run ends.
	wrote map[string]map[string]bool
}

// RunWrote records a vault path the MCP token of a run wrote, moved or
// deleted (the MCP server calls it on each write of a run's token): the
// gate of a team's task reads what its run wrote.
func (e *Engine) RunWrote(runID, path string) {
	if runID == "" || path == "" {
		return
	}
	e.tasks.mu.Lock()
	defer e.tasks.mu.Unlock()
	if e.tasks.wrote == nil {
		e.tasks.wrote = map[string]map[string]bool{}
	}
	if e.tasks.wrote[runID] == nil {
		e.tasks.wrote[runID] = map[string]bool{}
	}
	e.tasks.wrote[runID][path] = true
}

// RunWrites is what the run's MCP tokens wrote, sorted, until the run ends.
func (e *Engine) RunWrites(runID string) []string {
	e.tasks.mu.Lock()
	defer e.tasks.mu.Unlock()
	out := make([]string, 0, len(e.tasks.wrote[runID]))
	for p := range e.tasks.wrote[runID] {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (e *Engine) forgetRunWrites(runID string) {
	e.tasks.mu.Lock()
	defer e.tasks.mu.Unlock()
	delete(e.tasks.wrote, runID)
}

// TakesTask says whether a task trigger takes event for a task of team
// (its note's path) and role, coordinator being the team's.
func (t Trigger) TakesTask(event, team, role, coordinator string) bool {
	if t.On != TriggerTask || !slices.Contains(t.Events, event) {
		return false
	}
	if t.Team != "" {
		project, _, _ := strings.Cut(team, "/")
		if fleet.LinkPath(t.Team, project) != team {
			return false
		}
	}
	if t.Coordinator != nil && role != "" && *t.Coordinator != (role == coordinator) {
		return false
	}
	return len(t.Roles) == 0 || slices.Contains(t.Roles, role)
}

// KickTasks asks the task loop for a tick soon: a task was written.
func (e *Engine) KickTasks() {
	select {
	case e.tasks.kick <- struct{}{}:
	default:
	}
}

// StartTasks ticks the task triggers every minute, and soon after a kick,
// until ctx ends.
func (e *Engine) StartTasks(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		e.TickTasks()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-e.tasks.kick:
			// The writes of a burst land together.
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
}

// TeamLimit is the limit of a team's resource (team:<name>), its
// max_agents; 0 for no limit or a team not read yet.
func (e *Engine) TeamLimit(name string) int {
	e.tasks.mu.Lock()
	defer e.tasks.mu.Unlock()
	return e.tasks.teams[name].MaxAgents
}

// TaskWaits says why the submitted tasks of project did not start at the
// last tick, by path.
func (e *Engine) TaskWaits(project string) map[string]string {
	e.tasks.mu.Lock()
	defer e.tasks.mu.Unlock()
	out := map[string]string{}
	for p, w := range e.tasks.waits {
		if strings.HasPrefix(p, project+"/") {
			out[p] = w
		}
	}
	return out
}

// TeamSpend is what team (its note's path) spent today, in the notes' zone.
func (e *Engine) TeamSpend(project, team string) (jobs.Spend, error) {
	return e.runner.Store().TeamSpend(project, team, e.dayStart(e.now()))
}

func (e *Engine) dayStart(now time.Time) time.Time {
	l := now.In(e.cfg.Location)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, e.cfg.Location)
}

// TickTasks starts the task triggers' runs: it gives back the tasks whose
// holder is gone, starts the ready tasks and the coordinators of the
// objectives whose sub-tasks closed, and tells of a spent budget.
func (e *Engine) TickTasks() {
	fl, ok := e.host.(Fleet)
	if !ok {
		return
	}
	ps, err := e.host.Pipelines("")
	if err != nil {
		e.log.Warn("pipeline: listing the pipelines for the task triggers", "err", err)
		return
	}
	byProject := map[string][]Pipeline{}
	for _, p := range ps {
		if p.Err == nil && p.Spec.Trigger.On == TriggerTask && e.host.Enabled(p.Project) {
			byProject[p.Project] = append(byProject[p.Project], p)
		}
	}
	waits := map[string]string{}
	teamsSeen := map[string]fleet.Team{}
	projects := make([]string, 0, len(byProject))
	for p := range byProject {
		projects = append(projects, p)
	}
	sort.Strings(projects)
	for _, project := range projects {
		teams, err := fl.Teams(project)
		if err != nil {
			e.log.Warn("pipeline: reading the teams", "project", project, "err", err)
			continue
		}
		for _, t := range teams {
			teamsSeen[t.Name()] = t
		}
		e.tickProject(fl, project, byProject[project], teams, waits)
	}
	e.tasks.mu.Lock()
	e.tasks.teams, e.tasks.waits = teamsSeen, waits
	e.tasks.mu.Unlock()
}

func (e *Engine) tickProject(fl Fleet, project string, pipes []Pipeline, teams map[string]fleet.Team, waits map[string]string) {
	tasks, err := fl.Tasks(project)
	if err != nil {
		e.log.Warn("pipeline: reading the tasks", "project", project, "err", err)
		return
	}
	now := e.now()
	// The holds whose holder is gone: a run that ended without the engine
	// hearing of it (a restart), an outside agent past its lease.
	released := false
	for _, t := range tasks {
		if t.ClaimedBy == "" || fleet.Ended(t.Status) {
			continue
		}
		if id, ok := strings.CutPrefix(t.ClaimedBy, "run-"); ok {
			if run, err := e.runner.Store().Run(id); err == nil && run.Status.Terminal() {
				e.giveBack(fl, t.Path, run, tasks)
				released = true
			}
			continue
		}
		if !t.LeaseUntil.IsZero() && now.After(t.LeaseUntil) {
			e.leaseEnded(fl, t)
			released = true
		}
	}
	if released {
		if tasks, err = fl.Tasks(project); err != nil {
			return
		}
	}
	byPath := make(map[string]fleet.Task, len(tasks))
	children := map[string][]fleet.Task{}
	for _, t := range tasks {
		byPath[t.Path] = t
		if t.Parent != "" {
			children[t.Parent] = append(children[t.Parent], t)
		}
	}
	spent := map[string]bool{}
	budgetOK := func(team fleet.Team) (bool, string) {
		if team.Budget.DayUSD <= 0 {
			return true, ""
		}
		s, err := e.TeamSpend(project, team.Path)
		if err != nil {
			return false, "the team's spend does not read: " + err.Error()
		}
		if s.CostUSD >= team.Budget.DayUSD {
			spent[team.Path] = true
			return false, fmt.Sprintf("the team spent its budget of the day (%.2f of %.2f USD)", s.CostUSD, team.Budget.DayUSD)
		}
		return true, ""
	}
	for _, t := range tasks {
		if t.Status != fleet.Submitted || t.ClaimedBy != "" && !t.LeaseUntil.IsZero() && now.Before(t.LeaseUntil) {
			continue
		}
		if w := t.Waits(byPath, now); w != "" {
			waits[t.Path] = w
			continue
		}
		team, ok := teams[t.Team]
		if !ok {
			waits[t.Path] = "team " + t.Team + " is no team of the project, or its block does not read"
			continue
		}
		if _, ok := team.Role(t.Assignee); !ok {
			waits[t.Path] = "role " + t.Assignee + " is no role of " + t.Team
			continue
		}
		p, ok := takerOf(pipes, EventReady, team, t.Assignee)
		if !ok {
			waits[t.Path] = "no pipeline of the project takes the ready tasks of role " + t.Assignee
			continue
		}
		if ok, why := budgetOK(team); !ok {
			waits[t.Path] = why
			continue
		}
		if err := e.startTask(fl, p, t, team, EventReady, nil); err != nil {
			waits[t.Path] = err.Error()
		}
	}
	parents := make([]string, 0, len(children))
	for p := range children {
		parents = append(parents, p)
	}
	sort.Strings(parents)
	for _, parent := range parents {
		obj, ok := byPath[parent]
		if !ok || obj.Status != fleet.Working || obj.ClaimedBy != "" {
			continue
		}
		kids := children[parent]
		if !childrenDone(kids) {
			continue
		}
		state := childrenState(kids)
		if state == obj.ChildrenState {
			continue
		}
		team, ok := teams[obj.Team]
		if !ok {
			continue
		}
		p, ok := takerOf(pipes, EventChildrenDone, team, obj.Assignee)
		if !ok {
			continue
		}
		if obj.Rounds >= team.MaxRounds {
			e.askPerson(fl, obj.Path, state, fmt.Sprintf("The coordinator ran %d rounds on this objective, the team's max_rounds: a person decides how it goes on.", obj.Rounds))
			continue
		}
		if ok, why := budgetOK(team); !ok {
			waits[obj.Path] = why
			continue
		}
		if err := e.startTask(fl, p, obj, team, EventChildrenDone, kids); err != nil {
			waits[obj.Path] = err.Error()
		}
	}
	day := e.dayStart(now).Format("2006-01-02")
	for _, team := range sortedTeams(teams) {
		if !spent[team.Path] {
			continue
		}
		e.tasks.mu.Lock()
		sent := e.tasks.budget[team.Path] == day
		if !sent {
			if e.tasks.budget == nil {
				e.tasks.budget = map[string]string{}
			}
			e.tasks.budget[team.Path] = day
		}
		e.tasks.mu.Unlock()
		if sent {
			continue
		}
		s, _ := e.TeamSpend(project, team.Path)
		input := map[string]any{"event": EventBudget, "team": team.Path, "project": project, "day": day,
			"spent_usd": s.CostUSD, "budget_usd": team.Budget.DayUSD}
		for _, p := range pipes {
			if !p.Spec.Trigger.TakesTask(EventBudget, team.Path, "", team.Coordinator) {
				continue
			}
			if _, _, err := e.Start(StartRequest{Path: p.Path, Trigger: TriggerTask, Input: input, Actor: "task", IdemKey: "budget:" + team.Path + ":" + day}); err != nil {
				e.log.Warn("pipeline: a team's budget run", "pipeline", p.Path, "err", err)
			}
		}
	}
}

func sortedTeams(teams map[string]fleet.Team) []fleet.Team {
	out := make([]fleet.Team, 0, len(teams))
	for _, t := range teams {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// takerOf is the first pipeline that takes event for a task of team and
// role, in the order of their paths.
func takerOf(pipes []Pipeline, event string, team fleet.Team, role string) (Pipeline, bool) {
	for _, p := range pipes {
		if p.Spec.Trigger.TakesTask(event, team.Path, role, team.Coordinator) {
			return p, true
		}
	}
	return Pipeline{}, false
}

// childrenDone says whether an objective's sub-tasks call its coordinator:
// all closed, or one failed or waiting for a person.
func childrenDone(kids []fleet.Task) bool {
	all := true
	for _, k := range kids {
		if k.Status == fleet.Failed || k.Status == fleet.InputRequired {
			return true
		}
		if !fleet.Ended(k.Status) {
			all = false
		}
	}
	return all
}

// childrenState is a digest of what calls the coordinator: all the
// sub-tasks' states when they are all closed, else those of the ones that
// failed or wait for a person. The coordinator runs once for each: a
// sub-task that failed calls it once, not again each time another one
// starts or closes (the fitness pilot's third run burnt four rounds so).
func childrenState(kids []fleet.Task) string {
	all := true
	for _, k := range kids {
		if !fleet.Ended(k.Status) {
			all = false
		}
	}
	lines := make([]string, 0, len(kids)+1)
	for _, k := range kids {
		if all || k.Status == fleet.Failed || k.Status == fleet.InputRequired {
			lines = append(lines, k.Path+"="+k.Status)
		}
	}
	if all {
		lines = append(lines, "all closed")
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:8])
}

// startTask takes t for a run of p and starts it.
func (e *Engine) startTask(fl Fleet, p Pipeline, t fleet.Task, team fleet.Team, event string, kids []fleet.Task) error {
	id := jobs.NewID()
	holder := "run-" + id
	now := e.now()
	var n int
	_, err := fl.UpdateTask(t.Path, func(cur fleet.Task, content []byte) ([]byte, error) {
		set := map[string]any{fleet.KeyClaimedBy: holder, fleet.KeyUpdated: clock.NoteTime(now)}
		switch event {
		case EventReady:
			if cur.Status != fleet.Submitted || (cur.ClaimedBy != "" && cur.LeaseUntil.After(now)) {
				return nil, ErrTaskSkip
			}
			n = cur.Attempts + 1
			set[fleet.KeyStatus], set[fleet.KeyAttempts] = fleet.Working, n
		case EventChildrenDone:
			if cur.Status != fleet.Working || cur.ClaimedBy != "" {
				return nil, ErrTaskSkip
			}
			n = cur.Rounds + 1
			set[fleet.KeyRounds], set[fleet.KeyChildrenState] = n, childrenState(kids)
		}
		return fleet.Set(content, set, fleet.KeyLeaseUntil)
	}, "task")
	if errors.Is(err, ErrTaskSkip) {
		// It changed since the tick read it.
		return nil
	}
	if err != nil {
		return fmt.Errorf("taking the task: %w", err)
	}
	input := map[string]any{"event": event, "task": t.Path, "title": t.Title, "team": team.Path, "role": t.Assignee,
		"project": team.Project(), "attempt": n}
	if event == EventChildrenDone {
		input["round"] = n
		delete(input, "attempt")
		var ks []map[string]any
		for _, k := range kids {
			ks = append(ks, map[string]any{"path": k.Path, "title": k.Title, "status": k.Status, "assignee": k.Assignee, "cost_usd": k.CostUSD})
		}
		input["children"] = ks
	}
	key := "task:" + t.Path + "#" + event + "-" + strconv.Itoa(n)
	req := StartRequest{Path: p.Path, Trigger: TriggerTask, Input: input, Actor: "task", IdemKey: key,
		id: id, task: t.Path, team: team.Path}
	run, _, err := e.Start(req)
	if err == nil && run.ID != id && run.Status.Terminal() {
		// The key's run ended before: the task's count went back (a person
		// set its attempts by hand). A run that ended never holds a task
		// again: this attempt is a run of its own.
		req.IdemKey = key + "~" + id
		run, _, err = e.Start(req)
	}
	if err == nil && run.ID != id {
		// The key's run was already there: it holds the task.
		_, err = fl.UpdateTask(t.Path, func(cur fleet.Task, content []byte) ([]byte, error) {
			if cur.ClaimedBy != holder {
				return nil, ErrTaskSkip
			}
			return fleet.Set(content, map[string]any{fleet.KeyClaimedBy: "run-" + run.ID})
		}, "task")
		if run.Status.Terminal() {
			e.taskRunEnded(run)
		}
		return err
	}
	if err != nil {
		// Give it back as it was.
		_, _ = fl.UpdateTask(t.Path, func(cur fleet.Task, content []byte) ([]byte, error) {
			if cur.ClaimedBy != holder {
				return nil, ErrTaskSkip
			}
			set := map[string]any{}
			if event == EventReady {
				set[fleet.KeyStatus], set[fleet.KeyAttempts] = fleet.Submitted, n-1
			} else {
				set[fleet.KeyRounds] = n - 1
			}
			return fleet.Set(content, set, fleet.KeyClaimedBy, fleet.KeyChildrenState)
		}, "task")
		return fmt.Errorf("starting %s: %w", p.Path, err)
	}
	return nil
}

// taskRunEnded gives back the task its run held: it stays as the run left
// it when closed, waits for its sub-tasks when it has open ones, goes
// back to submitted when the run failed (failed after max_attempts), and
// to a person when the run was canceled or ended without closing it.
func (e *Engine) taskRunEnded(run jobs.Run) {
	if run.Task == "" {
		return
	}
	fl, ok := e.host.(Fleet)
	if !ok {
		return
	}
	tasks, err := fl.Tasks(run.Project)
	if err != nil {
		e.log.Warn("pipeline: reading the tasks at the end of a task's run", "run", run.ID, "err", err)
		return
	}
	e.giveBack(fl, run.Task, run, tasks)
	e.KickTasks()
}

func (e *Engine) giveBack(fl Fleet, path string, run jobs.Run, tasks []fleet.Task) {
	open, parent := false, false
	for _, t := range tasks {
		if t.Parent == path {
			parent = true
			if !fleet.Ended(t.Status) {
				open = true
			}
		}
	}
	holder := "run-" + run.ID
	max := e.cfg.Tasks.MaxAttempts
	if max <= 0 {
		max = 3
	}
	now := e.now()
	_, err := fl.UpdateTask(path, func(cur fleet.Task, content []byte) ([]byte, error) {
		if cur.ClaimedBy != holder {
			return nil, ErrTaskSkip
		}
		set := map[string]any{fleet.KeyUpdated: clock.NoteTime(now)}
		unset := []string{fleet.KeyClaimedBy, fleet.KeyLeaseUntil}
		var note string
		switch {
		case fleet.Ended(cur.Status) || cur.Status == fleet.InputRequired:
		case cur.Status == fleet.Review:
			// The approval it waited for will not come: a person decides.
			set[fleet.KeyStatus] = fleet.InputRequired
			note = "Run " + run.ID + " ended (" + string(run.Status) + ") while the task waited for a person's approval: a person decides how it goes on."
		case open:
			// An objective waits for its sub-tasks, held by none.
			set[fleet.KeyStatus] = fleet.Working
		case run.Status == jobs.Failed && cur.Attempts >= max:
			set[fleet.KeyStatus] = fleet.Failed
			note = fmt.Sprintf("Attempt %d of %d failed, the last one: run %s failed: %s", cur.Attempts, max, run.ID, clipHead(run.Error, 1000))
		case run.Status == jobs.Failed:
			set[fleet.KeyStatus] = fleet.Submitted
			note = fmt.Sprintf("Attempt %d of %d: run %s failed: %s", cur.Attempts, max, run.ID, clipHead(run.Error, 1000))
		case run.Status == jobs.Canceled:
			set[fleet.KeyStatus] = fleet.InputRequired
			note = "Run " + run.ID + " was canceled: a person decides how the task goes on."
		case parent:
			set[fleet.KeyStatus] = fleet.InputRequired
			note = "The coordinator's run " + run.ID + " neither closed the objective nor gave it new tasks: a person decides how it goes on."
		default:
			set[fleet.KeyStatus] = fleet.InputRequired
			note = "Run " + run.ID + " ended without closing the task: a person decides how it goes on."
		}
		out, err := fleet.Set(content, set, unset...)
		if err != nil {
			return nil, err
		}
		if note != "" {
			out = fleet.AppendSection(out, now.In(clock.Location()).Format("2006-01-02 15:04")+" — "+string(run.Status), note)
		}
		return out, nil
	}, "task")
	if err != nil && !errors.Is(err, ErrTaskSkip) {
		e.log.Warn("pipeline: giving back a task", "task", path, "run", run.ID, "err", err)
	}
}

// leaseEnded gives back a task an outside agent held past its lease.
func (e *Engine) leaseEnded(fl Fleet, t fleet.Task) {
	max := e.cfg.Tasks.MaxAttempts
	if max <= 0 {
		max = 3
	}
	now := e.now()
	_, err := fl.UpdateTask(t.Path, func(cur fleet.Task, content []byte) ([]byte, error) {
		if cur.ClaimedBy != t.ClaimedBy || cur.LeaseUntil.IsZero() || now.Before(cur.LeaseUntil) || fleet.Ended(cur.Status) {
			return nil, ErrTaskSkip
		}
		status := fleet.Submitted
		if cur.Attempts >= max {
			status = fleet.Failed
		}
		out, err := fleet.Set(content, map[string]any{fleet.KeyStatus: status, fleet.KeyUpdated: clock.NoteTime(now)}, fleet.KeyClaimedBy, fleet.KeyLeaseUntil)
		if err != nil {
			return nil, err
		}
		return fleet.AppendSection(out, now.In(clock.Location()).Format("2006-01-02 15:04")+" — lease ended",
			fmt.Sprintf("%s held the task until %s without a heartbeat (attempt %d of %d).", cur.ClaimedBy, clock.NoteTime(cur.LeaseUntil), cur.Attempts, max)), nil
	}, "task")
	if err != nil && !errors.Is(err, ErrTaskSkip) {
		e.log.Warn("pipeline: a task's lease", "task", t.Path, "err", err)
	}
}

// askPerson puts an objective in input-required with why.
func (e *Engine) askPerson(fl Fleet, path, state, why string) {
	now := e.now()
	_, err := fl.UpdateTask(path, func(cur fleet.Task, content []byte) ([]byte, error) {
		if cur.Status != fleet.Working || cur.ClaimedBy != "" {
			return nil, ErrTaskSkip
		}
		out, err := fleet.Set(content, map[string]any{fleet.KeyStatus: fleet.InputRequired, fleet.KeyChildrenState: state, fleet.KeyUpdated: clock.NoteTime(now)})
		if err != nil {
			return nil, err
		}
		return fleet.AppendSection(out, now.In(clock.Location()).Format("2006-01-02 15:04")+" — input-required", why), nil
	}, "task")
	if err != nil && !errors.Is(err, ErrTaskSkip) {
		e.log.Warn("pipeline: an objective to a person", "task", path, "err", err)
	}
}

// addTaskCost adds what a step of a task's run cost to the task.
func (e *Engine) addTaskCost(run jobs.Run, output []byte) {
	if run.Task == "" {
		return
	}
	cost, tokens := jobs.CostOf(output)
	if cost <= 0 && tokens <= 0 {
		return
	}
	fl, ok := e.host.(Fleet)
	if !ok {
		return
	}
	_, err := fl.UpdateTask(run.Task, func(cur fleet.Task, content []byte) ([]byte, error) {
		c := cur.CostUSD + cost
		return fleet.Set(content, map[string]any{fleet.KeyCostUSD: float64(int64(c*10000+0.5)) / 10000, fleet.KeyTokens: cur.Tokens + tokens})
	}, "task")
	if err != nil && !errors.Is(err, ErrTaskSkip) {
		e.log.Warn("pipeline: a task's cost", "task", run.Task, "run", run.ID, "err", err)
	}
}

// agentNode says whether node runs an agent: a plugin's node of the
// agent-run contract.
func (e *Engine) agentNode(node *Node) bool {
	if _, base := e.types.types[node.Use]; base {
		return false
	}
	x, ok := e.types.ext.(interface{ Contract(use string) string })
	return ok && x.Contract(node.Use) == AgentContract
}

// RunWritePaths says where the MCP token of a run's agent may write
// (plugins.RunScopes): a run of a team's task writes only in its role's
// folders (writes), and nowhere when the role names none; another run is
// not scoped.
func (e *Engine) RunWritePaths(runID string) ([]string, bool, error) {
	run, err := e.runner.Store().Run(runID)
	if err != nil {
		return nil, false, err
	}
	if run.Task == "" {
		return nil, false, nil
	}
	fl, ok := e.host.(Fleet)
	if !ok {
		return nil, true, nil
	}
	t, _, err := fl.ReadTask(run.Task)
	if err != nil {
		return nil, true, fmt.Errorf("the run's task %s: %w", run.Task, err)
	}
	teams, err := fl.Teams(run.Project)
	if err != nil {
		return nil, true, err
	}
	team, ok := teams[run.Team]
	if !ok {
		return nil, true, fmt.Errorf("the run's team %s is no team of the project, or its block does not read", run.Team)
	}
	role, ok := team.Role(t.Assignee)
	if !ok {
		return nil, true, fmt.Errorf("role %s is no role of %s", t.Assignee, run.Team)
	}
	return role.Writes, true, nil
}

// executorResources are the places the executor's plugin of a run's task
// takes (task.run, task.pr call it): its plugin and shared resources.
func (e *Engine) executorResources(run jobs.Run) []string {
	fl, ok := e.host.(Fleet)
	if !ok || run.Task == "" {
		return nil
	}
	t, _, err := fl.ReadTask(run.Task)
	if err != nil {
		return nil
	}
	teams, err := fl.Teams(run.Project)
	if err != nil {
		return nil
	}
	role, ok := teams[run.Team].Role(t.Assignee)
	if !ok {
		return nil
	}
	plugin, _, _ := strings.Cut(role.Run, ".")
	if x, ok := e.types.ext.(interface{ Resources(use string) []string }); ok {
		return x.Resources(plugin + ".run")
	}
	return nil
}
