package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/jobs"
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
		"replaces": {"type": "string"},
		"budget_usd": {"type": "number"}}}},
	"summary": {"type": "string"},
	"question": {"type": "string"}}}`

// planInstructions end a coordinator's prompt with plan: true.
const planInstructions = `Answer with JSON, as the schema says:
- "action": "tasks", with "tasks", when work is left: each with a "key" of
  your own, its "assignee" (one of the roles you give tasks to), a "title",
  a "body" (what to do and how it is accepted, with what it needs to know),
  and "depends_on": the keys of tasks of this answer, or the paths of tasks
  there are, that must be completed first. A task that checks the work of
  others (a review) depends on every task whose work it checks: it then
  sees their result, not what was there before them. A task given in the
  place of one that failed or was canceled names it in "replaces" (its
  path): the tasks that waited for that one then wait for this. Do not
  give again a task that is still open;
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
	// The task's budget, within what the executor's preset lets a step
	// spend: a budget above it would fail every attempt at the plugin's
	// door (a coordinator on a small model gave budgets of 10 to 100
	// USD), and a preset that lets a step spend nothing (a free
	// endpoint) takes no max_cost at all.
	maxCost := c.maxCost
	if most, ok := maximum(t, "max_cost"); ok && maxCost > most {
		maxCost = most
	}
	set("max_cost", maxCost, maxCost > 0)
	// A role that writes in the vault gets a token for its folders; the
	// others read.
	args["mcp"] = "read"
	if len(c.role.Writes) > 0 {
		args["mcp"] = "write"
	}
	if c.role.Repo != "" {
		args["access"] = fleet.AccessEdit
		repo := map[string]any{"name": c.role.Repo, "pr": map[string]any{"title": c.task.Title,
			"body": fmt.Sprintf("Task %s of the team %s, run %s of %s.", c.task.Path, strings.TrimSuffix(c.team.Path, ".md"), env.Run.ID, env.Run.Ref)}}
		// An attempt the gate or a person sent back left its work on a
		// branch: this one continues it, with an executor that can.
		if c.task.Branch != "" && takes(t, "repo", "resume") {
			repo["branch"], repo["resume"] = c.task.Branch, true
		}
		args["repo"] = repo
	}
	set("access", c.role.Access, c.role.Access != "")
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

// takes says whether a node type has the setting at path, for a type
// that says (a plugin's, from its accepted manifest); another takes what
// it takes.
// maximum is the most the executor's input schema lets the setting at
// path be, when it says one.
func maximum(t Type, path ...string) (float64, bool) {
	if s, ok := t.(interface {
		Maximum(path ...string) (float64, bool)
	}); ok {
		return s.Maximum(path...)
	}
	return 0, false
}

func takes(t Type, path ...string) bool {
	if s, ok := t.(interface{ Takes(path ...string) bool }); ok {
		return s.Takes(path...)
	}
	return true
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
	pushed, _ := repo["pushed"].(bool)
	resumed, _ := repo["resumed"].(bool)
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
		// An attempt that ended before (out of time, of tokens) may have
		// written them already: this one, which found them, wrote none.
		var before []string
		if len(notes) == 0 {
			for _, p := range env.Engine.TaskWrites(c.task.Path) {
				if slices.ContainsFunc(c.role.Writes, func(w string) bool { return p == w || strings.HasPrefix(p, strings.TrimSuffix(w, "/")+"/") }) {
					before = append(before, p)
				}
			}
		}
		switch {
		case len(notes) > 0:
			lines = append(lines, fmt.Sprintf("Notes written (%d): %s.", len(notes), strings.Join(clipList(notes, 20), ", ")))
		case len(before) > 0:
			notes = before
			lines = append(lines, fmt.Sprintf("Notes written by an attempt before this one (%d), none by this one: %s.", len(before), strings.Join(clipList(before, 20), ", ")))
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
		lines = append(lines, "The checks did not run: their node failed."+failedSteps(env))
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
		lines = append(lines, "The preview was not published or not looked at: its nodes failed."+failedSteps(env))
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
	// An answer cut at the model's output limit, or none at all: a role
	// judged on its words alone (no repository, no folder to write in)
	// did not do its task; for the others it is a fact of the report.
	answer, _ := agent["text"].(string)
	answer = strings.TrimSpace(answer)
	words := c.role.Review || c.role.Repo == "" && len(c.role.Writes) == 0 && len(c.role.DelegatesTo) == 0
	if stop, _ := agent["stop"].(string); stop == "length" {
		lines = append(lines, "The agent's answer was cut at its model's output limit (stop: length): it is not whole.")
		if words {
			ok = false
		}
	} else if answer == "" && words {
		ok = false
		lines = append(lines, "The agent gave no report: its answer is empty, and this role's work is its report.")
	}
	// A reviewer's verdict and problems, counted for the coordinator: a
	// review that gives no verdict line is not one.
	var review map[string]any
	if c.role.Review && answer != "" {
		rv := fleet.ReviewOf(answer)
		review = map[string]any{"verdict": rv.Verdict, "serious": rv.Serious, "minor": rv.Minor}
		switch {
		case rv.Verdict == "":
			ok = false
			lines = append(lines, "The review has no verdict line (`VERDICT: passed` or `VERDICT: to correct`): the pipeline cannot count it.")
		case rv.Verdict == "passed" && rv.Serious > 0:
			lines = append(lines, fmt.Sprintf("The review: it says passed, and lists %d serious and %d minor problems open: it counts as to correct.", rv.Serious, rv.Minor))
		default:
			lines = append(lines, fmt.Sprintf("The review: %s, %d serious and %d minor problems left open.", rv.Verdict, rv.Serious, rv.Minor))
		}
	}
	// What the pipeline found comes first, as its own: an agent may run no
	// checks (a coordinator or a reviewer that reads only its words takes
	// «I ran nothing» for checks never run), or run them on its side; the
	// pipeline's run is the one that counts.
	who := "it runs the checks itself; the agent does not"
	if c.role.Access == fleet.AccessExec {
		who = "it runs the checks itself, whatever the agent ran"
	}
	facts := "What the pipeline found, after the agent (" + who + "):\n\n" + strings.Join(lines, "\n\n")
	if len(lines) == 0 {
		facts = "What the pipeline found, after the agent: nothing to check for this role."
	}
	// The note keeps the whole report ([agents] max_report); a message to
	// a person or back to the agent its beginning.
	report, whole := facts, facts
	if answer != "" {
		report += "\n\nThe agent's report:\n" + clipHead(answer, 3000)
		if len(answer) > 3000 {
			report += "\n(The beginning: the whole report is in the task's note when it closes.)"
		}
		max := env.Engine.cfg.Tasks.maxReport()
		whole += "\n\nThe agent's report:\n" + clipHead(answer, max)
		if len(answer) > max {
			whole += fmt.Sprintf("\n(Cut at %d of %d bytes, [agents] max_report.)", max, len(answer))
		}
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
	text, prLine := report, ""
	if prURL != "" {
		prLine = "\n\nPR: " + prURL
		text += prLine
	}
	link := prURL
	if link == "" {
		link = c.task.Path
	}
	var img any
	if image != "" {
		img = image
	}
	// Another attempt continues the branch, which holds this one's work,
	// when its files were within the paths: one with a file it must not
	// change starts again from base, which the agent cannot restore.
	resume := c.role.Repo != "" && branch != "" && (pushed || resumed) && len(forbidden) == 0 && len(outside) == 0
	return map[string]any{"ok": ok, "checks_ok": checksOK, "files": nonNil(files), "outside": nonNil(outside), "forbidden": nonNil(forbidden),
		"notes": nonNil(notes), "pr_url": prURL, "branch": branch, "resume": resume, "image": img, "link": link, "text": text, "feedback": report, "report": whole + prLine, "review": review,
		"title": fmt.Sprintf("%s: %s (attempt %d)", c.role.Name, c.task.Title, c.task.Attempts),
		"ask":   ok && needs, "close": ok && !needs, "redo": !ok && again, "fail": !ok && !again,
		"approve": c.role.Approve, "choices": choices, "labels": labels}, nil
}

// failedSteps says why the nodes of the run that failed and let it go on
// (on_error: continue) failed: what the agent of the next attempt reads.
func failedSteps(env *Env) string {
	steps, err := env.Engine.runner.Store().Steps(env.Run.ID)
	if err != nil {
		return " The run's steps say why."
	}
	var out []string
	for _, st := range steps {
		if st.Status == jobs.Failed && strings.TrimSpace(st.Error) != "" {
			out = append(out, fmt.Sprintf("Step %s: %s", st.Name, clipHead(strings.TrimSpace(st.Error), 600)))
		}
	}
	if len(out) == 0 {
		return " The run's steps say why."
	}
	return "\n" + strings.Join(out, "\n")
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
	// The whole report closes the task; a gate of before 2.161 gives the
	// message only.
	text, _ := gate["report"].(string)
	if text == "" {
		text, _ = gate["text"].(string)
	}
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
	// its own, on the same branch when the gate says it may continue it
	// (branch, which task.update keeps on the task); otherwise the branch
	// goes with the pull request.
	out["close_pr"] = prURL != "" && !out["merge"].(bool) && (out["redo"].(bool) || out["status"] != fleet.Completed)
	out["branch"] = ""
	if resume, _ := gate["resume"].(bool); resume && out["redo"].(bool) {
		out["branch"], _ = gate["branch"].(string)
	}
	out["delete_branch"] = out["branch"] == ""
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
		// same is the open sub-task this one repeats, which stands for it;
		// replaces the failed or canceled one it takes the place of.
		same     string
		replaces string
	}
	byPath := map[string]fleet.Task{}
	var open []fleet.Task
	for _, t := range tasks {
		byPath[t.Path] = t
		if t.Parent == c.task.Path && !fleet.Ended(t.Status) {
			open = append(open, t)
		}
	}
	sort.Slice(open, func(i, j int) bool { return open[i].Path < open[j].Path })
	sameWork := func(role, title string) string {
		return role + "\x00" + strings.ToLower(strings.Join(strings.Fields(title), " "))
	}
	given := map[string]string{}
	for _, t := range open {
		given[sameWork(t.Assignee, t.Title)] = t.Path
	}
	var skipped, ignored []map[string]any
	var plans []item
	var lowered []map[string]any
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
			// A coordinator's agent gives no more than its team gives a
			// task: a person may, in the task's note.
			if most := c.team.Budget.TaskUSD; most > 0 && b > most {
				lowered = append(lowered, map[string]any{"key": it.key, "asked_usd": b, "budget_usd": most})
				b = most
			}
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
		// A task given twice (a small model repeats its plan) is one task:
		// the open one stands for the key.
		if p, ok := given[sameWork(it.task.Assignee, it.task.Title)]; ok {
			it.same = p
			skipped = append(skipped, map[string]any{"key": it.key, "title": it.task.Title, "same_as": strings.TrimPrefix(p, "key:")})
		} else {
			given[sameWork(it.task.Assignee, it.task.Title)] = "key:" + it.key
		}
		if r := str("replaces"); r != "" && it.same == "" {
			old, ok := byPath[fleet.LinkPath(r, env.Project)]
			switch {
			case !ok || old.Parent != c.task.Path:
				ignored = append(ignored, map[string]any{"key": it.key, "replaces": r, "why": "no sub-task of this objective"})
			case old.Status != fleet.Failed && old.Status != fleet.Canceled:
				ignored = append(ignored, map[string]any{"key": it.key, "replaces": r, "why": "it is " + old.Status + ", neither failed nor canceled"})
			default:
				it.replaces = old.Path
			}
		}
		keys[it.key] = true
		plans = append(plans, it)
	}
	// Two open tasks on the same repository whose paths may meet work one
	// after the other: side by side, each from the base, their pull
	// requests conflict (the pilot's site and its check). The later one
	// waits for the earlier, unless one already waits for the other.
	id := func(it item) string {
		if it.same != "" {
			return it.same
		}
		return "key:" + it.key
	}
	replaced := map[string]string{}
	for _, it := range plans {
		if it.replaces != "" {
			replaced[it.replaces] = id(it)
		}
	}
	depsOf := map[string][]string{}
	for _, t := range tasks {
		for _, d := range t.DependsOn {
			if n, ok := replaced[d]; ok && !fleet.Ended(t.Status) {
				d = n
			}
			depsOf[t.Path] = append(depsOf[t.Path], d)
		}
	}
	keyID := map[string]string{}
	for _, it := range plans {
		keyID["key:"+it.key] = id(it)
	}
	for _, it := range plans {
		if it.same != "" {
			continue
		}
		for _, d := range it.deps {
			if n, ok := keyID[d]; ok {
				d = n
			}
			depsOf[id(it)] = append(depsOf[id(it)], d)
		}
	}
	var reaches func(from, to string, seen map[string]bool) bool
	reaches = func(from, to string, seen map[string]bool) bool {
		if from == to {
			return true
		}
		if seen[from] {
			return false
		}
		seen[from] = true
		for _, d := range depsOf[from] {
			if reaches(d, to, seen) {
				return true
			}
		}
		return false
	}
	type work struct {
		id, repo string
		paths    []string
	}
	meet := func(a, b []string) bool {
		if len(a) == 0 || len(b) == 0 {
			return true
		}
		for _, x := range a {
			for _, y := range b {
				if matchPath(x, y) || matchPath(y, x) || strings.HasPrefix(x, strings.TrimSuffix(y, "/")+"/") || strings.HasPrefix(y, strings.TrimSuffix(x, "/")+"/") {
					return true
				}
			}
		}
		return false
	}
	var works []work
	for _, t := range open {
		if r, ok := c.team.Role(t.Assignee); ok && r.Repo != "" {
			works = append(works, work{t.Path, r.Repo, t.Paths})
		}
	}
	var ordered []map[string]any
	for i := range plans {
		it := &plans[i]
		r, ok := c.team.Role(it.task.Assignee)
		if it.same != "" || !ok || r.Repo == "" {
			continue
		}
		me := id(*it)
		for j := len(works) - 1; j >= 0; j-- {
			w := works[j]
			if w.repo != r.Repo || !meet(w.paths, it.task.Paths) || reaches(me, w.id, map[string]bool{}) || reaches(w.id, me, map[string]bool{}) {
				continue
			}
			it.deps = append(it.deps, w.id)
			depsOf[me] = append(depsOf[me], w.id)
			ordered = append(ordered, map[string]any{"key": it.key, "after": strings.TrimPrefix(w.id, "key:")})
		}
		works = append(works, work{me, r.Repo, it.task.Paths})
	}
	paths := map[string]string{}
	var created []map[string]any
	for _, it := range plans {
		if it.same != "" {
			paths[it.key] = strings.TrimPrefix(it.same, "key:")
			if k, ok := strings.CutPrefix(it.same, "key:"); ok {
				paths[it.key] = paths[k]
			}
			continue
		}
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
	// The tasks that waited for a task now replaced wait for the one in
	// its place.
	var repointed []map[string]any
	for _, it := range plans {
		if it.replaces == "" || it.same != "" {
			continue
		}
		for _, t := range tasks {
			if fleet.Ended(t.Status) || !slices.Contains(t.DependsOn, it.replaces) {
				continue
			}
			_, err := fl.UpdateTask(t.Path, func(cur fleet.Task, content []byte) ([]byte, error) {
				var deps []string
				for _, d := range cur.DependsOn {
					if d == it.replaces {
						d = paths[it.key]
					}
					if !slices.Contains(deps, fleet.Link(d)) {
						deps = append(deps, fleet.Link(d))
					}
				}
				return fleet.Set(content, map[string]any{"depends_on": deps})
			}, Actor(env.Run.Ref))
			if err != nil {
				return nil, fmt.Errorf("pointing %s to the task in the place of %s: %w", t.Path, it.replaces, err)
			}
			repointed = append(repointed, map[string]any{"task": t.Path, "from": it.replaces, "to": paths[it.key]})
		}
	}
	out["created"] = created
	for k, v := range map[string][]map[string]any{"skipped": skipped, "ignored": ignored, "ordered": ordered, "repointed": repointed} {
		if len(v) > 0 {
			out[k] = v
		}
	}
	if len(lowered) > 0 {
		out["lowered"] = lowered
	}
	env.Engine.KickTasks()
	return out, nil
}
