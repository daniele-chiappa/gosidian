package webauth

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

// enrolWithCodes provisions an owner with TOTP under the optional mode and
// returns the user id, the secret and the plaintext recovery codes.
func enrolWithCodes(t *testing.T, s *Store) (id, secret string, codes []string) {
	t.Helper()
	id = setupOwner(t, s, "owner", "ownerpass1")
	s.SetTOTPMode(TOTPOptional)
	secret, _, err := s.GenerateTOTPSecret("owner", "test")
	if err != nil {
		t.Fatal(err)
	}
	codes, err = s.EnrollTOTP(id, secret)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Fatalf("EnrollTOTP returned %d codes, want %d", len(codes), RecoveryCodeCount)
	}
	return id, secret, codes
}

func TestRecoveryCodes_Shape(t *testing.T) {
	plain, stored, err := newRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != RecoveryCodeCount || len(stored) != RecoveryCodeCount {
		t.Fatalf("got %d/%d codes, want %d", len(plain), len(stored), RecoveryCodeCount)
	}
	shape := regexp.MustCompile(`^[` + recoveryAlphabet + `]{5}-[` + recoveryAlphabet + `]{5}$`)
	seen := map[string]bool{}
	for i, c := range plain {
		if !shape.MatchString(c) {
			t.Errorf("code %q does not match the display shape", c)
		}
		if seen[c] {
			t.Errorf("duplicate code %q", c)
		}
		seen[c] = true
		if !strings.HasPrefix(stored[i].Hash, "$2a$") || stored[i].UsedAt != nil {
			t.Errorf("stored[%d] = %+v, want fresh bcrypt record", i, stored[i])
		}
		if !looksLikeRecoveryCode(normalizeRecoveryCode(c)) {
			t.Errorf("own code %q not recognised as a recovery code", c)
		}
	}
	// Normalisation tolerates case, dashes and spaces; the shape check keeps
	// TOTP-looking input and wrong lengths away from bcrypt.
	if got := normalizeRecoveryCode(" ab-cde fgh "); got != "ABCDEFGH" {
		t.Errorf("normalize = %q", got)
	}
	for _, bad := range []string{"123456", "ABCDEFGHJ", "ABCDEFGHJKM", "ABCDEFGH1K", "abcdefgh0k"} {
		if looksLikeRecoveryCode(normalizeRecoveryCode(bad)) {
			t.Errorf("%q must not look like a recovery code", bad)
		}
	}
}

func TestRecoveryCodes_LoginConsumesOnce(t *testing.T) {
	s := newStore(t)
	_, secret, codes := enrolWithCodes(t, s)

	// A TOTP still works and does not count as recovery.
	code, _ := totp.GenerateCode(secret, time.Now())
	res, err := s.Authenticate("owner", "ownerpass1", code, nil)
	if err != nil || res.RecoveryCodeUsed {
		t.Fatalf("totp login: err=%v used=%v", err, res.RecoveryCodeUsed)
	}
	if got := res.User.RecoveryCodesRemaining(); got != RecoveryCodeCount {
		t.Errorf("remaining after totp login = %d, want %d", got, RecoveryCodeCount)
	}

	// A recovery code works once — lowercase, no dash — and the returned
	// user carries the decremented counter.
	res, err = s.Authenticate("owner", "ownerpass1", strings.ToLower(strings.ReplaceAll(codes[0], "-", "")), nil)
	if err != nil {
		t.Fatalf("recovery login: %v", err)
	}
	if !res.RecoveryCodeUsed {
		t.Error("RecoveryCodeUsed not flagged")
	}
	if got := res.User.RecoveryCodesRemaining(); got != RecoveryCodeCount-1 {
		t.Errorf("remaining = %d, want %d", got, RecoveryCodeCount-1)
	}
	if _, err := s.Authenticate("owner", "ownerpass1", codes[0], nil); !errors.Is(err, ErrInvalidSecondFactor) {
		t.Errorf("reused code: err = %v, want ErrInvalidSecondFactor", err)
	}
	// A wrong password never reaches the second factor: the account limiter
	// relies on this distinction.
	if _, err := s.Authenticate("owner", "wrong", codes[1], nil); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong password: err = %v, want ErrInvalidCredentials", err)
	}
	if _, err := s.Authenticate("owner", "ownerpass1", "000000", nil); !errors.Is(err, ErrInvalidSecondFactor) {
		t.Errorf("wrong totp: err = %v, want ErrInvalidSecondFactor", err)
	}
	if _, err := s.Authenticate("owner", "ownerpass1", "", nil); !errors.Is(err, ErrInvalidSecondFactor) {
		t.Errorf("missing code: err = %v, want ErrInvalidSecondFactor", err)
	}
	// The other codes are untouched.
	if res, err := s.Authenticate("owner", "ownerpass1", codes[1], nil); err != nil || !res.RecoveryCodeUsed {
		t.Errorf("second code: err=%v used=%v", err, res.RecoveryCodeUsed)
	}
	// Verify (local-only entry point) accepts them too.
	if _, err := s.Verify("owner", "ownerpass1", codes[2]); err != nil {
		t.Errorf("Verify with recovery code: %v", err)
	}
}

func TestRecoveryCodes_RegenerateReplacesSet(t *testing.T) {
	s := newStore(t)
	id, _, old := enrolWithCodes(t, s)
	fresh, err := s.GenerateRecoveryCodes(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != RecoveryCodeCount {
		t.Fatalf("got %d fresh codes", len(fresh))
	}
	if _, err := s.Authenticate("owner", "ownerpass1", old[0], nil); !errors.Is(err, ErrInvalidSecondFactor) {
		t.Errorf("old code after regeneration: err = %v", err)
	}
	if _, err := s.Authenticate("owner", "ownerpass1", fresh[0], nil); err != nil {
		t.Errorf("fresh code: %v", err)
	}
	// Codes are a fallback for a secret: no secret, no codes.
	bare, err := s.AddUser("bare", "barepass123", RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GenerateRecoveryCodes(bare.ID); !errors.Is(err, ErrTOTPNotEnrolled) {
		t.Errorf("not enrolled: err = %v, want ErrTOTPNotEnrolled", err)
	}
	if _, err := s.GenerateRecoveryCodes("nope"); err == nil {
		t.Error("unknown user accepted")
	}
}

func TestResetTOTP_ClearsSecretAndCodes(t *testing.T) {
	s := newStore(t)
	id, _, codes := enrolWithCodes(t, s)
	if err := s.ResetTOTP(id); err != nil {
		t.Fatal(err)
	}
	u, ok := s.UserByID(id)
	if !ok || u.TOTPSec != "" || len(u.RecoveryCodes) != 0 {
		t.Fatalf("after reset: %+v", u)
	}
	// Optional mode + nothing enrolled: the password alone signs in again,
	// and a leftover code is just ignored input.
	if res, err := s.Authenticate("owner", "ownerpass1", "", nil); err != nil || res.RecoveryCodeUsed {
		t.Errorf("password-only login after reset: err=%v used=%v", err, res.RecoveryCodeUsed)
	}
	if res, err := s.Authenticate("owner", "ownerpass1", codes[0], nil); err != nil || res.RecoveryCodeUsed {
		t.Errorf("stale code after reset: err=%v used=%v", err, res.RecoveryCodeUsed)
	}
	if err := s.ResetTOTP("nope"); err == nil {
		t.Error("unknown user accepted")
	}
}

func TestRecoveryCodes_PersistHashedOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _, codes := enrolWithCodes(t, s)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"recovery_codes"`) {
		t.Fatal("recovery codes not persisted")
	}
	for _, c := range codes {
		if strings.Contains(string(data), c) || strings.Contains(string(data), strings.ReplaceAll(c, "-", "")) {
			t.Fatalf("plaintext code %q found on disk", c)
		}
	}

	// A consumption survives a restart: the reopened store refuses the code.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s2.SetTOTPMode(TOTPOptional)
	if _, err := s2.Authenticate("owner", "ownerpass1", codes[2], nil); err != nil {
		t.Fatalf("reopened store rejected a fresh code: %v", err)
	}
	s3, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s3.SetTOTPMode(TOTPOptional)
	if _, err := s3.Authenticate("owner", "ownerpass1", codes[2], nil); !errors.Is(err, ErrInvalidSecondFactor) {
		t.Errorf("consumed code accepted after reopen: err = %v", err)
	}
	if u, _ := s3.UserByUsername("owner"); u.RecoveryCodesRemaining() != RecoveryCodeCount-1 {
		t.Errorf("remaining after reopen = %d", u.RecoveryCodesRemaining())
	}
}

func TestRecoveryCodes_LDAPAccount(t *testing.T) {
	s := newStore(t)
	setupOwner(t, s, "owner", "ownerpass1")
	s.SetTOTPMode(TOTPOptional)
	ld := &fakeLDAP{ok: map[string]string{"alice": "ldappass"}}
	first, err := s.Authenticate("alice", "ldappass", "", ld)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := s.GenerateTOTPSecret("alice", "test")
	if err != nil {
		t.Fatal(err)
	}
	codes, err := s.EnrollTOTP(first.User.ID, secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate("alice", "ldappass", "", ld); !errors.Is(err, ErrInvalidSecondFactor) {
		t.Errorf("enrolled ldap account without code: err = %v", err)
	}
	if _, err := s.Authenticate("alice", "wrongpass", codes[0], ld); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong ldap password: err = %v, want ErrInvalidCredentials", err)
	}
	res, err := s.Authenticate("alice", "ldappass", codes[0], ld)
	if err != nil || !res.RecoveryCodeUsed {
		t.Fatalf("ldap recovery login: err=%v used=%v", err, res.RecoveryCodeUsed)
	}
	if got := res.User.RecoveryCodesRemaining(); got != RecoveryCodeCount-1 {
		t.Errorf("remaining = %d", got)
	}
}
