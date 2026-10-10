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

type acceptedFile struct {
	Plugins map[string]Accepted `json:"plugins"`
}

// store keeps the accepted manifests in <state-dir>/plugins.json. The CLI
// writes it while the server runs, so the server reads it again whenever
// the file changed.
type store struct {
	path string

	mu   sync.Mutex
	mod  time.Time
	size int64
	cur  map[string]Accepted
	err  error
}

func newStore(path string) *store { return &store{path: path} }

// all is the accepted manifests by plugin name; a file that cannot be read
// accepts none, and says why.
func (s *store) all() (map[string]Accepted, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fi, err := os.Stat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		s.cur, s.err, s.mod, s.size = nil, nil, time.Time{}, 0
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s.cur != nil && fi.ModTime().Equal(s.mod) && fi.Size() == s.size {
		return s.cur, s.err
	}
	s.mod, s.size = fi.ModTime(), fi.Size()
	s.cur, s.err = readAccepted(s.path)
	if s.cur == nil {
		s.cur = map[string]Accepted{}
	}
	return s.cur, s.err
}

func readAccepted(path string) (map[string]Accepted, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f acceptedFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f.Plugins, nil
}

// set records a for the plugin name, or forgets it for nil.
func (s *store) set(name string, a *Accepted) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := readAccepted(s.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if cur == nil {
		cur = map[string]Accepted{}
	}
	if a == nil {
		delete(cur, name)
	} else {
		cur[name] = *a
	}
	b, err := json.MarshalIndent(acceptedFile{Plugins: cur}, "", "  ")
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
