package webauth

import (
	"path/filepath"
	"strings"
	"testing"
)

// The admin may reuse the username of a disabled account: that account is
// renamed and kept, the new one starts from nothing, and the old credentials
// never sign in again. /signup (AddUser) still refuses the name.
func TestAddUserReclaiming_DisabledUsername(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	setupOwner(t, s, "admin", "ownerpass1")
	old, err := s.AddUser("alice", "oldpassword", RoleMember)
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.AddUserReclaiming("alice", "newpassword", RoleMember); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("enabled holder: err %v, want already exists", err)
	}
	if err := s.DisableUser(old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddUser("alice", "newpassword", RoleMember); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("AddUser on a disabled name: err %v, want already exists", err)
	}

	held, _ := s.UserByID(old.ID)
	day := held.DisabledAt.UTC().Format("2006-01-02") // the archive name takes the disable's day
	created, archived, err := s.AddUserReclaiming("alice", "newpassword", RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if archived == nil || archived.ID != old.ID || archived.Username != "alice~disabled-"+day || archived.Enabled() {
		t.Fatalf("archived = %+v, want the old account renamed alice~disabled-%s, still disabled", archived, day)
	}
	if created.ID == old.ID || !created.Enabled() || !created.Restricted {
		t.Fatalf("created = %+v, want a new, enabled, restricted account", created)
	}
	if u, ok := s.UserByUsername("alice"); !ok || u.ID != created.ID {
		t.Fatalf("alice now resolves to %+v, want the new account", u)
	}
	if _, err := s.Authenticate("alice", "oldpassword", "", nil); err == nil {
		t.Error("the old password signs in")
	}
	if _, err := s.Authenticate("alice", "newpassword", "", nil); err != nil {
		t.Errorf("new account cannot sign in: %v", err)
	}

	// A second reuse on the same day does not collide with the first archive.
	if err := s.DisableUser(created.ID); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.UserByID(created.ID); again.DisabledAt.UTC().Format("2006-01-02") != day {
		t.Skip("the two disables fell on different UTC days")
	}
	_, again, err := s.AddUserReclaiming("alice", "thirdpassword", RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != created.ID || again.Username != "alice~disabled-"+day+"-2" {
		t.Fatalf("second archive = %+v, want alice~disabled-%s-2", again, day)
	}

	// Persisted: a fresh store reads the same names.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{old.ID: "alice~disabled-" + day, created.ID: "alice~disabled-" + day + "-2"} {
		if u, ok := s2.UserByID(id); !ok || u.Username != want {
			t.Errorf("reloaded %s = %+v, want username %s", id, u, want)
		}
	}
}
