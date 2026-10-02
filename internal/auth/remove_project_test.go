package auth

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A deleted project leaves every token's scope: a token scoped to it alone
// is revoked (emptied it would be admin), one scoped to several loses it,
// expired ones included; admin and unrelated tokens stay (IMP-124).
func TestStore_RemoveProject(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	mk := func(name string, projects []string, ttl time.Duration) {
		t.Helper()
		if _, _, err := s.Create(name, projects, []string{ScopeRead}, ttl, ""); err != nil {
			t.Fatal(err)
		}
	}
	mk("only", []string{"gone"}, 0)
	mk("both", []string{"gone", "kept"}, 0)
	mk("other", []string{"kept"}, 0)
	mk("admin", nil, 0)
	mk("expired", []string{"gone"}, time.Nanosecond)
	// A legacy single-project record names the project in Project only.
	s.mu.Lock()
	s.tokens = append(s.tokens, Token{ID: "legacy01", Name: "legacy", Project: "gone", Scopes: []string{ScopeRead}})
	err = s.save()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)

	revoked, narrowed, err := s.RemoveProject("gone")
	if err != nil {
		t.Fatal(err)
	}
	if revoked != 3 || narrowed != 1 {
		t.Errorf("revoked=%d narrowed=%d, want 3 and 1", revoked, narrowed)
	}
	got := map[string][]string{}
	for _, tok := range s.List() {
		got[tok.Name] = tok.ProjectList()
	}
	for _, gone := range []string{"only", "expired", "legacy"} {
		if _, ok := got[gone]; ok {
			t.Errorf("token %q should be revoked", gone)
		}
	}
	if !slices.Equal(got["both"], []string{"kept"}) {
		t.Errorf("both = %v, want [kept]", got["both"])
	}
	// The stored shape is mint's: the legacy Project field holds the first
	// project, so a binary reading only it never sees an empty (admin) scope.
	for _, tok := range s.List() {
		if tok.Name == "both" && (tok.Project != "kept" || tok.Projects != nil) {
			t.Errorf("narrowed token stored as Project=%q Projects=%v, want Project=kept and no list", tok.Project, tok.Projects)
		}
	}
	if !slices.Equal(got["other"], []string{"kept"}) {
		t.Errorf("other = %v, want [kept]", got["other"])
	}
	if l, ok := got["admin"]; !ok || len(l) != 0 {
		t.Errorf("admin token = %v (present %v), want unscoped and present", l, ok)
	}

	// The file says the same after a reload.
	s2, err := Open(filepath.Join(filepath.Dir(s.path), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(s2.List()); n != 3 {
		t.Errorf("reloaded store has %d tokens, want 3", n)
	}
	if r, n, err := s.RemoveProject("gone"); r != 0 || n != 0 || err != nil {
		t.Errorf("second call = %d, %d, %v; want a no-op", r, n, err)
	}
}
