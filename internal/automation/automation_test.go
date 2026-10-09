package automation

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const demoDatabase = `title: Tasks
type: database
source: demo/tasks
automations:
  - name: Deadlines near
    due: due
    before: 3
    where:
      - status in [open, in-progress]
    handoff: demo-dev
    message: Look at these.
  - name: Monday backlog
    every: monday 09:00
    snapshot: hot
  - name: Weekly review
    every: lunedì 10:30
    handoff: demo-dev
    message: Review the backlog.
`

func TestParse(t *testing.T) {
	rules, errs := Parse("demo/tasks.md", demoDatabase)
	if len(errs) != 0 || len(rules) != 3 {
		t.Fatalf("rules = %+v, errs = %v", rules, errs)
	}
	if r := rules[0]; r.Due != "due" || r.Before != 3 || r.Handoff != "demo-dev" || len(r.Where) != 1 || r.Trigger() != "due - 3 days" {
		t.Errorf("due rule = %+v", r)
	}
	if r := rules[1]; r.Snapshot != "demo/hot.md" || r.Every.String() != "monday 09:00" || r.Action() != "snapshot of demo/hot.md" {
		t.Errorf("every rule = %+v", r)
	}
	if r := rules[2]; r.Every.Weekday != time.Monday || r.Every.Hour != 10 || r.Every.Minute != 30 {
		t.Errorf("Italian day = %+v", r.Every)
	}
	if _, errs := Parse("demo/tasks.md", "title: x\ntype: database\n"); len(errs) != 1 || !errors.Is(errs[0], ErrNone) {
		t.Errorf("no automations: %v", errs)
	}
}

// One mistake leaves the other rules running, and says what is wrong.
func TestParse_Errors(t *testing.T) {
	cases := map[string]string{
		"- {due: due, handoff: a}":                            "name is required",
		"- {name: a, handoff: x}":                             "say when",
		"- {name: a, due: due, every: day, handoff: x}":       "one or the other",
		"- {name: a, due: due}":                               "say what",
		"- {name: a, due: due, snapshot: hot}":                "a due rule hands off",
		"- {name: a, every: someday, handoff: x}":             "not a day of the week",
		"- {name: a, every: monday 25:00, handoff: x}":        "not a time of day",
		"- {name: a, due: due, before: -1, handoff: x}":       "want 0 to",
		"- {name: a, due: due, before: soon, handoff: x}":     "not a number of days",
		"- {name: a, due: due, handoff: 'two words'}":         "not an agent's slug",
		"- {name: a, every: day, snapshot: ../x/hot}":         "not a markdown note",
		"- {name: a, every: day, where: [x = 1], handoff: x}": "where goes with due",
		"- {name: a, due: due, handoff: x, when: now}":        `unknown key "when"`,
	}
	for rule, want := range cases {
		rules, errs := Parse("demo/tasks.md", "type: database\nautomations:\n  "+rule+"\n  - {name: ok, every: day, handoff: x}\n")
		if len(rules) != 1 || rules[0].Name != "ok" || len(errs) != 1 || !strings.Contains(errs[0].Error(), want) {
			t.Errorf("%s: rules %+v, errs %v, want %q", rule, rules, errs, want)
		}
	}
	_, errs := Parse("demo/tasks.md", "type: database\nautomations:\n  - {name: a, every: day, handoff: x}\n  - {name: a, every: day, handoff: y}\n")
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "taken by another rule") {
		t.Errorf("duplicate names: %v", errs)
	}
}

func TestSchedule(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	s, _ := ParseSchedule("monday 09:00")
	at := func(v string) time.Time {
		tm, err := time.ParseInLocation("2006-01-02 15:04", v, rome)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	for now, want := range map[string]string{
		"2026-10-05 08:59": "2026-09-28 09:00", // Monday before the slot: last week's
		"2026-10-05 09:00": "2026-10-05 09:00",
		"2026-10-07 12:00": "2026-10-05 09:00",
		"2026-10-26 10:00": "2026-10-26 09:00", // the day after the change of time
	} {
		if got := s.Last(at(now), rome).Format("2006-01-02 15:04"); got != want {
			t.Errorf("Last(%s) = %s, want %s", now, got, want)
		}
	}
	if got := s.Next(at("2026-10-05 09:00"), rome).Format("2006-01-02 15:04"); got != "2026-10-12 09:00" {
		t.Errorf("Next = %s", got)
	}
	d, _ := ParseSchedule("day 18:30")
	if got := d.Last(at("2026-10-05 08:00"), rome).Format("2006-01-02 15:04"); got != "2026-10-04 18:30" {
		t.Errorf("daily Last = %s", got)
	}
}

// fakeHost records what the engine asks of it.
type fakeHost struct {
	dbs       []Database
	rows      []Row
	rowsErr   error
	handoffs  []string
	snapshots []string
}

func (h *fakeHost) Databases(string) ([]Database, error) { return h.dbs, nil }
func (h *fakeHost) Rows(Database, Rule, time.Time) ([]Row, error) {
	return h.rows, h.rowsErr
}
func (h *fakeHost) Handoff(db Database, r Rule, summary string, items []string, now time.Time) (string, error) {
	h.handoffs = append(h.handoffs, summary+"\n"+strings.Join(items, "\n"))
	return "demo/handoffs/h" + string(rune('0'+len(h.handoffs))) + ".md", nil
}
func (h *fakeHost) Snapshot(db Database, r Rule, now time.Time) (string, error) {
	h.snapshots = append(h.snapshots, r.Snapshot)
	return "demo/hot.snapshots/s.md", nil
}

func newEngine(t *testing.T, h *fakeHost, now *time.Time) (*Engine, string) {
	t.Helper()
	rome, _ := time.LoadLocation("Europe/Rome")
	path := filepath.Join(t.TempDir(), "automations.json")
	e, err := New(h, NewStore(path), rome, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.SetClock(func() time.Time { return *now })
	return e, path
}

func demoHost(t *testing.T) *fakeHost {
	rules, errs := Parse("demo/tasks.md", demoDatabase)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	return &fakeHost{
		dbs: []Database{{Path: "demo/tasks.md", Project: "demo", Source: "demo/tasks", Rules: rules}},
		rows: []Row{
			{Path: "demo/tasks/late.md", Title: "Late", Due: "2026-10-04"},
			{Path: "demo/tasks/today.md", Title: "Today", Due: "2026-10-05"},
			{Path: "demo/tasks/soon.md", Title: "Soon", Due: "2026-10-08"},
			{Path: "demo/tasks/later.md", Title: "Later", Due: "2026-11-04"},
			{Path: "demo/tasks/nodate.md", Title: "No date", Due: "next week"},
		},
	}
}

// A due rule hands off, once, the rows whose date is within its days or
// past; an every rule fires once a slot, from the first slot after it was
// first seen; the state survives a restart.
func TestEngine_Tick(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	h := demoHost(t)
	now := time.Date(2026, 10, 5, 8, 0, 0, 0, rome) // a Monday, before the slots
	e, statePath := newEngine(t, h, &now)

	runs := e.Tick("")
	if len(runs) != 1 || runs[0].Rule != "Deadlines near" || len(runs[0].Rows) != 3 || len(h.handoffs) != 1 || len(h.snapshots) != 0 {
		t.Fatalf("first tick: runs %+v, handoffs %d, snapshots %d", runs, len(h.handoffs), len(h.snapshots))
	}
	for _, want := range []string{"Look at these.", `Automation "Deadlines near" of [[demo/tasks]]: 3 rows come to their due (due - 3 days).`,
		"[[demo/tasks/late|Late]] — due 2026-10-04 (1 day late)", "[[demo/tasks/today|Today]] — due 2026-10-05 (today)",
		"[[demo/tasks/soon|Soon]] — due 2026-10-08 (in 3 days)"} {
		if !strings.Contains(h.handoffs[0], want) {
			t.Errorf("handoff lacks %q:\n%s", want, h.handoffs[0])
		}
	}
	if strings.Contains(h.handoffs[0], "Later") || strings.Contains(h.handoffs[0], "No date") {
		t.Errorf("only the rows that come in:\n%s", h.handoffs[0])
	}
	if runs := e.Tick(""); len(runs) != 0 {
		t.Errorf("a row is handed off once: %+v", runs)
	}

	// Past 09:00: the snapshot; past 10:30, the weekly review.
	now = time.Date(2026, 10, 5, 9, 5, 0, 0, rome)
	if runs := e.Tick(""); len(runs) != 1 || runs[0].Action != "snapshot" || len(h.snapshots) != 1 {
		t.Errorf("09:05: %+v", runs)
	}
	now = time.Date(2026, 10, 5, 10, 31, 0, 0, rome)
	if runs := e.Tick(""); len(runs) != 1 || runs[0].Rule != "Weekly review" || !strings.Contains(h.handoffs[1], "Review the backlog.") {
		t.Errorf("10:31: %+v", runs)
	}

	// A restart remembers; a row whose date changes comes in again.
	e2, err := New(h, NewStore(statePath), rome, nil)
	if err != nil {
		t.Fatal(err)
	}
	e2.SetClock(func() time.Time { return now })
	if runs := e2.Tick(""); len(runs) != 0 {
		t.Errorf("after a restart nothing fires again: %+v", runs)
	}
	h.rows[2].Due = "2026-10-06"
	if runs := e2.Tick(""); len(runs) != 1 || len(runs[0].Rows) != 1 || runs[0].Rows[0] != "demo/tasks/soon.md" {
		t.Errorf("a new date: %+v", runs)
	}

	// The next Monday: the slots fire again. On 2026-11-01 the later row
	// comes in, and the slots of 2026-10-26, missed while no tick ran,
	// fire once.
	now = time.Date(2026, 10, 12, 11, 0, 0, 0, rome)
	if runs := e2.Tick(""); len(runs) != 2 {
		t.Errorf("next Monday: %+v", runs)
	}
	now = time.Date(2026, 11, 1, 0, 1, 0, 0, rome)
	if runs := e2.Tick(""); len(runs) != 3 || runs[0].Rows[0] != "demo/tasks/later.md" {
		t.Errorf("2026-11-01: %+v", runs)
	}
	if runs := e2.Tick(""); len(runs) != 0 {
		t.Errorf("a missed slot fires once: %+v", runs)
	}
}

// A rule seen for the first time after its slot waits for the next one.
func TestEngine_FirstSeen(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	h := demoHost(t)
	h.rows = nil
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, rome) // Wednesday
	e, _ := newEngine(t, h, &now)
	if runs := e.Tick(""); len(runs) != 0 {
		t.Errorf("Monday's slot was before the rule: %+v", runs)
	}
	now = time.Date(2026, 10, 12, 9, 0, 0, 0, rome)
	if runs := e.Tick(""); len(runs) != 1 || runs[0].Action != "snapshot" {
		t.Errorf("the next Monday: %+v", runs)
	}
}

// A failing rule is logged once, until it changes or works again.
func TestEngine_Errors(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	h := demoHost(t)
	h.rowsErr = errors.New("where: bad condition")
	now := time.Date(2026, 10, 5, 8, 0, 0, 0, rome)
	e, _ := newEngine(t, h, &now)
	if runs := e.Tick(""); len(runs) != 1 || runs[0].Error != "where: bad condition" {
		t.Errorf("first failure: %+v", runs)
	}
	if runs := e.Tick(""); len(runs) != 0 {
		t.Errorf("the same failure is not logged again: %+v", runs)
	}
	h.rowsErr = nil
	if runs := e.Tick(""); len(runs) != 1 || runs[0].Error != "" {
		t.Errorf("working again: %+v", runs)
	}
}

// The dry run says what would fire at a moment, and writes nothing.
func TestEngine_Plan(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	h := demoHost(t)
	now := time.Date(2026, 10, 5, 8, 0, 0, 0, rome)
	e, _ := newEngine(t, h, &now)
	p, err := e.Plan("demo", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.handoffs) != 0 || len(h.snapshots) != 0 || len(p.Rules) != 3 || p.Timezone != "Europe/Rome" {
		t.Fatalf("plan = %+v", p)
	}
	due := p.Rules[0]
	if !due.Fires || len(due.Rows) != 3 || len(due.Upcoming) != 1 || due.Upcoming[0].From != "2026-11-01" || due.Next != "2026-11-01" {
		t.Errorf("due rule now = %+v", due)
	}
	if p.Rules[1].Fires || p.Rules[1].Next != "2026-10-05T09:00:00+02:00" {
		t.Errorf("snapshot now = %+v", p.Rules[1])
	}
	// As of 2026-11-01: the later row too; as of next Monday, the slots.
	p, _ = e.Plan("demo", time.Date(2026, 11, 1, 12, 0, 0, 0, rome))
	if len(p.Rules[0].Rows) != 4 || !p.Rules[1].Fires {
		t.Errorf("as of 2026-11-01 = %+v", p.Rules)
	}
	e.Tick("")
	p, _ = e.Plan("demo", time.Time{})
	if p.Rules[0].Fires || len(p.Rules[0].Fired) != 3 || len(p.Log) != 1 {
		t.Errorf("after the run: %+v", p)
	}
}

// A rule over MaxRows rows checks the first MaxRows, and its run and its
// plan say so (IMP-161, S3-8).
func TestEngine_RowCap(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	h := demoHost(t)
	h.rows = make([]Row, MaxRows+1)
	for k := range h.rows {
		h.rows[k] = Row{Path: fmt.Sprintf("demo/tasks/r%05d.md", k), Due: "2026-10-04"}
	}
	now := time.Date(2026, 10, 5, 8, 0, 0, 0, rome)
	e, _ := newEngine(t, h, &now)
	plan, err := e.Plan("", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, pr := range plan.Rules {
		if pr.Name == "Deadlines near" && (!strings.Contains(pr.Warning, "more than 10000 rows") || len(pr.Rows) != MaxRows) {
			t.Errorf("plan: warning %q, %d rows", pr.Warning, len(pr.Rows))
		}
	}
	runs := e.Tick("")
	if len(runs) != 1 || len(runs[0].Rows) != MaxRows || runs[0].Warning == "" {
		t.Fatalf("tick over the cap: %d runs", len(runs))
	}
	// Past the cap, the firing of a row the rule did not read stays: the
	// row is not gone, only unread.
	rs := e.state.Rules[h.dbs[0].Rules[0].Key(h.dbs[0].Path)]
	rs.Fired["demo/tasks/unread.md@2024-01-01"] = now.Add(-2 * forgetAfter)
	e.Tick("")
	if _, kept := rs.Fired["demo/tasks/unread.md@2024-01-01"]; !kept {
		t.Error("the firing of a row past the cap was forgotten")
	}
}

// A row handed off and still past its date more than 400 days later is not
// handed off again; the firing of a row gone goes after that time (IMP-162,
// S4-14).
func TestEngine_RowFiringOutlivesForgetAfter(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	h := demoHost(t)
	h.rows = []Row{{Path: "demo/tasks/late.md", Title: "Late", Due: "2026-10-04"}, {Path: "demo/tasks/gone.md", Due: "2026-10-04"}}
	now := time.Date(2026, 10, 5, 8, 0, 0, 0, rome)
	e, _ := newEngine(t, h, &now)
	if runs := e.Tick(""); len(runs) != 1 || len(runs[0].Rows) != 2 {
		t.Fatalf("first tick: %+v", runs)
	}
	h.rows = h.rows[:1]
	now = now.Add(forgetAfter + 48*time.Hour)
	// Twice: the state is pruned at the end of a run.
	for range 2 {
		for _, run := range e.Tick("") {
			if run.Rule == "Deadlines near" {
				t.Errorf("the row was handed off again: %+v", run)
			}
		}
	}
	rs := e.state.Rules[h.dbs[0].Rules[0].Key(h.dbs[0].Path)]
	if _, kept := rs.Fired["demo/tasks/late.md@2026-10-04"]; !kept {
		t.Error("the firing of the row still there went")
	}
	if _, kept := rs.Fired["demo/tasks/gone.md@2026-10-04"]; kept {
		t.Error("the firing of the row gone stays")
	}
}
