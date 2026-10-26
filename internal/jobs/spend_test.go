package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// CostOf reads the cost and usage.total_tokens of an agent's output
// (IMP-195), and 0 from anything else.
func TestCostOf(t *testing.T) {
	for _, c := range []struct {
		out    string
		cost   float64
		tokens int64
	}{
		{`{"cost": 0.125, "usage": {"total_tokens": 1500}}`, 0.125, 1500},
		{" \n{\"text\": \"done\", \"cost\": 1e-3, \"usage\": {\"input_tokens\": 3, \"total_tokens\": 7}}", 0.001, 7},
		{`{"cost": 0.5}`, 0.5, 0},
		{`{"usage": {"total_tokens": 10}}`, 0, 10},
		{`{"cost": 0, "usage": {"total_tokens": 0}}`, 0, 0},
		{`{"cost": -1, "usage": {"total_tokens": -5}}`, 0, 0},
		{`{"cost": "0.1", "usage": {"total_tokens": "10"}}`, 0, 0},
		{`{"cost": null, "usage": null}`, 0, 0},
		{`{"cost": true, "usage": 7}`, 0, 0},
		{`{"cost": [1], "usage": {"total_tokens": {"n": 1}}}`, 0, 0},
		{`{"cost": 1e400, "usage": {"total_tokens": 1e19}}`, 0, 0},
		{`{"cost": 2, "usage": {"total_tokens": 12.0}}`, 2, 12},
		{`{"COST": 2, "Usage": {"total_tokens": 3}}`, 0, 0},
		{`{"result": {"cost": 2, "usage": {"total_tokens": 3}}}`, 0, 0},
		{`[{"cost": 1}]`, 0, 0},
		{`"cost"`, 0, 0},
		{`{"cost": 1, `, 0, 0},
		{`{"text": "no spend"}`, 0, 0},
		{``, 0, 0},
	} {
		cost, tokens := CostOf([]byte(c.out))
		if cost != c.cost || tokens != c.tokens {
			t.Errorf("CostOf(%s) = %v, %d; want %v, %d", c.out, cost, tokens, c.cost, c.tokens)
		}
	}
}

// A run keeps its task and team (IMP-195), wherever runs are read.
func TestStore_RunTaskAndTeam(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	in := Run{Kind: "pipeline", Ref: "p/pipelines/dev.md", Project: "p", IdemKey: "k1",
		Task: "p/tasks/write-docs.md", Team: "p/teams/docs.md"}
	run, _, err := st.CreateRun(in, chain("a"))
	if err != nil {
		t.Fatal(err)
	}
	if run.Task != in.Task || run.Team != in.Team {
		t.Errorf("created %+v", run)
	}
	got, err := st.Run(run.ID)
	if err != nil || got.Task != in.Task || got.Team != in.Team {
		t.Errorf("Run: %+v %v", got, err)
	}
	if got, err := st.RunByIdem(in.Ref, "k1"); err != nil || got.Task != in.Task || got.Team != in.Team {
		t.Errorf("RunByIdem: %+v %v", got, err)
	}
	if list, err := st.Runs(RunFilter{Projects: []string{"p"}}); err != nil || len(list) != 1 || list[0].Task != in.Task || list[0].Team != in.Team {
		t.Errorf("Runs: %+v %v", list, err)
	}
	other, _, _ := st.CreateRun(Run{Kind: "pipeline", Ref: "p/pipelines/dev.md", Project: "p"}, chain("a"))
	if got, _ := st.Run(other.ID); got.Task != "" || got.Team != "" {
		t.Errorf("a run without a task: %+v", got)
	}
	b, _ := json.Marshal(got)
	if !strings.Contains(string(b), `"task":"p/tasks/write-docs.md"`) || !strings.Contains(string(b), `"team":"p/teams/docs.md"`) {
		t.Errorf("json %s", b)
	}
}

// A step that finishes with an agent's output keeps its cost and tokens
// (IMP-195); a retry's output and a step without one keep none.
func TestStore_AFinishedStepKeepsItsCost(t *testing.T) {
	out := json.RawMessage(`{"text": "ok", "cost": 0.25, "usage": {"total_tokens": 1200}}`)
	exec := &fakeExec{fns: map[string]func(context.Context, Step) Outcome{
		"agent": func(context.Context, Step) Outcome { return Outcome{Status: Done, Output: out} },
	}}
	r, _, _ := newRunner(t, "", exec)
	run, _, err := r.Submit(Run{Kind: "test", Ref: "p/x.md", Project: "p", Team: "p/teams/a.md"}, chain("agent", "plain"))
	if err != nil {
		t.Fatal(err)
	}
	waitRun(t, r, run.ID, Done)
	if s := stepByName(t, r, run.ID, "agent"); s.CostUSD != 0.25 || s.Tokens != 1200 {
		t.Errorf("agent step: cost %v, tokens %d", s.CostUSD, s.Tokens)
	}
	if s := stepByName(t, r, run.ID, "plain"); s.CostUSD != 0 || s.Tokens != 0 {
		t.Errorf("plain step: cost %v, tokens %d", s.CostUSD, s.Tokens)
	}

	st := r.Store()
	retry, _, err := st.CreateRun(Run{Kind: "other", Ref: "p/y.md", Project: "p"}, []Step{{Name: "a", Status: Queued}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.apply(retry.ID, []StepChange{{Name: "a", Status: Queued, Output: out, Queued: time.Now()}}, nil); err != nil {
		t.Fatal(err)
	}
	steps, _ := st.Steps(retry.ID)
	if steps[0].CostUSD != 0 || steps[0].Tokens != 0 {
		t.Errorf("a step not finished: cost %v, tokens %d", steps[0].CostUSD, steps[0].Tokens)
	}
	if err := st.apply(retry.ID, []StepChange{{Name: "a", Status: Failed, Error: "x", Finished: time.Now()}}, nil); err != nil {
		t.Fatal(err)
	}
	steps, _ = st.Steps(retry.ID)
	if steps[0].CostUSD != 0 || steps[0].Tokens != 0 {
		t.Errorf("a step finished without an output: cost %v, tokens %d", steps[0].CostUSD, steps[0].Tokens)
	}
}

// TeamSpend sums the steps of the team's runs in the project that
// finished from since on (IMP-195), and nothing of another team, another
// project, or before since.
func TestStore_TeamSpend(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	since := now.Add(-time.Hour)
	before := since.Add(-time.Hour)
	const a, b = "p/teams/a.md", "p/teams/b.md"
	spend := func(cost float64, tokens int) json.RawMessage {
		out, _ := json.Marshal(map[string]any{"cost": cost, "usage": map[string]any{"total_tokens": tokens}})
		return out
	}
	type step struct {
		name string
		out  json.RawMessage
		at   time.Time
	}
	add := func(project, team string, ended time.Time, steps ...step) {
		t.Helper()
		var list []Step
		var changes []StepChange
		for _, s := range steps {
			list = append(list, Step{Name: s.name, Status: Queued})
			changes = append(changes, StepChange{Name: s.name, Status: Done, Output: s.out, Finished: s.at})
		}
		run, _, err := st.CreateRun(Run{Kind: "test", Ref: "p/dev.md", Project: project, Team: team}, list)
		if err != nil {
			t.Fatal(err)
		}
		var rc *RunChange
		if !ended.IsZero() {
			rc = &RunChange{Status: Done, Finished: ended}
		}
		if err := st.apply(run.ID, changes, rc); err != nil {
			t.Fatal(err)
		}
	}
	add("p", a, now, step{"x", spend(1, 100), now}, step{"y", spend(0.5, 0), now}, step{"z", json.RawMessage(`"ok"`), now})
	add("p", b, now, step{"x", spend(2, 200), now})
	add("q", a, now, step{"x", spend(4, 400), now})
	add("p", a, before, step{"x", spend(8, 800), before})
	// Running across since: its step before since is out, the other in.
	add("p", a, time.Time{}, step{"x", spend(16, 1600), before}, step{"y", spend(32, 50), now})
	add("p", "", now, step{"x", spend(64, 6400), now})

	for _, c := range []struct {
		project, team string
		since         time.Time
		want          Spend
	}{
		{"p", a, since, Spend{CostUSD: 33.5, Tokens: 150, Steps: 3}},
		{"p", b, since, Spend{CostUSD: 2, Tokens: 200, Steps: 1}},
		{"q", a, since, Spend{CostUSD: 4, Tokens: 400, Steps: 1}},
		{"q", b, since, Spend{}},
		{"p", a, time.Time{}, Spend{CostUSD: 57.5, Tokens: 2550, Steps: 5}},
		{"p", "", since, Spend{}},
	} {
		got, err := st.TeamSpend(c.project, c.team, c.since)
		if err != nil || got != c.want {
			t.Errorf("TeamSpend(%s, %q, %v) = %+v, %v; want %+v", c.project, c.team, c.since, got, err, c.want)
		}
	}

	// The query reads the runs of the team through their index.
	rows, err := st.db.Query(`EXPLAIN QUERY PLAN SELECT COALESCE(SUM(s.cost_usd), 0), COALESCE(SUM(s.tokens), 0), COUNT(*)
		FROM runs r JOIN steps s ON s.run_id = r.id
		WHERE r.project = ? AND r.team = ? AND r.team != '' AND (r.finished = 0 OR r.finished >= ?)
		AND s.finished >= ? AND (s.cost_usd > 0 OR s.tokens > 0)`, "p", a, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if p := strings.Join(plan, "; "); !strings.Contains(p, "runs_team") {
		t.Errorf("plan %q does not use runs_team", p)
	}
}

// TestStore_OpensADatabaseWithoutTheTaskColumns: a jobs.db made before
// IMP-195 gets runs.task, runs.team, steps.cost_usd, steps.tokens and the
// index of the teams; its runs and steps read empty ones.
func TestStore_OpensADatabaseWithoutTheTaskColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"task", "team", "cost_usd", "tokens"} {
		if strings.Contains(schema, "\t"+col+" ") {
			t.Fatalf("the schema has %s: the test no longer makes an old database", col)
		}
	}
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO runs (id, kind, ref, project, status, created) VALUES ('r1', 'test', 'a.md', 'p', 'done', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO steps (run_id, name, pos, status, output, finished) VALUES ('r1', 'a', 0, 'done', '{"cost": 1}', 1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for i := 0; i < 2; i++ { // twice: the second open finds the columns
		st, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		run, err := st.Run("r1")
		if err != nil || run.Task != "" || run.Team != "" {
			t.Errorf("open %d: run %+v %v", i+1, run, err)
		}
		steps, err := st.Steps("r1")
		if err != nil || len(steps) != 1 || steps[0].CostUSD != 0 || steps[0].Tokens != 0 {
			t.Errorf("open %d: steps %+v %v", i+1, steps, err)
		}
		var n int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'runs_team'`).Scan(&n); err != nil || n != 1 {
			t.Errorf("open %d: runs_team %d %v", i+1, n, err)
		}
		if i == 0 {
			if _, _, err := st.CreateRun(Run{Kind: "test", Ref: "a.md", Project: "p", Task: "p/t.md", Team: "p/team.md"}, chain("a")); err != nil {
				t.Fatal(err)
			}
		} else if got, err := st.Runs(RunFilter{Projects: []string{"p"}}); err != nil || len(got) != 2 || got[0].Team != "p/team.md" {
			t.Errorf("open %d: runs %+v %v", i+1, got, err)
		}
		st.Close()
	}
}
