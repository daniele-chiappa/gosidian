package webauth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readInitialPassword(t *testing.T, s *Store) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(s.InitialPasswordPath())
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b)), true
}

// On an empty store the first start creates the owner with a random
// password, written beside the accounts file (0600) and to be changed at
// the first sign-in; the owner's change removes the file (IMP-044).
func TestProvisionInitialOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	password, err := s.ProvisionInitialOwner("admin")
	if err != nil || len(password) < 20 {
		t.Fatalf("provision = %q, %v", password, err)
	}
	if got, ok := readInitialPassword(t, s); !ok || got != password {
		t.Fatalf("file = %q %v, want the password", got, ok)
	}
	if st, err := os.Stat(s.InitialPasswordPath()); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, %v", st.Mode().Perm(), err)
	}
	if filepath.Dir(s.InitialPasswordPath()) != filepath.Dir(path) {
		t.Errorf("file at %s, not beside the accounts", s.InitialPasswordPath())
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	owner := reopened.FirstOwner()
	if owner == nil || owner.Username != "admin" || !owner.MustChangePassword {
		t.Fatalf("owner = %+v", owner)
	}
	if _, err := reopened.Verify("admin", password, ""); err != nil {
		t.Errorf("sign in with the first password: %v", err)
	}

	// A second start finds the account and does nothing.
	if again, err := reopened.ProvisionInitialOwner("admin"); err != nil || again != "" {
		t.Errorf("second start = %q, %v", again, err)
	}
	if _, ok := readInitialPassword(t, reopened); !ok {
		t.Error("the file went away before the owner changed the password")
	}

	if err := reopened.SetPassword(owner.ID, "my-own-password", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := readInitialPassword(t, reopened); ok {
		t.Error("the file is still there after the owner's change")
	}
}

// Accounts already there: nothing is created, and a file left by an owner
// who has changed the password since goes away.
func TestProvisionInitialOwner_ExistingAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Setup("root", "root-pass-1234", false, "test"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.InitialPasswordPath(), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ProvisionInitialOwner("admin"); err != nil || got != "" {
		t.Fatalf("provision = %q, %v", got, err)
	}
	if _, ok := s.UserByUsername("admin"); ok {
		t.Error("an admin was created beside the existing owner")
	}
	if _, ok := readInitialPassword(t, s); ok {
		t.Error("the stale file was kept")
	}
}

// The console's `user setup` sets the owner's password: the file of the
// first start goes away too.
func TestProvisionInitialOwner_SetupRemovesFile(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProvisionInitialOwner("admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Setup("admin", "console-pass-1234", false, "test"); err != nil {
		t.Fatal(err)
	}
	if _, ok := readInitialPassword(t, s); ok {
		t.Error("the file is still there after user setup")
	}
	if o := s.FirstOwner(); o == nil || o.MustChangePassword {
		t.Errorf("owner after setup = %+v", o)
	}
}
