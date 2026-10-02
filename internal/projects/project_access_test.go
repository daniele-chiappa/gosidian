package projects

import (
	"path/filepath"
	"testing"

	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/webauth"
)

// Access and SetAccess round-trip a project's entry, SetAccess replaces what
// the name held (a leftover entry's grants must not survive), and
// AccessConfigWith answers for a project from a saved Access (IMP-124).
func TestStore_AccessRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	team, err := s.CreateTeam("crew", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddTeamUser(team.ID, "u-team"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("p", Flags{Visibility: VisibilityPrivate, UseGlobals: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMember("p", "u-admin", LevelAdmin); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTeamGrant(team.ID, "p", LevelWrite); err != nil {
		t.Fatal(err)
	}
	a := s.Access("p")
	if a.Flags.Visibility != VisibilityPrivate || !a.Flags.UseGlobals || len(a.Members) != 1 || a.TeamGrants[team.ID] != LevelWrite {
		t.Fatalf("Access = %+v", a)
	}

	// A leftover entry under the target name is replaced, not merged.
	if err := s.Set("q", Flags{Visibility: VisibilityPublic}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMember("q", "u-stale", LevelAdmin); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("p"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAccess("q", a); err != nil {
		t.Fatal(err)
	}
	if s.Visibility("q") != VisibilityPrivate {
		t.Errorf("q visibility = %q, want private", s.Visibility("q"))
	}
	if _, ok := s.MemberLevel("q", "u-stale"); ok {
		t.Error("the leftover member grant survived SetAccess")
	}
	if lvl, _ := s.MemberLevel("q", "u-admin"); lvl != LevelAdmin {
		t.Errorf("u-admin on q = %q, want admin", lvl)
	}
	if got := s.TeamLevelsFor("u-team", "q"); len(got) != 1 || got[0].Level != LevelWrite {
		t.Errorf("team grant on q = %+v", got)
	}

	// A team deleted meanwhile is skipped.
	if err := s.DeleteTeam(team.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAccess("r", a); err != nil {
		t.Fatalf("SetAccess with a deleted team: %v", err)
	}

	// AccessConfigWith: the saved access answers for "gone", the store for
	// everything else.
	gone := Access{Flags: Flags{Visibility: VisibilityPrivate}, Members: []ProjectMember{{UserID: "u-admin", Level: LevelAdmin}}}
	cfg := s.AccessConfigWith("gone", gone)
	admin := authz.Principal{UserID: "u-admin", Role: webauth.RoleMember}
	other := authz.Principal{UserID: "u-other", Role: webauth.RoleMember}
	if lvl := admin.Level("gone", cfg); lvl != authz.LevelAdmin {
		t.Errorf("admin on trashed project = %v, want admin", lvl)
	}
	if lvl := other.Level("gone", cfg); lvl != authz.LevelNone {
		t.Errorf("stranger on trashed private project = %v, want none", lvl)
	}
	if lvl := admin.Level("q", cfg); lvl != authz.LevelAdmin {
		t.Errorf("live project through the overlay = %v, want admin", lvl)
	}
}
