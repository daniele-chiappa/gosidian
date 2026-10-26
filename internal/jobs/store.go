// Package jobs is the durable queue of the server's background work
// (IMP-166, ADR-035): runs made of named steps, kept in a SQLite file of
// the state dir, run by a pool of goroutines in the server process.
//
// A run belongs to a kind (today "pipeline"); an Executor of that kind
// runs its steps and decides, when one ends, which steps come next. The
// store knows nothing of what a step does: input, output and spec are JSON
// it keeps as they are.
package jobs

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Status is where a run or a step stands.
type Status string

const (
	// Pending: a step waiting for the steps it depends on.
	Pending Status = "pending"
	// Queued: ready to run, from NotBefore on.
	Queued Status = "queued"
	// Running: a worker holds it until LeaseUntil.
	Running Status = "running"
	// Waiting: suspended until a call resumes it, or WaitUntil passes.
	Waiting Status = "waiting"
	// Done: ended well (succeeded, for a run).
	Done Status = "done"
	// Failed: ended with an error.
	Failed Status = "failed"
	// Skipped: a step its condition left out.
	Skipped Status = "skipped"
	// Canceled: stopped from outside, or left behind by a failed run.
	Canceled Status = "canceled"
)

// The priorities of a run: an interactive one (a chat's message, IMP-191)
// has its steps taken before the others', and the workers of the
// interactive lane (Options.InteractiveWorkers) take only those; a low one
// (a category's batch, IMP-196) after the normal ones.
const (
	PriorityLow         = -1
	PriorityNormal      = 0
	PriorityInteractive = 1
)

// Terminal says whether s is an end state.
func (s Status) Terminal() bool {
	return s == Done || s == Failed || s == Skipped || s == Canceled
}

// Run is one execution: of a pipeline, for kind "pipeline".
type Run struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Ref     string `json:"ref"` // what runs: the pipeline note's path
	Project string `json:"project"`
	Trigger string `json:"trigger"`
	Status  Status `json:"status"`
	// Input is what started it (the trigger's payload); Spec the
	// definition it runs, frozen at the start so an edit of the note
	// does not change a run halfway.
	Input   json.RawMessage `json:"input,omitempty"`
	Spec    json.RawMessage `json:"-"`
	IdemKey string          `json:"idempotency_key,omitempty"`
	Actor   string          `json:"actor"`
	// CallbackURL is called with the run's summary when it ends.
	CallbackURL string `json:"callback_url,omitempty"`
	// ResumeKey lets an outside caller resume the run's waiting steps.
	ResumeKey string `json:"-"`
	// Priority is PriorityLow, PriorityNormal or PriorityInteractive.
	Priority int `json:"priority,omitempty"`
	// Category is the category of its pipeline (IMP-196), "" for a run
	// of another kind.
	Category string `json:"category,omitempty"`
	// Task is the vault path of the task note the run executes, Team that
	// of the task's team note (IMP-195); "" for the other runs.
	Task     string    `json:"task,omitempty"`
	Team     string    `json:"team,omitempty"`
	Error    string    `json:"error,omitempty"`
	Created  time.Time `json:"created"`
	Started  time.Time `json:"started,omitzero"`
	Finished time.Time `json:"finished,omitzero"`
}

// Step is one node of a run.
type Step struct {
	RunID   string   `json:"-"`
	Name    string   `json:"name"`
	Deps    []string `json:"deps,omitempty"`
	Status  Status   `json:"status"`
	Attempt int      `json:"attempt"`
	// Output is what the step produced; Error why it failed.
	Output     json.RawMessage `json:"output,omitempty"`
	Error      string          `json:"error,omitempty"`
	Started    time.Time       `json:"started,omitzero"`
	Finished   time.Time       `json:"finished,omitzero"`
	NotBefore  time.Time       `json:"not_before,omitzero"`
	LeaseUntil time.Time       `json:"-"`
	WaitUntil  time.Time       `json:"wait_until,omitzero"`
	// Resources are what the step takes a place in while it runs (IMP-196):
	// plugin:<name>, llm:<provider>, res:<name>. QueuedAt is when it last
	// entered the queue.
	Resources []string  `json:"resources,omitempty"`
	QueuedAt  time.Time `json:"queued_at,omitzero"`
	// CostUSD and Tokens are what the step spent, read from its output
	// when it finished (IMP-195, CostOf): an agent's cost and tokens.
	CostUSD float64 `json:"cost_usd,omitempty"`
	Tokens  int64   `json:"tokens,omitempty"`
}

// ErrNotFound: no such run or step.
var ErrNotFound = errors.New("jobs: not found")

// ErrState: the run or step is not in a state that allows the change.
var ErrState = errors.New("jobs: not in a state that allows it")

const schema = `
CREATE TABLE IF NOT EXISTS runs (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	ref TEXT NOT NULL,
	project TEXT NOT NULL,
	trigger TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	input TEXT NOT NULL DEFAULT '',
	spec TEXT NOT NULL DEFAULT '',
	idem_key TEXT NOT NULL DEFAULT '',
	actor TEXT NOT NULL DEFAULT '',
	callback_url TEXT NOT NULL DEFAULT '',
	resume_key TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	created INTEGER NOT NULL,
	started INTEGER NOT NULL DEFAULT 0,
	finished INTEGER NOT NULL DEFAULT 0,
	priority INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS runs_ref ON runs(ref, created);
CREATE INDEX IF NOT EXISTS runs_project ON runs(project, created);
CREATE INDEX IF NOT EXISTS runs_status ON runs(status);
CREATE UNIQUE INDEX IF NOT EXISTS runs_idem ON runs(ref, idem_key) WHERE idem_key != '';
CREATE INDEX IF NOT EXISTS runs_idem_key ON runs(idem_key) WHERE idem_key != '';
CREATE TABLE IF NOT EXISTS steps (
	run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	pos INTEGER NOT NULL,
	deps TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	attempt INTEGER NOT NULL DEFAULT 0,
	output TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	started INTEGER NOT NULL DEFAULT 0,
	finished INTEGER NOT NULL DEFAULT 0,
	not_before INTEGER NOT NULL DEFAULT 0,
	lease_until INTEGER NOT NULL DEFAULT 0,
	wait_until INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (run_id, name)
);
CREATE INDEX IF NOT EXISTS steps_ready ON steps(status, not_before);
CREATE TABLE IF NOT EXISTS tokens (
	id TEXT PRIMARY KEY,
	ref TEXT NOT NULL,
	hash TEXT NOT NULL UNIQUE,
	name TEXT NOT NULL DEFAULT '',
	created INTEGER NOT NULL,
	created_by TEXT NOT NULL DEFAULT '',
	last_used INTEGER NOT NULL DEFAULT 0
);
`

// Store is the jobs database. Writes are serialized: SQLite has one
// writer, and a claim must not race another.
type Store struct {
	db *sql.DB
	mu sync.Mutex
	// served is when the fair claim last took a step of each project, as
	// the count turn of the steps it took (IMP-196); under mu.
	served map[string]uint64
	turn   uint64
}

// Open opens (creating it if needed) the jobs database at path. It is
// not derived data, unlike the index: synchronous(FULL), so a run
// acknowledged to a caller survives a crash.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("jobs: schema: %w", err)
	}
	for _, c := range []struct{ table, col, def string }{
		{"runs", "priority", "INTEGER NOT NULL DEFAULT 0"},
		{"steps", "resources", "TEXT NOT NULL DEFAULT ''"},
		{"steps", "queued_at", "INTEGER NOT NULL DEFAULT 0"},
		{"runs", "category", "TEXT NOT NULL DEFAULT ''"},
		{"runs", "task", "TEXT NOT NULL DEFAULT ''"},
		{"runs", "team", "TEXT NOT NULL DEFAULT ''"},
		{"steps", "cost_usd", "REAL NOT NULL DEFAULT 0"},
		{"steps", "tokens", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := addColumn(db, c.table, c.col, c.def); err != nil {
			db.Close()
			return nil, fmt.Errorf("jobs: schema: %w", err)
		}
	}
	// The runs of a team, for its spend (IMP-195, TeamSpend): made after
	// the columns, which an older database gets only above.
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS runs_team ON runs(project, team, finished) WHERE team != ''`); err != nil {
		db.Close()
		return nil, fmt.Errorf("jobs: schema: %w", err)
	}
	return &Store{db: db}, nil
}

// addColumn adds a column to a table made before it existed.
func addColumn(db *sql.DB, table, col, def string) error {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if name == col {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + col + ` ` + def)
	return err
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// NewID returns a random identifier: 16 hex digits.
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// NewSecret returns a random secret: 32 bytes, hex.
func NewSecret() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

func raw(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

// CreateRun stores a new run with its steps. A run with the same Ref and
// a non-empty IdemKey as one already stored is not created again: the
// stored run comes back, with existing true.
func (s *Store) CreateRun(r Run, steps []Step) (Run, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.IdemKey != "" {
		if old, err := s.runByIdem(r.Ref, r.IdemKey); err == nil {
			return old, true, nil
		} else if !errors.Is(err, ErrNotFound) {
			return Run{}, false, err
		}
	}
	if r.ID == "" {
		r.ID = NewID()
	}
	if r.Created.IsZero() {
		r.Created = time.Now().UTC()
	}
	if r.Status == "" {
		r.Status = Queued
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Run{}, false, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO runs (id, kind, ref, project, trigger, status, input, spec, idem_key, actor, callback_url, resume_key, error, created, priority, category, task, team)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.Kind, r.Ref, r.Project, r.Trigger, r.Status, string(r.Input), string(r.Spec), r.IdemKey, r.Actor, r.CallbackURL, r.ResumeKey, r.Error, ms(r.Created), r.Priority, r.Category, r.Task, r.Team)
	if err != nil {
		return Run{}, false, err
	}
	for i, st := range steps {
		if st.Status == "" {
			st.Status = Pending
		}
		_, err = tx.Exec(`INSERT INTO steps (run_id, name, pos, deps, status, not_before) VALUES (?,?,?,?,?,?)`,
			r.ID, st.Name, i, strings.Join(st.Deps, ","), st.Status, ms(st.NotBefore))
		if err != nil {
			return Run{}, false, fmt.Errorf("jobs: step %q: %w", st.Name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Run{}, false, err
	}
	return r, false, nil
}

const runCols = `id, kind, ref, project, trigger, status, input, spec, idem_key, actor, callback_url, resume_key, error, created, started, finished, priority, category, task, team`

type scanner interface{ Scan(dest ...any) error }

func scanRun(sc scanner) (Run, error) {
	var r Run
	var input, spec string
	var created, started, finished int64
	err := sc.Scan(&r.ID, &r.Kind, &r.Ref, &r.Project, &r.Trigger, &r.Status, &input, &spec, &r.IdemKey, &r.Actor, &r.CallbackURL, &r.ResumeKey, &r.Error, &created, &started, &finished, &r.Priority, &r.Category, &r.Task, &r.Team)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	r.Input, r.Spec = raw(input), raw(spec)
	r.Created, r.Started, r.Finished = fromMS(created), fromMS(started), fromMS(finished)
	return r, nil
}

func (s *Store) runByIdem(ref, key string) (Run, error) {
	return scanRun(s.db.QueryRow(`SELECT `+runCols+` FROM runs WHERE ref = ? AND idem_key = ?`, ref, key))
}

// RunByIdem is the run of ref started with key, ErrNotFound when none.
func (s *Store) RunByIdem(ref, key string) (Run, error) { return s.runByIdem(ref, key) }

// Unended counts the runs of category not ended yet (IMP-196): queued,
// running or waiting.
func (s *Store) Unended(category string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE category = ? AND status IN (?, ?, ?)`, category, Queued, Running, Waiting).Scan(&n)
	return n, err
}

// QueuedTwins are the runs of ref with the same input still queued, no
// step of theirs started, other than the run but (IMP-196, replace).
func (s *Store) QueuedTwins(ref string, input json.RawMessage, but string) ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM runs WHERE ref = ? AND input = ? AND status = ? AND id != ?`, ref, string(input), Queued, but)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Run returns a run.
func (s *Store) Run(id string) (Run, error) {
	return scanRun(s.db.QueryRow(`SELECT `+runCols+` FROM runs WHERE id = ?`, id))
}

// RunFilter selects runs, newest first.
type RunFilter struct {
	Kind     string   // one kind of run: pipeline, automation
	Ref      string   // one pipeline
	Projects []string // these projects; nil: all
	Status   Status
	// IdemPrefix keeps the runs whose idempotency key starts with it: the
	// runs another run started (pipeline.run, IMP-186).
	IdemPrefix string
	Limit      int // default 50, at most 500
}

// CountRuns counts the runs whose idempotency key starts with idemPrefix:
// the runs another run started (pipeline.run). The keys are ASCII, and a
// range on them reads the index, where a LIKE would read every run.
func (s *Store) CountRuns(idemPrefix string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM runs WHERE idem_key >= ? AND idem_key < ?`, idemPrefix, idemPrefix+"\x7f").Scan(&n)
	return n, err
}

// Runs lists runs.
func (s *Store) Runs(f RunFilter) ([]Run, error) {
	var where []string
	var args []any
	if f.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, f.Kind)
	}
	if f.Ref != "" {
		where = append(where, "ref = ?")
		args = append(args, f.Ref)
	}
	if f.Projects != nil {
		if len(f.Projects) == 0 {
			return nil, nil
		}
		where = append(where, "project IN ("+strings.TrimSuffix(strings.Repeat("?,", len(f.Projects)), ",")+")")
		for _, p := range f.Projects {
			args = append(args, p)
		}
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if f.IdemPrefix != "" {
		esc := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(f.IdemPrefix)
		where = append(where, `idem_key LIKE ? ESCAPE '\'`)
		args = append(args, esc+"%")
	}
	q := `SELECT ` + runCols + ` FROM runs`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	q += " ORDER BY created DESC, id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const stepCols = `run_id, name, deps, status, attempt, output, error, started, finished, not_before, lease_until, wait_until, resources, queued_at, cost_usd, tokens`

func scanStep(sc scanner) (Step, error) {
	var st Step
	var deps, output, resources string
	var started, finished, notBefore, lease, wait, queued int64
	err := sc.Scan(&st.RunID, &st.Name, &deps, &st.Status, &st.Attempt, &output, &st.Error, &started, &finished, &notBefore, &lease, &wait, &resources, &queued, &st.CostUSD, &st.Tokens)
	if errors.Is(err, sql.ErrNoRows) {
		return Step{}, ErrNotFound
	}
	if err != nil {
		return Step{}, err
	}
	if deps != "" {
		st.Deps = strings.Split(deps, ",")
	}
	st.Output = raw(output)
	st.Started, st.Finished = fromMS(started), fromMS(finished)
	st.NotBefore, st.LeaseUntil, st.WaitUntil = fromMS(notBefore), fromMS(lease), fromMS(wait)
	if resources != "" {
		st.Resources = strings.Split(resources, ",")
	}
	st.QueuedAt = fromMS(queued)
	return st, nil
}

// Steps returns the steps of a run, in their order.
func (s *Store) Steps(runID string) ([]Step, error) {
	rows, err := s.db.Query(`SELECT `+stepCols+` FROM steps WHERE run_id = ? ORDER BY pos`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Step
	for rows.Next() {
		st, err := scanStep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// claim takes up to n queued steps whose time has come, those of the
// interactive runs first (only those with interactive), marks them running
// until now+lease, and starts their runs. Within a priority the oldest run
// goes first: a step queued without a delay has no not_before, and ordering
// by it and the run's random id left, under a full queue, the runs whose id
// sorts last waiting for hours behind newer ones.
func (s *Store) claim(now time.Time, n int, lease time.Duration, interactive bool, gate *Gate, fair bool) (out []Step, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// The places the steps took, given back if the claim fails.
	defer func() {
		if err != nil {
			for _, st := range out {
				gate.Release(st.Resources)
			}
			out = nil
		}
	}()
	if fair {
		out, err = s.pickFair(tx, now, n, interactive, gate)
	} else {
		out, err = pickInOrder(tx, now, n, interactive, gate)
	}
	if err != nil {
		return out, err
	}
	for i := range out {
		out[i].Status = Running
		out[i].Attempt++
		out[i].Started = now
		out[i].LeaseUntil = now.Add(lease)
		if _, err := tx.Exec(`UPDATE steps SET status = ?, attempt = ?, started = ?, lease_until = ?, error = '' WHERE run_id = ? AND name = ?`,
			Running, out[i].Attempt, ms(now), ms(out[i].LeaseUntil), out[i].RunID, out[i].Name); err != nil {
			return out, err
		}
		if _, err := tx.Exec(`UPDATE runs SET status = ?, started = CASE WHEN started = 0 THEN ? ELSE started END WHERE id = ? AND status IN (?, ?)`,
			Running, ms(now), out[i].RunID, Queued, Waiting); err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}

// pickInOrder takes up to n steps in the queue's order: priority, then
// the oldest run. A step whose resource is full stays in the queue, and
// the ones after it may go (IMP-196).
func pickInOrder(tx *sql.Tx, now time.Time, n int, interactive bool, gate *Gate) (out []Step, err error) {
	cols := "s." + strings.ReplaceAll(stepCols, ", ", ", s.")
	q := `SELECT ` + cols + ` FROM steps s JOIN runs r ON r.id = s.run_id WHERE s.status = ? AND s.not_before <= ?`
	if interactive {
		q += ` AND r.priority >= ` + fmt.Sprint(PriorityInteractive)
	}
	q += ` ORDER BY r.priority DESC, r.created, s.run_id, s.pos`
	if gate == nil {
		q += ` LIMIT ` + fmt.Sprint(n)
	}
	rows, err := tx.Query(q, Queued, ms(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	full := map[string]bool{}
	for len(out) < n && rows.Next() {
		st, err := scanStep(rows)
		if err != nil {
			return out, err
		}
		if skipFull(st.Resources, full) || !gate.TryTake(st.Resources) {
			if r := gate.Full(st.Resources); r != "" {
				full[r] = true
			}
			continue
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// withExtra scans a row of stepCols and more columns after them.
type withExtra struct {
	rows *sql.Rows
	more []any
}

func (w withExtra) Scan(dest ...any) error { return w.rows.Scan(append(dest, w.more...)...) }

// pickFair takes up to n steps in turn among the projects (IMP-196): at
// each priority, from the highest, the project with the fewest steps
// running and taken in this claim gives its next step, the one served
// least lately among equals, in the queue's order; a step whose resource
// is full is passed over. It reads the first steps of each project and set
// of resources, so that the thousands of steps a project queued, or those
// waiting for a full plugin, hide no other's.
func (s *Store) pickFair(tx *sql.Tx, now time.Time, n int, interactive bool, gate *Gate) (out []Step, err error) {
	inner := make([]string, 0, 16)
	for _, c := range strings.Split(stepCols, ", ") {
		inner = append(inner, "s."+c+" AS "+c)
	}
	q := `SELECT ` + stepCols + `, proj, prio FROM (SELECT ` + strings.Join(inner, ", ") + `, s.pos AS pos, r.project AS proj, r.priority AS prio, r.created AS cr,
		ROW_NUMBER() OVER (PARTITION BY r.project, s.resources ORDER BY r.priority DESC, r.created, s.run_id, s.pos) AS rn
		FROM steps s JOIN runs r ON r.id = s.run_id WHERE s.status = ? AND s.not_before <= ?`
	if interactive {
		q += ` AND r.priority >= ` + fmt.Sprint(PriorityInteractive)
	}
	q += `) WHERE rn <= ? ORDER BY prio DESC, cr, run_id, pos`
	rows, err := tx.Query(q, Queued, ms(now), n+2)
	if err != nil {
		return nil, err
	}
	type cand struct {
		st   Step
		proj string
	}
	var levels []int
	queues := map[int]map[string][]cand{}
	order := map[int][]string{}
	for rows.Next() {
		var proj string
		var prio int
		st, err := scanStep(withExtra{rows, []any{&proj, &prio}})
		if err != nil {
			rows.Close()
			return nil, err
		}
		if queues[prio] == nil {
			queues[prio] = map[string][]cand{}
			levels = append(levels, prio)
		}
		if _, seen := queues[prio][proj]; !seen {
			order[prio] = append(order[prio], proj)
		}
		queues[prio][proj] = append(queues[prio][proj], cand{st, proj})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	running := map[string]int{}
	rr, err := tx.Query(`SELECT r.project, COUNT(*) FROM steps s JOIN runs r ON r.id = s.run_id WHERE s.status = ? GROUP BY r.project`, Running)
	if err != nil {
		return nil, err
	}
	for rr.Next() {
		var p string
		var c int
		if err := rr.Scan(&p, &c); err != nil {
			rr.Close()
			return nil, err
		}
		running[p] = c
	}
	rr.Close()
	picked := map[string]int{}
	for _, lv := range levels {
		qs := queues[lv]
		for len(out) < n {
			best := ""
			for _, p := range order[lv] {
				if len(qs[p]) == 0 {
					continue
				}
				load, bl := running[p]+picked[p], running[best]+picked[best]
				if best == "" || load < bl || (load == bl && s.served[p] < s.served[best]) {
					best = p
				}
			}
			if best == "" {
				break
			}
			for len(qs[best]) > 0 {
				c := qs[best][0]
				qs[best] = qs[best][1:]
				if gate.TryTake(c.st.Resources) {
					out = append(out, c.st)
					picked[best]++
					if s.served == nil {
						s.served = map[string]uint64{}
					}
					s.turn++
					s.served[best] = s.turn
					break
				}
			}
		}
	}
	return out, nil
}

// skipFull says whether one of resources was found full in this claim.
func skipFull(resources []string, full map[string]bool) bool {
	for _, r := range resources {
		if full[r] {
			return true
		}
	}
	return false
}

// StepChange is a change to one step, decided by an Executor.
type StepChange struct {
	Name      string
	Status    Status
	Output    json.RawMessage
	Error     string
	NotBefore time.Time
	WaitUntil time.Time
	// Finished stamps the end time (for a terminal status).
	Finished time.Time
	// Resources, when not nil, are the step's from now on; Queued, when
	// set, is when it entered the queue.
	Resources []string
	Queued    time.Time
}

// RunChange is the run's new status, when it changes.
type RunChange struct {
	Status   Status
	Error    string
	Finished time.Time
}

// apply writes step changes and, if rc is set, the run's status, in one
// transaction. A step change only applies to a step that is not terminal
// yet, unless force: a late result never overwrites a canceled step.
func (s *Store) apply(runID string, changes []StepChange, rc *RunChange) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range changes {
		// A step that finishes with an output keeps what it spent
		// (IMP-195).
		spent := len(c.Output) > 0 && !c.Finished.IsZero()
		var cost float64
		var tokens int64
		if spent {
			cost, tokens = CostOf(c.Output)
		}
		_, err := tx.Exec(`UPDATE steps SET status = ?, output = CASE WHEN ? = '' THEN output ELSE ? END, error = ?, not_before = ?, wait_until = ?, finished = ?, lease_until = 0,
			resources = CASE WHEN ? THEN ? ELSE resources END, queued_at = CASE WHEN ? > 0 THEN ? ELSE queued_at END,
			cost_usd = CASE WHEN ? THEN ? ELSE cost_usd END, tokens = CASE WHEN ? THEN ? ELSE tokens END
			WHERE run_id = ? AND name = ? AND status NOT IN (?, ?, ?, ?)`,
			c.Status, string(c.Output), string(c.Output), c.Error, ms(c.NotBefore), ms(c.WaitUntil), ms(c.Finished),
			c.Resources != nil, strings.Join(c.Resources, ","), ms(c.Queued), ms(c.Queued),
			spent, cost, spent, tokens,
			runID, c.Name, Done, Failed, Skipped, Canceled)
		if err != nil {
			return err
		}
	}
	if rc != nil {
		if _, err := tx.Exec(`UPDATE runs SET status = ?, error = ?, finished = ? WHERE id = ? AND status NOT IN (?, ?, ?)`,
			rc.Status, rc.Error, ms(rc.Finished), runID, Done, Failed, Canceled); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// recoverRunning puts back in the queue the steps a previous process
// left running (a crash or a restart while they ran). One process uses
// the database at a time, so at its start none of them still runs.
func (s *Store) recoverRunning(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`UPDATE steps SET status = ?, not_before = ?, queued_at = ?, lease_until = 0 WHERE status = ?`,
		Queued, ms(now), ms(now), Running)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// requeueWaiting puts a waiting step back in the queue, in the attempt it
// is in (the claim counts it again): its node runs again where it waited.
// It says whether the step waited.
func (s *Store) requeueWaiting(runID, name string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`UPDATE steps SET status = ?, attempt = MAX(attempt - 1, 0), not_before = ?, queued_at = ?, wait_until = 0, lease_until = 0
		WHERE run_id = ? AND name = ? AND status = ?`, Queued, ms(now), ms(now), runID, name, Waiting)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// expiredWaits returns the waiting steps whose time is up.
func (s *Store) expiredWaits(now time.Time) ([]Step, error) {
	rows, err := s.db.Query(`SELECT `+stepCols+` FROM steps WHERE status = ? AND wait_until > 0 AND wait_until <= ?`, Waiting, ms(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Step
	for rows.Next() {
		st, err := scanStep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Prune deletes the runs that ended before cutoff, with their steps.
func (s *Store) Prune(cutoff time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM runs WHERE finished > 0 AND finished < ?`, ms(cutoff))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// QueueRow is what waits in the queue with the same resources (IMP-196):
// the steps queued, those whose time has come, and since when the oldest
// of these waits.
type QueueRow struct {
	Resources []string
	Queued    int
	Ready     int
	Oldest    time.Time
}

// Queue is the queue by resources, of the runs of projects (all for nil).
// A step waits since it entered the queue, its retry time, or its run's
// start, whichever comes last.
func (s *Store) Queue(projects []string, now time.Time) ([]QueueRow, error) {
	q := `SELECT s.resources, COUNT(*), SUM(CASE WHEN s.not_before <= ? THEN 1 ELSE 0 END),
		MIN(CASE WHEN s.not_before <= ? THEN MAX(s.queued_at, s.not_before, r.created) END)
		FROM steps s JOIN runs r ON r.id = s.run_id WHERE s.status = ?`
	args := []any{ms(now), ms(now), Queued}
	if projects != nil {
		if len(projects) == 0 {
			return nil, nil
		}
		q += ` AND r.project IN (?` + strings.Repeat(",?", len(projects)-1) + `)`
		for _, p := range projects {
			args = append(args, p)
		}
	}
	q += ` GROUP BY s.resources`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QueueRow
	for rows.Next() {
		var res string
		var row QueueRow
		var oldest sql.NullInt64
		if err := rows.Scan(&res, &row.Queued, &row.Ready, &oldest); err != nil {
			return nil, err
		}
		if res != "" {
			row.Resources = strings.Split(res, ",")
		}
		if oldest.Valid {
			row.Oldest = fromMS(oldest.Int64)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Running counts the steps running, of the runs of projects (all for nil).
func (s *Store) Running(projects []string) (int, error) {
	q := `SELECT COUNT(*) FROM steps s JOIN runs r ON r.id = s.run_id WHERE s.status = ?`
	args := []any{Running}
	if projects != nil {
		if len(projects) == 0 {
			return 0, nil
		}
		q += ` AND r.project IN (?` + strings.Repeat(",?", len(projects)-1) + `)`
		for _, p := range projects {
			args = append(args, p)
		}
	}
	var n int
	err := s.db.QueryRow(q, args...).Scan(&n)
	return n, err
}

// Ahead counts the steps whose time has come that the claim takes before
// the queued step name of run, in the claim's order (whatever their
// resources: a full one lets the steps after it go).
func (s *Store) Ahead(run Run, name string, now time.Time) (int, error) {
	var pos int
	if err := s.db.QueryRow(`SELECT pos FROM steps WHERE run_id = ? AND name = ?`, run.ID, name).Scan(&pos); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM steps s JOIN runs r ON r.id = s.run_id
		WHERE s.status = ? AND s.not_before <= ? AND (r.priority > ? OR (r.priority = ? AND (r.created < ? OR (r.created = ? AND (s.run_id < ? OR (s.run_id = ? AND s.pos < ?))))))`,
		Queued, ms(now), run.Priority, run.Priority, ms(run.Created), ms(run.Created), run.ID, run.ID, pos).Scan(&n)
	return n, err
}
