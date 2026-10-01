package projectops

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/vault"
)

type fixture struct {
	v      *vault.Vault
	idx    *index.Index
	ps     *projects.Store
	tokens *auth.Store
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(dir, "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	ps, err := projects.Open(filepath.Join(dir, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := auth.Open(filepath.Join(dir, "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	return fixture{vault.New(filepath.Join(dir, "vault")), idx, ps, tokens}
}

func (f fixture) note(t *testing.T, path string) {
	t.Helper()
	if err := f.v.Save(path, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := f.idx.Upsert(index.NoteDoc{Path: path, Title: filepath.Base(path), Body: "x"}); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) scopes(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, tok := range f.tokens.List() {
		out[tok.Name] = tok.ProjectList()
	}
	return out
}

// A rename carries the project's visibility, member and team grants, index
// entries and the MCP tokens scoped to it (BUG-078, IMP-123).
func TestRename_CarriesAccessAndTokens(t *testing.T) {
	f := newFixture(t)
	f.note(t, "alpha/a.md")
	f.note(t, "beta/b.md")
	fl := f.ps.Get("alpha")
	fl.Visibility = projects.VisibilityPrivate
	if err := f.ps.Set("alpha", fl); err != nil {
		t.Fatal(err)
	}
	if err := f.ps.SetMember("alpha", "u1", projects.LevelWrite); err != nil {
		t.Fatal(err)
	}
	team, err := f.ps.CreateTeam("crew", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.ps.SetTeamGrant(team.ID, "alpha", projects.LevelRead); err != nil {
		t.Fatal(err)
	}
	for name, scope := range map[string][]string{"single": {"alpha"}, "multi": {"beta", "alpha"}, "other": {"beta"}, "admin": nil} {
		if _, _, err := f.tokens.Create(name, scope, []string{auth.ScopeRead}, 0, ""); err != nil {
			t.Fatal(err)
		}
	}

	moved, err := Rename(f.v, f.idx, f.ps, f.tokens, "alpha", "gamma")
	if err != nil || moved != 2 {
		t.Fatalf("Rename = %d, %v; want 2 tokens moved", moved, err)
	}
	if f.ps.Visibility("gamma") != projects.VisibilityPrivate {
		t.Errorf("gamma visibility %q, want private", f.ps.Visibility("gamma"))
	}
	if lvl, _ := f.ps.MemberLevel("gamma", "u1"); lvl != projects.LevelWrite || f.ps.TeamsCount("gamma") != 1 {
		t.Errorf("grants did not follow: member %q, teams %d", lvl, f.ps.TeamsCount("gamma"))
	}
	if rows, _ := f.idx.NotesByPrefix("gamma"); len(rows) != 1 {
		t.Errorf("index under gamma: %d notes", len(rows))
	}
	got := f.scopes(t)
	if !slices.Equal(got["single"], []string{"gamma"}) || !slices.Equal(got["multi"], []string{"beta", "gamma"}) ||
		!slices.Equal(got["other"], []string{"beta"}) || len(got["admin"]) != 0 {
		t.Errorf("token scopes after the rename: %v", got)
	}
}

// Onto an existing project nothing moves; a failed folder rename puts the
// access entry back.
func TestRename_RefusedLeavesEverything(t *testing.T) {
	f := newFixture(t)
	f.note(t, "alpha/a.md")
	f.note(t, "beta/b.md")
	fl := f.ps.Get("alpha")
	fl.Visibility = projects.VisibilityPrivate
	if err := f.ps.Set("alpha", fl); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.tokens.Create("single", []string{"alpha"}, []string{auth.ScopeRead}, 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Rename(f.v, f.idx, f.ps, f.tokens, "alpha", "beta"); err == nil || errors.Is(err, ErrIncomplete) {
		t.Fatalf("rename onto an existing project: %v", err)
	}
	if _, err := Rename(f.v, f.idx, f.ps, f.tokens, "alpha", "bad:name"); err == nil {
		t.Fatal("rename to an invalid name succeeded")
	}
	if f.ps.Visibility("alpha") != projects.VisibilityPrivate || !Exists(f.v, "alpha") || !slices.Equal(f.scopes(t)["single"], []string{"alpha"}) {
		t.Errorf("a refused rename changed something: vis %q, scopes %v", f.ps.Visibility("alpha"), f.scopes(t))
	}
}

// Nothing changes when the source is missing or when MCP tokens already name
// the target; a stale access entry under the target is replaced, not merged.
func TestRename_Checks(t *testing.T) {
	f := newFixture(t)
	f.note(t, "alpha/a.md")
	if err := f.ps.SetMember("ghost", "intruder", projects.LevelAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := Rename(f.v, f.idx, f.ps, f.tokens, "nope", "ghost"); err == nil {
		t.Fatal("renaming a missing project succeeded")
	}
	if _, ok := f.ps.MemberLevel("nope", "intruder"); ok {
		t.Error("a refused rename moved the stale grant onto the missing source")
	}

	if _, _, err := f.tokens.Create("old-agent", []string{"taken"}, []string{auth.ScopeRead}, 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Rename(f.v, f.idx, f.ps, f.tokens, "alpha", "taken"); err == nil {
		t.Fatal("rename onto a name MCP tokens are scoped to succeeded")
	}
	if !Exists(f.v, "alpha") {
		t.Fatal("a refused rename moved the folder")
	}

	if _, err := Rename(f.v, f.idx, f.ps, f.tokens, "alpha", "ghost"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.ps.MemberLevel("ghost", "intruder"); ok {
		t.Error("the stale grant under ghost was merged into the renamed project")
	}
}
