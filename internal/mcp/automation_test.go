package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/automation"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

const tasksWithAutomations = `---
title: Tasks
type: database
source: p/tasks
fields:
  title: {type: text}
  status: {type: select, options: [open, done]}
  due: {type: date}
automations:
  - name: Deadlines near
    due: due
    before: 3
    where:
      - status = open
    handoff: p-dev
  - name: Daily hot
    every: day 09:00
    snapshot: hot
tags: [p, type:index]
---

# Tasks
`

// automationServer is a test server with a database of tasks that declares
// automations, its rows dated around 2026-10-05, and an engine on a clock
// the test moves.
func automationServer(t *testing.T) (*Server, *automation.Engine, *time.Time, *audit.Log) {
	t.Helper()
	s, _, dir := newTestServer(t)
	al, err := audit.Open(filepath.Join(dir, "..", filepath.Base(dir)+"-audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetAuditLog(al)
	ctx := context.Background()
	for path, content := range map[string]string{
		"p/tasks.md":       tasksWithAutomations,
		"p/tasks/late.md":  "---\ntitle: Late\nstatus: open\ndue: 2026-10-01\ntags: [p]\n---\n",
		"p/tasks/today.md": "---\ntitle: Today\nstatus: open\ndue: 2026-10-05\ntags: [p]\n---\n",
		"p/tasks/soon.md":  "---\ntitle: Soon\nstatus: open\ndue: 2026-10-08\ntags: [p]\n---\n",
		"p/tasks/later.md": "---\ntitle: Later\nstatus: open\ndue: 2026-11-04\ntags: [p]\n---\n",
		"p/tasks/done.md":  "---\ntitle: Done\nstatus: done\ndue: 2026-10-05\ntags: [p]\n---\n",
		"p/tasks/none.md":  "---\ntitle: No date\nstatus: open\ntags: [p]\n---\n",
		"p/hot.md":         "---\ntitle: Hot\ntags: [p, type:index]\n---\n\n# Hot\n\nOpen: `=count(p/tasks where status = open)`.\n",
		"q/tasks.md":       strings.ReplaceAll(tasksWithAutomations, "p/", "q/"),
		"q/tasks/x.md":     "---\ntitle: X\nstatus: open\ndue: 2026-10-05\ntags: [q]\n---\n",
	} {
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": path, "content": content})); err != nil || res.IsError {
			t.Fatalf("create %s: %v %+v", path, err, res)
		}
	}
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	e, err := automation.New(s.AutomationHost(), automation.NewStore(""), time.UTC, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.SetClock(func() time.Time { return now })
	s.SetAutomations(e)
	return s, e, &now, al
}

// A due rule hands its rows off to the agent, once, as the automation and
// inside its project; an every rule snapshots its note at its slot.
func TestAutomations_Run(t *testing.T) {
	s, e, now, al := automationServer(t)
	runs := e.Tick("p")
	if len(runs) != 1 || runs[0].Rule != "Deadlines near" || len(runs[0].Rows) != 3 {
		t.Fatalf("runs = %+v", runs)
	}
	h, err := s.vault.Load(runs[0].Path)
	if err != nil || !strings.HasPrefix(runs[0].Path, "p/handoffs/") {
		t.Fatalf("handoff %s: %v", runs[0].Path, err)
	}
	c := string(h.Content)
	for _, want := range []string{"from_agent: automation\n", "to_agent: p-dev\n", "created_by: automation\n", "status: pending\n",
		"[[p/tasks/late|Late]] — due 2026-10-01 (4 days late)", "[[p/tasks/today|Today]] — due 2026-10-05 (today)", "[[p/tasks/soon|Soon]] — due 2026-10-08 (in 3 days)"} {
		if !strings.Contains(c, want) {
			t.Errorf("handoff lacks %q:\n%s", want, c)
		}
	}
	for _, not := range []string{"Later", "Done", "No date", "q/tasks"} {
		if strings.Contains(c, not) {
			t.Errorf("handoff has %q:\n%s", not, c)
		}
	}
	if runs := e.Tick("p"); len(runs) != 0 {
		t.Errorf("once: %+v", runs)
	}
	var entries []audit.Entry
	_ = al.Each(func(en audit.Entry) {
		if en.Source == audit.SourceAutomation {
			entries = append(entries, en)
		}
	})
	if len(entries) != 1 || entries[0].Path != runs[0].Path || entries[0].Actor != "automation" {
		t.Errorf("audit = %+v", entries)
	}

	// The next day after 09:00: the snapshot of p/hot.md.
	*now = time.Date(2026, 10, 6, 9, 5, 0, 0, time.UTC)
	runs = e.Tick("p")
	if len(runs) != 1 || runs[0].Path != "p/hot.snapshots/2026-10-06.md" {
		t.Fatalf("snapshot runs = %+v", runs)
	}
	snap, err := s.vault.Load(runs[0].Path)
	if err != nil || !strings.Contains(string(snap.Content), "Open: 5 (`` `=count(p/tasks where status = open)` ``).") {
		t.Errorf("snapshot: %v\n%s", err, snap.Content)
	}
}

// The bootstrap lists the project's pending handoffs, the automation's
// among them.
func TestAutomations_Bootstrap(t *testing.T) {
	s, e, _, _ := automationServer(t)
	e.Tick("p")
	res, _ := s.handleBootstrap(context.Background(), call(map[string]any{"project": "p"}))
	var out struct {
		Pending *bootstrapPendingHandoffs `json:"pending_handoffs"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	if out.Pending == nil || out.Pending.Count != 1 || out.Pending.Handoffs[0].FromAgent != "automation" ||
		!strings.Contains(out.Pending.Handoffs[0].Summary, `Automation "Deadlines near"`) {
		t.Errorf("pending_handoffs = %+v", out.Pending)
	}
	res, _ = s.handleBootstrap(context.Background(), call(map[string]any{"project": "q"}))
	out.Pending = nil
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil || out.Pending != nil {
		t.Errorf("q has no handoff yet: %+v %v", out.Pending, err)
	}
}

// memory_automations: the dry run at a date writes nothing; run acts now.
func TestAutomations_Tool(t *testing.T) {
	s, _, _, _ := automationServer(t)
	ctx := context.Background()
	var out struct {
		DryRun bool            `json:"dry_run"`
		Plan   automation.Plan `json:"plan"`
		Ran    []automation.Run
	}
	res, _ := s.handleAutomations(ctx, call(map[string]any{"project": "p", "as_of": "2026-11-01"}))
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	if !out.DryRun || len(out.Plan.Rules) != 2 || !out.Plan.Rules[0].Fires || len(out.Plan.Rules[0].Rows) != 4 {
		t.Fatalf("dry run = %+v", out.Plan)
	}
	if notes, _ := s.index.NotesByPrefix("p/handoffs"); len(notes) != 0 {
		t.Errorf("the dry run wrote %d notes", len(notes))
	}
	res, _ = s.handleAutomations(ctx, call(map[string]any{"project": "p"}))
	_ = json.Unmarshal([]byte(resultText(t, res)), &out)
	if p := out.Plan.Rules[0]; len(p.Rows) != 3 || p.Next != "2026-11-01" || len(p.Upcoming) != 1 {
		t.Errorf("now = %+v", p)
	}
	res, _ = s.handleAutomations(ctx, call(map[string]any{"project": "p", "run": true}))
	out.Ran = nil
	_ = json.Unmarshal([]byte(resultText(t, res)), &out)
	if len(out.Ran) != 1 || len(out.Plan.Rules[0].Fired) != 3 {
		t.Errorf("run = %+v", out)
	}
	for args, want := range map[string]string{`{"project":"p","as_of":"soon"}`: "is not a date", `{"project":"p","run":true,"as_of":"2026-11-01"}`: "as_of is for the dry run"} {
		var a map[string]any
		_ = json.Unmarshal([]byte(args), &a)
		res, _ := s.handleAutomations(ctx, call(a))
		if !res.IsError || !strings.Contains(res.Content[0].(mcplib.TextContent).Text, want) {
			t.Errorf("%s: %+v", args, res)
		}
	}
}

// A rule that would not run is said by lint and by the dry run.
func TestAutomations_Invalid(t *testing.T) {
	s, _, _, _ := automationServer(t)
	ctx := context.Background()
	bad := strings.Replace(tasksWithAutomations, "    due: due\n", "    due: title\n", 1)
	bad = strings.Replace(bad, "snapshot: hot", "snapshot: nope", 1)
	bad = strings.Replace(bad, "tags: [p, type:index]", "  - name: Broken\n    every: someday\n    handoff: x\ntags: [p, type:index]", 1)
	if res, err := s.handleUpdate(ctx, call(map[string]any{"path": "p/tasks.md", "content": bad})); err != nil || res.IsError {
		t.Fatalf("update: %v %+v", err, res)
	}
	res, _ := s.handleLint(ctx, call(map[string]any{"project": "p", "rules": []any{"database-field-invalid"}}))
	text := resultText(t, res)
	for _, want := range []string{`rule \"Deadlines near\": due \"title\" is not a date field`, `the note to snapshot, p/nope.md, does not exist`, `rule \"Broken\": every: \"someday\" is not a day of the week`} {
		if !strings.Contains(text, want) {
			t.Errorf("lint lacks %q:\n%s", want, text)
		}
	}
	res, _ = s.handleAutomations(ctx, call(map[string]any{"project": "p"}))
	text = resultText(t, res)
	if !strings.Contains(text, `is not a date field of the database`) || !strings.Contains(text, `is not a day of the week`) {
		t.Errorf("dry run:\n%s", text)
	}
}

// A rule reads every row of its database, a page at a time, not the first
// 500 (IMP-161, S3-8).
func TestAutomations_RowsPastOnePage(t *testing.T) {
	s, _, _, _ := automationServer(t)
	for k := range 520 {
		rel := fmt.Sprintf("p/tasks/bulk-%03d.md", k)
		if err := s.writeAndIndex(rel, []byte("---\ntitle: Bulk\nstatus: open\ndue: 2026-12-01\ntags: [p]\n---\n")); err != nil {
			t.Fatal(err)
		}
	}
	host := s.AutomationHost()
	dbs, err := host.Databases("p")
	if err != nil || len(dbs) != 1 {
		t.Fatalf("databases: %v %+v", err, dbs)
	}
	var rule automation.Rule
	for _, r := range dbs[0].Rules {
		if r.Name == "Deadlines near" {
			rule = r
		}
	}
	rows, err := host.Rows(dbs[0], rule, time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC))
	if err != nil || len(rows) != 524 {
		t.Fatalf("rows = %d, %v; want the 520 bulk rows and the 4 open dated ones", len(rows), err)
	}
}

// A date alone is 23:59 of that day on the clock, on the days the clocks
// change too (IMP-161, S3-15).
func TestParseAsOf_DayEndAcrossDST(t *testing.T) {
	rome, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Skip("no tzdata")
	}
	for _, day := range []string{"2026-03-29", "2026-10-25", "2026-10-05"} {
		got, err := parseAsOf(day, rome)
		if err != nil || got.Format("2006-01-02 15:04") != day+" 23:59" {
			t.Errorf("parseAsOf(%s) = %s, %v", day, got, err)
		}
	}
}
