package gitsync

// TokenStore persists a single git-sync token (PAT) plaintext in
// <vault>/.gosidian/gitsync.json so the operator can rotate it from the web
// UI without restarting the container. Stored on disk in cleartext (perms
// 0o600) because git push needs the unhashed value at request time; it is
// never echoed back to the audit log nor surfaced to the web UI in full —
// only the last four characters are revealed via Mask().
//
// Precedence at runtime: env var (cfg.TokenEnv) wins over the file. This
// preserves the project's CLI > env > file > default convention so an
// operator can still override the file value with a one-shot env injection
// for debugging or CI.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TokenStore is the on-disk runtime token, concurrent-safe with mtime-based
// transparent reload (mirrors auth.Store / projects.Store).
type TokenStore struct {
	path  string
	mu    sync.RWMutex
	token string
	mtime time.Time
}

type tokenFile struct {
	Token     string    `json:"token"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OpenTokenStore loads (or initialises empty) the token file at the given
// path. A missing file is not an error.
func OpenTokenStore(path string) (*TokenStore, error) {
	t := &TokenStore{path: path}
	if err := t.load(); err != nil {
		return nil, err
	}
	return t, nil
}

// Path returns the on-disk file path. Useful for diagnostics.
func (t *TokenStore) Path() string { return t.path }

// load replaces the in-memory token from disk. Caller must hold mu.Lock or
// be in initialization context.
func (t *TokenStore) load() error {
	data, err := os.ReadFile(t.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.token = ""
			t.mtime = time.Time{}
			return nil
		}
		return err
	}
	if len(data) == 0 {
		t.token = ""
	} else {
		var tf tokenFile
		if err := json.Unmarshal(data, &tf); err != nil {
			return fmt.Errorf("parse gitsync token file: %w", err)
		}
		t.token = tf.Token
	}
	if st, err := os.Stat(t.path); err == nil {
		t.mtime = st.ModTime()
	}
	return nil
}

// reloadIfStale re-reads the file when its mtime diverges. Caller must hold
// mu.Lock.
func (t *TokenStore) reloadIfStale() {
	st, err := os.Stat(t.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !t.mtime.IsZero() || t.token != "" {
				t.token = ""
				t.mtime = time.Time{}
			}
		}
		return
	}
	if st.ModTime().Equal(t.mtime) {
		return
	}
	_ = t.load()
}

// Get returns the current plaintext token, or empty if unset.
func (t *TokenStore) Get() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reloadIfStale()
	return t.token
}

// Set writes the plaintext token atomically (write+rename, perms 0o600). An
// empty value is rejected — call Clear instead so the intent is explicit.
func (t *TokenStore) Set(plain string) error {
	if plain == "" {
		return fmt.Errorf("empty token: use Clear to remove the saved token")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(t.path), 0o755); err != nil {
		return err
	}
	tf := tokenFile{Token: plain, UpdatedAt: time.Now().UTC()}
	data, err := json.MarshalIndent(tf, "", "  ")
	if err != nil {
		return err
	}
	tmp := t.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, t.path); err != nil {
		return err
	}
	t.token = plain
	if st, err := os.Stat(t.path); err == nil {
		t.mtime = st.ModTime()
	}
	return nil
}

// Clear removes the on-disk token file. No-op if already absent.
func (t *TokenStore) Clear() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := os.Remove(t.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	t.token = ""
	t.mtime = time.Time{}
	return nil
}
