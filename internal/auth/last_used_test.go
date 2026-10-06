package auth

import (
	"path/filepath"
	"testing"
	"time"
)

// A use stamps LastUsedAt on disk, at most every lastUsedEvery; the write
// keeps a token another process added meanwhile (IMP-100).
func TestLastUsedAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	plain, tok, err := s.Create("agent", nil, []string{ScopeRead}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if !tok.LastUsedAt.IsZero() {
		t.Fatalf("a new token has LastUsedAt %v", tok.LastUsedAt)
	}

	// Another process (the CLI) adds a token behind this store's back.
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // a distinct mtime for the reload
	if _, _, err := other.Create("cli", nil, []string{ScopeRead}, 0, ""); err != nil {
		t.Fatal(err)
	}

	before := time.Now().UTC().Add(-time.Second)
	if _, err := s.Validate(plain); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var used time.Time
	names := map[string]bool{}
	for _, x := range reopened.List() {
		names[x.Name] = true
		if x.ID == tok.ID {
			used = x.LastUsedAt
		}
	}
	if used.Before(before) {
		t.Errorf("LastUsedAt on disk = %v, want about now", used)
	}
	if !names["cli"] {
		t.Error("the write lost the token the CLI added")
	}

	// A second use within lastUsedEvery does not move it.
	if _, err := s.Validate(plain); err != nil {
		t.Fatal(err)
	}
	for _, x := range s.List() {
		if x.ID == tok.ID && !x.LastUsedAt.Equal(used) {
			t.Errorf("LastUsedAt moved within lastUsedEvery: %v → %v", used, x.LastUsedAt)
		}
	}
}
