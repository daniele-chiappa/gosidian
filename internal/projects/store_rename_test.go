package projects

import (
	"os"
	"path/filepath"
	"testing"
)

// A rename replaces whatever the target name had, so a stale entry cannot
// graft its grants onto the renamed project, and a failed save leaves memory
// as it was (BUG-078).
func TestStore_RenameReplacesAndRestores(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.SetMember("alpha", "u1", LevelWrite))
	must(s.SetMember("ghost", "intruder", LevelAdmin))
	team, err := s.CreateTeam("crew", "")
	must(err)
	must(s.SetTeamGrant(team.ID, "ghost", LevelRead))

	must(s.Rename("alpha", "ghost"))
	if lvl, _ := s.MemberLevel("ghost", "u1"); lvl != LevelWrite {
		t.Errorf("u1 on ghost = %q, want write", lvl)
	}
	if _, ok := s.MemberLevel("ghost", "intruder"); ok || s.TeamsCount("ghost") != 0 {
		t.Errorf("the stale grants on ghost survived the rename (teams %d)", s.TeamsCount("ghost"))
	}

	// Every later save fails: the store file becomes a directory.
	must(os.Remove(s.Path()))
	must(os.MkdirAll(s.Path(), 0o755))
	if err := s.Rename("ghost", "delta"); err == nil {
		t.Fatal("rename with a failing save succeeded")
	}
	if lvl, _ := s.MemberLevel("ghost", "u1"); lvl != LevelWrite {
		t.Errorf("after the failed save u1 on ghost = %q, want write", lvl)
	}
	if _, ok := s.MemberLevel("delta", "u1"); ok {
		t.Error("the failed rename left the grant on delta")
	}
}
