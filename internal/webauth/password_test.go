package webauth

import (
	"errors"
	"path/filepath"
	"testing"
)

// A password is checked and replaced in place, the temporary mark kept
// with it on disk; an LDAP account has none to check or set (IMP-063).
func TestPasswordLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Setup("owner", "owner-pass-1234", false, "test"); err != nil {
		t.Fatal(err)
	}
	u, err := s.AddUser("ada", "ada-pass-1234", RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckPassword(u.ID, "ada-pass-1234"); err != nil {
		t.Errorf("right password: %v", err)
	}
	if err := s.CheckPassword(u.ID, "nope"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong password: %v", err)
	}
	if err := s.CheckPassword("no-such-id", "x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("unknown account: %v", err)
	}
	if err := s.SetPassword(u.ID, "short", false); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("short: %v", err)
	}
	if err := s.SetPassword(u.ID, "temp-pass-1234", true); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reopened.UserByID(u.ID)
	if !got.MustChangePassword || reopened.CheckPassword(u.ID, "temp-pass-1234") != nil || reopened.CheckPassword(u.ID, "ada-pass-1234") == nil {
		t.Errorf("after the reset: %+v", got)
	}
	if err := reopened.SetMustChangePassword(u.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := reopened.UserByID(u.ID); got.MustChangePassword {
		t.Error("the mark is still set")
	}

	l, err := s.AddLDAPUser("lin")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckPassword(l.ID, "anything"); !errors.Is(err, ErrNotLocalAccount) {
		t.Errorf("LDAP check: %v", err)
	}
	if err := s.SetPassword(l.ID, "a-long-password", false); !errors.Is(err, ErrNotLocalAccount) {
		t.Errorf("LDAP set: %v", err)
	}

	// The console's setup chooses the owner's password: no mark.
	owner := s.FirstOwner()
	if err := s.SetMustChangePassword(owner.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Setup("owner", "owner-pass-5678", false, "test"); err != nil {
		t.Fatal(err)
	}
	if o, _ := s.UserByID(owner.ID); o.MustChangePassword {
		t.Error("setup left the mark on the owner")
	}
}

// A change made to auth.json by another process (a `gosidian user` command
// while the server runs) survives the next change the server saves: every
// mutator starts from the file on disk, not from the copy of the last
// sign-in (BUG-111, S1-4).
func TestMutatorsKeepChangesFromAnotherProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	server, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Setup("owner", "owner-pass-1234", false, "test"); err != nil {
		t.Fatal(err)
	}
	ada, err := server.AddUser("ada", "ada-pass-1234", RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := server.AddUser("bob", "bob-pass-1234", RoleMember)
	if err != nil {
		t.Fatal(err)
	}

	cli, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cli.SetPassword(ada.ID, "ada-reset-5678", true); err != nil {
		t.Fatal(err)
	}
	// The server saves an unrelated change, with no sign-in in between.
	if err := server.SetMustChangePassword(bob.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := server.SetRestricted(bob.ID, false); err != nil {
		t.Fatal(err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.CheckPassword(ada.ID, "ada-reset-5678"); err != nil {
		t.Errorf("the reset from the other process was undone: %v", err)
	}
	if u, _ := again.UserByID(bob.ID); !u.MustChangePassword || u.Restricted {
		t.Errorf("the server's own changes are lost: %+v", u)
	}
}
