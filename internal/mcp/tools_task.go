package mcp

// The task tools (IMP-195): the tasks of a project's teams of agents. A
// team's run gets its task from the task trigger, and its pipeline moves
// and closes it with the task nodes: the agent of a run reads the tasks and
// creates sub-tasks only for the roles its own role delegates to, under its
// own task, but moves none, so it never closes its work past the checks
// and approvals of its pipeline. An outside agent takes a ready task with
// memory_task_claim, keeps it with heartbeats, and moves and closes the
// tasks it holds (name@id). The same tools work on a board's cards, which
// have no team (IMP-216, board.go): an agent takes the board's next card,
// moves it between columns and closes it, within the board's limits.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/clock"
	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/mark3labs/mcp-go/mcp"
)

func (s *Server) registerTaskTools() {
	s.impl.AddTool(mcp.NewTool("memory_tasks",
		mcp.WithDescription("List a project's tasks (IMP-195): status, role, holder, attempts, cost, a card's column and, if submitted, why it waits; and each team's spend today against its budget. Tasks are notes in <project>/tasks/; a team is a note with a ```team block, a board one with a ```board block (docs/agents.md, docs/board.md)."),
		mcp.WithString("project", mcp.Required(), mcp.Description("The project. "+scopedProjectNote)),
		mcp.WithString("team", mcp.Description("Only this team's tasks: a link to its note or its path.")),
		mcp.WithString("board", mcp.Description("Only this board's cards, with its columns.")),
		mcp.WithString("status", mcp.Description("open (default: not completed, failed or canceled), all, or one status.")),
		mcp.WithString("role", mcp.Description("Only the tasks of this role.")),
	), s.handleTasks)

	s.impl.AddTool(mcp.NewTool("memory_task_create",
		mcp.WithDescription("Create a task (IMP-195) for a team's role: it starts the role's pipeline once submitted and its dependencies completed; a run token gives tasks only to roles yours delegates to, under your task. Or a card of a board, with no team (IMP-216)."),
		mcp.WithString("project", mcp.Required(), mcp.Description("The project. "+scopedProjectNote)),
		mcp.WithString("team", mcp.Description("The team ([[p/squadra]]), with assignee.")),
		mcp.WithString("assignee", mcp.Description("The team's role that does it.")),
		mcp.WithString("board", mcp.Description("Or the board of a card.")),
		mcp.WithString("column", mcp.Description("A card's column, of backlog or submitted; default the first.")),
		mcp.WithString("title", mcp.Required(), mcp.Description("What to do, in a line.")),
		mcp.WithString("body", mcp.Description("What to do and how it is accepted.")),
		mcp.WithString("parent", mcp.Description("Its objective; a run token's own task by default.")),
		mcp.WithArray("depends_on", mcp.Description("Tasks completed before it starts; their reports are in its prompt.")),
		mcp.WithArray("for", mcp.Description("The notes a card serves: IMP, plan, bug.")),
		mcp.WithString("done_by", mcp.Description("agent, or person after review; default the board's.")),
		mcp.WithArray("paths", mcp.Description("Repo paths it may change beyond its role's.")),
		mcp.WithArray("forbid", mcp.Description("Repo paths it must not change.")),
		mcp.WithNumber("budget_usd", mcp.Description("Cost cap in USD; default the team's task_usd.")),
		mcp.WithBoolean("hold", mcp.Description("true: input-required, so a person starts it.")),
	), s.handleTaskCreate)

	s.impl.AddTool(mcp.NewTool("memory_task_claim",
		mcp.WithDescription("Take a ready task as an outside agent (IMP-195): it goes working, held by your token for [agents] lease (15 min), which memory_task_heartbeat renews; an ended lease gives it back. With board, the board's next card, and the board's text: what done means there. A column at its wip limit refuses it. A run's agent takes none."),
		mcp.WithString("path", mcp.Description("The task's note.")),
		mcp.WithString("board", mcp.Description("Or a board's note ([[p/board]]).")),
	), s.handleTaskClaim)

	s.impl.AddTool(mcp.NewTool("memory_task_heartbeat",
		mcp.WithDescription("Renew the lease of a task you hold as an outside agent (IMP-195): call it more often than [agents] lease while you work."),
		mcp.WithString("path", mcp.Required(), mcp.Description("The task's note.")),
	), s.handleTaskHeartbeat)

	s.impl.AddTool(mcp.NewTool("memory_task_update",
		mcp.WithDescription("Move a task you hold as an outside agent (IMP-195): input-required for a person's answer, review for their approval, working again, submitted to give it back; a card, to a column. The message goes in the task as a dated section. A run's agent moves none."),
		mcp.WithString("path", mcp.Required(), mcp.Description("The task's note.")),
		mcp.WithString("status", mcp.Description("working, input-required, review or submitted.")),
		mcp.WithString("column", mcp.Description("Or a card's column.")),
		mcp.WithString("message", mcp.Description("What you ask, or what the next attempt must know.")),
	), s.handleTaskUpdate)

	s.impl.AddTool(mcp.NewTool("memory_task_complete",
		mcp.WithDescription("Close a task you hold as an outside agent with your report (IMP-195): its section Report, which the tasks after it and the coordinator read. A card a person closes goes to review instead. A run's agent closes none: its pipeline does, after its checks."),
		mcp.WithString("path", mcp.Required(), mcp.Description("The task's note.")),
		mcp.WithString("status", mcp.Description("completed (default), failed or canceled.")),
		mcp.WithString("report", mcp.Required(), mcp.Description("What was done, where it is, what is left.")),
	), s.handleTaskComplete)

	s.impl.AddTool(mcp.NewTool("memory_create_team",
		mcp.WithDescription("Create a team of agents in a project (IMP-195): the team's note with a ```team block to fill in (roles, budget, approvals: docs/agents.md), an agent note per role (agents/<role>.md) when missing, the pipeline that gives the roles' tasks to their agents (pipelines/<team>-work.md: the agent, the checks, the gate, the approval) and, with a coordinator, its own (pipelines/<team>-coordinate.md), the database of the project's tasks (tasks.md) when it has none, and a note that shows the team's tasks."),
		mcp.WithString("project", mcp.Required(), mcp.Description("The project. "+scopedProjectNote)),
		mcp.WithString("name", mcp.Description("The team's note, in the project's root: lower-case letters, digits and -; default team.")),
		mcp.WithArray("roles", mcp.Description("The roles' names; default [dev].")),
		mcp.WithString("run", mcp.Description("The node that runs the roles; default claude.run.")),
		mcp.WithString("coordinator", mcp.Description("The role that coordinates, one of roles: it gives tasks to the others, with its own pipeline.")),
	), s.handleCreateTeam)
}

// taskMax is the most sub-tasks an objective may have ([agents] max_tasks).
func (s *Server) taskMax() int {
	if s.pipelines != nil {
		if n := s.pipelines.Config().Tasks.MaxTasks; n > 0 {
			return n
		}
	}
	return 50
}

// taskRel resolves the path a task tool names.
func (s *Server) taskRel(req mcp.CallToolRequest) (string, *mcp.CallToolResult) {
	p, err := req.RequireString("path")
	if err != nil {
		return "", mcp.NewToolResultError(err.Error())
	}
	rel, err := s.vault.Rel(fleet.LinkPath(p, ""))
	if err != nil {
		return "", mcp.NewToolResultErrorFromErr("invalid path", err)
	}
	return rel, nil
}

// taskHolder is who moves a task: a token with the write scope on it, an
// outside agent's. A run's agent moves none: its pipeline does.
func (s *Server) taskHolder(ctx context.Context, rel string) (*auth.Token, string, *mcp.CallToolResult) {
	tok := s.tokenFromContext(ctx)
	if tok == nil {
		return nil, "", mcp.NewToolResultError("unauthorized")
	}
	if tok.Kind == auth.KindRun {
		return nil, "", mcp.NewToolResultError("the agent of a run moves no task: its pipeline moves and closes the task it works on (task.update, task.complete). Give your report as your answer")
	}
	tok, res := s.authorizeWrite(ctx, rel)
	if res != nil {
		return nil, "", res
	}
	return tok, tokenIdentity(tok), nil
}

// taskResult is a task tool's answer.
func taskResult(t fleet.Task, extra map[string]any) (*mcp.CallToolResult, error) {
	out := map[string]any{"task": t}
	for k, v := range extra {
		out[k] = v
	}
	return mcp.NewToolResultJSON(out)
}

// taskError is a refused write of a task.
func taskError(err error) *mcp.CallToolResult {
	var werr *writeError
	if errors.As(err, &werr) {
		return werr.result()
	}
	return mcp.NewToolResultError(err.Error())
}

func (s *Server) handleTasks(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, res := s.authorizeRead(ctx)
	if res != nil {
		return res, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	team := fleet.LinkPath(req.GetString("team", ""), project)
	board := fleet.LinkPath(req.GetString("board", ""), project)
	role := strings.TrimSpace(req.GetString("role", ""))
	status := strings.TrimSpace(req.GetString("status", "open"))
	if status != "open" && status != "all" && !fleet.Known(status) {
		return mcp.NewToolResultErrorf("status: open, all or one of %s, %s", fleet.Backlog, strings.Join(fleet.States, ", ")), nil
	}
	teams, err := s.fleetTeams(project)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	tasks, err := s.fleetTasks(project)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var waits map[string]string
	if s.pipelines != nil {
		waits = s.pipelines.TaskWaits(project)
	}
	type row struct {
		fleet.Task
		Waits string `json:"waits,omitempty"`
	}
	rows := []row{}
	for _, t := range tasks {
		switch {
		case team != "" && t.Team != team, board != "" && t.Board != board, role != "" && t.Assignee != role:
			continue
		case status == "open" && fleet.Ended(t.Status), status != "open" && status != "all" && t.Status != status:
			continue
		}
		r := row{Task: t}
		if t.Status == fleet.Submitted {
			r.Waits = waits[t.Path]
		}
		rows = append(rows, r)
	}
	teamRows := []map[string]any{}
	for _, t := range teams {
		if team != "" && t.Path != team {
			continue
		}
		r := map[string]any{"path": t.Path, "coordinator": t.Coordinator, "budget": t.Budget, "max_agents": t.MaxAgents}
		var roles []string
		for _, ro := range t.Roles {
			roles = append(roles, ro.Name)
		}
		r["roles"] = roles
		if s.pipelines != nil {
			if sp, err := s.pipelines.TeamSpend(project, t.Path); err == nil {
				r["spent_today"] = sp
			}
		}
		teamRows = append(teamRows, r)
	}
	slices.SortFunc(teamRows, func(a, b map[string]any) int { return strings.Compare(a["path"].(string), b["path"].(string)) })
	out := map[string]any{"project": project, "teams": teamRows, "tasks": rows, "count": len(rows)}
	if board != "" {
		b, _, err := s.fleetBoard(board)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		type column struct {
			fleet.Column
			Cards int `json:"cards"`
		}
		var columns []column
		for _, c := range b.Columns {
			columns = append(columns, column{c, b.Count(tasks, c.Name, "")})
		}
		out["board"] = map[string]any{"path": b.Path, "columns": columns, "wip": b.WIP, "done": b.Done}
	}
	return mcp.NewToolResultJSON(out)
}

func (s *Server) handleTaskCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok := s.tokenFromContext(ctx)
	if tok == nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	title := strings.TrimSpace(req.GetString("title", ""))
	if title == "" {
		return mcp.NewToolResultError("title is required"), nil
	}
	teamPath := fleet.LinkPath(req.GetString("team", ""), project)
	assignee := strings.TrimSpace(req.GetString("assignee", ""))
	switch board := fleet.LinkPath(req.GetString("board", ""), project); {
	case board != "" && (teamPath != "" || assignee != ""):
		return mcp.NewToolResultError("a card of a board has no team: the fleet works on boards with a later phase (IMP-216). Leave out board, or team and assignee"), nil
	case board != "":
		return s.createCard(ctx, tok, req, project, board, title)
	case teamPath == "" || assignee == "":
		return mcp.NewToolResultError("team and assignee for a task of a team's role, or board for a card"), nil
	}
	teams, err := s.fleetTeams(project)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	team, ok := teams[teamPath]
	if !ok {
		return mcp.NewToolResultErrorf("team %s is no team of project %s, or its block does not read (memory_lint, rule team-invalid)", teamPath, project), nil
	}
	if _, ok := team.Role(assignee); !ok {
		return mcp.NewToolResultErrorf("role %q is no role of %s", assignee, teamPath), nil
	}
	parent := fleet.LinkPath(req.GetString("parent", ""), project)
	if tok.Kind == auth.KindRun && tok.RunID != "" {
		// A run's agent gives tasks only as its role delegates, under its
		// own task.
		if s.pipelines == nil {
			return mcp.NewToolResultError("this server runs no pipelines"), nil
		}
		run, err := s.pipelines.Runner().Store().Run(tok.RunID)
		if err != nil || run.Task == "" {
			return mcp.NewToolResultError("only the run of a team's task creates tasks with a run token"), nil
		}
		own, _, err := s.fleetRead(run.Task)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		mine, ok := team.Role(own.Assignee)
		if run.Team != team.Path || !ok || !slices.Contains(mine.DelegatesTo, assignee) {
			return mcp.NewToolResultErrorf("role %s does not give tasks to %s of %s (delegates_to in the team)", own.Assignee, assignee, team.Path), nil
		}
		if parent == "" {
			parent = run.Task
		}
		if parent != run.Task {
			return mcp.NewToolResultErrorf("a sub-task of your own task %s only, not of %s", run.Task, parent), nil
		}
	} else if _, res := s.authorizeWrite(ctx, project+"/"+fleet.Folder+"/new.md"); res != nil {
		return res, nil
	}
	n := fleet.NewTask{Title: title, Body: req.GetString("body", ""), Team: team.Path, Assignee: assignee, Parent: parent,
		Paths: req.GetStringSlice("paths", nil), Forbid: req.GetStringSlice("forbid", nil), BudgetUSD: req.GetFloat("budget_usd", 0)}
	if n.BudgetUSD < 0 {
		return mcp.NewToolResultError("budget_usd: 0 (the team's task_usd) or more"), nil
	}
	if req.GetBool("hold", false) {
		n.Status = fleet.InputRequired
	}
	tasks, err := s.fleetTasks(project)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	byPath := map[string]fleet.Task{}
	for _, t := range tasks {
		byPath[t.Path] = t
	}
	if parent != "" {
		p, ok := byPath[parent]
		switch {
		case !ok:
			return mcp.NewToolResultErrorf("parent %s is no task of the project", parent), nil
		case p.Parent != "":
			return mcp.NewToolResultErrorf("parent %s is itself a sub-task: a team delegates one level", parent), nil
		case p.Team != team.Path:
			return mcp.NewToolResultErrorf("parent %s is a task of another team", parent), nil
		}
		kids := 0
		for _, t := range tasks {
			if t.Parent == parent {
				kids++
			}
		}
		if kids >= s.taskMax() {
			return mcp.NewToolResultErrorf("objective %s has %d sub-tasks, [agents] max_tasks", parent, kids), nil
		}
	}
	deps, res := dependsOn(req, project, tasks)
	if res != nil {
		return res, nil
	}
	n.DependsOn = deps
	t, err := s.fleetCreate(ctx, taskWrite{tok: tok}, project, n)
	if err != nil {
		return taskError(err), nil
	}
	return taskResult(t, nil)
}

// dependsOn reads the tasks a new task depends on, among the project's.
func dependsOn(req mcp.CallToolRequest, project string, tasks []fleet.Task) ([]string, *mcp.CallToolResult) {
	var out []string
	for _, d := range req.GetStringSlice("depends_on", nil) {
		dp := fleet.LinkPath(d, project)
		if _, ok := taskIn(tasks, dp); !ok {
			return nil, mcp.NewToolResultErrorf("depends_on %s is no task of the project", dp)
		}
		out = append(out, dp)
	}
	return out, nil
}

// createCard creates a card of the board at board (IMP-216): in its
// column, of backlog or submitted, at the bottom and within its limit. The
// first card of a project makes its database of the tasks hold cards.
func (s *Server) createCard(ctx context.Context, tok *auth.Token, req mcp.CallToolRequest, project, board, title string) (*mcp.CallToolResult, error) {
	switch {
	case tok.Kind == auth.KindRun:
		return mcp.NewToolResultError("the agent of a run creates no card: its tasks are its team's"), nil
	case req.GetBool("hold", false), req.GetString("parent", "") != "":
		return mcp.NewToolResultError("hold and parent are for a team's task: a card waits in its column"), nil
	case !strings.HasPrefix(board, project+"/"):
		return mcp.NewToolResultErrorf("board %s is not in project %s", board, project), nil
	}
	if _, res := s.authorizeWrite(ctx, project+"/"+fleet.Folder+"/new.md"); res != nil {
		return res, nil
	}
	b, _, err := s.fleetBoard(board)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	c := b.Columns[0]
	if name := req.GetString("column", ""); name != "" {
		var ok bool
		if c, ok = b.Column(name); !ok {
			return mcp.NewToolResultErrorf("the board %s has no column %s", b.Path, name), nil
		}
	}
	if c.Status != fleet.Backlog && c.Status != fleet.Submitted {
		return mcp.NewToolResultErrorf("the column %s is %s: a new card goes in a column of %s or %s", c.Name, c.Status, fleet.Backlog, fleet.Submitted), nil
	}
	doneBy := strings.TrimSpace(req.GetString("done_by", ""))
	switch doneBy {
	case "", fleet.DoneAgent:
	case fleet.DonePerson:
		if _, ok := b.First(fleet.Review); !ok {
			return mcp.NewToolResultErrorf("done_by person: the card waits for the person in a column of review, and %s has none", b.Path), nil
		}
	default:
		return mcp.NewToolResultErrorf("done_by %q: agent or person", doneBy), nil
	}
	n := fleet.NewTask{Title: title, Body: req.GetString("body", ""), Board: b.Path, Column: c.Name, Status: c.Status, DoneBy: doneBy,
		Paths: req.GetStringSlice("paths", nil), Forbid: req.GetStringSlice("forbid", nil), BudgetUSD: req.GetFloat("budget_usd", 0)}
	for _, f := range req.GetStringSlice("for", nil) {
		fp := fleet.LinkPath(f, project)
		if _, err := s.vault.Load(fp); err != nil || !strings.HasPrefix(fp, project+"/") {
			return mcp.NewToolResultErrorf("for %s is no note of the project", fp), nil
		}
		n.For = append(n.For, fp)
	}
	unlock := s.lockBoard(b.Path)
	defer unlock()
	tasks, err := s.boardCards(ctx, b, project)
	if err != nil {
		return taskError(err), nil
	}
	deps, res := dependsOn(req, project, tasks)
	if res != nil {
		return res, nil
	}
	n.DependsOn = deps
	extra := map[string]any{}
	warning, err := wip(b, tasks, c, "", false)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if warning != "" {
		extra["warning"] = warning
	}
	in := columnCards(b, tasks, c.Name, "")
	var ranks []float64
	n.Rank, ranks = place(in, nil)
	if err := s.cardsDatabase(ctx, project); err != nil {
		return taskError(err), nil
	}
	w := taskWrite{tok: tok}
	t, err := s.fleetCreate(ctx, w, project, n)
	if err == nil {
		err = s.renumber(ctx, w, in, ranks)
	}
	if err != nil {
		return taskError(err), nil
	}
	return taskResult(t, extra)
}

func (s *Server) handleTaskClaim(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rel, board := "", ""
	if req.GetString("path", "") != "" {
		var res *mcp.CallToolResult
		if rel, res = s.taskRel(req); res != nil {
			return res, nil
		}
		if cur, _, err := s.fleetRead(rel); err == nil {
			board = cur.Board
		}
	} else if b := req.GetString("board", ""); b != "" {
		var err error
		if board, err = s.vault.Rel(fleet.LinkPath(b, "")); err != nil {
			return mcp.NewToolResultErrorFromErr("invalid board", err), nil
		}
	}
	switch {
	case rel == "" && board == "":
		return mcp.NewToolResultError("path or board: the task you take, or the board whose next card you take"), nil
	case board != "":
		return s.claimCard(ctx, board, rel)
	}
	tok, holder, res := s.taskHolder(ctx, rel)
	if res != nil {
		return res, nil
	}
	project, _, _ := strings.Cut(rel, "/")
	tasks, err := s.fleetTasks(project)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	byPath := map[string]fleet.Task{}
	for _, t := range tasks {
		byPath[t.Path] = t
	}
	now := time.Now()
	lease := now.Add(s.taskLease())
	t, err := s.fleetUpdate(ctx, taskWrite{tok: tok}, rel, func(cur fleet.Task, content []byte) ([]byte, error) {
		if w := cur.Waits(byPath, now); w != "" {
			return nil, fmt.Errorf("the task does not start: %s", w)
		}
		return fleet.Set(content, map[string]any{fleet.KeyStatus: fleet.Working, fleet.KeyClaimedBy: holder,
			fleet.KeyLeaseUntil: clock.NoteTime(lease), fleet.KeyAttempts: cur.Attempts + 1, fleet.KeyUpdated: clock.NoteTime(now)})
	})
	if err != nil {
		return taskError(err), nil
	}
	return taskResult(t, map[string]any{"lease_until": clock.NoteTime(lease)})
}

// claimCard takes the card at rel of board, or the board's next card when
// rel is "" (fleet.Board.Next), into the board's first column of working,
// within its limit. The agent gets the board's text with it.
func (s *Server) claimCard(ctx context.Context, board, rel string) (*mcp.CallToolResult, error) {
	project, _, _ := strings.Cut(board, "/")
	at := rel
	if at == "" {
		at = project + "/" + fleet.Folder + "/next.md" // the token writes the project's cards
	}
	tok, holder, res := s.taskHolder(ctx, at)
	if res != nil {
		return res, nil
	}
	b, content, err := s.fleetBoard(board)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	unlock := s.lockBoard(b.Path)
	defer unlock()
	cards, err := s.boardCards(ctx, b, project)
	if err != nil {
		return taskError(err), nil
	}
	now := time.Now()
	cur, ok := taskIn(cards, rel)
	switch {
	case rel == "":
		if cur, ok = b.Next(cards, now); !ok {
			return mcp.NewToolResultErrorf("no card of %s is ready: none in its columns of %s has its dependencies completed, no holder and no team", b.Path, fleet.Submitted), nil
		}
	case !ok:
		return mcp.NewToolResultErrorf("%s is no task of the project", rel), nil
	}
	byPath := make(map[string]fleet.Task, len(cards))
	for _, t := range cards {
		byPath[t.Path] = t
	}
	lease := now.Add(s.taskLease())
	t, warning, err := s.moveCard(ctx, taskWrite{tok: tok}, b, cards, cur, "", fleet.Working, nil, func(c fleet.Task, content []byte, status string) ([]byte, error) {
		if w := c.Waits(byPath, now); w != "" {
			return nil, fmt.Errorf("the task does not start: %s", w)
		}
		return fleet.Set(content, map[string]any{fleet.KeyStatus: status, fleet.KeyClaimedBy: holder,
			fleet.KeyLeaseUntil: clock.NoteTime(lease), fleet.KeyAttempts: c.Attempts + 1, fleet.KeyUpdated: clock.NoteTime(now)})
	})
	if err != nil {
		return taskError(err), nil
	}
	return cardResult(tok, t, b, content, warning, map[string]any{"lease_until": clock.NoteTime(lease)})
}

// cardResult is a task tool's answer for a card: with its board's text,
// for a token that reads, and the warning of a column past its limit.
func cardResult(tok *auth.Token, t fleet.Task, b fleet.Board, content []byte, warning string, extra map[string]any) (*mcp.CallToolResult, error) {
	if extra == nil {
		extra = map[string]any{}
	}
	board := map[string]any{"path": b.Path}
	if tok.HasScope(auth.ScopeRead) {
		board["text"] = fleet.BoardText(content)
	}
	extra["board"] = board
	if warning != "" {
		extra["warning"] = warning
	}
	return taskResult(t, extra)
}

// withCard runs fn on the card at rel under its board's lock, with the
// project's cards; ok is false for a team's task, on no board.
func (s *Server) withCard(ctx context.Context, rel string, fn func(b fleet.Board, content []byte, cards []fleet.Task, cur fleet.Task) (*mcp.CallToolResult, error)) (*mcp.CallToolResult, bool, error) {
	cur, _, err := s.fleetRead(rel)
	if err != nil || cur.Board == "" {
		return nil, false, nil
	}
	b, content, err := s.fleetBoard(cur.Board)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), true, nil
	}
	unlock := s.lockBoard(b.Path)
	defer unlock()
	project, _, _ := strings.Cut(rel, "/")
	cards, err := s.boardCards(ctx, b, project)
	if err != nil {
		return taskError(err), true, nil
	}
	if cur, ok := taskIn(cards, rel); ok {
		res, err := fn(b, content, cards, cur)
		return res, true, err
	}
	return mcp.NewToolResultErrorf("%s is no task of the project", rel), true, nil
}

func (s *Server) handleTaskHeartbeat(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rel, res := s.taskRel(req)
	if res != nil {
		return res, nil
	}
	tok, holder, res := s.taskHolder(ctx, rel)
	if res != nil {
		return res, nil
	}
	lease := time.Now().Add(s.taskLease())
	t, err := s.fleetUpdate(ctx, taskWrite{tok: tok}, rel, func(cur fleet.Task, content []byte) ([]byte, error) {
		if cur.ClaimedBy != holder || fleet.Ended(cur.Status) {
			return nil, fmt.Errorf("the task %s is %s, held by %q, not by %s", rel, cur.Status, cur.ClaimedBy, holder)
		}
		return fleet.Set(content, map[string]any{fleet.KeyLeaseUntil: clock.NoteTime(lease)})
	})
	if err != nil {
		return taskError(err), nil
	}
	return taskResult(t, map[string]any{"lease_until": clock.NoteTime(lease)})
}

func (s *Server) handleTaskUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rel, res := s.taskRel(req)
	if res != nil {
		return res, nil
	}
	tok, holder, res := s.taskHolder(ctx, rel)
	if res != nil {
		return res, nil
	}
	status, column, message := req.GetString("status", ""), req.GetString("column", ""), req.GetString("message", "")
	if status == "" && column == "" {
		return mcp.NewToolResultError("status or column: where the task goes"), nil
	}
	res, card, err := s.withCard(ctx, rel, func(b fleet.Board, content []byte, cards []fleet.Task, cur fleet.Task) (*mcp.CallToolResult, error) {
		t, warning, err := s.moveCard(ctx, taskWrite{tok: tok}, b, cards, cur, column, status, nil, func(c fleet.Task, content []byte, status string) ([]byte, error) {
			return fleet.Update(c, content, holder, status, message, clock.Now())
		})
		if err != nil {
			return taskError(err), nil
		}
		return cardResult(tok, t, b, content, warning, nil)
	})
	switch {
	case card:
		return res, err
	case column != "":
		return mcp.NewToolResultErrorf("the task %s is on no board: it has no column", rel), nil
	}
	t, err := s.fleetUpdate(ctx, taskWrite{tok: tok}, rel, func(cur fleet.Task, content []byte) ([]byte, error) {
		return fleet.Update(cur, content, holder, status, message, clock.Now())
	})
	if err != nil {
		return taskError(err), nil
	}
	return taskResult(t, nil)
}

func (s *Server) handleTaskComplete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rel, res := s.taskRel(req)
	if res != nil {
		return res, nil
	}
	tok, holder, res := s.taskHolder(ctx, rel)
	if res != nil {
		return res, nil
	}
	status := req.GetString("status", fleet.Completed)
	report := req.GetString("report", "")
	// A card a person closes goes to review with the report.
	res, card, err := s.withCard(ctx, rel, func(b fleet.Board, content []byte, cards []fleet.Task, cur fleet.Task) (*mcp.CallToolResult, error) {
		to, move := status, func(c fleet.Task, content []byte, status string) ([]byte, error) {
			return fleet.Complete(c, content, holder, status, report, clock.Now())
		}
		if doneBy := cmp.Or(cur.DoneBy, b.Done); status == fleet.Completed && doneBy == fleet.DonePerson {
			to, move = fleet.Review, func(c fleet.Task, content []byte, _ string) ([]byte, error) {
				return fleet.ToReview(c, content, holder, report, clock.Now())
			}
		}
		t, warning, err := s.moveCard(ctx, taskWrite{tok: tok}, b, cards, cur, "", to, nil, move)
		if err != nil {
			return taskError(err), nil
		}
		return cardResult(tok, t, b, content, warning, nil)
	})
	if card {
		return res, err
	}
	t, err := s.fleetUpdate(ctx, taskWrite{tok: tok}, rel, func(cur fleet.Task, content []byte) ([]byte, error) {
		return fleet.Complete(cur, content, holder, status, report, clock.Now())
	})
	if err != nil {
		return taskError(err), nil
	}
	return taskResult(t, nil)
}

var teamName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

func (s *Server) handleCreateTeam(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok := s.tokenFromContext(ctx)
	if tok == nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	name := strings.TrimSpace(req.GetString("name", "team"))
	if !teamName.MatchString(name) {
		return mcp.NewToolResultErrorf("name %q: lower-case letters, digits and -, starting with a letter", name), nil
	}
	roles := req.GetStringSlice("roles", []string{"dev"})
	if len(roles) == 0 {
		roles = []string{"dev"}
	}
	coordinator := strings.TrimSpace(req.GetString("coordinator", ""))
	if coordinator != "" && !slices.Contains(roles, coordinator) {
		return mcp.NewToolResultErrorf("coordinator %q is none of the roles %s", coordinator, strings.Join(roles, ", ")), nil
	}
	run := strings.TrimSpace(req.GetString("run", "claude.run"))
	teamPath := project + "/" + name + ".md"
	content := fleet.TeamNote(project, name, run, roles, coordinator)
	if _, problems := fleet.Parse(fleet.Find(content)[0].Src, teamPath); len(problems) > 0 {
		return mcp.NewToolResultErrorf("the team would not read: %s", problems[0]), nil
	}
	type note struct {
		rel     string
		content []byte
		missing bool // written only when missing
	}
	notes := []note{
		{teamPath, content, false},
		{project + "/" + fleet.Database, fleet.DatabaseNote(project), true},
		{project + "/" + name + "-tasks.md", fleet.ViewNote(project, teamPath), false},
		{fleet.WorkPath(project, name), fleet.WorkPipeline(teamPath), false},
	}
	if coordinator != "" {
		notes = append(notes, note{fleet.CoordinatePath(project, name), fleet.CoordinatePipeline(teamPath), false})
	}
	for _, r := range roles {
		notes = append(notes, note{fleet.AgentPath(project, r), fleet.AgentNote(project, r, r == coordinator), true})
	}
	// Every note checked before any is written: a team is made whole or
	// not at all.
	for _, n := range notes {
		if _, res := s.authorizeWrite(ctx, n.rel); res != nil {
			return res, nil
		}
		if _, err := s.vault.Load(n.rel); err == nil && !n.missing {
			return mcp.NewToolResultErrorf("%s already exists", n.rel), nil
		}
	}
	var written []string
	for _, n := range notes {
		if _, err := s.vault.Load(n.rel); err == nil {
			continue
		}
		if _, werr := s.writeNote(ctx, tok, noteWrite{rel: n.rel, mode: writeCreate, content: fixedContent(n.content), schemaCheck: true}); werr != nil {
			return werr.result(), nil
		}
		written = append(written, n.rel)
	}
	return mcp.NewToolResultJSON(map[string]any{"team": teamPath, "written": written,
		"next": "Fill in the team's roles (model, repo, paths, checks, writes, approve), its alert and approvers, and its agents' notes (docs/agents.md); the pipelines take the tasks once the project runs pipelines."})
}
