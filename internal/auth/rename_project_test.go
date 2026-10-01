package auth

import (
	"path/filepath"
	"slices"
	"testing"
)

// A rename rewrites the scopes naming the project, legacy single field
// included, without duplicating a project already in the list (IMP-123).
func TestStore_RenameProject(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	for name, scope := range map[string][]string{"single": {"a"}, "both": {"a", "b"}, "none": {"c"}} {
		if _, _, err := s.Create(name, scope, []string{ScopeRead}, 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.RenameProject("a", "b"); err != nil || n != 2 {
		t.Fatalf("RenameProject = %d, %v; want 2", n, err)
	}
	for _, tok := range s.List() {
		want := map[string][]string{"single": {"b"}, "both": {"b"}, "none": {"c"}}[tok.Name]
		if !slices.Equal(tok.ProjectList(), want) {
			t.Errorf("%s: scope %v, want %v", tok.Name, tok.ProjectList(), want)
		}
	}
	if n, _ := s.RenameProject("zzz", "y"); n != 0 {
		t.Errorf("renaming an unknown project changed %d tokens", n)
	}
}
