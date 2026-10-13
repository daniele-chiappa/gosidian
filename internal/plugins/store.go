package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/infolder"
)

// FileName is the file of the accepted manifests, in the state dir.
const FileName = "plugins.json"

// Accepted is a manifest the owner accepted.
type Accepted struct {
	SHA      string    `json:"sha"`
	Manifest Manifest  `json:"manifest"`
	At       time.Time `json:"accepted_at"`
	By       string    `json:"accepted_by,omitempty"`
}

// Bound is a source bound to a project's IN folder (IMP-172 phase 2), and
// when, by whom.
type Bound struct {
	infolder.Binding
	At time.Time `json:"bound_at"`
	By string    `json:"bound_by,omitempty"`
}

// Exposed is a plugin whose tools the agents of a project see (IMP-182),
// and when, by whom.
type Exposed struct {
	Plugin  string    `json:"plugin"`
	Project string    `json:"project"`
	At      time.Time `json:"exposed_at"`
	By      string    `json:"exposed_by,omitempty"`
}

type acceptedFile struct {
	Plugins  map[string]Accepted `json:"plugins"`
	Bindings []Bound             `json:"bindings,omitempty"`
	Exposed  []Exposed           `json:"exposed,omitempty"`
}

// store keeps the accepted manifests, the bound sources and the exposed
// plugins in
// <state-dir>/plugins.json. The CLI writes it while the server runs, so
// the server reads it again whenever the file changed.
type store struct {
	path string

	mu      sync.Mutex
	mod     time.Time
	size    int64
	cur     map[string]Accepted
	bound   []Bound
	exposed []Exposed
	err     error
}

func newStore(path string) *store { return &store{path: path} }

// all is the accepted manifests by plugin name; a file that cannot be read
// accepts none, and says why.
func (s *store) all() (map[string]Accepted, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fi, err := os.Stat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		s.cur, s.bound, s.exposed, s.err, s.mod, s.size = nil, nil, nil, nil, time.Time{}, 0
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s.cur != nil && fi.ModTime().Equal(s.mod) && fi.Size() == s.size {
		return s.cur, s.err
	}
	s.mod, s.size = fi.ModTime(), fi.Size()
	f, err := readAccepted(s.path)
	s.cur, s.bound, s.exposed, s.err = f.Plugins, f.Bindings, f.Exposed, err
	if s.cur == nil {
		s.cur = map[string]Accepted{}
	}
	return s.cur, s.err
}

// bindings is the bound sources.
func (s *store) bindings() ([]Bound, error) {
	_, err := s.all()
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Bound(nil), s.bound...), err
}

// exposures is the exposed plugins.
func (s *store) exposures() ([]Exposed, error) {
	_, err := s.all()
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Exposed(nil), s.exposed...), err
}

func readAccepted(path string) (acceptedFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return acceptedFile{}, err
	}
	var f acceptedFile
	if err := json.Unmarshal(b, &f); err != nil {
		return acceptedFile{}, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// set records a for the plugin name, or forgets it for nil.
func (s *store) set(name string, a *Accepted) error {
	return s.update(func(f *acceptedFile) error {
		if a == nil {
			delete(f.Plugins, name)
		} else {
			f.Plugins[name] = *a
		}
		return nil
	})
}

// update changes the file with change, which may refuse.
func (s *store) update(change func(*acceptedFile) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := readAccepted(s.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if cur.Plugins == nil {
		cur.Plugins = map[string]Accepted{}
	}
	if err := change(&cur); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cur, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".plugins-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
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
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return err
	}
	s.cur = nil // read again
	return nil
}
