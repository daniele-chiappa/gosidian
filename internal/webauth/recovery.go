package webauth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Recovery codes are the offline fallback for a lost authenticator: a short
// set of single-use secrets minted when TOTP is enrolled (and on demand from
// Settings), accepted in place of a TOTP code at login and consumed on first
// use. They are stored bcrypt-hashed — a leaked accounts file must not yield
// a working second factor, and at ~49 bits a fast hash would be crackable
// offline — which is affordable because the comparison only runs on the
// recovery branch of a login.
const (
	// RecoveryCodeCount is how many codes one set holds.
	RecoveryCodeCount = 8
	// recoveryCodeLen is the number of alphabet symbols per code. Displayed
	// as two dash-separated groups of five.
	recoveryCodeLen = 10
	// recoveryAlphabet is Crockford base32 minus 0 and 1: no character that
	// can be mistaken for another (0/O, 1/I/L, U/V), so a code read off paper
	// survives transcription. 30 symbols → ~4.9 bits each.
	recoveryAlphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"
)

// ErrTOTPNotEnrolled is returned when a recovery-code operation targets an
// account without a TOTP secret: codes only make sense as a fallback for one.
var ErrTOTPNotEnrolled = errors.New("two-factor is not enrolled")

// RecoveryCode is one stored recovery code. The plaintext is shown to the
// user exactly once, at generation.
type RecoveryCode struct {
	Hash   string     `json:"hash"`              // bcrypt of the normalised code
	UsedAt *time.Time `json:"used_at,omitempty"` // set once consumed; never reused
}

// RecoveryCodesRemaining reports how many unused recovery codes u holds.
func (u *User) RecoveryCodesRemaining() int {
	if u == nil {
		return 0
	}
	n := 0
	for _, rc := range u.RecoveryCodes {
		if rc.UsedAt == nil {
			n++
		}
	}
	return n
}

// normalizeRecoveryCode uppercases and strips separators so a code typed with
// or without the dash, or pasted with spaces, still matches its hash.
func normalizeRecoveryCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// looksLikeRecoveryCode reports whether a normalised second-factor input has
// the shape of a recovery code rather than a 6-digit TOTP: exactly
// recoveryCodeLen symbols, all from the alphabet. Anything else is rejected
// before bcrypt runs, so a wrong TOTP does not pay the recovery cost.
func looksLikeRecoveryCode(norm string) bool {
	if len(norm) != recoveryCodeLen {
		return false
	}
	for i := 0; i < len(norm); i++ {
		if !strings.ContainsRune(recoveryAlphabet, rune(norm[i])) {
			return false
		}
	}
	return true
}

// formatRecoveryCode inserts the display dash: "ABCDEFGHJK" → "ABCDE-FGHJK".
func formatRecoveryCode(raw string) string {
	return raw[:recoveryCodeLen/2] + "-" + raw[recoveryCodeLen/2:]
}

// newRecoveryCodes mints a fresh set: display plaintexts plus the hashed
// records to persist. Symbols are drawn with rejection sampling so the
// distribution stays uniform over the 30-symbol alphabet.
func newRecoveryCodes() (plain []string, stored []RecoveryCode, err error) {
	const limit = byte(len(recoveryAlphabet) * (256 / len(recoveryAlphabet))) // 240: largest multiple of 30 below 256
	plain = make([]string, 0, RecoveryCodeCount)
	stored = make([]RecoveryCode, 0, RecoveryCodeCount)
	buf := make([]byte, 1)
	for len(plain) < RecoveryCodeCount {
		var raw strings.Builder
		for raw.Len() < recoveryCodeLen {
			if _, err := rand.Read(buf); err != nil {
				return nil, nil, fmt.Errorf("recovery codes: %w", err)
			}
			if buf[0] >= limit {
				continue
			}
			raw.WriteByte(recoveryAlphabet[int(buf[0])%len(recoveryAlphabet)])
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(raw.String()), bcrypt.DefaultCost)
		if err != nil {
			return nil, nil, err
		}
		plain = append(plain, formatRecoveryCode(raw.String()))
		stored = append(stored, RecoveryCode{Hash: string(hash)})
	}
	return plain, stored, nil
}

// EnrollTOTP activates secret for userID and mints the account's first set of
// recovery codes in the same save, so an enrolled account never exists without
// its fallback. Returns the plaintext codes (shown once).
func (s *Store) EnrollTOTP(userID, secret string) ([]string, error) {
	if secret == "" {
		return nil, errors.New("totp secret required")
	}
	plain, stored, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.file.Users {
		if s.file.Users[i].ID == userID {
			s.file.Users[i].TOTPSec = secret
			s.file.Users[i].RecoveryCodes = stored
			if err := s.saveLocked(); err != nil {
				return nil, err
			}
			return plain, nil
		}
	}
	return nil, fmt.Errorf("user %q not found", userID)
}

// GenerateRecoveryCodes replaces userID's recovery codes with a fresh set and
// returns the plaintexts (shown once). Every previous code, used or not, stops
// working. Requires an enrolled secret (ErrTOTPNotEnrolled otherwise).
func (s *Store) GenerateRecoveryCodes(userID string) ([]string, error) {
	plain, stored, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.file.Users {
		if s.file.Users[i].ID != userID {
			continue
		}
		if s.file.Users[i].TOTPSec == "" {
			return nil, ErrTOTPNotEnrolled
		}
		s.file.Users[i].RecoveryCodes = stored
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
		return plain, nil
	}
	return nil, fmt.Errorf("user %q not found", userID)
}

// ResetTOTP removes userID's TOTP secret and recovery codes in one save: the
// lockout escape hatch for the owner (admin API) and the operator (CLI). The
// per-user policy is untouched, so an account that must have TOTP meets the
// enrolment interstitial at its next login and re-enrols on its own; sessions
// are untouched too — a lost device is not a compromised account.
func (s *Store) ResetTOTP(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.file.Users {
		if s.file.Users[i].ID == userID {
			s.file.Users[i].TOTPSec = ""
			s.file.Users[i].RecoveryCodes = nil
			return s.saveLocked()
		}
	}
	return fmt.Errorf("user %q not found", userID)
}

// consumeRecoveryCode marks the first unused code of userID matching norm as
// used and persists it. Returns false when no unused code matches. The bcrypt
// comparisons run under the read lock against a snapshot — they are slow by
// design and must not stall writers — and the write lock re-checks that the
// matched entry is still the same unused code, so a concurrent regeneration
// or a double submit of the same code cannot both succeed.
func (s *Store) consumeRecoveryCode(userID, norm string) (bool, error) {
	s.mu.RLock()
	var hashes []string
	for i := range s.file.Users {
		if s.file.Users[i].ID != userID {
			continue
		}
		for _, rc := range s.file.Users[i].RecoveryCodes {
			if rc.UsedAt == nil {
				hashes = append(hashes, rc.Hash)
			} else {
				hashes = append(hashes, "")
			}
		}
	}
	s.mu.RUnlock()

	match := -1
	for i, h := range hashes {
		if h != "" && bcrypt.CompareHashAndPassword([]byte(h), []byte(norm)) == nil {
			match = i
			break
		}
	}
	if match < 0 {
		return false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.file.Users {
		u := &s.file.Users[i]
		if u.ID != userID {
			continue
		}
		if match >= len(u.RecoveryCodes) || u.RecoveryCodes[match].UsedAt != nil || u.RecoveryCodes[match].Hash != hashes[match] {
			return false, nil
		}
		now := time.Now().UTC()
		u.RecoveryCodes[match].UsedAt = &now
		if err := s.saveLocked(); err != nil {
			u.RecoveryCodes[match].UsedAt = nil
			return false, err
		}
		return true, nil
	}
	return false, nil
}
