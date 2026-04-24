package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStore_EmptyThenCreate(t *testing.T) {
	s := newStore(t)
	if !s.Empty() {
		t.Errorf("expected empty store")
	}
	plain, tok, err := s.Create("demo", "", []string{ScopeRead, ScopeWrite}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if plain == "" || len(plain) < 40 {
		t.Errorf("plaintext too short: %q", plain)
	}
	if tok.ID == "" || len(tok.ID) != 8 {
		t.Errorf("id = %q", tok.ID)
	}
	if s.Empty() {
		t.Errorf("store should not be empty after create")
	}
}

func TestStore_Validate(t *testing.T) {
	s := newStore(t)
	plain, _, _ := s.Create("demo", "", []string{ScopeRead}, 0, "")

	got, err := s.Validate(plain)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "demo" {
		t.Errorf("got name %q", got.Name)
	}
	if !got.HasScope(ScopeRead) || got.HasScope(ScopeWrite) {
		t.Errorf("scopes wrong: %v", got.Scopes)
	}

	if _, err := s.Validate("gosidian_wrong"); err == nil {
		t.Errorf("bogus token should fail")
	}
	if _, err := s.Validate(""); err == nil {
		t.Errorf("empty should fail")
	}
}

func TestStore_Persistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	s1, _ := Open(path)
	plain, _, _ := s1.Create("first", "lavoro", []string{ScopeRead, ScopeWrite}, 0, "")

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Validate(plain)
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != "lavoro" {
		t.Errorf("project = %q", got.Project)
	}
}

func TestStore_Revoke(t *testing.T) {
	s := newStore(t)
	plain, tok, _ := s.Create("to-delete", "", []string{ScopeRead}, 0, "")
	if err := s.Revoke(tok.ID); err != nil {
		t.Fatal(err)
	}
	if !s.Empty() {
		t.Errorf("store should be empty after revoke")
	}
	if _, err := s.Validate(plain); err == nil {
		t.Errorf("revoked token should fail validation")
	}
	if err := s.Revoke("deadbeef"); err == nil {
		t.Errorf("revoke of missing id should fail")
	}
}

func TestStore_Expired(t *testing.T) {
	s := newStore(t)
	plain, _, _ := s.Create("ephemeral", "", []string{ScopeRead}, -time.Second, "")
	if _, err := s.Validate(plain); err == nil {
		t.Errorf("expired token should fail")
	}
}

func TestToken_AllowsPath(t *testing.T) {
	admin := &Token{}
	scoped := &Token{Project: "lavoro"}

	cases := []struct {
		tok  *Token
		path string
		want bool
	}{
		{admin, "any/note.md", true},
		{admin, "lavoro/x.md", true},
		{scoped, "lavoro/x.md", true},
		{scoped, "lavoro", true},
		{scoped, "other/x.md", false},
		{scoped, "lavorone/x.md", false},
	}
	for _, c := range cases {
		if got := c.tok.AllowsPath(c.path); got != c.want {
			t.Errorf("AllowsPath(%q) project=%q = %v, want %v", c.path, c.tok.Project, got, c.want)
		}
	}
}

// TestStore_HotReload exercises IMP-006 / BUG-004: a token created by a
// second actor after Open() becomes effective without restart, thanks to the
// lazy mtime-check in Validate / Empty.
func TestStore_HotReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	s1, _ := Open(path)
	if !s1.Empty() {
		t.Fatalf("expected empty store")
	}

	// Simulate a second process (the CLI) writing a token to the same file.
	s2, _ := Open(path)
	plain, _, err := s2.Create("via-cli", "", []string{ScopeRead}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	// Force mtime to advance past Open()'s recorded value — on fast
	// filesystems the write can share the same second as Open's stat.
	future := time.Now().Add(time.Second)
	_ = os.Chtimes(path, future, future)

	// s1 was opened before the write — hot-reload must pick up the new token.
	if s1.Empty() {
		t.Errorf("s1 should no longer be empty after external write")
	}
	tok, err := s1.Validate(plain)
	if err != nil {
		t.Fatalf("hot-reloaded token rejected: %v", err)
	}
	if tok.Name != "via-cli" {
		t.Errorf("unexpected token: %+v", tok)
	}
}

// TestStore_HotReload_FileDeleted ensures removing the file drops the
// in-memory snapshot (reverts to auth-disabled bootstrap).
func TestStore_HotReload_FileDeleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	s, _ := Open(path)
	plain, _, _ := s.Create("x", "", []string{ScopeRead}, 0, "")
	if s.Empty() {
		t.Fatal("store should have 1 token")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if !s.Empty() {
		t.Errorf("Empty() should observe the deletion")
	}
	if _, err := s.Validate(plain); err == nil {
		t.Errorf("Validate should fail after file deletion")
	}
}

func TestExtractBearer(t *testing.T) {
	if got := ExtractBearer("Bearer xyz"); got != "xyz" {
		t.Errorf("got %q", got)
	}
	if got := ExtractBearer("bearer abc"); got != "abc" {
		t.Errorf("got %q (case-insensitive)", got)
	}
	if got := ExtractBearer(""); got != "" {
		t.Errorf("empty should be empty")
	}
	if got := ExtractBearer("Basic xyz"); got != "" {
		t.Errorf("non-bearer should be empty")
	}
}
