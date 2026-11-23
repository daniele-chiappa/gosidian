package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/jobs"
)

// crew is a plugin of agents and the plugins a team's pipelines use, all
// in memory: x.run answers as its next answer says, x.pr records the pull
// requests it merged or closed, exec.run passes or fails the checks,
// notify.ask asks nobody.
type crew struct {
	mu      sync.Mutex
	answers []map[string]any // x.run's answers, in turn
	asked   []map[string]any // what x.run was asked
	prs     []string         // "merge <url>", "close <url>"
	deletes []any            // x.pr's delete_branch, call by call
	old     bool             // x.run is of a plugin too old to resume a branch
	checks  float64          // exec.run's exit code
	execs   int              // exec.run's calls
	looks   int              // preview.publish's calls
	timeout bool             // exec.run runs out of time
	noSite  bool             // preview.publish fails
	execErr bool             // exec.run fails (the repository is not there)
	soft    []any            // exec.run's fail_on_error, call by call
	notify  []map[string]any
	most    *float64 // the most x.run's preset lets a step spend
}

func (c *crew) Known(use string) bool {
	switch use {
	case "x.run", "x.pr", "exec.run", "preview.publish", "preview.check", "notify.ask", "notify.send":
		return true
	}
	return false
}

func (c *crew) Contract(use string) string {
	if use == "x.run" {
		return AgentContract
	}
	return ""
}

// picky is an executor of a plugin too old for a setting.
type picky struct{ Type }

func (picky) Takes(...string) bool { return false }

// capped is an executor whose preset lets a step spend at most most.
type capped struct {
	Type
	most float64
}

func (c capped) Maximum(path ...string) (float64, bool) {
	return c.most, len(path) == 1 && path[0] == "max_cost"
}

func (c *crew) Type(use string) (Type, bool) {
	if use == "x.run" && c.most != nil {
		t, _ := c.typ(use)
		return capped{t, *c.most}, true
	}
	if use == "x.run" && c.old {
		t, _ := c.typ(use)
		return picky{t}, true
	}
	return c.typ(use)
}

func (c *crew) typ(use string) (Type, bool) {
	return TypeFunc(func(_ context.Context, env *Env, with map[string]any) (any, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		switch use {
		case "x.run":
			c.asked = append(c.asked, with)
			out := c.answers[0]
			if len(c.answers) > 1 {
				c.answers = c.answers[1:]
			}
			// An agent held by its provider's limit says so, with what it
			// spent before, when it did.
			if msg, ok := out["hold"].(string); ok {
				err := errors.New(msg)
				if out["spent"] != nil {
					err = FailedWith{Output: out["spent"], Err: err}
				}
				return nil, Hold{Until: time.Now().Add(300 * time.Millisecond), Err: err}
			}
			// The notes the agent writes go through the run's MCP token.
			for _, p := range toStrings(out["wrote"]) {
				env.Engine.RunWrote(env.Run.ID, p)
			}
			// An agent stopped at its limits fails with what it spent.
			if msg, ok := out["fail"].(string); ok {
				return nil, FailedWith{Output: out["spent"], Err: errors.New(msg)}
			}
			return maps.Clone(out), nil
		case "x.pr":
			c.prs = append(c.prs, with["action"].(string)+" "+with["url"].(string))
			c.deletes = append(c.deletes, with["delete_branch"])
			return map[string]any{"merged": with["action"] == "merge"}, nil
		case "exec.run":
			c.execs++
			c.soft = append(c.soft, with["fail_on_error"])
			if c.execErr {
				return nil, errors.New("repository sandbox has no branch agent/x")
			}
			if c.timeout {
				return map[string]any{"exit_code": nil, "timed_out": true, "stdout": "", "stderr": "still building", "files": []any{}}, nil
			}
			return map[string]any{"exit_code": c.checks, "stdout": "", "stderr": "1 test failed", "files": []any{map[string]any{"path": "p/attachments/site.zip"}}}, nil
		case "preview.publish":
			c.looks++
			if c.noSite {
				return nil, errors.New("the preview's server does not answer")
			}
			return map[string]any{"id": "pv1"}, nil
		case "preview.check":
			return map[string]any{"ok": true, "files": []any{map[string]any{"path": "p/attachments/shot.png"}}}, nil
		case "notify.ask", "notify.send":
			c.notify = append(c.notify, with)
			return map[string]any{"sent": 1}, nil
		}
		return map[string]any{}, nil
	}), true
}

const flowTeam = "---\ntitle: squadra\ntype: team\n---\n\n```team\ncoordinator: capo\nalert: [\"#lab\"]\napprovers: [alice]\nroles:\n" +
	"  capo:\n    run: x.run\n    delegates_to: [dev, doc]\n" +
	"  dev:\n    run: x.run\n    repo: sandbox\n    paths: [web/]\n    forbid: [web/plan.js]\n    checks: npm test\n" +
	"  doc:\n    run: x.run\n    max_tokens: 4000000\n    writes: [p/kb]\n" +
	"  web:\n    run: x.run\n    repo: sandbox\n    checks: npm run build\n    preview: dist\n    approve: none\n" +
	"  tester:\n    run: x.run\n    repo: sandbox\n    access: exec\n    checks: npm test\n    approve: none\n" +
	"  rev:\n    run: x.run\n" +
	"  reader:\n    run: x.run\n    repo: sandbox\n    access: read\n    review: true\n```\n"

func flowEngine(t *testing.T) (*Engine, *fakeHost, *crew) {
	e, host := newEngine(t, Config{Tasks: TaskConfig{MaxAttempts: 2}})
	c := &crew{}
	e.Types().Extend(c)
	host.notes["p/squadra.md"] = flowTeam
	host.notes["p/pipelines/squadra-work.md"] = string(fleet.WorkPipeline("p/squadra.md"))
	host.notes["p/pipelines/squadra-coordinate.md"] = string(fleet.CoordinatePipeline("p/squadra.md"))
	return e, host, c
}

func runOf(t *testing.T, task fleet.Task) string {
	t.Helper()
	id, ok := strings.CutPrefix(task.ClaimedBy, "run-")
	if !ok {
		t.Fatalf("held by %q", task.ClaimedBy)
	}
	return id
}

func TestTaskFlow_ARoleWithoutApprovalClosesItsTask(t *testing.T) {
	e, host, c := flowEngine(t)
	c.answers = []map[string]any{{"text": "Wrote the note.", "cost": 0.1, "wrote": []any{"p/kb/nota.md", "p/elsewhere.md"}}}
	host.notes["p/tasks/a.md"] = taskNote("nota", "doc", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	if a.CostUSD != 0.1 || !strings.Contains(host.note(a.Path), "The agent's report:\nWrote the note.") || !strings.Contains(host.note(a.Path), "Notes written (1): p/kb/nota.md.") {
		t.Fatalf("a: %+v\n%s", a, host.note(a.Path))
	}
	if c.asked[0]["mcp"] != "write" || c.asked[0]["repo"] != nil || c.asked[0]["max_tokens"] != 4000000 || !strings.Contains(c.asked[0]["prompt"].(string), "Your task: nota") {
		t.Fatalf("asked %v", c.asked[0])
	}
	if len(c.notify) != 0 || len(c.prs) != 0 {
		t.Fatalf("notify %v prs %v", c.notify, c.prs)
	}
}

func TestTaskFlow_APersonApprovesAndThePullRequestIsMerged(t *testing.T) {
	e, host, c := flowEngine(t)
	c.answers = []map[string]any{{"text": "Made the page.", "repo": map[string]any{"files": []any{"web/index.html"}, "branch": "agent/x", "pushed": true, "pr_url": "https://git/o/sandbox/pulls/7"}}}
	host.notes["p/tasks/a.md"] = taskNote("pagina", "dev", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Review }, "a in review")
	run := runOf(t, a)
	eventually(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.notify) == 1 }, "the question")
	q := c.notify[0]
	if q["title"] != "dev: pagina (attempt 1)" || strings.Join(toStrings(q["choices"]), ",") != "approve,redo,discard" || q["link"] != "https://git/o/sandbox/pulls/7" ||
		!strings.Contains(q["text"].(string), "The checks (`npm test`) passed on the agent's branch.") || q["image"] != nil {
		t.Fatalf("question %v", q)
	}
	if asked := c.asked[0]; asked["access"] != "edit" || asked["mcp"] != "read" || asked["repo"].(map[string]any)["name"] != "sandbox" ||
		!strings.Contains(asked["prompt"].(string), "The pipeline runs the role's checks on your branch after you: `npm test`. You cannot run them.") {
		t.Fatalf("asked %v", asked)
	}
	waitStatus(t, e, run, jobs.Waiting)
	if err := e.Runner().Resume(run, "", json.RawMessage(`{"choice":"approve","by":"alice"}`)); err != nil {
		t.Fatal(err)
	}
	a = waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	if strings.Join(c.prs, ";") != "merge https://git/o/sandbox/pulls/7" || !strings.Contains(host.note(a.Path), "Answer of alice: approve. The pipeline merged the pull request https://git/o/sandbox/pulls/7.") {
		t.Fatalf("prs %v\n%s", c.prs, host.note(a.Path))
	}
}

func TestTaskFlow_AForbiddenFileSendsTheTaskBack(t *testing.T) {
	e, host, c := flowEngine(t)
	c.answers = []map[string]any{{"text": "Made the page.", "repo": map[string]any{"files": []any{"web/index.html", "web/plan.js", "build.js"}, "branch": "agent/x", "pushed": true, "pr_url": "https://git/o/sandbox/pulls/8"}}}
	host.notes["p/tasks/a.md"] = taskNote("pagina", "dev", "submitted", "")
	// The first attempt goes back with what to fix, its pull request
	// closed; the second, the last, fails.
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Failed }, "a failed")
	note := host.note(a.Path)
	for _, want := range []string{"Files it must not change: web/plan.js.", "Files outside its paths (web/): build.js.", "— submitted"} {
		if !strings.Contains(note, want) {
			t.Fatalf("a lacks %q:\n%s", want, note)
		}
	}
	if a.Attempts != 2 || strings.Join(c.prs, ";") != "close https://git/o/sandbox/pulls/8;close https://git/o/sandbox/pulls/8" || len(c.notify) != 0 {
		t.Fatalf("a %+v prs %v notify %d", a, c.prs, len(c.notify))
	}
	if !strings.Contains(c.asked[1]["prompt"].(string), "Files it must not change: web/plan.js.") {
		t.Fatalf("the second attempt was not told what to fix: %s", c.asked[1]["prompt"])
	}
	// A file it must not change is on the branch: the second attempt starts
	// again from base, and the branch goes with its pull request.
	if repo := c.asked[1]["repo"].(map[string]any); repo["branch"] != nil || repo["resume"] != nil || c.deletes[0] != true || a.Branch != "" {
		t.Fatalf("second attempt's repo %v, deletes %v, branch %q", repo, c.deletes, a.Branch)
	}
}

func TestTaskFlow_ThePersonAsksForAnotherAttempt(t *testing.T) {
	e, host, c := flowEngine(t)
	c.answers = []map[string]any{{"text": "Made it.", "repo": map[string]any{"files": []any{"web/index.html"}, "branch": "agent/x", "pushed": true, "pr_url": "https://git/o/sandbox/pulls/9"}}}
	host.notes["p/tasks/a.md"] = taskNote("pagina", "dev", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Review }, "a in review")
	run := runOf(t, a)
	waitStatus(t, e, run, jobs.Waiting)
	if err := e.Runner().Resume(run, "", json.RawMessage(`{"choice":"redo","by":"alice","comment":"Bigger title."}`)); err != nil {
		t.Fatal(err)
	}
	a = waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Attempts == 2 && x.Status == fleet.Review }, "a's second attempt")
	if !strings.Contains(c.asked[1]["prompt"].(string), "A person asked for another attempt. Answer of alice: redo. Bigger title.") ||
		c.prs[0] != "close https://git/o/sandbox/pulls/9" {
		t.Fatalf("prompt %s prs %v", c.asked[1]["prompt"], c.prs)
	}
	// The second attempt continues the first one's branch, which closing
	// its pull request kept.
	if repo := c.asked[1]["repo"].(map[string]any); repo["branch"] != "agent/x" || repo["resume"] != true || c.deletes[0] != false || a.Branch != "agent/x" {
		t.Fatalf("second attempt's repo %v, deletes %v, branch %q", repo, c.deletes, a.Branch)
	}
	// The second time only approve and discard: no attempt is left.
	eventually(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.notify) == 2 }, "the second question")
	if got := strings.Join(toStrings(c.notify[1]["choices"]), ","); got != "approve,discard" {
		t.Fatalf("choices %s", got)
	}
	waitStatus(t, e, runOf(t, a), jobs.Waiting)
	if err := e.Runner().Resume(runOf(t, a), "", json.RawMessage(`{"choice":"discard","by":"alice"}`)); err != nil {
		t.Fatal(err)
	}
	a = waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Canceled }, "a discarded")
	if len(c.prs) != 2 || c.prs[1] != "close https://git/o/sandbox/pulls/9" || c.deletes[1] != true || a.Branch != "" {
		t.Fatalf("prs %v deletes %v branch %q", c.prs, c.deletes, a.Branch)
	}
}

func TestTaskFlow_ARoleThatRunsCommandsRunsItsChecksToo(t *testing.T) {
	e, host, c := flowEngine(t)
	c.answers = []map[string]any{{"text": "Ran npm test: green.", "repo": map[string]any{"files": []any{"test/a.test.js"}, "branch": "agent/old", "pushed": true, "resumed": true}}}
	host.notes["p/tasks/a.md"] = taskNote("prove", "tester", "submitted", "branch: agent/old\n")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	asked := c.asked[0]
	if repo := asked["repo"].(map[string]any); asked["access"] != "exec" || repo["branch"] != "agent/old" || repo["resume"] != true ||
		!strings.Contains(asked["prompt"].(string), "Run the role's checks in repo/ before you finish: `npm test`. Then delete the files they made that `git status` lists as untracked") {
		t.Fatalf("asked %v", asked)
	}
	if note := host.note(a.Path); !strings.Contains(note, "it runs the checks itself, whatever the agent ran") || a.Branch != "" {
		t.Fatalf("a %+v:\n%s", a, note)
	}
}

func TestTaskFlow_AnExecutorTooOldStartsAgainFromBase(t *testing.T) {
	e, host, c := flowEngine(t)
	c.old = true
	c.answers = []map[string]any{{"text": "Done.", "repo": map[string]any{"files": []any{"test/a.test.js"}, "branch": "agent/r-s", "pushed": true}}}
	host.notes["p/tasks/a.md"] = taskNote("prove", "tester", "submitted", "branch: agent/old\n")
	waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	if repo := c.asked[0]["repo"].(map[string]any); repo["branch"] != nil || repo["resume"] != nil {
		t.Fatalf("repo %v", repo)
	}
}

// A role that reads its repository gets a clone to read and pushes
// nothing: its review closes the task, no pull request, nobody asked. An
// attempt whose executor gave it no repository read nothing, and goes
// back.
func TestTaskFlow_ARoleThatReadsItsRepositoryClosesOnItsReport(t *testing.T) {
	e, host, c := flowEngine(t)
	review := "Read web/.\n\n- [minor] a label is in English\n\nVERDICT: passed"
	c.answers = []map[string]any{{"text": review},
		{"text": review, "repo": map[string]any{"name": "sandbox", "changed": 0, "files": []any{}, "pushed": false, "note": "nothing changed: no commit"}}}
	host.notes["p/tasks/a.md"] = taskNote("revisione", "reader", "submitted", "paths: [web/]\n")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	note := host.note(a.Path)
	if a.Attempts != 2 || !strings.Contains(note, "The executor gave no repository: the agent could not read sandbox.") ||
		!strings.Contains(note, "The review: passed, 0 serious and 1 minor problems left open.") || strings.Contains(note, "The agent changed no file.") {
		t.Fatalf("a %+v\n%s", a, note)
	}
	asked := c.asked[1]
	repo, _ := asked["repo"].(map[string]any)
	if asked["access"] != "read" || repo["name"] != "sandbox" || repo["push"] != false || repo["pr"] != nil || repo["branch"] != nil {
		t.Fatalf("asked %v", asked)
	}
	p := asked["prompt"].(string)
	if !strings.Contains(p, "The repository sandbox is there for you to read, as its base branch has it") ||
		!strings.Contains(p, "The paths of the repository this task is about: web/.") || strings.Contains(p, "Change only these paths") {
		t.Fatalf("prompt:\n%s", p)
	}
	if len(c.notify) != 0 || len(c.prs) != 0 {
		t.Fatalf("notify %v prs %v", c.notify, c.prs)
	}
}

// The coordinator knows which role reads a repository, and that it reads
// the work merged so far.
func TestTaskFlow_TheCoordinatorKnowsARoleThatReadsItsRepository(t *testing.T) {
	e, host, c := flowEngine(t)
	host.notes["p/squadra.md"] = strings.Replace(flowTeam, "delegates_to: [dev, doc]", "delegates_to: [dev, doc, reader]", 1)
	c.answers = []map[string]any{{"json": map[string]any{"action": "done", "summary": "Niente da fare."}}}
	host.notes["p/tasks/o.md"] = taskNote("obiettivo", "capo", "submitted", "")
	eventually(t, func() bool { e.TickTasks(); c.mu.Lock(); defer c.mu.Unlock(); return len(c.asked) > 0 }, "the coordinator's run")
	c.mu.Lock()
	p := c.asked[0]["prompt"].(string)
	c.mu.Unlock()
	for _, want := range []string{"- dev (x.run, repository sandbox, checks", "- reader (x.run, reads the repository sandbox and writes nothing, reviews:",
		"A role that reads a repository sees its base branch, with the work merged so far: make its task depend on the tasks whose work it reads."} {
		if !strings.Contains(p, want) {
			t.Fatalf("the prompt lacks %q:\n%s", want, p)
		}
	}
	waitTask(t, e, host, "p/tasks/o.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "o completed")
}

func TestTaskFlow_ARoleThatWritesNothingGoesBack(t *testing.T) {
	e, host, c := flowEngine(t)
	// The agent says it wrote, and wrote nowhere but outside its folder.
	c.answers = []map[string]any{{"text": "Ecco la nota nel rapporto.", "wrote": []any{"p/elsewhere.md"}}}
	host.notes["p/tasks/a.md"] = taskNote("nota", "doc", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Failed }, "a failed")
	if a.Attempts != 2 || !strings.Contains(host.note(a.Path), "The agent wrote no note in p/kb/.") {
		t.Fatalf("a %+v\n%s", a, host.note(a.Path))
	}
}

func TestTaskFlow_AnAgentStoppedAtItsLimitsCountsWhatItSpent(t *testing.T) {
	e, host, c := flowEngine(t)
	c.answers = []map[string]any{
		{"fail": "Claude Code stopped at max_tokens (3000000 tokens) after 28 turns, 3076168 tokens",
			"spent": map[string]any{"cost": 0.5, "usage": map[string]any{"total_tokens": 3076168}}},
		{"text": "Wrote the note.", "cost": 0.1, "wrote": []any{"p/kb/nota.md"}},
	}
	host.notes["p/tasks/a.md"] = taskNote("nota", "doc", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	if a.Attempts != 2 || a.CostUSD != 0.6 || a.Tokens != 3076168 || !strings.Contains(host.note(a.Path), "stopped at max_tokens") {
		t.Fatalf("a %+v\n%s", a, host.note(a.Path))
	}
	s, err := e.TeamSpend("p", "p/squadra.md")
	if err != nil || s.CostUSD != 0.6 || s.Tokens != 3076168 {
		t.Fatalf("the team's spend %+v %v", s, err)
	}
}

func TestTaskFlow_AnAgentThatPushedNothingRunsNoChecks(t *testing.T) {
	e, host, c := flowEngine(t)
	c.answers = []map[string]any{{"text": "Non posso eseguire nulla.", "repo": map[string]any{"files": []any{}, "branch": "agent/x", "pushed": false}}}
	host.notes["p/tasks/a.md"] = taskNote("pagina", "dev", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Failed }, "a failed")
	c.mu.Lock()
	execs := c.execs
	c.mu.Unlock()
	if execs != 0 || a.Attempts != 2 || !strings.Contains(host.note(a.Path), "The agent changed no file.") {
		t.Fatalf("execs %d a %+v\n%s", execs, a, host.note(a.Path))
	}
}

func TestTaskFlow_ChecksThatFailPublishNoPreview(t *testing.T) {
	e, host, c := flowEngine(t)
	c.checks = 1
	c.answers = []map[string]any{{"text": "Fatto.", "repo": map[string]any{"files": []any{"web/a.html"}, "branch": "agent/x", "pushed": true}}}
	host.notes["p/tasks/a.md"] = taskNote("sito", "web", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Failed }, "a failed")
	c.mu.Lock()
	execs, looks := c.execs, c.looks
	c.mu.Unlock()
	if execs != 2 || looks != 0 || !strings.Contains(host.note(a.Path), "The checks (`npm run build`) failed on the agent's branch (exit 1):\n1 test failed") {
		t.Fatalf("execs %d looks %d\n%s", execs, looks, host.note(a.Path))
	}
	if c.soft[0] != false {
		t.Fatalf("fail_on_error %v: the checks must give their exit code to the gate", c.soft[0])
	}
}

func TestTaskFlow_ChecksOutOfTimeDoNotPass(t *testing.T) {
	e, host, c := flowEngine(t)
	c.timeout = true
	c.answers = []map[string]any{{"text": "Fatto.", "repo": map[string]any{"files": []any{"web/a.html"}, "branch": "agent/x", "pushed": true}}}
	host.notes["p/tasks/a.md"] = taskNote("sito", "web", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Failed }, "a failed")
	if note := host.note(a.Path); !strings.Contains(note, "The checks (`npm run build`) failed on the agent's branch (the time ran out):\nstill building") {
		t.Fatal(note)
	}
}

func TestTaskFlow_APreviewNotPublishedGoesBack(t *testing.T) {
	e, host, c := flowEngine(t)
	c.noSite = true
	c.answers = []map[string]any{{"text": "Fatto.", "repo": map[string]any{"files": []any{"web/a.html"}, "branch": "agent/x", "pushed": true}}}
	host.notes["p/tasks/a.md"] = taskNote("sito", "web", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Failed }, "a failed")
	note := host.note(a.Path)
	if !strings.Contains(note, "The checks (`npm run build`) passed on the agent's branch.") || !strings.Contains(note, "The preview was not published or not looked at: its nodes failed") {
		t.Fatal(note)
	}
}

func TestTaskFlow_ChecksWhoseNodeFailsSendTheWorkBack(t *testing.T) {
	e, host, c := flowEngine(t)
	c.execErr = true
	c.answers = []map[string]any{{"text": "Fatto.", "repo": map[string]any{"files": []any{"web/a.html"}, "branch": "agent/x", "pushed": true, "pr_url": "https://git/o/sandbox/pulls/11"}}}
	host.notes["p/tasks/a.md"] = taskNote("sito", "web", "submitted", "")
	// The run reaches the gate, which gives the work back and closes the
	// pull request, as for checks that fail.
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Failed }, "a failed")
	if note := host.note(a.Path); !strings.Contains(note, "The checks did not run: their node failed") ||
		strings.Join(c.prs, ";") != "close https://git/o/sandbox/pulls/11;close https://git/o/sandbox/pulls/11" {
		t.Fatalf("prs %v\n%s", c.prs, note)
	}
}

func TestTaskFlow_ChecksThatPassPublishThePreview(t *testing.T) {
	e, host, c := flowEngine(t)
	c.answers = []map[string]any{{"text": "Fatto.", "repo": map[string]any{"files": []any{"web/a.html"}, "branch": "agent/x", "pushed": true}}}
	host.notes["p/tasks/a.md"] = taskNote("sito", "web", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	c.mu.Lock()
	looks := c.looks
	c.mu.Unlock()
	note := host.note(a.Path)
	if looks != 1 || !strings.Contains(note, "What the pipeline found, after the agent") || !strings.Contains(note, "The preview opens.") {
		t.Fatalf("looks %d\n%s", looks, note)
	}
}

func TestTaskFlow_TheCoordinatorPlansAndCloses(t *testing.T) {
	e, host, c := flowEngine(t)
	c.answers = []map[string]any{
		{"json": map[string]any{"action": "tasks", "tasks": []any{
			map[string]any{"key": "page", "assignee": "doc", "title": "La nota", "body": "Scrivi la nota."},
			map[string]any{"key": "check", "assignee": "doc", "title": "Il controllo", "body": "Controlla la nota.", "depends_on": []any{"page"}},
		}}},
		{"text": "Fatto.", "wrote": []any{"p/kb/nota.md"}},
		{"text": "Controllato.", "wrote": []any{"p/kb/nota.md"}},
		{"json": map[string]any{"action": "done", "summary": "Nota scritta e controllata."}},
	}
	host.notes["p/tasks/o.md"] = taskNote("obiettivo", "capo", "submitted", "")
	o := waitTask(t, e, host, "p/tasks/o.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "o completed")
	if o.Rounds != 1 || !strings.Contains(host.note(o.Path), "## Report\n\nNota scritta e controllata.") {
		t.Fatalf("o: %+v\n%s", o, host.note(o.Path))
	}
	if c.asked[0]["schema"] == nil || !strings.Contains(c.asked[0]["prompt"].(string), `"action": "tasks"`) {
		t.Fatalf("the coordinator was not asked for a plan: %v", c.asked[0])
	}
	// It knows what the pipeline runs, and reads the reports as facts and words.
	if p := c.asked[0]["prompt"].(string); !strings.Contains(p, "checks `npm test` run by the pipeline") || !strings.Contains(p, "ask an agent for neither.") ||
		!strings.Contains(p, "A task that checks the work of\n  others (a review) depends on every task whose work it checks") {
		t.Fatalf("first prompt: %s", p)
	}
	if p := c.asked[3]["prompt"].(string); !strings.Contains(p, reportsNote) || !strings.Contains(p, "Notes written (1): p/kb/nota.md.") {
		t.Fatalf("last prompt: %s", p)
	}
	tasks, _ := host.Tasks("p")
	var check fleet.Task
	for _, x := range tasks {
		if x.Title == "Il controllo" {
			check = x
		}
	}
	if check.Parent != "p/tasks/o.md" || len(check.DependsOn) != 1 || check.Status != fleet.Completed {
		t.Fatalf("check %+v", check)
	}
}

func TestTaskFlow_APlanOutsideTheDelegationIsRefused(t *testing.T) {
	e, host, c := flowEngine(t)
	c.answers = []map[string]any{{"json": map[string]any{"action": "tasks", "tasks": []any{
		map[string]any{"key": "a", "assignee": "capo", "title": "Me", "body": "x"},
	}}}}
	host.notes["p/tasks/o.md"] = taskNote("obiettivo", "capo", "submitted", "")
	o := waitTask(t, e, host, "p/tasks/o.md", func(x fleet.Task) bool { return x.Attempts == 2 }, "o's second attempt")
	if !strings.Contains(host.note(o.Path), `role capo does not give tasks to \"capo\"`) && !strings.Contains(host.note(o.Path), `does not give tasks to "capo"`) {
		t.Fatalf("o:\n%s", host.note(o.Path))
	}
	tasks, _ := host.Tasks("p")
	if len(tasks) != 1 {
		t.Fatalf("%d tasks", len(tasks))
	}
}

func TestTaskFlow_TheTeamsPipelinesRead(t *testing.T) {
	for _, src := range [][]byte{fleet.WorkPipeline("p/squadra.md"), fleet.CoordinatePipeline("p/squadra.md")} {
		block, err := FindBlock(src)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(block, nil); err != nil {
			t.Fatalf("%v\n%s", err, src)
		}
	}
}

func toStrings(v any) []string {
	var out []string
	switch l := v.(type) {
	case []any:
		for _, x := range l {
			s, _ := x.(string)
			out = append(out, s)
		}
	case []string:
		out = l
	}
	return out
}

func TestTaskFlow_AnApprovalThatNeverComesCallsAPerson(t *testing.T) {
	e, host, c := flowEngine(t)
	c.answers = []map[string]any{{"text": "Made it.", "repo": map[string]any{"files": []any{"web/index.html"}, "branch": "agent/x", "pr_url": "https://git/o/sandbox/pulls/10"}}}
	host.notes["p/tasks/a.md"] = taskNote("pagina", "dev", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Review }, "a in review")
	run := runOf(t, a)
	waitStatus(t, e, run, jobs.Waiting)
	// The wait expires: the run fails while the task is in review.
	if err := e.Runner().EndWait(run, "", jobs.Failed, nil, "no answer within 24h"); err != nil {
		t.Fatal(err)
	}
	a = waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.InputRequired }, "a to a person")
	if a.Attempts != 1 || !strings.Contains(host.note(a.Path), "while the task waited for a person's approval") {
		t.Fatalf("a: %+v\n%s", a, host.note(a.Path))
	}
}

// The checks' script fails when the checks fail, under sh -e as exec.run
// runs it: also on the left of an &&, which -e alone lets pass.
func TestChecksScript_FailsWhenTheChecksFail(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	for _, tc := range []struct {
		checks string
		ok     bool
	}{
		{"false && true", false},
		{"true && false", false},
		{"false\ntrue", false},
		{"true && mkdir -p dist && touch dist/index.html", true},
	} {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "work", "repo"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "work", "out"), 0o755); err != nil {
			t.Fatal(err)
		}
		c := &taskCtx{role: fleet.Role{Checks: tc.checks, Preview: "dist"}}
		// The zip succeeds without a dist as python3 -m zipfile does: only the
		// checks may fail the script.
		cmd := exec.Command("sh", "-e", "-c", strings.ReplaceAll(c.checksScript(), "python3 -m zipfile -c ../out/site.zip dist/.", "touch ../out/site.zip"))
		cmd.Dir = filepath.Join(dir, "work")
		err := cmd.Run()
		if (err == nil) != tc.ok {
			t.Fatalf("checks %q: err %v, want ok %v", tc.checks, err, tc.ok)
		}
	}
}

// guidedTeam gives the team a guide and the role doc an agent, a guide of
// its own and a skill (IMP-233).
func guidedTeam(host *fakeHost, skill string) {
	team := strings.Replace(flowTeam, "```team\n", "```team\nguide: regole\n", 1)
	team = strings.Replace(team, "  doc:\n    run: x.run\n", "  doc:\n    agent: agents/doc\n    run: x.run\n    guide: \"[[p/guide/scrivere]]\"\n    skills: [skills/fonti]\n", 1)
	host.put("p/squadra.md", team)
	host.put("p/agents/doc.md", "---\ntype: agent\n---\n\n# doc\n\nYou write notes.\n")
	host.put("p/regole.md", "---\ntitle: Regole\n---\n\n# Regole\n\nNever invent a source.\n")
	host.put("p/guide/scrivere.md", "# Scrivere\n\nShort sentences.\n")
	host.put("p/skills/fonti.md", skill)
}

// A role's agent receives, after its agent note, the team's guide, its own
// and its skills, whole; the coordinator knows a role's skills.
func TestTaskFlow_ARoleReceivesItsGuidesAndSkills(t *testing.T) {
	e, host, c := flowEngine(t)
	guidedTeam(host, "---\ntype: skill\ndescription: How to cite a source.\n---\n\n# Fonti\n\n## Steps\n\nCite the page.\n")
	c.answers = []map[string]any{{"text": "Wrote the note.", "wrote": []any{"p/kb/nota.md"}}}
	host.put("p/tasks/a.md", taskNote("nota", "doc", "submitted", ""))
	waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	c.mu.Lock()
	system, _ := c.asked[0]["system"].(string)
	c.mu.Unlock()
	want := "You write notes.\n\n# Guide: p/regole.md (p/regole.md)\n\nNever invent a source.\n\n" +
		"# Guide: p/guide/scrivere.md (p/guide/scrivere.md)\n\nShort sentences.\n\n" +
		"# Skill: p/skills/fonti.md (p/skills/fonti.md)\n\nHow to cite a source.\n\n## Steps\n\nCite the page."
	if system != want {
		t.Fatalf("system:\n%s\nwant:\n%s", system, want)
	}

	c.answers = []map[string]any{{"json": map[string]any{"action": "done", "summary": "Niente da fare."}}}
	host.put("p/tasks/o.md", taskNote("obiettivo", "capo", "submitted", ""))
	waitTask(t, e, host, "p/tasks/o.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "o completed")
	c.mu.Lock()
	p, _ := c.asked[1]["prompt"].(string)
	plan, _ := c.asked[1]["system"].(string)
	c.mu.Unlock()
	if !strings.Contains(p, "- doc (x.run, no repository nor host: it sees only the vault, writes p/kb/, skills fonti)") {
		t.Fatalf("the coordinator's prompt:\n%s", p)
	}
	// The team's guide reaches every role, the coordinator too.
	if plan != "# Guide: p/regole.md (p/regole.md)\n\nNever invent a source." {
		t.Fatalf("the coordinator's system: %q", plan)
	}
}

// A skill that is no note type: skill, and guides and skills over [agents]
// max_prompt_notes, stop the run before its agent: the task fails with why.
func TestTaskFlow_ASkillThatIsNoneOrTooLongStopsTheRun(t *testing.T) {
	for name, tc := range map[string]struct{ skill, why string }{
		"no skill": {"---\ntype: note\n---\n\n# Fonti\n\nCite.\n", "role doc: skill p/skills/fonti.md is no note type: skill"},
		"too long": {"---\ntype: skill\n---\n\n# Fonti\n\n" + strings.Repeat("Cite the page. ", 5000) + "\n", "over [agents] max_prompt_notes (65536)"},
		"missing":  {"", "role doc: skill p/skills/fonti.md is no note of the project"},
	} {
		t.Run(name, func(t *testing.T) {
			e, host, c := flowEngine(t)
			guidedTeam(host, tc.skill)
			if tc.skill == "" {
				host.mu.Lock()
				delete(host.notes, "p/skills/fonti.md")
				host.mu.Unlock()
			}
			c.answers = []map[string]any{{"text": "Wrote the note.", "wrote": []any{"p/kb/nota.md"}}}
			host.put("p/tasks/a.md", taskNote("nota", "doc", "submitted", ""))
			a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Failed }, "a failed")
			if note := host.note(a.Path); !strings.Contains(note, tc.why) {
				t.Fatalf("the task does not say %q:\n%s", tc.why, note)
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			if len(c.asked) != 0 {
				t.Fatalf("the agent ran: %v", c.asked)
			}
		})
	}
}

// A role's tools (IMP-233): the coordinator reads them, a run of the role
// has them for its tokens, and a role without the field has no limit.
func TestTaskFlow_ARolesToolsReachItsRunAndItsCoordinator(t *testing.T) {
	e, host, c := flowEngine(t)
	team := strings.Replace(flowTeam, "  doc:\n    run: x.run\n", "  doc:\n    run: x.run\n    tools: [vec-pg, layout_render]\n", 1)
	team = strings.Replace(team, "  dev:\n    run: x.run\n", "  dev:\n    run: x.run\n    tools: []\n", 1)
	host.put("p/squadra.md", team)
	c.answers = []map[string]any{{"json": map[string]any{"action": "done", "summary": "Niente da fare."}}}
	host.put("p/tasks/o.md", taskNote("obiettivo", "capo", "submitted", ""))
	waitTask(t, e, host, "p/tasks/o.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "o completed")
	c.mu.Lock()
	p, _ := c.asked[0]["prompt"].(string)
	c.mu.Unlock()
	for _, want := range []string{"- doc (x.run, no repository nor host: it sees only the vault, writes p/kb/, tools vec-pg, layout_render)",
		"- dev (x.run, repository sandbox, checks `npm test` run by the pipeline, merged after a person approves, no plugin's tools)"} {
		if !strings.Contains(p, want) {
			t.Fatalf("the coordinator's prompt lacks %q:\n%s", want, p)
		}
	}

	c.answers = []map[string]any{{"text": "Wrote the note.", "wrote": []any{"p/kb/nota.md"}}}
	host.put("p/tasks/a.md", taskNote("nota", "doc", "submitted", ""))
	waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	runs, err := e.Runner().Store().Runs(jobs.RunFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"p/tasks/a.md": "true [vec-pg layout_render]", "p/tasks/o.md": "false []"}
	for _, r := range runs {
		w, ok := want[r.Task]
		if !ok {
			continue
		}
		delete(want, r.Task)
		if tools, limited, err := e.RunTools(r.ID); err != nil || fmt.Sprint(limited, " ", tools) != w {
			t.Errorf("the tools of %s's run: %v %v %v, want %s", r.Task, limited, tools, err, w)
		}
	}
	if len(want) > 0 {
		t.Fatalf("no run of %v", want)
	}
}

// A role takes from its profile the fields it does not write (IMP-233):
// the executor, the model, the effort, the guide and the tools reach its
// run as the role's own.
func TestTaskFlow_ARoleTakesItsProfile(t *testing.T) {
	e, host, c := flowEngine(t)
	host.put("p/profiles/scrittore.md", "---\ntype: profile\n---\n\n```profile\nrun: x.run\nmodel: m-grande\neffort: high\nmax_tokens: 100\nguide: regole\ntools: [vec-pg]\n```\n")
	host.put("p/regole.md", "# Regole\n\nNever invent a source.\n")
	host.put("p/squadra.md", strings.Replace(flowTeam, "  doc:\n    run: x.run\n", "  doc:\n    profile: scrittore\n", 1))
	c.answers = []map[string]any{{"text": "Wrote the note.", "wrote": []any{"p/kb/nota.md"}}}
	host.put("p/tasks/a.md", taskNote("nota", "doc", "submitted", ""))
	waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	c.mu.Lock()
	asked := c.asked[0]
	c.mu.Unlock()
	// max_tokens is the role's own (4000000): the profile's 100 does not win.
	if asked["model"] != "m-grande" || asked["effort"] != "high" || asked["max_tokens"] != 4000000 || asked["mcp"] != "write" ||
		asked["system"] != "# Guide: p/regole.md (p/regole.md)\n\nNever invent a source." {
		t.Fatalf("asked %v", asked)
	}
	runs, err := e.Runner().Store().Runs(jobs.RunFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runs {
		if r.Task == "p/tasks/a.md" {
			if tools, limited, err := e.RunTools(r.ID); err != nil || !limited || fmt.Sprint(tools) != "[vec-pg]" {
				t.Fatalf("the profile's tools: %v %v %v", tools, limited, err)
			}
		}
	}
}

// roomTeam is a room of the team squadra (IMP-233): its coordinator lead
// splits what it receives for ui.
const roomTeam = "---\ntitle: front\ntype: team\n---\n\n```team\ncoordinator: lead\nbudget: {day_usd: 5}\nroles:\n" +
	"  lead:\n    run: x.run\n    delegates_to: [ui]\n" +
	"  ui:\n    run: x.run\n```\n"

// withRoom gives squadra the room front, which capo delegates to.
func withRoom(host *fakeHost, budget string) {
	team := strings.Replace(flowTeam, "```team\n", "```team\nrooms:\n  front: \"[[p/front]]\"\n"+budget, 1)
	host.put("p/squadra.md", strings.Replace(team, "delegates_to: [dev, doc]", "delegates_to: [dev, doc, front]", 1))
	host.put("p/front.md", roomTeam)
	host.put("p/pipelines/front-work.md", string(fleet.WorkPipeline("p/front.md")))
	host.put("p/pipelines/front-coordinate.md", string(fleet.CoordinatePipeline("p/front.md")))
}

// A coordinator gives an objective to a room: the room's coordinator
// receives it and splits it among its roles, and its summary comes back to
// the objective of the team, whose coordinator then closes it (IMP-233).
func TestTaskFlow_AnObjectiveGivenToARoom(t *testing.T) {
	e, host, c := flowEngine(t)
	withRoom(host, "")
	c.answers = []map[string]any{
		{"json": map[string]any{"action": "tasks", "tasks": []any{
			map[string]any{"key": "page", "assignee": "front", "title": "La pagina", "body": "Fai la pagina."},
		}}},
		{"json": map[string]any{"action": "tasks", "tasks": []any{
			map[string]any{"key": "button", "assignee": "ui", "title": "Il bottone", "body": "Fai il bottone."},
		}}},
		{"text": "Bottone fatto."},
		{"json": map[string]any{"action": "done", "summary": "Pagina fatta, col bottone."}},
		{"json": map[string]any{"action": "done", "summary": "Obiettivo raggiunto."}},
	}
	host.put("p/tasks/o.md", taskNote("obiettivo", "capo", "submitted", ""))
	o := waitTask(t, e, host, "p/tasks/o.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "o completed")
	if !strings.Contains(host.note(o.Path), "Obiettivo raggiunto.") {
		t.Fatalf("o:\n%s", host.note(o.Path))
	}
	tasks, _ := host.Tasks("p")
	by := map[string]fleet.Task{}
	for _, x := range tasks {
		by[x.Title] = x
	}
	page, button := by["La pagina"], by["Il bottone"]
	if page.Team != "p/front.md" || page.Assignee != "lead" || page.Parent != "p/tasks/o.md" || page.Status != fleet.Completed {
		t.Fatalf("the room's objective: %+v", page)
	}
	if button.Team != "p/front.md" || button.Assignee != "ui" || button.Parent != page.Path || button.Status != fleet.Completed {
		t.Fatalf("the room's task: %+v", button)
	}
	c.mu.Lock()
	first, _ := c.asked[0]["prompt"].(string)
	last, _ := c.asked[4]["prompt"].(string)
	c.mu.Unlock()
	if !strings.Contains(first, "- front (a room, the team p/front: give it a whole objective, which its coordinator splits among its roles ui; its summary comes back as the report)") {
		t.Fatalf("the coordinator's prompt:\n%s", first)
	}
	if !strings.Contains(last, "Pagina fatta, col bottone.") {
		t.Fatalf("the room's summary does not come back:\n%s", last)
	}
}

// A room spends of its team's budget: once the team and its rooms spent
// the team's day, the room's tasks wait too, and say why (IMP-233).
func TestTaskFlow_ARoomSpendsOfItsTeamsBudget(t *testing.T) {
	e, host, c := flowEngine(t)
	withRoom(host, "budget: {day_usd: 1}\n")
	host.put("p/pipelines/front-work.md", taskPipeline(`trigger: {on: task, team: "[[p/front]]", coordinator: false}`, "0.6"))
	c.answers = []map[string]any{{"text": "unused"}}
	host.put("p/tasks/a.md", strings.Replace(taskNote("uno", "ui", "submitted", ""), "p/squadra", "p/front", 1))
	waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	host.put("p/tasks/b.md", strings.Replace(taskNote("due", "ui", "submitted", ""), "p/squadra", "p/front", 1))
	waitTask(t, e, host, "p/tasks/b.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "b completed")
	if s, err := e.TeamSpend("p", "p/squadra.md"); err != nil || s.CostUSD < 1.19 {
		t.Fatalf("the team's spend counts its room's: %+v %v", s, err)
	}
	host.put("p/tasks/c.md", strings.Replace(taskNote("tre", "ui", "submitted", ""), "p/squadra", "p/front", 1))
	eventually(t, func() bool {
		e.TickTasks()
		return strings.HasPrefix(e.TaskWaits("p")["p/tasks/c.md"], "it is a room of p/squadra: the team spent its budget of the day")
	}, "c waits for the team's budget")
}
