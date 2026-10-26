package pipeline

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gosidian/gosidian/internal/clock"
	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/parser"
)

// The task nodes (IMP-195): task.context reads the task a run holds, with
// its team, role, agent and the reports it builds on, and writes the
// prompt of its agent; task.update moves it while the run works on it;
// task.complete closes it with the report. Only the run that holds a task
// moves or closes it.

// maxReport caps a report of another task in a prompt.
const maxReport = 4 << 10

func taskFleet(env *Env) (Fleet, error) {
	fl, ok := env.Engine.host.(Fleet)
	if !ok {
		return nil, errors.New("this server keeps no tasks")
	}
	return fl, nil
}

// taskPath is the task a node names (with.task: its vault path, or its
// path in the project), or the one its run's trigger gave.
func taskPath(env *Env, with map[string]any) (string, error) {
	p, _ := with["task"].(string)
	if p == "" {
		if tr, ok := env.Scope["trigger"].(map[string]any); ok {
			p, _ = tr["task"].(string)
		}
	}
	p = fleet.LinkPath(p, env.Project)
	if p == "" {
		return "", errors.New("with.task: the task's note (the task trigger gives {{ trigger.task }})")
	}
	if !strings.HasPrefix(p, env.Project+"/") {
		// Relative to the project, as the other nodes name notes.
		p = env.Project + "/" + p
	}
	return p, nil
}

// taskOutput is a task as the task nodes give it.
func taskOutput(t fleet.Task) map[string]any {
	return map[string]any{"path": t.Path, "title": t.Title, "status": t.Status, "assignee": t.Assignee, "team": t.Team,
		"parent": t.Parent, "depends_on": nonNil(t.DependsOn), "paths": nonNil(t.Paths), "forbid": nonNil(t.Forbid),
		"budget_usd": t.BudgetUSD, "cost_usd": t.CostUSD, "tokens": t.Tokens, "attempts": t.Attempts, "rounds": t.Rounds}
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

func roleOutput(r fleet.Role) map[string]any {
	return map[string]any{"name": r.Name, "agent": r.Agent, "run": r.Run, "model": r.Model, "effort": r.Effort,
		"max_turns": r.MaxTurns, "max_seconds": r.MaxSeconds, "max_tokens": r.MaxTokens, "repo": r.Repo, "host": r.Host,
		"paths": nonNil(r.Paths), "forbid": nonNil(r.Forbid), "checks": r.Checks, "writes": nonNil(r.Writes),
		"preview": r.Preview, "delegates_to": nonNil(r.DelegatesTo), "approve": r.Approve}
}

// body is a note's text below its frontmatter and its title.
func body(content []byte) string { return untitled(parser.BodyAfterFrontmatter(content)) }

// untitled is a note's body without its title, the # heading it starts
// with.
func untitled(b string) string {
	b = strings.TrimLeft(b, "\n")
	if strings.HasPrefix(b, "# ") {
		if i := strings.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		} else {
			b = ""
		}
	}
	return strings.TrimSpace(b)
}

// AgentPrompt is the system prompt of an agent note (IMP-195): its body
// below its title. link names it as a link or a path, in the project or
// relative to it.
func (env *Env) AgentPrompt(link string) (string, error) {
	p := fleet.LinkPath(link, env.Project)
	if p == "" {
		return "", errors.New("no agent note named")
	}
	if !strings.HasPrefix(p, env.Project+"/") {
		p = env.Project + "/" + p
	}
	nd, err := env.Engine.host.ReadNote(env.Project, p)
	if err != nil {
		return "", fmt.Errorf("the agent note %s: %w", p, err)
	}
	return untitled(nd.Body), nil
}

// report is the report a task closed with last ("" before it closed).
func report(content []byte) string {
	return clipHead(fleet.LastSection(content, "Report"), maxReport)
}

// taskCtx is a task with what its agent needs: its team, its role, the
// agent's prompt, the reports it builds on.
type taskCtx struct {
	task          fleet.Task
	team          fleet.Team
	role          fleet.Role
	system        string
	prompt        string
	text          string
	maxCost       float64
	paths, forbid []string
	deps, kids    []map[string]any
}

// loadTaskCtx reads the task a node names, with its team, role and agent,
// and writes its agent's prompt, extra at its end.
func loadTaskCtx(env *Env, with map[string]any, extra string) (*taskCtx, error) {
	fl, err := taskFleet(env)
	if err != nil {
		return nil, err
	}
	p, err := taskPath(env, with)
	if err != nil {
		return nil, err
	}
	t, content, err := fl.ReadTask(p)
	if err != nil {
		return nil, fmt.Errorf("the task %s: %w", p, err)
	}
	teams, err := fl.Teams(env.Project)
	if err != nil {
		return nil, err
	}
	c := &taskCtx{task: t}
	var ok bool
	if c.team, ok = teams[t.Team]; !ok {
		return nil, fmt.Errorf("the task's team %s is no team of the project, or its block does not read", t.Team)
	}
	if c.role, ok = c.team.Role(t.Assignee); !ok {
		return nil, fmt.Errorf("the task's role %s is no role of %s", t.Assignee, t.Team)
	}
	if c.role.Agent != "" {
		if c.system, err = env.AgentPrompt(c.role.Agent); err != nil {
			return nil, fmt.Errorf("the agent of role %s: %w", c.role.Name, err)
		}
	}
	tasks, err := fl.Tasks(env.Project)
	if err != nil {
		return nil, err
	}
	byPath := map[string]fleet.Task{}
	for _, x := range tasks {
		byPath[x.Path] = x
	}
	reportOf := func(x fleet.Task) string {
		_, rc, err := fl.ReadTask(x.Path)
		if err != nil {
			return ""
		}
		return report(rc)
	}
	for _, d := range t.DependsOn {
		if x, ok := byPath[d]; ok {
			c.deps = append(c.deps, map[string]any{"path": x.Path, "title": x.Title, "status": x.Status, "assignee": x.Assignee, "report": reportOf(x)})
		}
	}
	for _, x := range tasks {
		if x.Parent == t.Path {
			c.kids = append(c.kids, map[string]any{"path": x.Path, "title": x.Title, "status": x.Status, "assignee": x.Assignee,
				"cost_usd": x.CostUSD, "report": reportOf(x)})
		}
	}
	c.paths = append(slices.Clone(c.role.Paths), t.Paths...)
	c.forbid = append(slices.Clone(c.role.Forbid), t.Forbid...)
	c.maxCost = t.BudgetUSD
	if c.maxCost == 0 {
		c.maxCost = c.team.Budget.TaskUSD
	}
	c.text = body(content)
	c.prompt = c.writePrompt(extra)
	return c, nil
}

// reportsNote tells an agent how to read the reports of other tasks: the
// pipeline's facts first, then the agent's own words (IMP-195, the pilot).
const reportsNote = "Each report starts with what the pipeline itself found (the files, the notes written, the checks it ran, the preview, a person's answer, the merge): those are facts. The rest is the agent's own account; the agents run no checks themselves. A report's headings are two levels below the agent's (its # is ###): the task's note keeps them inside its section."

func (c *taskCtx) writePrompt(extra string) string {
	t, team, role := c.task, c.team, c.role
	var b strings.Builder
	fmt.Fprintf(&b, "You are the role %q of the team %s. Your task: %s\n\n", role.Name, strings.TrimSuffix(team.Path, ".md"), t.Title)
	if c.text != "" {
		b.WriteString(c.text + "\n\n")
	}
	if len(c.paths) > 0 {
		b.WriteString("Change only these paths of the repository: " + strings.Join(c.paths, ", ") + ".\n")
	}
	if len(c.forbid) > 0 {
		b.WriteString("Never change: " + strings.Join(c.forbid, ", ") + ".\n")
	}
	if len(role.Writes) > 0 {
		b.WriteString("You may write notes of the vault only in: " + strings.Join(role.Writes, ", ") + ".\n")
	}
	if len(c.deps) > 0 {
		b.WriteString("\nThe tasks this one builds on, and what they reported. " + reportsNote + "\n")
		for _, d := range c.deps {
			fmt.Fprintf(&b, "\n### %s (%s, %s)\n\n%s\n", d["title"], d["assignee"], d["status"], orNone(d["report"].(string)))
		}
	}
	if len(role.DelegatesTo) > 0 {
		b.WriteString("\nYou coordinate. The roles you give tasks to:\n")
		for _, name := range role.DelegatesTo {
			r, _ := team.Role(name)
			fmt.Fprintf(&b, "- %s (%s", r.Name, r.Run)
			if r.Repo != "" {
				fmt.Fprintf(&b, ", repository %s", r.Repo)
			}
			if r.Host != "" {
				fmt.Fprintf(&b, ", host %s", r.Host)
			}
			if r.Repo == "" && r.Host == "" {
				b.WriteString(", no repository nor host: it sees only the vault")
			}
			if len(r.Writes) > 0 {
				fmt.Fprintf(&b, ", writes %s", strings.Join(r.Writes, " "))
			} else if r.Repo == "" && r.Host == "" {
				b.WriteString(" and writes nothing")
			}
			if r.Checks != "" {
				fmt.Fprintf(&b, ", checks `%s` run by the pipeline", r.Checks)
			}
			if r.Approve == fleet.ApproveMerge {
				b.WriteString(", merged after a person approves")
			}
			b.WriteString(")\n")
		}
		b.WriteString("An agent with a repository edits its files and runs nothing: the pipeline runs the role's checks on its branch and merges after the approval. Ask an agent for neither.\n")
		b.WriteString("Give the work on a repository only to a role that has it: a role without one cannot open its files, only the reports about them.\n")
		if team.Split == fleet.SplitCraft {
			b.WriteString("Split the work along a chain of crafts, each task passing its result to the next.\n")
		} else {
			b.WriteString("Split the work by context: give each task a whole piece of it, with what it needs to know, rather than a step of a chain.\n")
		}
	}
	if len(c.kids) > 0 {
		b.WriteString("\nThe sub-tasks of this objective, and what they reported. " + reportsNote + "\n")
		for _, k := range c.kids {
			fmt.Fprintf(&b, "\n### %s (%s, %s)\n\n%s\n", k["title"], k["assignee"], k["status"], orNone(k["report"].(string)))
		}
	}
	if extra = strings.TrimSpace(extra); extra != "" {
		b.WriteString("\n" + extra + "\n")
	}
	return strings.TrimSpace(b.String())
}

// checksScript is what exec.run runs for the role's checks: in the
// repository's folder, and, with a preview, the build zipped for
// preview.publish. The checks run in a subshell of their own: under sh -e
// a failure on the left of && (npm test && npm run build) does not stop
// the script, the subshell's exit status does.
func (c *taskCtx) checksScript() string {
	if c.role.Checks == "" {
		return ""
	}
	s := "cd repo\n(\n" + c.role.Checks + "\n)\n"
	if c.role.Preview != "" {
		s += "python3 -m zipfile -c ../out/site.zip " + c.role.Preview + "/.\n"
	}
	return s
}

func (c *taskCtx) output() map[string]any {
	team, role := c.team, c.role
	return map[string]any{"task": taskOutput(c.task), "body": c.text, "team": map[string]any{"path": team.Path, "coordinator": team.Coordinator,
		"split": team.Split, "budget": map[string]any{"day_usd": team.Budget.DayUSD, "task_usd": team.Budget.TaskUSD},
		"max_agents": team.MaxAgents, "max_rounds": team.MaxRounds, "alert": nonNil(team.Alert), "approvers": nonNil(team.Approvers)},
		"role": roleOutput(role), "system": c.system, "prompt": c.prompt, "max_cost": c.maxCost,
		"paths": nonNil(c.paths), "forbid": nonNil(c.forbid), "depends_on": orEmpty(c.deps), "children": orEmpty(c.kids),
		"checks_script": c.checksScript(),
		// What a pipeline's when reads: expressions have no operators.
		"flags": map[string]any{"repo": role.Repo != "", "checks": role.Checks != "", "preview": role.Preview != "",
			"writes": len(role.Writes) > 0, "coordinator": role.Name == team.Coordinator, "delegates": len(role.DelegatesTo) > 0,
			"approve_merge": role.Approve == fleet.ApproveMerge, "approve_ops": role.Approve == fleet.ApproveOps,
			"approve_none": role.Approve == fleet.ApproveNone}}
}

func taskContext(_ context.Context, env *Env, with map[string]any) (any, error) {
	extra, _ := with["prompt"].(string)
	c, err := loadTaskCtx(env, with, extra)
	if err != nil {
		return nil, err
	}
	return c.output(), nil
}

func orNone(s string) string {
	if s == "" {
		return "(no report)"
	}
	return s
}

func orEmpty(xs []map[string]any) []map[string]any {
	if xs == nil {
		return []map[string]any{}
	}
	return xs
}

func taskUpdate(_ context.Context, env *Env, with map[string]any) (any, error) {
	fl, err := taskFleet(env)
	if err != nil {
		return nil, err
	}
	p, err := taskPath(env, with)
	if err != nil {
		return nil, err
	}
	status, _ := with["status"].(string)
	message, _ := with["message"].(string)
	at := env.Engine.now().In(clock.Location())
	t, err := fl.UpdateTask(p, func(cur fleet.Task, content []byte) ([]byte, error) {
		return fleet.Update(cur, content, "run-"+env.Run.ID, status, message, at)
	}, Actor(env.Run.Ref))
	if err != nil {
		return nil, err
	}
	return map[string]any{"task": taskOutput(t)}, nil
}

func taskComplete(_ context.Context, env *Env, with map[string]any) (any, error) {
	fl, err := taskFleet(env)
	if err != nil {
		return nil, err
	}
	p, err := taskPath(env, with)
	if err != nil {
		return nil, err
	}
	status, _ := with["status"].(string)
	if status == "" {
		status = fleet.Completed
	}
	text, _ := with["report"].(string)
	at := env.Engine.now().In(clock.Location())
	t, err := fl.UpdateTask(p, func(cur fleet.Task, content []byte) ([]byte, error) {
		return fleet.Complete(cur, content, "run-"+env.Run.ID, status, text, at)
	}, Actor(env.Run.Ref))
	if err != nil {
		return nil, err
	}
	return map[string]any{"task": taskOutput(t)}, nil
}
