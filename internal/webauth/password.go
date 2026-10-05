package webauth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// Passwords after creation (IMP-063, IMP-088): an account changes its own,
// the owner sets a temporary one that must be changed at the next login,
// and a sensitive action is confirmed with the current one. An LDAP
// account has no local password: the directory owns it.

// MinPasswordLen is the shortest password an account may have.
const MinPasswordLen = 8

var (
	// ErrNotLocalAccount is returned for an account whose password LDAP
	// owns: CheckPassword cannot verify it and SetPassword cannot set it.
	ErrNotLocalAccount = errors.New("the account's password is managed by LDAP")
	// ErrSamePassword refuses a new password equal to the current one.
	ErrSamePassword = errors.New("the new password must differ from the current one")
	// ErrPasswordTooShort is the length rule of every password.
	ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", MinPasswordLen)
)

func validatePassword(p string) error {
	if len(p) < MinPasswordLen {
		return ErrPasswordTooShort
	}
	return nil
}

// CheckPassword verifies password against the account's own, for the
// confirmation of a sensitive action: nil, ErrInvalidCredentials,
// ErrAccountDisabled, or ErrNotLocalAccount for an LDAP account, which
// the caller verifies with a bind instead.
func (s *Store) CheckPassword(userID, password string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.file.Users {
		u := &s.file.Users[i]
		if u.ID != userID {
			continue
		}
		if !u.Enabled() {
			return ErrAccountDisabled
		}
		if u.AuthSource == "ldap" {
			return ErrNotLocalAccount
		}
		if bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(password)) != nil {
			return ErrInvalidCredentials
		}
		return nil
	}
	return ErrInvalidCredentials
}

// SetPassword replaces a local account's password. mustChange marks it
// temporary, one the owner chose: the account must change it before
// anything else (MustChangePassword).
func (s *Store) SetPassword(userID, password string, mustChange bool) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), hashCost)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.file.Users {
		u := &s.file.Users[i]
		if u.ID != userID {
			continue
		}
		if u.AuthSource == "ldap" {
			return ErrNotLocalAccount
		}
		u.Hash = string(hash)
		u.MustChangePassword = mustChange
		return s.saveLocked()
	}
	return fmt.Errorf("user %q not found", userID)
}

// SetMustChangePassword marks or clears the account's obligation to change
// its password at the next request.
func (s *Store) SetMustChangePassword(userID string, must bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.file.Users {
		if s.file.Users[i].ID == userID {
			s.file.Users[i].MustChangePassword = must
			return s.saveLocked()
		}
	}
	return fmt.Errorf("user %q not found", userID)
}
