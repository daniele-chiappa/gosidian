package automation

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxLog caps the runs the state keeps.
const MaxLog = 200

// forgetAfter is how long the state keeps a rule no database declares any
// more, a slot's firing, and the firing of a row no longer among the rule's
// rows: then they are dropped.
const forgetAfter = 400 * 24 * time.Hour

// State is what the engine remembers between runs, in the state dir: when
// it first saw each rule, what each one already fired, and the last runs.
type State struct {
	Rules map[string]*RuleState `json:"rules"`
	Log   []Run                 `json:"log,omitempty"`
}

// RuleState is one rule's memory.
type RuleState struct {
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	// Fired holds, for a due rule, "<row path>@<date>" for each row handed
	// off; for an every rule, each slot fired (RFC 3339).
	Fired map[string]time.Time `json:"fired,omitempty"`
	// Error is the rule's last failure, logged once until it changes.
	Error string `json:"error,omitempty"`
	// Warning is a rule over MaxRows rows, logged once until it changes.
	Warning string `json:"warning,omitempty"`
}

// Run is one execution of a rule, or its failure.
type Run struct {
	At       time.Time `json:"at"`
	Database string    `json:"database"`
	Rule     string    `json:"rule"`
	Action   string    `json:"action"`
	// Path is the note it wrote: the handoff or the snapshot.
	Path    string   `json:"path,omitempty"`
	Rows    []string `json:"rows,omitempty"`
	Error   string   `json:"error,omitempty"`
	Warning string   `json:"warning,omitempty"`
}

// Store keeps the State in a JSON file, written whole through a temporary
// file so a crash never leaves half of it.
type Store struct {
	path string
}

// NewStore keeps the state at path; "" keeps it in memory only.
func NewStore(path string) *Store { return &Store{path: path} }

// Load reads the state; a missing file is an empty state.
func (s *Store) Load() (*State, error) {
	st := &State{Rules: map[string]*RuleState{}}
	if s.path == "" {
		return st, nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, err
	}
	if st.Rules == nil {
		st.Rules = map[string]*RuleState{}
	}
	return st, nil
}

// Save writes the state.
func (s *Store) Save(st *State) error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".automations-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// rule returns the state of the rule at key, created as first seen now.
func (st *State) rule(key string, now time.Time) *RuleState {
	rs := st.Rules[key]
	if rs == nil {
		rs = &RuleState{FirstSeen: now, Fired: map[string]time.Time{}}
		st.Rules[key] = rs
	}
	if rs.Fired == nil {
		rs.Fired = map[string]time.Time{}
	}
	return rs
}

func (st *State) log(r Run) {
	st.Log = append(st.Log, r)
	if len(st.Log) > MaxLog {
		st.Log = append([]Run(nil), st.Log[len(st.Log)-MaxLog:]...)
	}
}

// prune drops the rules no database declared for a long time, and the
// slots fired long ago. A row's firing ("<path>@<date>", rowKey) stays
// while the row does: dropped by age, a row still past its date was handed
// off again (IMP-162, S4-14). The engine drops it when the rule no longer
// reads the row (forgetRows).
func (st *State) prune(now time.Time) {
	for k, rs := range st.Rules {
		if now.Sub(rs.LastSeen) > forgetAfter {
			delete(st.Rules, k)
			continue
		}
		for f, at := range rs.Fired {
			if !isRowKey(f) && now.Sub(at) > forgetAfter {
				delete(rs.Fired, f)
			}
		}
	}
}

// forgetRows drops the firings, long past, of the rows not in rows.
func (rs *RuleState) forgetRows(rows []Row, now time.Time) {
	keep := make(map[string]bool, len(rows))
	for _, r := range rows {
		keep[rowKey(r)] = true
	}
	for f, at := range rs.Fired {
		if isRowKey(f) && !keep[f] && now.Sub(at) > forgetAfter {
			delete(rs.Fired, f)
		}
	}
}

// isRowKey tells a row's firing from a slot's (RFC 3339, without "@").
func isRowKey(k string) bool { return strings.Contains(k, "@") }
