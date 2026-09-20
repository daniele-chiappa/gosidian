package webauth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// setupOwner provisions the first owner and returns their user id.
func setupOwner(t *testing.T, s *Store, username, password string) string {
	t.Helper()
	if _, err := s.Setup(username, password, false, "Gosidian"); err != nil {
		t.Fatal(err)
	}
	u := s.FirstOwner()
	if u == nil {
		t.Fatal("FirstOwner returned nil after Setup")
	}
	return u.ID
}

func TestStore_DisabledByDefault(t *testing.T) {
	s := newStore(t)
	if s.Enabled() {
		t.Errorf("expected disabled")
	}
	if _, err := s.Verify("x", "y", ""); err == nil {
		t.Errorf("verify should fail on disabled")
	}
}

func TestStore_SetupAndVerify(t *testing.T) {
	s := newStore(t)
	uri, err := s.Setup("admin", "s3cretp4ss", false, "Gosidian")
	if err != nil {
		t.Fatal(err)
	}
	if uri != "" {
		t.Errorf("no TOTP requested: uri = %q", uri)
	}
	if !s.Enabled() {
		t.Errorf("should be enabled")
	}
	u, err := s.Verify("admin", "s3cretp4ss", "")
	if err != nil {
		t.Errorf("valid creds rejected: %v", err)
	}
	if u == nil || u.Role != RoleOwner {
		t.Errorf("first user should be owner, got %+v", u)
	}
	if _, err := s.Verify("admin", "wrong", ""); err == nil {
		t.Errorf("wrong password accepted")
	}
	if _, err := s.Verify("other", "s3cretp4ss", ""); err == nil {
		t.Errorf("wrong username accepted")
	}
}

func TestStore_SetupWithTOTP(t *testing.T) {
	s := newStore(t)
	uri, err := s.Setup("admin", "longenoughpass", true, "Gosidian")
	if err != nil {
		t.Fatal(err)
	}
	if uri == "" {
		t.Fatalf("expected otpauth URI")
	}
	if !s.TOTPEnabled() {
		t.Errorf("TOTP should be enabled")
	}
	// Under the policy model an enrolled secret is enforced once the global
	// mode is optional/required ("off" keeps it dormant). Opt in so this test
	// exercises enforcement.
	s.SetTOTPMode(TOTPOptional)

	// Extract secret from the store to generate a valid code.
	s.mu.RLock()
	sec := s.file.Users[0].TOTPSec
	s.mu.RUnlock()
	code, err := totp.GenerateCode(sec, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Verify("admin", "longenoughpass", code); err != nil {
		t.Errorf("valid TOTP rejected: %v", err)
	}
	if _, err := s.Verify("admin", "longenoughpass", "000000"); err == nil {
		t.Errorf("bogus TOTP accepted")
	}
	if _, err := s.Verify("admin", "longenoughpass", ""); err == nil {
		t.Errorf("missing TOTP accepted")
	}
}

func TestTOTPResolution(t *testing.T) {
	enrolled := &User{TOTPSec: "SECRET"}
	bare := &User{}
	cases := []struct {
		name   string
		mode   string
		u      *User
		active bool
	}{
		{"off+enrolled dormant", TOTPOff, enrolled, false},
		{"off+bare", TOTPOff, bare, false},
		{"optional+enrolled", TOTPOptional, enrolled, true},
		{"optional+bare", TOTPOptional, bare, false},
		{"required+enrolled", TOTPRequired, enrolled, true},
		{"required+bare (must enrol)", TOTPRequired, bare, true},
		{"override enabled dormant under master-off", TOTPOff, &User{TOTPPolicy: TOTPEnabled}, false},
		{"override disabled exempts", TOTPRequired, &User{TOTPPolicy: TOTPDisabled, TOTPSec: "X"}, false},
	}
	for _, c := range cases {
		if got := totpActive(c.mode, c.u); got != c.active {
			t.Errorf("%s: totpActive(%q)=%v want %v", c.name, c.mode, got, c.active)
		}
	}
}

type fakeLDAP struct{ ok map[string]string }

func (f *fakeLDAP) Authenticate(username, password string) error {
	if p, ok := f.ok[username]; ok && p == password {
		return nil
	}
	return errors.New("ldap: invalid credentials")
}

func TestAuthenticate(t *testing.T) {
	s := newStore(t)
	if _, err := s.Setup("owner", "ownerpass1", false, "test"); err != nil {
		t.Fatal(err)
	}
	ld := &fakeLDAP{ok: map[string]string{"alice": "ldappass"}}

	// Unknown user + valid LDAP → auto-provisioned guest.
	res, err := s.Authenticate("alice", "ldappass", "", ld)
	if err != nil {
		t.Fatalf("ldap login: %v", err)
	}
	if u := res.User; u.Role != RoleGuest || u.AuthSource != "ldap" {
		t.Errorf("expected ldap guest, got role=%s src=%q", u.Role, u.AuthSource)
	}
	// Second login: existing ldap account, re-checked against LDAP.
	if _, err := s.Authenticate("alice", "ldappass", "", ld); err != nil {
		t.Errorf("second ldap login failed: %v", err)
	}
	// Wrong LDAP password → rejected.
	if _, err := s.Authenticate("alice", "wrong", "", ld); err == nil {
		t.Error("wrong ldap password accepted")
	}
	// Local owner shadows LDAP and authenticates locally.
	if _, err := s.Authenticate("owner", "ownerpass1", "", ld); err != nil {
		t.Errorf("local owner login failed: %v", err)
	}
	// A wrong local password is NOT retried against LDAP.
	if _, err := s.Authenticate("owner", "nope", "", ld); err == nil {
		t.Error("wrong local password accepted")
	}
	// Unknown user with LDAP disabled → rejected.
	if _, err := s.Authenticate("ghost", "x", "", nil); err == nil {
		t.Error("unknown user accepted with nil ldap")
	}
}

func TestStore_SetupValidation(t *testing.T) {
	s := newStore(t)
	if _, err := s.Setup("", "goodpass1", false, "Gosidian"); err == nil {
		t.Errorf("empty username should fail")
	}
	if _, err := s.Setup("admin", "short", false, "Gosidian"); err == nil {
		t.Errorf("short password should fail")
	}
}

func TestStore_Persistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s1, _ := Open(path)
	if _, err := s1.Setup("admin", "goodpassword", false, "Gosidian"); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Enabled() || s2.Username() != "admin" {
		t.Errorf("account not persisted: enabled=%v user=%q", s2.Enabled(), s2.Username())
	}
	if _, err := s2.Verify("admin", "goodpassword", ""); err != nil {
		t.Errorf("verify after reload: %v", err)
	}
}

func TestStore_Disable(t *testing.T) {
	s := newStore(t)
	_, _ = s.Setup("admin", "goodpassword", false, "Gosidian")
	if err := s.Disable(); err != nil {
		t.Fatal(err)
	}
	if s.Enabled() {
		t.Errorf("should be disabled after Disable()")
	}
}

func TestStore_Session(t *testing.T) {
	s := newStore(t)
	uid := setupOwner(t, s, "admin", "goodpassword")

	id, err := s.CreateSession(uid, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !s.ValidateSession(id) {
		t.Errorf("session not valid right after creation")
	}
	if u, ok := s.UserBySession(id); !ok || u.ID != uid {
		t.Errorf("UserBySession = (%+v, %v), want matching owner", u, ok)
	}
	s.RevokeSession(id)
	if s.ValidateSession(id) {
		t.Errorf("session still valid after revoke")
	}
}

func TestStore_ExpiredSession(t *testing.T) {
	s := newStore(t)
	uid := setupOwner(t, s, "admin", "goodpassword")
	id, _ := s.CreateSession(uid, -time.Second) // already expired
	if s.ValidateSession(id) {
		t.Errorf("expired session should be invalid")
	}
}

func TestStore_AddUserAndInvite(t *testing.T) {
	s := newStore(t)
	ownerID := setupOwner(t, s, "owner", "ownerpass1")

	// Owner creates an invite with short TTL.
	inv, err := s.CreateInvite(ownerID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Token == "" {
		t.Fatal("invite token empty")
	}
	found := s.FindInvite(inv.Token)
	if found == nil || !found.Pending() {
		t.Errorf("invite should be pending: %+v", found)
	}

	// Member signup via invite.
	member, err := s.AddUser("member1", "memberpass1", RoleMember)
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if member.Role != RoleMember {
		t.Errorf("member.Role = %q", member.Role)
	}
	if err := s.ClaimInvite(inv.Token, member.ID); err != nil {
		t.Fatal(err)
	}
	// After claim, FindInvite must no longer return it as pending.
	if s.FindInvite(inv.Token) != nil {
		t.Errorf("claimed invite should not be pending")
	}

	// Duplicate signup same username should fail.
	if _, err := s.AddUser("member1", "anotherpass1", RoleMember); err == nil {
		t.Errorf("duplicate username should fail")
	}

	// Verify member can log in.
	u, err := s.Verify("member1", "memberpass1", "")
	if err != nil {
		t.Errorf("member verify: %v", err)
	}
	if u == nil || u.Role != RoleMember {
		t.Errorf("expected member user, got %+v", u)
	}
}

func TestStore_DisableUserCascade(t *testing.T) {
	s := newStore(t)
	ownerID := setupOwner(t, s, "owner", "ownerpass1")
	member, _ := s.AddUser("alice", "alicepass1", RoleMember)

	// Owner cannot be disabled.
	if err := s.DisableUser(ownerID); err == nil {
		t.Errorf("owner disable should fail")
	}

	// Install cascade hook.
	var cascadedID string
	s.SetOnUserDisabled(func(id string) { cascadedID = id })

	// Create a session for the member, then disable — session must be gone.
	sid, _ := s.CreateSession(member.ID, time.Hour)
	if !s.ValidateSession(sid) {
		t.Fatal("session should be valid")
	}
	if err := s.DisableUser(member.ID); err != nil {
		t.Fatal(err)
	}
	if s.ValidateSession(sid) {
		t.Errorf("session should be evicted after disable")
	}
	if cascadedID != member.ID {
		t.Errorf("cascade called with %q, want %q", cascadedID, member.ID)
	}

	// Verify reports disabled.
	if _, err := s.Verify("alice", "alicepass1", ""); err == nil {
		t.Errorf("disabled user should not be able to login")
	}
}

func TestStore_LegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	// Write a legacy v1 file manually.
	legacy := []byte(`{"username":"legacy","hash":"x","totp_secret":"","updated_at":"2024-01-01T00:00:00Z"}`)
	if err := writeFile(path, legacy); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Enabled() {
		t.Fatal("should be enabled after legacy migration")
	}
	if s.Username() != "legacy" {
		t.Errorf("username = %q, want legacy", s.Username())
	}
	u := s.FirstOwner()
	if u == nil || u.Role != RoleOwner {
		t.Errorf("first user should be owner, got %+v", u)
	}
	if u.ID == "" {
		t.Error("derived user id should be non-empty")
	}
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

// ldapProvisionsLocal simulates the race behind BUG-052: while the LDAP bind
// is in flight, a local account with the same username appears (a signup or
// an admin create). The bind succeeds, AddLDAPUser then collides, and the
// re-fetch must refuse the local record instead of logging the LDAP caller
// into it.
type ldapProvisionsLocal struct{ store *Store }

func (l ldapProvisionsLocal) Authenticate(username, _ string) error {
	if _, err := l.store.AddUser(username, "local-password", RoleOwner); err != nil {
		return err
	}
	return nil
}

func TestAuthenticate_LDAPCollisionRefusesLocalAccount(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.Authenticate("bob", "ldap-password", "", ldapProvisionsLocal{store: s})
	if err == nil {
		t.Fatalf("LDAP bind must not unlock a same-name local account; got user %+v", u)
	}
	if local, ok := s.UserByUsername("bob"); !ok || local.AuthSource == "ldap" {
		t.Fatalf("local account must be left untouched: %+v ok=%v", local, ok)
	}
}
