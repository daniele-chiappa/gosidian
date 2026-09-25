package projects

import (
	"path/filepath"
	"testing"
)

func TestProjectMembers_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := s.MemberLevel("Work", "u1"); ok {
		t.Fatal("expected no grant initially")
	}
	if err := s.SetMember("Work", "u1", LevelWrite); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMember("Work", "u2", LevelRead); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMember("Work", "u3", LevelAdmin); err != nil {
		t.Fatal(err)
	}
	if lvl, ok := s.MemberLevel("Work", "u1"); !ok || lvl != LevelWrite {
		t.Errorf("u1 level = %q,%v want write,true", lvl, ok)
	}
	if s.MembersCount("Work") != 3 {
		t.Errorf("MembersCount = %d want 3", s.MembersCount("Work"))
	}

	// Update level in place (no duplicate).
	if err := s.SetMember("Work", "u1", LevelRead); err != nil {
		t.Fatal(err)
	}
	if got := s.MembersOf("Work"); len(got) != 3 {
		t.Errorf("members = %d want 3 (update must not duplicate)", len(got))
	}
	if lvl, _ := s.MemberLevel("Work", "u1"); lvl != LevelRead {
		t.Errorf("u1 level after update = %q want read", lvl)
	}

	// Invalid level rejected.
	if err := s.SetMember("Work", "u4", "root"); err == nil {
		t.Error("expected error on invalid level")
	}

	// Persistence: reopen and re-read.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if lvl, ok := s2.MemberLevel("Work", "u3"); !ok || lvl != LevelAdmin {
		t.Errorf("persisted u3 = %q,%v want admin,true", lvl, ok)
	}
}

func TestProjectMembers_RemoveAndCascade(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "projects.json"))
	_ = s.SetMember("A", "u1", LevelWrite)
	_ = s.SetMember("A", "u2", LevelRead)
	_ = s.SetMember("B", "u1", LevelRead)

	if err := s.RemoveMember("A", "u1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.MemberLevel("A", "u1"); ok {
		t.Error("u1 still granted on A after RemoveMember")
	}
	if _, ok := s.MemberLevel("A", "u2"); !ok {
		t.Error("u2 must keep the grant on A")
	}

	// RemoveUserEverywhere drops u1 from B too.
	if err := s.RemoveUserEverywhere("u1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.MemberLevel("B", "u1"); ok {
		t.Error("u1 still granted on B after RemoveUserEverywhere")
	}
}

func TestProjectMembers_DeleteAndRename(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "projects.json"))
	_ = s.SetMember("Old", "u1", LevelWrite)

	// Delete drops grants even when the project has no Flags entry.
	if err := s.Delete("Old"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.MemberLevel("Old", "u1"); ok {
		t.Error("grants survived project Delete")
	}

	// Rename moves grants.
	_ = s.SetMember("Src", "u9", LevelRead)
	if err := s.Rename("Src", "Dst"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.MemberLevel("Src", "u9"); ok {
		t.Error("grants left behind at old name after Rename")
	}
	if lvl, ok := s.MemberLevel("Dst", "u9"); !ok || lvl != LevelRead {
		t.Errorf("grants not moved to new name: %q,%v", lvl, ok)
	}
}

func TestLevelRank(t *testing.T) {
	if !(LevelRank(LevelRead) < LevelRank(LevelWrite) && LevelRank(LevelWrite) < LevelRank(LevelAdmin)) {
		t.Error("levels must be ordered read < write < admin")
	}
	if LevelRank("root") != 0 {
		t.Error("unknown level must rank 0")
	}
}

func TestVisibility_DefaultsAndOverrides(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "projects.json"))
	if s.Visibility("nope") != VisibilityPrivate || s.DefaultVisibility() != VisibilityPrivate {
		t.Fatal("unknown project must be private by default")
	}
	if err := s.SetDefaultVisibility(VisibilityInternal); err != nil {
		t.Fatal(err)
	}
	if s.Visibility("nope") != VisibilityInternal {
		t.Error("unknown project must follow the store default")
	}
	if err := s.Set("p", Flags{Visibility: VisibilityPrivate}); err != nil {
		t.Fatal(err)
	}
	if s.Visibility("p") != VisibilityPrivate || s.IsPublic("p") {
		t.Error("explicit visibility must win over the default")
	}
	// The legacy Public flag still reads as public until migrated.
	if err := s.Set("legacy", Flags{Public: true}); err != nil {
		t.Fatal(err)
	}
	if !s.IsPublic("legacy") {
		t.Error("legacy Public flag must resolve to public")
	}
	if err := s.Set("bad", Flags{Visibility: "secret"}); err == nil {
		t.Error("invalid visibility must be rejected")
	}
	if err := s.SetDefaultVisibility("secret"); err == nil {
		t.Error("invalid default visibility must be rejected")
	}
}

func TestPersonalProjectsSwitch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.json")
	s, _ := Open(path)
	if !s.PersonalProjectsEnabled() {
		t.Fatal("personal projects must be on by default")
	}
	if err := s.SetPersonalProjects(false); err != nil {
		t.Fatal(err)
	}
	s2, _ := Open(path)
	if s2.PersonalProjectsEnabled() {
		t.Error("switch must persist")
	}
	if err := s2.SetPersonalProjects(true); err != nil || !s2.PersonalProjectsEnabled() {
		t.Error("switch back must work")
	}
}
