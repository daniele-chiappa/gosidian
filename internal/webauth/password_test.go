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
