package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/fleet"
)

// The nodes of a team's pipelines (IMP-195 phase 1b): task.run runs the
// role's executor on the task; task.gate looks at what it did (the files
// it touched against the paths, the checks, the preview); task.verdict
// reads the gate and a person's answer into what follows; task.pr merges or
// closes the pull request through the executor's plugin, whose preset
// holds the credentials; task.plan writes the tasks a coordinator planned.

// planSchema is the answer task.run asks a coordinator for (plan: true).
const planSchema = `{"type": "object", "required": ["action"], "properties": {
	"action": {"type": "string", "enum": ["tasks", "wait", "done", "ask"]},
	"tasks": {"type": "array", "items": {"type": "object", "required": ["key", "assignee", "title", "body"], "properties": {
		"key": {"type": "string"}, "assignee": {"type": "string"}, "title": {"type": "string"}, "body": {"type": "string"},
		"depends_on": {"type": "array", "items": {"type": "string"}},
		"paths": {"type": "array", "items": {"type": "string"}},
		"budget_usd": {"type": "number"}}}},
	"summary": {"type": "string"},
	"question": {"type": "string"}}}`

// planInstructions end a coordinator's prompt with plan: true.
const planInstructions = `Answer with JSON, as the schema says:
- "action": "tasks", with "tasks", when work is left: each with a "key" of
  your own, its "assignee" (one of the roles you give tasks to), a "title",
  a "body" (what to do and how it is accepted, with what it needs to know),
  and "depends_on": the keys of tasks of this answer, or the paths of tasks
  there are, that must be completed first;
- "action": "wait", when the sub-tasks at work are enough and there is
  nothing to give yet (one failed, and the others will do its part): the
  objective waits for them;
- "action": "done", with a "summary", when the objective is reached;
- "action": "ask", with a "question", when a person must decide.
You do not do the work yourself.`

// executorType is the node type of a role's executor, or of another node of
// its plugin (pr).
func (env *Env) executorType(role fleet.Role, node string) (string, Type, error) {
	plugin, _, _ := strings.Cut(role.Run, ".")
	use := plugin + "." + node
	t, ok := env.Engine.types.get(use)
	if !ok {
		why := env.Engine.types.Why(use)
		if why == "" {
			why = "no such node type"
		}
		return use, nil, fmt.Errorf("the executor of role %s: %s: %s", role.Name, use, why)
	}
	return use, t, nil
}

func taskRun(ctx context.Context, env *Env, with map[string]any) (any, error) {
	extra, _ := with["prompt"].(string)
	plan, _ := with["plan"].(bool)
	if plan {
		extra = strings.TrimSpace(extra + "\n\n" + planInstructions)
	}
	c, err := loadTaskCtx(env, with, extra)
	if err != nil {
		return nil, err
	}
	if plan && len(c.role.DelegatesTo) == 0 {
		return nil, fmt.Errorf("with.plan: role %s gives tasks to none (delegates_to)", c.role.Name)
	}
	_, t, err := env.executorType(c.role, "run")
	if err != nil {
		return nil, err
	}
	args := map[string]any{"prompt": c.prompt}
	if c.system != "" {
		args["system"] = c.system
	}
	set := func(k string, v any, ok bool) {
		if ok {
			args[k] = v
		}
	}
	set("model", c.role.Model, c.role.Model != "")
	set("effort", c.role.Effort, c.role.Effort != "")
	set("max_turns", c.role.MaxTurns, c.role.MaxTurns > 0)
	set("max_seconds", c.role.MaxSeconds, c.role.MaxSeconds > 0)
	set("max_tokens", c.role.MaxTokens, c.role.MaxTokens > 0)
	set("max_cost", c.maxCost, c.maxCost > 0)
	// A role that writes in the vault gets a token for its folders; the
	// others read.
	args["mcp"] = "read"
	if len(c.role.Writes) > 0 {
		args["mcp"] = "write"
	}
	if c.role.Repo != "" {
		args["access"] = "edit"
		args["repo"] = map[string]any{"name": c.role.Repo, "pr": map[string]any{"title": c.task.Title,
			"body": fmt.Sprintf("Task %s of the team %s, run %s of %s.", c.task.Path, strings.TrimSuffix(c.team.Path, ".md"), env.Run.ID, env.Run.Ref)}}
	}
	if plan {
		var schema map[string]any
		if err := json.Unmarshal([]byte(planSchema), &schema); err != nil {
			return nil, err
		}
		args["schema"] = schema
	}
	// The node's own settings win, but for the task and its prompt.
	for k, v := range with {
		if k != "task" && k != "prompt" && k != "plan" {
			args[k] = v
		}
	}
	out, err := t.Run(ctx, env, args)
	if m, ok := out.(map[string]any); ok && err == nil {
		// What the pipeline does next, for its when: the role's checks and
		// preview run on a branch the agent pushed, never on one that is
		// not there (the gate then says it changed nothing).
		repo, _ := m["repo"].(map[string]any)
		pushed, _ := repo["pushed"].(bool)
		m["next"] = map[string]any{"checks": c.role.Checks != "" && pushed, "preview": c.role.Preview != "" && pushed}
	}
	return out, err
}

// matchPath says whether a file of the repository is under a path of a
// role or task: a folder ends with /, anything else is a file or a glob.
func matchPath(pattern, file string) bool {
	if strings.HasSuffix(pattern, "/") {
		return strings.HasPrefix(file, pattern)
	}
	if ok, _ := path.Match(pattern, file); ok {
		return true
	}
	return pattern == file
}

// Choice labels of an approval, as task.gate gives them and task.verdict
// reads them back.
var defaultChoices = map[string]string{"approve": "approve", "redo": "redo", "discard": "discard"}

func taskGate(_ context.Context, env *Env, with map[string]any) (any, error) {
	c, err := loadTaskCtx(env, with, "")
	if err != nil {
		return nil, err
	}
	agent, _ := with["agent"].(map[string]any)
	if agent == nil {
		return nil, errors.New("with.agent: the output of the agent's node ({{ agent }})")
	}
	repo, _ := agent["repo"].(map[string]any)
	var files, outside, forbidden []string
	if repo != nil {
		for _, f := range asList(repo["files"]) {
			if s, ok := f.(string); ok {
				files = append(files, s)
			}
		}
	}
	for _, f := range files {
		switch {
		case slices.ContainsFunc(c.forbid, func(p string) bool { return matchPath(p, f) }):
			forbidden = append(forbidden, f)
		case len(c.paths) > 0 && !slices.ContainsFunc(c.paths, func(p string) bool { return matchPath(p, f) }):
			outside = append(outside, f)
		}
	}
	var lines []string
	ok := true
	prURL, _ := repo["pr_url"].(string)
	branch, _ := repo["branch"].(string)
	if c.role.Repo != "" {
		switch {
		case repo == nil:
			ok = false
			lines = append(lines, "The agent gave no repository: nothing to look at.")
		case len(files) == 0:
			ok = false
			lines = append(lines, "The agent changed no file.")
		default:
			lines = append(lines, fmt.Sprintf("Files changed (%d): %s.", len(files), strings.Join(clipList(files, 20), ", ")))
		}
	}
	// The notes the run's MCP token wrote in the role's folders: a role
	// that writes in the vault and wrote nothing did not do its task (a
	// role with a repository is judged on its files).
	var notes []string
	if len(c.role.Writes) > 0 {
		for _, p := range env.Engine.RunWrites(env.Run.ID) {
			if slices.ContainsFunc(c.role.Writes, func(w string) bool { return p == w || strings.HasPrefix(p, strings.TrimSuffix(w, "/")+"/") }) {
				notes = append(notes, p)
			}
		}
		switch {
		case len(notes) > 0:
			lines = append(lines, fmt.Sprintf("Notes written (%d): %s.", len(notes), strings.Join(clipList(notes, 20), ", ")))
		case c.role.Repo == "":
			ok = false
			lines = append(lines, "The agent wrote no note in "+strings.Join(c.role.Writes, ", ")+".")
		}
	}
	if len(forbidden) > 0 {
		ok = false
		lines = append(lines, "Files it must not change: "+strings.Join(forbidden, ", ")+".")
	}
	if len(outside) > 0 {
		ok = false
		lines = append(lines, "Files outside its paths ("+strings.Join(c.paths, ", ")+"): "+strings.Join(outside, ", ")+".")
	}
	checksOK := true
	ch, _ := with["checks"].(map[string]any)
	next, _ := agent["next"].(map[string]any)
	if want, _ := next["checks"].(bool); want && ch == nil {
		// Its node failed (on_error: continue): the run's steps say why.
		checksOK, ok = false, false
		lines = append(lines, "The checks did not run: their node failed (the run's steps say why).")
	}
	if ch != nil {
		// exec.run with fail_on_error false: an exit code, or none after a
		// signal or the timeout, which is no pass either.
		code, hasCode := ch["exit_code"].(float64)
		what := "The checks"
		if c.role.Checks != "" {
			what += " (`" + c.role.Checks + "`)"
		}
		if !hasCode || code != 0 {
			checksOK, ok = false, false
			how := fmt.Sprintf("exit %d", int(code))
			if !hasCode {
				how = "no exit code"
				if to, _ := ch["timed_out"].(bool); to {
					how = "the time ran out"
				} else if sig, _ := ch["signal"].(string); sig != "" {
					how = "signal " + sig
				}
			}
			out, _ := ch["stderr"].(string)
			if strings.TrimSpace(out) == "" {
				out, _ = ch["stdout"].(string)
			}
			lines = append(lines, fmt.Sprintf("%s failed on the agent's branch (%s):\n%s", what, how, clipTail(strings.TrimSpace(out), 1500)))
		} else {
			lines = append(lines, what+" passed on the agent's branch.")
		}
	}
	image := ""
	pv, _ := with["preview"].(map[string]any)
	if pv == nil && c.role.Preview != "" && ch != nil && checksOK {
		// The checks passed and nothing was looked at: the preview's nodes
		// failed (they go on after an error) and the run's steps say why.
		ok = false
		lines = append(lines, "The preview was not published or not looked at: its nodes failed (the run's steps say why).")
	}
	if pv != nil {
		if good, _ := pv["ok"].(bool); good {
			lines = append(lines, "The preview opens.")
		} else {
			ok = false
			msg, _ := pv["error"].(string)
			lines = append(lines, "The preview does not open well: "+strings.TrimSpace(msg))
		}
		for _, f := range asList(pv["files"]) {
			if m, _ := f.(map[string]any); m != nil {
				if p, _ := m["path"].(string); p != "" && image == "" {
					image = p
				}
			}
		}
	}
	// What the pipeline found comes first, as its own: the agents run no
	// checks themselves, and a coordinator or a reviewer that reads only an
	// agent's words takes «I ran nothing» for checks never run.
	facts := "What the pipeline found, after the agent (it runs the checks itself; the agent does not):\n\n" + strings.Join(lines, "\n\n")
	if len(lines) == 0 {
		facts = "What the pipeline found, after the agent: nothing to check for this role."
	}
	report := facts
	if text, _ := agent["text"].(string); strings.TrimSpace(text) != "" {
		report += "\n\nThe agent's report:\n" + clipHead(strings.TrimSpace(text), 3000)
	}
	labels := maps.Clone(defaultChoices)
	if m, _ := with["choices"].(map[string]any); m != nil {
		for k, v := range m {
			if s, _ := v.(string); s != "" && labels[k] != "" {
				labels[k] = s
			}
		}
	}
	maxAttempts := env.Engine.cfg.Tasks.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	again := c.task.Attempts < maxAttempts
	choices := []string{labels["approve"], labels["discard"]}
	if again {
		choices = []string{labels["approve"], labels["redo"], labels["discard"]}
	}
	needs := c.role.Approve != fleet.ApproveNone
	text := report
	if prURL != "" {
		text += "\n\nPR: " + prURL
	}
	link := prURL
	if link == "" {
		link = c.task.Path
	}
	var img any
	if image != "" {
		img = image
	}
	return map[string]any{"ok": ok, "checks_ok": checksOK, "files": nonNil(files), "outside": nonNil(outside), "forbidden": nonNil(forbidden),
		"notes": nonNil(notes), "pr_url": prURL, "branch": branch, "image": img, "link": link, "text": text, "feedback": report,
		"title": fmt.Sprintf("%s: %s (attempt %d)", c.role.Name, c.task.Title, c.task.Attempts),
		"ask":   ok && needs, "close": ok && !needs, "redo": !ok && again, "fail": !ok && !again,
		"approve": c.role.Approve, "choices": choices, "labels": labels}, nil
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func clipList(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return append(slices.Clone(xs[:n]), fmt.Sprintf("… %d more", len(xs)-n))
}

func taskVerdict(_ context.Context, env *Env, with map[string]any) (any, error) {
	gate, _ := with["gate"].(map[string]any)
	if gate == nil {
		return nil, errors.New("with.gate: the output of task.gate ({{ gate }})")
	}
	flag := func(k string) bool { b, _ := gate[k].(bool); return b }
	text, _ := gate["text"].(string)
	feedback, _ := gate["feedback"].(string)
	prURL, _ := gate["pr_url"].(string)
	approve, _ := gate["approve"].(string)
	out := map[string]any{"merge": false, "close": false, "redo": false, "close_pr": false, "status": fleet.Completed,
		"choice": "", "by": "", "report": text, "feedback": feedback}
	switch {
	case flag("close"):
		out["close"] = true
	case flag("redo"):
		out["redo"] = true
	case flag("fail"):
		out["close"], out["status"] = true, fleet.Failed
	case flag("ask"):
		answer, _ := with["answer"].(map[string]any)
		if answer == nil {
			return nil, errors.New("with.answer: the person's answer ({{ answer }}), which the gate asks for")
		}
		choice, _ := answer["choice"].(string)
		by, _ := answer["by"].(string)
		comment, _ := answer["comment"].(string)
		out["choice"], out["by"] = choice, by
		labels, _ := gate["labels"].(map[string]any)
		which := ""
		for k, v := range labels {
			if v == choice {
				which = k
			}
		}
		note := fmt.Sprintf("%s: %s.", strings.TrimSpace("Answer of "+by), choice)
		if strings.TrimSpace(comment) != "" {
			note += " " + strings.TrimSpace(comment)
		}
		out["report"] = text + "\n\n" + note
		switch which {
		case "approve":
			out["close"] = true
			out["merge"] = approve == fleet.ApproveMerge && prURL != ""
			if out["merge"].(bool) {
				// The task closes after task.pr merged, or not at all: the
				// report says it as the pipeline's fact.
				out["report"] = text + "\n\n" + note + " The pipeline merged the pull request " + prURL + "."
			}
		case "redo":
			out["redo"] = true
			out["feedback"] = feedback + "\n\nA person asked for another attempt. " + note
		case "discard":
			out["close"], out["status"] = true, fleet.Canceled
			if prURL != "" {
				out["report"] = text + "\n\n" + note + " The pipeline closed the pull request " + prURL + " without merging it."
			}
		default:
			return nil, fmt.Errorf("the answer %q is none of the gate's choices", choice)
		}
	default:
		return nil, errors.New("the gate says neither close, redo, fail nor ask")
	}
	// A pull request that is not merged is closed: another attempt opens
	// its own.
	out["close_pr"] = prURL != "" && !out["merge"].(bool) && (out["redo"].(bool) || out["status"] != fleet.Completed)
	return out, nil
}

func taskPR(ctx context.Context, env *Env, with map[string]any) (any, error) {
	c, err := loadTaskCtx(env, with, "")
	if err != nil {
		return nil, err
	}
	action, _ := with["action"].(string)
	if action != "merge" && action != "close" {
		return nil, errors.New("with.action: merge or close")
	}
	url, _ := with["url"].(string)
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("with.url: the pull request ({{ gate.pr_url }})")
	}
	if c.role.Repo == "" {
		return nil, fmt.Errorf("role %s has no repo", c.role.Name)
	}
	_, t, err := env.executorType(c.role, "pr")
	if err != nil {
		return nil, err
	}
	args := map[string]any{"repo": c.role.Repo, "url": url, "action": action}
	for k, v := range with {
		if k != "task" && k != "url" && k != "action" {
			args[k] = v
		}
	}
	return t.Run(ctx, env, args)
}

func taskPlan(_ context.Context, env *Env, with map[string]any) (any, error) {
	fl, err := taskFleet(env)
	if err != nil {
		return nil, err
	}
	c, err := loadTaskCtx(env, with, "")
	if err != nil {
		return nil, err
	}
	if c.task.ClaimedBy != "run-"+env.Run.ID {
		return nil, fmt.Errorf("the task %s is held by %q, not by this run", c.task.Path, c.task.ClaimedBy)
	}
	if len(c.role.DelegatesTo) == 0 {
		return nil, fmt.Errorf("role %s gives tasks to none (delegates_to)", c.role.Name)
	}
	plan, _ := with["plan"].(map[string]any)
	if plan == nil {
		return nil, errors.New("with.plan: the coordinator's answer ({{ plan.json }})")
	}
	action, _ := plan["action"].(string)
	summary, _ := plan["summary"].(string)
	question, _ := plan["question"].(string)
	out := map[string]any{"action": action, "close": action == "done", "ask": action == "ask", "wait": action == "tasks" || action == "wait",
		"summary": strings.TrimSpace(summary), "question": strings.TrimSpace(question), "created": []map[string]any{}}
	switch action {
	case "done", "ask":
		return out, nil
	case "wait":
		// Nothing to give: the objective waits for its sub-tasks at work,
		// or goes to a person when none is (giveBack).
		return out, nil
	case "tasks":
	default:
		return nil, fmt.Errorf("plan.action %q: tasks, wait, done or ask", action)
	}
	items := asList(plan["tasks"])
	if len(items) == 0 {
		return nil, errors.New("plan.tasks: action tasks gives at least one")
	}
	tasks, err := fl.Tasks(env.Project)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	kids := 0
	for _, t := range tasks {
		known[t.Path] = true
		if t.Parent == c.task.Path {
			kids++
		}
	}
	maxTasks := env.Engine.cfg.Tasks.MaxTasks
	if maxTasks <= 0 {
		maxTasks = 50
	}
	if kids+len(items) > maxTasks {
		return nil, fmt.Errorf("the objective would have %d sub-tasks, [agents] max_tasks %d", kids+len(items), maxTasks)
	}
	// Check the whole plan before writing any of it.
	type item struct {
		key  string
		task fleet.NewTask
		deps []string
	}
	var plans []item
	keys := map[string]bool{}
	for i, x := range items {
		m, _ := x.(map[string]any)
		str := func(k string) string { s, _ := m[k].(string); return strings.TrimSpace(s) }
		it := item{key: str("key"), task: fleet.NewTask{Title: str("title"), Body: str("body"), Team: c.team.Path,
			Assignee: str("assignee"), Parent: c.task.Path}}
		if it.key == "" {
			it.key = strconv.Itoa(i + 1)
		}
		switch {
		case it.task.Title == "":
			return nil, fmt.Errorf("plan.tasks[%d]: no title", i)
		case !slices.Contains(c.role.DelegatesTo, it.task.Assignee):
			return nil, fmt.Errorf("plan.tasks[%d]: role %s does not give tasks to %q (delegates_to: %s)", i, c.role.Name, it.task.Assignee, strings.Join(c.role.DelegatesTo, ", "))
		case keys[it.key]:
			return nil, fmt.Errorf("plan.tasks[%d]: key %q twice", i, it.key)
		}
		for _, p := range asList(m["paths"]) {
			if s, _ := p.(string); s != "" {
				it.task.Paths = append(it.task.Paths, s)
			}
		}
		if b, _ := m["budget_usd"].(float64); b > 0 {
			it.task.BudgetUSD = b
		}
		for _, d := range asList(m["depends_on"]) {
			s, _ := d.(string)
			switch {
			case keys[s]:
				it.deps = append(it.deps, "key:"+s)
			case known[fleet.LinkPath(s, env.Project)]:
				it.deps = append(it.deps, fleet.LinkPath(s, env.Project))
			default:
				return nil, fmt.Errorf("plan.tasks[%d]: depends_on %q is neither a key before it nor a task of the project", i, s)
			}
		}
		keys[it.key] = true
		plans = append(plans, it)
	}
	paths := map[string]string{}
	var created []map[string]any
	for _, it := range plans {
		for _, d := range it.deps {
			if k, ok := strings.CutPrefix(d, "key:"); ok {
				d = paths[k]
			}
			it.task.DependsOn = append(it.task.DependsOn, d)
		}
		t, err := fl.CreateTask(env.Project, it.task, Actor(env.Run.Ref))
		if err != nil {
			return nil, fmt.Errorf("writing task %q: %w", it.key, err)
		}
		paths[it.key] = t.Path
		created = append(created, map[string]any{"key": it.key, "path": t.Path, "assignee": t.Assignee, "title": t.Title})
	}
	out["created"] = created
	env.Engine.KickTasks()
	return out, nil
}
