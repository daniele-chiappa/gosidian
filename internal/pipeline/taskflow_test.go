package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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
	checks  float64          // exec.run's exit code
	execs   int              // exec.run's calls
	looks   int              // preview.publish's calls
	timeout bool             // exec.run runs out of time
	noSite  bool             // preview.publish fails
	execErr bool             // exec.run fails (the repository is not there)
	soft    []any            // exec.run's fail_on_error, call by call
	notify  []map[string]any
}

func (c *crew) Known(use string) bool {
	switch use {
	case "x.run", "x.pr", "exec.run", "preview.publish", "preview.check", "notify.ask":
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

func (c *crew) Type(use string) (Type, bool) {
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
			// An agent stopped at its limits fails with what it spent.
			if msg, ok := out["fail"].(string); ok {
				return nil, FailedWith{Output: out["spent"], Err: errors.New(msg)}
			}
			// The notes the agent writes go through the run's MCP token.
			for _, p := range toStrings(out["wrote"]) {
				env.Engine.RunWrote(env.Run.ID, p)
			}
			return maps.Clone(out), nil
		case "x.pr":
			c.prs = append(c.prs, with["action"].(string)+" "+with["url"].(string))
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
		case "notify.ask":
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
	"  web:\n    run: x.run\n    repo: sandbox\n    checks: npm run build\n    preview: dist\n    approve: none\n```\n"

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
	if asked := c.asked[0]; asked["access"] != "edit" || asked["mcp"] != "read" || asked["repo"].(map[string]any)["name"] != "sandbox" {
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
	if len(c.prs) != 2 || c.prs[1] != "close https://git/o/sandbox/pulls/9" {
		t.Fatalf("prs %v", c.prs)
	}
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
	if p := c.asked[0]["prompt"].(string); !strings.Contains(p, "checks `npm test` run by the pipeline") || !strings.Contains(p, "Ask an agent for neither.") {
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
