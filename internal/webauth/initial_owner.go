package webauth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// The owner created at the first start (IMP-044). A fresh install had a
// login page and no account to sign in with: the only way in was
// `gosidian user setup` at the console. Now the server creates the owner
// itself when there is no account at all, with a random password it writes
// beside the accounts file (and the caller logs), to be changed at the
// first sign-in. The file goes away when the owner's password changes.

// InitialPasswordFile is the file, beside the accounts file in the state
// dir, that holds the first owner's password until the owner changes it.
// Never in the vault: the vault may be pushed to a git remote.
const InitialPasswordFile = "initial-admin-password"

// InitialPasswordPath is where this store keeps InitialPasswordFile.
func (s *Store) InitialPasswordPath() string {
	return filepath.Join(filepath.Dir(s.path), InitialPasswordFile)
}

// ProvisionInitialOwner creates the owner username with a random password
// when the store holds no account, marks the password to be changed at the
// first sign-in and writes it to InitialPasswordPath (0600). It returns the
// password, or "" when accounts exist: then it only removes a file left
// from an owner who has changed the password since.
func (s *Store) ProvisionInitialOwner(username string) (string, error) {
	if s.Enabled() {
		s.dropStaleInitialPassword()
		return "", nil
	}
	password, err := randomPassword()
	if err != nil {
		return "", err
	}
	u, _, err := newOwner(username, password, false, "")
	if err != nil {
		return "", err
	}
	u.MustChangePassword = true
	// The file first: an owner whose password was never written down
	// would lock the install out.
	path := s.InitialPasswordPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(password+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	s.mu.Lock()
	if len(s.file.Users) > 0 {
		s.mu.Unlock()
		_ = os.Remove(path)
		return "", nil
	}
	s.file = AccountsFile{Version: accountsVersion, Users: []User{u}}
	err = s.saveLocked()
	if err != nil {
		s.file = AccountsFile{}
	}
	s.mu.Unlock()
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return password, nil
}

// dropStaleInitialPassword removes the file once no owner still has to
// change the password it holds.
func (s *Store) dropStaleInitialPassword() {
	if _, err := os.Stat(s.InitialPasswordPath()); err != nil {
		return
	}
	s.mu.RLock()
	owner := s.firstOwnerLocked()
	pending := owner != nil && owner.MustChangePassword
	s.mu.RUnlock()
	if !pending {
		s.removeInitialPassword()
	}
}

// removeInitialPassword deletes the file, if any: the owner's password has
// changed, or the accounts are gone.
func (s *Store) removeInitialPassword() {
	if err := os.Remove(s.InitialPasswordPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("webauth: remove %s: %v", s.InitialPasswordPath(), err)
	}
}

// randomPassword is 18 random bytes as 24 base64url characters, about 144
// bits: a temporary password, changed at the first sign-in.
func randomPassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
