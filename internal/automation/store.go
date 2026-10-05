package automation

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// MaxLog caps the runs the state keeps.
const MaxLog = 200

// forgetAfter is how long the state keeps a rule no database declares any
// more, and a row's firing after its date: then they are dropped.
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
}

// Run is one execution of a rule, or its failure.
type Run struct {
	At       time.Time `json:"at"`
	Database string    `json:"database"`
	Rule     string    `json:"rule"`
	Action   string    `json:"action"`
	// Path is the note it wrote: the handoff or the snapshot.
	Path  string   `json:"path,omitempty"`
	Rows  []string `json:"rows,omitempty"`
	Error string   `json:"error,omitempty"`
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
// firings long past.
func (st *State) prune(now time.Time) {
	for k, rs := range st.Rules {
		if now.Sub(rs.LastSeen) > forgetAfter {
			delete(st.Rules, k)
			continue
		}
		for f, at := range rs.Fired {
			if now.Sub(at) > forgetAfter {
				delete(rs.Fired, f)
			}
		}
	}
}
