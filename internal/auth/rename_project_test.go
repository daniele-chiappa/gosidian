package auth

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
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

// A run token's write folders follow its project through a rename
// (IMP-195).
func TestStore_RenameProjectWritePaths(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create("admin", nil, []string{ScopeRead, ScopeWrite}, 0, ""); err != nil {
		t.Fatal(err)
	}
	_, before, err := s.CreateRun("run-1-a", "a", true, time.Minute, "r1", []string{"a/kb/"})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.RenameProject("a", "b"); err != nil || n != 1 {
		t.Fatalf("RenameProject = %d, %v; want 1", n, err)
	}
	after, _ := s.ByID(before.ID)
	if !slices.Equal(after.WritePaths, []string{"b/kb/"}) || !after.AllowsWrite("b/kb/x.md") {
		t.Errorf("renamed run token = %+v", after)
	}
	if !slices.Equal(before.WritePaths, []string{"a/kb/"}) {
		t.Errorf("the copy handed out changed: %v", before.WritePaths)
	}
}
