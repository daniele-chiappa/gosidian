package mcp

// The task tools (IMP-195): the tasks of a project's teams of agents. A
// team's run gets its task from the task trigger, and its pipeline moves
// and closes it with the task nodes: the agent of a run reads the tasks and
// creates sub-tasks only for the roles its own role delegates to, under its
// own task, but moves none, so it never closes its work past the checks
// and approvals of its pipeline. An outside agent takes a ready task with
// memory_task_claim, keeps it with heartbeats, and moves and closes the
// tasks it holds (name@id).

import (
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
		mcp.WithDescription("List the tasks of a project's teams of agents (IMP-195): each with its status (submitted, working, input-required, review, completed, failed, canceled), role, holder, attempts, cost and, for a submitted one, why it does not start yet; and each team with what it spent today against its budget. Tasks are notes in <project>/tasks/, rows of the database tasks.md; a team is a note type: team with a ```team block (docs/agents.md)."),
		mcp.WithString("project", mcp.Required(), mcp.Description("The project. "+scopedProjectNote)),
		mcp.WithString("team", mcp.Description("Only this team's tasks: a link to its note ([[p/squadra]]) or its path.")),
		mcp.WithString("status", mcp.Description("open (default: not completed, failed or canceled), all, or one status.")),
		mcp.WithString("role", mcp.Description("Only the tasks of this role.")),
	), s.handleTasks)

	s.impl.AddTool(mcp.NewTool("memory_task_create",
		mcp.WithDescription("Create a task for a role of a team (IMP-195): a note in <project>/tasks/ that starts, once submitted and its dependencies completed, the pipeline that takes the role's tasks. With a run token, only for the roles your role delegates to, as a sub-task of your own task (one level). Returns the task's path."),
		mcp.WithString("project", mcp.Required(), mcp.Description("The project. "+scopedProjectNote)),
		mcp.WithString("team", mcp.Required(), mcp.Description("The team: a link to its note ([[p/squadra]]) or its path.")),
		mcp.WithString("assignee", mcp.Required(), mcp.Description("The role of the team that does it.")),
		mcp.WithString("title", mcp.Required(), mcp.Description("What to do, in a line.")),
		mcp.WithString("body", mcp.Description("What to do and how it is accepted: the agent reads it as its task.")),
		mcp.WithString("parent", mcp.Description("The objective it is part of (a task without a parent); a run token's own task by default.")),
		mcp.WithArray("depends_on", mcp.Description("Tasks of the project that must be completed before it starts: their reports are in its prompt.")),
		mcp.WithArray("paths", mcp.Description("Paths of the repository it may change, beyond its role's (a folder ends with /).")),
		mcp.WithArray("forbid", mcp.Description("Paths of the repository it must not change.")),
		mcp.WithNumber("budget_usd", mcp.Description("What it may cost, in USD; default the team's task_usd.")),
		mcp.WithBoolean("hold", mcp.Description("true: input-required instead of submitted, so a person starts it.")),
	), s.handleTaskCreate)

	s.impl.AddTool(mcp.NewTool("memory_task_claim",
		mcp.WithDescription("Take a ready task as an outside agent (IMP-195): it goes working, held by your token, with a lease of [agents] lease (15 minutes) that memory_task_heartbeat renews. A lease that ends gives the task back. A task whose dependencies are not completed, or that someone holds, is refused. The agent of a pipeline's run takes none: its task comes with the run."),
		mcp.WithString("path", mcp.Required(), mcp.Description("The task's note.")),
	), s.handleTaskClaim)

	s.impl.AddTool(mcp.NewTool("memory_task_heartbeat",
		mcp.WithDescription("Renew the lease of a task you hold as an outside agent (IMP-195): call it more often than [agents] lease while you work."),
		mcp.WithString("path", mcp.Required(), mcp.Description("The task's note.")),
	), s.handleTaskHeartbeat)

	s.impl.AddTool(mcp.NewTool("memory_task_update",
		mcp.WithDescription("Move a task you hold as an outside agent (IMP-195): input-required when you need a person's answer, review when the work waits for a person's approval, working again, or submitted to give it back for another attempt. The message goes in the task as a dated section. The agent of a pipeline's run moves none: its pipeline does."),
		mcp.WithString("path", mcp.Required(), mcp.Description("The task's note.")),
		mcp.WithString("status", mcp.Required(), mcp.Description("working, input-required, review or submitted.")),
		mcp.WithString("message", mcp.Description("What you ask, or what the next attempt must know.")),
	), s.handleTaskUpdate)

	s.impl.AddTool(mcp.NewTool("memory_task_complete",
		mcp.WithDescription("Close a task you hold as an outside agent with your report (IMP-195): the section Report of the task, which the tasks that depend on it and the coordinator read. The agent of a pipeline's run closes none: it gives its report as its answer, and its pipeline closes the task after its checks."),
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
	role := strings.TrimSpace(req.GetString("role", ""))
	status := strings.TrimSpace(req.GetString("status", "open"))
	if status != "open" && status != "all" && !slices.Contains(fleet.States, status) {
		return mcp.NewToolResultErrorf("status: open, all or one of %s", strings.Join(fleet.States, ", ")), nil
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
		case team != "" && t.Team != team, role != "" && t.Assignee != role:
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
	return mcp.NewToolResultJSON(map[string]any{"project": project, "teams": teamRows, "tasks": rows, "count": len(rows)})
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
	teams, err := s.fleetTeams(project)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	teamPath := fleet.LinkPath(req.GetString("team", ""), project)
	team, ok := teams[teamPath]
	if !ok {
		return mcp.NewToolResultErrorf("team %s is no team of project %s, or its block does not read (memory_lint, rule team-invalid)", teamPath, project), nil
	}
	assignee := strings.TrimSpace(req.GetString("assignee", ""))
	if _, ok := team.Role(assignee); !ok {
		return mcp.NewToolResultErrorf("role %q is no role of %s", assignee, teamPath), nil
	}
	title := strings.TrimSpace(req.GetString("title", ""))
	if title == "" {
		return mcp.NewToolResultError("title is required"), nil
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
	for _, d := range req.GetStringSlice("depends_on", nil) {
		dp := fleet.LinkPath(d, project)
		if _, ok := byPath[dp]; !ok {
			return mcp.NewToolResultErrorf("depends_on %s is no task of the project", dp), nil
		}
		n.DependsOn = append(n.DependsOn, dp)
	}
	t, err := s.fleetCreate(ctx, taskWrite{tok: tok}, project, n)
	if err != nil {
		return taskError(err), nil
	}
	return taskResult(t, nil)
}

func (s *Server) handleTaskClaim(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rel, res := s.taskRel(req)
	if res != nil {
		return res, nil
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
	status, message := req.GetString("status", ""), req.GetString("message", "")
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
