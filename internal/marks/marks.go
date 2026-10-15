// Package marks keeps the marks the pipelines put on notes (IMP-188):
// coloured spans of a note — a section, a paragraph, a chunk of
// note.sections — with a label and a text, which the web UI shows beside
// the note, never in it. A note's text stays the author's: marks written
// into it would change it, make a commit at each run, and grow old with
// the rules that made them.
//
// A pipeline writes a set of marks at a time, named (cut, review): a run
// replaces its set. Each set keeps the hash of the note it was made on, so
// a set made on an older text says so. The sets live in a SQLite file of
// the state dir, out of the vault and of git; a note renamed takes its
// sets along, a note deleted loses them.
package marks

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

// The colours of a mark: the theme's, readable in the light and the dark.
var Colors = []string{"blue", "green", "orange", "red", "cyan", "grey"}

// Limits of a set.
const (
	MaxMarks   = 500
	MaxSets    = 20
	MaxLabel   = 60
	MaxText    = 500
	maxSection = 1024
)

// Mark is a span of a note: a chunk of note.sections by its base, else a
// section by the path of its headings ("" the text before the first one),
// the whole of it or its paragraph Block (from 1).
type Mark struct {
	Base    string `json:"base,omitempty"`
	Section string `json:"section,omitempty"`
	Block   int    `json:"block,omitempty"`
	Color   string `json:"color"`
	Label   string `json:"label,omitempty"`
	Text    string `json:"text,omitempty"`
}

// Set is the marks a run put on a note.
type Set struct {
	Path     string `json:"path"`
	Name     string `json:"set"`
	Pipeline string `json:"pipeline,omitempty"`
	Run      string `json:"run,omitempty"`
	// Hash is the SHA-256 of the note the marks were made on.
	Hash    string    `json:"hash"`
	Created time.Time `json:"created"`
	Marks   []Mark    `json:"marks"`
}

var setName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// Check says what is wrong with a set, nil when nothing.
func (s Set) Check() error {
	if !setName.MatchString(s.Name) {
		return fmt.Errorf("set %q: lower-case letters, digits, _ and -, at most 32", s.Name)
	}
	if len(s.Marks) > MaxMarks {
		return fmt.Errorf("set %s: %d marks, at most %d", s.Name, len(s.Marks), MaxMarks)
	}
	for i, m := range s.Marks {
		switch {
		case !validColor(m.Color):
			return fmt.Errorf("marks[%d].color %q: %s", i, m.Color, strings.Join(Colors, ", "))
		case utf8.RuneCountInString(m.Label) > MaxLabel:
			return fmt.Errorf("marks[%d].label: at most %d characters", i, MaxLabel)
		case utf8.RuneCountInString(m.Text) > MaxText:
			return fmt.Errorf("marks[%d].text: at most %d characters", i, MaxText)
		case m.Block < 0 || len(m.Section) > maxSection || len(m.Base) > 256:
			return fmt.Errorf("marks[%d]: block from 1, a section of at most %d bytes, a base of at most 256", i, maxSection)
		}
	}
	return nil
}

func validColor(c string) bool {
	for _, x := range Colors {
		if c == x {
			return true
		}
	}
	return false
}

// Store is the sets of marks of the vault's notes.
type Store struct{ db *sql.DB }

const schema = `CREATE TABLE IF NOT EXISTS sets (
	path TEXT NOT NULL,
	name TEXT NOT NULL,
	pipeline TEXT NOT NULL DEFAULT '',
	run TEXT NOT NULL DEFAULT '',
	hash TEXT NOT NULL,
	created INTEGER NOT NULL,
	marks TEXT NOT NULL,
	PRIMARY KEY (path, name)
)`

// Open opens (making it) the store at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("marks: schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the store.
func (s *Store) Close() error { return s.db.Close() }

// Put replaces the set of its name on its note; a set without marks
// removes it. A note has at most MaxSets sets.
func (s *Store) Put(set Set) error {
	if err := set.Check(); err != nil {
		return err
	}
	if len(set.Marks) == 0 {
		_, err := s.db.Exec(`DELETE FROM sets WHERE path = ? AND name = ?`, set.Path, set.Name)
		return err
	}
	b, err := json.Marshal(set.Marks)
	if err != nil {
		return err
	}
	if set.Created.IsZero() {
		set.Created = time.Now()
	}
	// The count and the write in one transaction: two runs marking the
	// same note at once would both find room for one more set.
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM sets WHERE path = ? AND name != ?`, set.Path, set.Name).Scan(&n); err != nil {
		return err
	}
	if n >= MaxSets {
		return fmt.Errorf("note %s has %d sets of marks, at most %d", set.Path, n, MaxSets)
	}
	if _, err := tx.Exec(`INSERT INTO sets (path, name, pipeline, run, hash, created, marks) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (path, name) DO UPDATE SET pipeline = excluded.pipeline, run = excluded.run, hash = excluded.hash,
		created = excluded.created, marks = excluded.marks`,
		set.Path, set.Name, set.Pipeline, set.Run, set.Hash, set.Created.UnixMilli(), string(b)); err != nil {
		return err
	}
	return tx.Commit()
}

// Sets are the sets of a note, by name.
func (s *Store) Sets(path string) ([]Set, error) {
	rows, err := s.db.Query(`SELECT name, pipeline, run, hash, created, marks FROM sets WHERE path = ? ORDER BY name`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Set{}
	for rows.Next() {
		set := Set{Path: path}
		var created int64
		var raw string
		if err := rows.Scan(&set.Name, &set.Pipeline, &set.Run, &set.Hash, &created, &raw); err != nil {
			return nil, err
		}
		set.Created = time.UnixMilli(created).UTC()
		if err := json.Unmarshal([]byte(raw), &set.Marks); err != nil {
			return nil, err
		}
		out = append(out, set)
	}
	return out, rows.Err()
}

// Renamed moves the sets of a note, or of every note under a folder or a
// project (from and to ending in /), to their new path. SQLite counts the
// prefix: its substr counts characters, Go's len bytes.
func (s *Store) Renamed(from, to string) error {
	if strings.HasSuffix(from, "/") {
		_, err := s.db.Exec(`UPDATE OR REPLACE sets SET path = ? || substr(path, length(?) + 1) WHERE substr(path, 1, length(?)) = ?`,
			to, from, from, from)
		return err
	}
	_, err := s.db.Exec(`UPDATE OR REPLACE sets SET path = ? WHERE path = ?`, to, from)
	return err
}

// Deleted removes the sets of a note, or of every note under a folder or
// a project (path ending in /).
func (s *Store) Deleted(path string) error {
	if strings.HasSuffix(path, "/") {
		_, err := s.db.Exec(`DELETE FROM sets WHERE substr(path, 1, length(?)) = ?`, path, path)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM sets WHERE path = ?`, path)
	return err
}
