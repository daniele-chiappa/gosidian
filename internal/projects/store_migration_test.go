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
	  "members": {"priv": [{"user_id": "u1", "level": "read"}, {"user_id": "u2", "level": "admin"}]}
	}`)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := s.MigrateAccessModel([]string{"pub", "priv", "disk-only"}, []string{"u1", "u2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Applied || rep.LegacyMembersMode || rep.Projects != 3 || rep.DefaultVisibility != VisibilityInternal {
		t.Fatalf("report = %+v", rep)
	}
	// u1: write on pub and disk-only, its read on priv raised to write (3);
	// u2: write on pub and disk-only, its admin on priv kept (2).
	if rep.GrantsSeeded != 5 {
		t.Errorf("GrantsSeeded = %d want 5", rep.GrantsSeeded)
	}
	if lvl, _ := s.MemberLevel("priv", "u2"); lvl != LevelAdmin {
		t.Errorf("an admin grant must stay admin: %q", lvl)
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
	// Under the legacy default a read membership was inert: u1 wrote on
	// priv anyway, and keeping it read took that away (IMP-159, S1-15).
	if lvl, _ := s.MemberLevel("priv", "u1"); lvl != LevelWrite {
		t.Errorf("a legacy read grant must become write: %q", lvl)
	}
	if lvl, _ := s.MemberLevel("disk-only", "u2"); lvl != LevelWrite {
		t.Errorf("seeded grant missing: %q", lvl)
	}
	if s.DefaultVisibility() != VisibilityInternal {
		t.Error("upgraded installation must default new projects to internal")
	}

	// Idempotent, also across a reopen.
	if rep2, _ := s.MigrateAccessModel(nil, []string{"u3"}, nil); rep2.Applied {
		t.Error("second migration must be a no-op")
	}
	s2, _ := Open(path)
	if rep3, _ := s2.MigrateAccessModel(nil, []string{"u3"}, nil); rep3.Applied {
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
	rep, err := s.MigrateAccessModel([]string{"pub", "priv", "other"}, []string{"u1", "u2"}, nil)
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
	rep, err := s.MigrateAccessModel([]string{"a", "b"}, nil, nil)
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
	rep, err := s.MigrateAccessModel([]string{"a"}, []string{"u1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.GrantsSeeded != 1 || rep.DefaultVisibility != VisibilityInternal || s.Visibility("a") != VisibilityInternal {
		t.Fatalf("report = %+v, a=%s", rep, s.Visibility("a"))
	}
}

// Projects the configuration reserves to the owner (private global project,
// self-improve target) stay out of member reach under the legacy default:
// private, no seeded grants. A reserved project the owner had published keeps
// its visibility, and explicit memberships survive (BUG-061).
func TestMigrate_OwnerOnlyProjectsStayPrivate(t *testing.T) {
	path := writeLegacyFile(t, `{
	  "projects": {"global-private": {}, "shared": {"public": true}},
	  "members": {"insights": [{"user_id": "u1", "level": "read"}]}
	}`)
	s, _ := Open(path)
	rep, err := s.MigrateAccessModel(
		[]string{"global-private", "insights", "shared", "work"},
		[]string{"u1", "u2"},
		[]string{"global-private", "insights", "shared"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.OwnerOnly; len(got) != 2 || got[0] != "global-private" || got[1] != "insights" {
		t.Errorf("OwnerOnly = %v", got)
	}
	for _, n := range []string{"global-private", "insights"} {
		if v := s.Visibility(n); v != VisibilityPrivate {
			t.Errorf("%s visibility = %s, want private", n, v)
		}
		if _, ok := s.MemberLevel(n, "u2"); ok {
			t.Errorf("%s: seeded grant for u2", n)
		}
	}
	if lvl, _ := s.MemberLevel("insights", "u1"); lvl != LevelRead {
		t.Errorf("explicit membership on insights lost: %q", lvl)
	}
	if s.Visibility("shared") != VisibilityPublic {
		t.Error("a reserved project the owner published keeps public")
	}
	if s.Visibility("work") != VisibilityInternal {
		t.Error("other projects follow the legacy default")
	}
	if lvl, _ := s.MemberLevel("work", "u2"); lvl != LevelWrite {
		t.Errorf("work: seeded grant missing: %q", lvl)
	}
}
