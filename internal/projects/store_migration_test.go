package projects

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLegacyFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "projects.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Legacy default (member_scope unset): every member could read and write
// everywhere, so the upgrade makes existing projects internal, seeds write
// grants for the member accounts and keeps internal as the default.
func TestMigrate_LegacyAllSeedsWriteGrants(t *testing.T) {
	path := writeLegacyFile(t, `{
	  "projects": {"pub": {"public": true}, "priv": {"hidden_from_mcp": true}},
	  "members": {"priv": [{"user_id": "u1", "level": "read"}]}
	}`)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := s.MigrateAccessModel([]string{"pub", "priv", "disk-only"}, []string{"u1", "u2"})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Applied || rep.LegacyMembersMode || rep.Projects != 3 || rep.DefaultVisibility != VisibilityInternal {
		t.Fatalf("report = %+v", rep)
	}
	// u1 keeps its read grant on priv (2 seeded), u2 gets 3.
	if rep.GrantsSeeded != 5 {
		t.Errorf("GrantsSeeded = %d want 5", rep.GrantsSeeded)
	}
	if s.Visibility("pub") != VisibilityPublic || s.Visibility("priv") != VisibilityInternal || s.Visibility("disk-only") != VisibilityInternal {
		t.Errorf("visibilities: pub=%s priv=%s disk-only=%s", s.Visibility("pub"), s.Visibility("priv"), s.Visibility("disk-only"))
	}
	if s.Get("pub").Public {
		t.Error("legacy Public flag must be cleared")
	}
	if !s.Get("priv").HiddenFromMCP {
		t.Error("other flags must survive")
	}
	if lvl, _ := s.MemberLevel("priv", "u1"); lvl != LevelRead {
		t.Errorf("existing grant overwritten: %q", lvl)
	}
	if lvl, _ := s.MemberLevel("disk-only", "u2"); lvl != LevelWrite {
		t.Errorf("seeded grant missing: %q", lvl)
	}
	if s.DefaultVisibility() != VisibilityInternal {
		t.Error("upgraded installation must default new projects to internal")
	}

	// Idempotent, also across a reopen.
	if rep2, _ := s.MigrateAccessModel(nil, []string{"u3"}); rep2.Applied {
		t.Error("second migration must be a no-op")
	}
	s2, _ := Open(path)
	if rep3, _ := s2.MigrateAccessModel(nil, []string{"u3"}); rep3.Applied {
		t.Error("migration must persist its marker")
	}
	if _, ok := s2.MemberLevel("pub", "u3"); ok {
		t.Error("no seeding after the migration ran")
	}
}

// member_scope=members already gated private projects behind memberships:
// they become private, nothing is seeded, new projects default to private.
func TestMigrate_LegacyMembersModeKeepsGating(t *testing.T) {
	path := writeLegacyFile(t, `{
	  "projects": {"pub": {"public": true}, "priv": {}},
	  "members": {"priv": [{"user_id": "u1", "level": "write"}]},
	  "member_scope": "members"
	}`)
	s, _ := Open(path)
	rep, err := s.MigrateAccessModel([]string{"pub", "priv", "other"}, []string{"u1", "u2"})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Applied || !rep.LegacyMembersMode || rep.GrantsSeeded != 0 || rep.DefaultVisibility != VisibilityPrivate {
		t.Fatalf("report = %+v", rep)
	}
	if s.Visibility("pub") != VisibilityPublic || s.Visibility("priv") != VisibilityPrivate || s.Visibility("other") != VisibilityPrivate {
		t.Errorf("visibilities: pub=%s priv=%s other=%s", s.Visibility("pub"), s.Visibility("priv"), s.Visibility("other"))
	}
	if _, ok := s.MemberLevel("other", "u2"); ok {
		t.Error("members mode must not seed grants")
	}
}

// A fresh installation (no file, only the owner) is default-deny from the
// start: existing folders become private and so do new projects.
func TestMigrate_FreshInstallIsPrivate(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "projects.json"))
	rep, err := s.MigrateAccessModel([]string{"a", "b"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Applied || rep.Projects != 2 || rep.GrantsSeeded != 0 || rep.DefaultVisibility != VisibilityPrivate {
		t.Fatalf("report = %+v", rep)
	}
	if s.Visibility("a") != VisibilityPrivate || s.Visibility("new") != VisibilityPrivate {
		t.Error("fresh installation must be private everywhere")
	}
}

// No file but member accounts exist (an installation that never touched
// flags): they could read and write everywhere, so it is an upgrade.
func TestMigrate_NoFileWithMembersIsUpgrade(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "projects.json"))
	rep, err := s.MigrateAccessModel([]string{"a"}, []string{"u1"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.GrantsSeeded != 1 || rep.DefaultVisibility != VisibilityInternal || s.Visibility("a") != VisibilityInternal {
		t.Fatalf("report = %+v, a=%s", rep, s.Visibility("a"))
	}
}
