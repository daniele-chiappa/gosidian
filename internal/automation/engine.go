package automation

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// Database is a database note that declares automations, as the host
// finds it.
type Database struct {
	Path    string // the database note
	Project string
	Source  string // folder of its rows
	Rules   []Rule
	Errors  []error // rules that do not parse
}

// Row is a row of a database a due rule watches, with the value of the
// rule's date field.
type Row struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	Due   string `json:"due"`
}

// Host is what the engine acts through: the server, which reads and
// writes as the automation identity, inside the database's project.
type Host interface {
	// Databases lists the database notes with automations, of one
	// project or, for "", of all.
	Databases(project string) ([]Database, error)
	// Rows returns the rows of db that meet r.Where and have r.Due set,
	// at least MaxRows+1 of them when there are more than MaxRows.
	Rows(db Database, r Rule, now time.Time) ([]Row, error)
	// Handoff writes a handoff from the automation to r.Handoff.
	Handoff(db Database, r Rule, summary string, items []string, now time.Time) (string, error)
	// Snapshot freezes r.Snapshot into a dated note beside it.
	Snapshot(db Database, r Rule, now time.Time) (string, error)
}

// Engine runs the rules on a clock and remembers what it did.
type Engine struct {
	host  Host
	store *Store
	loc   *time.Location
	now   func() time.Time
	log   *slog.Logger

	mu    sync.Mutex
	state *State
}

// New is an engine acting through host, its state in store, its days and
// slots in loc (nil: the server's local zone).
func New(host Host, store *Store, loc *time.Location, logger *slog.Logger) (*Engine, error) {
	if loc == nil {
		loc = time.Local
	}
	if logger == nil {
		logger = slog.Default()
	}
	st, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("automations state: %w", err)
	}
	return &Engine{host: host, store: store, loc: loc, now: time.Now, log: logger, state: st}, nil
}

// SetClock replaces the engine's clock, for tests.
func (e *Engine) SetClock(now func() time.Time) { e.now = now }

// Location is the engine's time zone.
func (e *Engine) Location() *time.Location { return e.loc }

// Start runs the rules every interval until ctx ends, the first time
// right away.
func (e *Engine) Start(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		e.Tick("")
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick runs the rules of a project, or of all for "", now: each action
// that is due happens once. It returns what it did.
func (e *Engine) Tick(project string) []Run {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	dbs, err := e.host.Databases(project)
	if err != nil {
		e.log.Warn("automations: list databases", "err", err)
		return nil
	}
	var runs []Run
	for _, db := range dbs {
		for _, r := range db.Rules {
			if run, ok := e.fire(db, r, now); ok {
				runs = append(runs, run)
				e.state.log(run)
			}
		}
	}
	if project == "" {
		e.state.prune(now)
	}
	if err := e.store.Save(e.state); err != nil {
		e.log.Warn("automations: save state", "err", err)
	}
	return runs
}

// fire runs r now if it is due, and says what it did. A failure is
// reported once, until it changes or the rule works again.
func (e *Engine) fire(db Database, r Rule, now time.Time) (Run, bool) {
	rs := e.state.rule(r.Key(db.Path), now)
	rs.LastSeen = now
	run := Run{At: now, Database: db.Path, Rule: r.Name, Action: actionName(r)}
	fail := func(err error) (Run, bool) {
		msg := err.Error()
		if rs.Error == msg {
			return Run{}, false
		}
		rs.Error = msg
		run.Error = msg
		e.log.Warn("automations: rule failed", "database", db.Path, "rule", r.Name, "err", err)
		return run, true
	}
	if r.Every != nil {
		slot := r.Every.Last(now, e.loc)
		key := slot.UTC().Format(time.RFC3339)
		if slot.Before(rs.FirstSeen) || !rs.Fired[key].IsZero() {
			return Run{}, false
		}
		var path string
		var err error
		if r.Snapshot != "" {
			path, err = e.host.Snapshot(db, r, now)
		} else {
			path, err = e.host.Handoff(db, r, everySummary(db, r), nil, now)
		}
		if err != nil {
			return fail(err)
		}
		rs.Fired[key] = now
		rs.Error = ""
		run.Path = path
		return run, true
	}
	rows, err := e.host.Rows(db, r, now)
	if err != nil {
		return fail(err)
	}
	rows, warning := capRows(rows)
	if warning != rs.Warning {
		rs.Warning = warning
		if warning != "" {
			e.log.Warn("automations: rule over the row cap", "database", db.Path, "rule", r.Name, "max_rows", MaxRows)
		}
	}
	run.Warning = warning
	if warning == "" {
		// Past the cap the rows not read are not gone: their firings stay.
		rs.forgetRows(rows, now)
	}
	var due []Row
	for _, row := range e.comeIn(r, rows, now) {
		if rs.Fired[rowKey(row)].IsZero() {
			due = append(due, row)
		}
	}
	if len(due) == 0 {
		rs.Error = ""
		return Run{}, false
	}
	summary, items := e.dueHandoff(db, r, due, now)
	path, err := e.host.Handoff(db, r, summary, items, now)
	if err != nil {
		return fail(err)
	}
	for _, row := range due {
		rs.Fired[rowKey(row)] = now
		run.Rows = append(run.Rows, row.Path)
	}
	rs.Error = ""
	run.Path = path
	return run, true
}

// comeIn keeps the rows whose date is within r.Before days of now's day,
// or past it, earliest first.
func (e *Engine) comeIn(r Rule, rows []Row, now time.Time) []Row {
	today := day(now, e.loc)
	var out []Row
	for _, row := range rows {
		d, ok := parseDay(row.Due, e.loc)
		if !ok {
			continue
		}
		if !today.Before(d.AddDate(0, 0, -r.Before)) {
			out = append(out, row)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Due < out[j].Due })
	return out
}

func (e *Engine) dueHandoff(db Database, r Rule, rows []Row, now time.Time) (string, []string) {
	today := day(now, e.loc)
	var b strings.Builder
	if r.Message != "" {
		b.WriteString(r.Message)
		b.WriteString("\n\n")
	}
	noun := "rows come"
	if len(rows) == 1 {
		noun = "row comes"
	}
	fmt.Fprintf(&b, "Automation %q of [[%s]]: %d %s to their %s (%s). Claim this handoff, deal with each row or say why not, then complete it.",
		r.Name, strings.TrimSuffix(db.Path, ".md"), len(rows), noun, r.Due, r.Trigger())
	items := make([]string, len(rows))
	for i, row := range rows {
		d, _ := parseDay(row.Due, e.loc)
		title := row.Title
		if title == "" {
			title = row.Path
		}
		items[i] = fmt.Sprintf("[[%s|%s]] — %s %s (%s)", strings.TrimSuffix(row.Path, ".md"), strings.ReplaceAll(title, "|", "-"), r.Due, d.Format("2006-01-02"), when(today, d))
	}
	return b.String(), items
}

func everySummary(db Database, r Rule) string {
	s := fmt.Sprintf("Automation %q of [[%s]], %s.", r.Name, strings.TrimSuffix(db.Path, ".md"), r.Trigger())
	if r.Message != "" {
		return r.Message + "\n\n" + s
	}
	return s
}

// when says how far d is from today: "today", "in 3 days", "2 days late".
func when(today, d time.Time) string {
	n := int(d.Sub(today).Hours()/24 + 0.5)
	if d.Before(today) {
		n = int(today.Sub(d).Hours()/24 + 0.5)
	}
	switch {
	case n == 0:
		return "today"
	case d.Before(today) && n == 1:
		return "1 day late"
	case d.Before(today):
		return fmt.Sprintf("%d days late", n)
	case n == 1:
		return "tomorrow"
	}
	return fmt.Sprintf("in %d days", n)
}

func actionName(r Rule) string {
	if r.Snapshot != "" {
		return "snapshot"
	}
	return "handoff"
}

func rowKey(r Row) string {
	d := r.Due
	if len(d) > 10 {
		d = d[:10]
	}
	return r.Path + "@" + d
}

func day(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// parseDay reads a date field's value: an ISO date, or the day of a
// datetime.
func parseDay(v string, loc *time.Location) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if len(v) < 10 {
		return time.Time{}, false
	}
	d, err := time.ParseInLocation("2006-01-02", v[:10], loc)
	return d, err == nil
}

// Plan is what the rules would do at a moment, without doing it: the
// dry run of memory_automations.
type Plan struct {
	AsOf     time.Time     `json:"as_of"`
	Timezone string        `json:"timezone"`
	Rules    []PlannedRule `json:"rules"`
	// Errors are the rules that do not parse, by database note.
	Errors map[string][]string `json:"errors,omitempty"`
	Log    []Run               `json:"log,omitempty"`
}

// PlannedRule is one rule in a Plan.
type PlannedRule struct {
	Database string `json:"database"`
	Name     string `json:"name"`
	Trigger  string `json:"trigger"`
	Action   string `json:"action"`
	// Fires says the rule would act at as_of; Rows are the rows it would
	// hand off, Slot the slot it would fire.
	Fires bool         `json:"fires"`
	Rows  []PlannedRow `json:"rows,omitempty"`
	Slot  string       `json:"slot,omitempty"`
	// Next is when it acts next after as_of: the next slot, or the first
	// day a row still to come comes in.
	Next string `json:"next,omitempty"`
	// Upcoming are the rows that come in after as_of, with their day.
	Upcoming []PlannedRow `json:"upcoming,omitempty"`
	// Fired are the rows already handed off for their date.
	Fired []PlannedRow `json:"already_fired,omitempty"`
	Error string       `json:"error,omitempty"`
	// Warning says the rule reads only its first MaxRows rows.
	Warning string `json:"warning,omitempty"`
}

// PlannedRow is a row in a Plan; From is the day it comes in.
type PlannedRow struct {
	Path  string `json:"path"`
	Title string `json:"title,omitempty"`
	Due   string `json:"due"`
	From  string `json:"from,omitempty"`
}

// MaxUpcoming caps the rows a plan lists as still to come, per rule.
const MaxUpcoming = 20

// MaxRows caps the rows a rule reads, in path order; past it the rule and
// its plan carry a warning, and the log says it once.
const MaxRows = 10000

// capRows keeps the first MaxRows rows, and says why when there were more.
func capRows(rows []Row) ([]Row, string) {
	if len(rows) <= MaxRows {
		return rows, ""
	}
	return rows[:MaxRows], fmt.Sprintf("the database has more than %d rows that meet the rule: only the first %d, in path order, are checked; narrow the rule's where", MaxRows, MaxRows)
}

// Plan says what the rules of project would do at asOf (zero: now), given
// what they already did. It writes nothing.
func (e *Engine) Plan(project string, asOf time.Time) (*Plan, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	if asOf.IsZero() {
		asOf = now
	}
	dbs, err := e.host.Databases(project)
	if err != nil {
		return nil, err
	}
	p := &Plan{AsOf: asOf.In(e.loc), Timezone: e.loc.String()}
	for _, db := range dbs {
		for _, err := range db.Errors {
			if p.Errors == nil {
				p.Errors = map[string][]string{}
			}
			p.Errors[db.Path] = append(p.Errors[db.Path], err.Error())
		}
		for _, r := range db.Rules {
			p.Rules = append(p.Rules, e.plan(db, r, now, asOf))
		}
	}
	for _, run := range e.state.Log {
		if project == "" || strings.HasPrefix(run.Database, project+"/") {
			p.Log = append(p.Log, run)
		}
	}
	if len(p.Log) > 20 {
		p.Log = p.Log[len(p.Log)-20:]
	}
	return p, nil
}

func (e *Engine) plan(db Database, r Rule, now, asOf time.Time) PlannedRule {
	pr := PlannedRule{Database: db.Path, Name: r.Name, Trigger: r.Trigger(), Action: r.Action()}
	firstSeen, fired := now, map[string]time.Time{}
	if rs := e.state.Rules[r.Key(db.Path)]; rs != nil {
		firstSeen, fired = rs.FirstSeen, rs.Fired
		pr.Error = rs.Error
	}
	if r.Every != nil {
		slot := r.Every.Last(asOf, e.loc)
		if !slot.Before(firstSeen) && fired[slot.UTC().Format(time.RFC3339)].IsZero() {
			pr.Fires = true
			pr.Slot = slot.Format(time.RFC3339)
		}
		pr.Next = r.Every.Next(asOf, e.loc).Format(time.RFC3339)
		return pr
	}
	rows, err := e.host.Rows(db, r, asOf)
	if err != nil {
		pr.Error = err.Error()
		return pr
	}
	rows, pr.Warning = capRows(rows)
	in := map[string]bool{}
	for _, row := range e.comeIn(r, rows, asOf) {
		in[row.Path] = true
		prow := PlannedRow{Path: row.Path, Title: row.Title, Due: row.Due}
		if !fired[rowKey(row)].IsZero() {
			pr.Fired = append(pr.Fired, prow)
			continue
		}
		pr.Rows = append(pr.Rows, prow)
	}
	pr.Fires = len(pr.Rows) > 0
	var later []PlannedRow
	for _, row := range rows {
		if in[row.Path] {
			continue
		}
		d, ok := parseDay(row.Due, e.loc)
		if !ok {
			continue
		}
		from := d.AddDate(0, 0, -r.Before).Format("2006-01-02")
		later = append(later, PlannedRow{Path: row.Path, Title: row.Title, Due: row.Due, From: from})
	}
	sort.SliceStable(later, func(i, j int) bool { return later[i].From < later[j].From })
	if len(later) > 0 {
		pr.Next = later[0].From
	}
	if len(later) > MaxUpcoming {
		later = later[:MaxUpcoming]
	}
	pr.Upcoming = later
	return pr
}
