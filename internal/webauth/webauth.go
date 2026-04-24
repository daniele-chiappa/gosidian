// Package webauth provides multi-user login for the gosidian web UI.
//
// Model (v1.4 onwards):
//   - 1..N accounts stored as a JSON list in <vault>/.gosidian/auth.json with
//     bcrypt password hashes, optional TOTP secrets, and a role (owner |
//     member). The first user ever provisioned becomes owner and is the
//     only account allowed to manage users and invite new members.
//   - Invites are single-use, time-limited tokens stored alongside users in
//     the same file; they are created by the owner, served once via the UI,
//     and consumed on signup.
//   - Sessions are an in-memory map keyed by a random cookie value; they are
//     lost on restart (accepted trade-off for team-ristretto self-hosted).
//     Sessions carry the user id so handlers can enforce role policies.
//   - If the auth file does not exist, authentication is disabled and the web
//     UI is open (bootstrap / local-only mode).
//
// Legacy single-user auth.json files (pre-v1.4) are migrated in-place on
// Open(): the lone account becomes the owner, the invites list starts empty.
//
// The package exposes a Store (persisted accounts + session cache) and
// helpers to validate credentials and set/validate session cookies. The HTTP
// middleware wiring lives in the server package.
package webauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

// Role defines the two levels of privilege in v1.4. Owner is singular and
// manages users + sees all tokens. Member can create notes + their own
// tokens only.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
)

// User is a single account in the accounts file.
type User struct {
	ID         string     `json:"id"`
	Username   string     `json:"username"`
	Hash       string     `json:"hash"`                 // bcrypt
	TOTPSec    string     `json:"totp_secret,omitempty"`
	Role       Role       `json:"role"`
	CreatedAt  time.Time  `json:"created_at"`
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
}

// Enabled reports whether the user is active (not disabled).
func (u *User) Enabled() bool { return u != nil && u.DisabledAt == nil }

// Invite is a single-use owner-minted registration ticket.
type Invite struct {
	Token      string     `json:"token"`       // plaintext; shown once, stored for lookup
	CreatedBy  string     `json:"created_by"`  // user id
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	ConsumedBy string     `json:"consumed_by,omitempty"`
	ConsumedAt *time.Time `json:"consumed_at,omitempty"`
}

// Pending reports whether the invite is still available (not consumed, not
// expired, not revoked).
func (i *Invite) Pending() bool {
	return i != nil && i.ConsumedAt == nil && time.Now().Before(i.ExpiresAt)
}

// AccountsFile is the on-disk shape from v1.4. Version=2 to distinguish from
// the legacy v1 single-account shape, which has no `version` field.
type AccountsFile struct {
	Version int      `json:"version"`
	Users   []User   `json:"users"`
	Invites []Invite `json:"invites,omitempty"`
}

const accountsVersion = 2

// Store holds the on-disk accounts plus an in-memory session map. All methods
// are safe for concurrent use.
type Store struct {
	path string

	mu       sync.RWMutex
	file     AccountsFile
	sessions map[string]session
	mtime    time.Time // mtime observed at last (re)load; zero when file absent

	// onUserDisabled, if set, is called after a user is disabled so callers
	// can cascade side-effects (e.g. revoke MCP tokens owned by that user).
	// Called without holding Store.mu.
	onUserDisabled func(userID string)
}

type session struct {
	userID  string
	expires time.Time
}

// SetOnUserDisabled installs the cascade hook. Safe to call at startup; not
// expected to change after that.
func (s *Store) SetOnUserDisabled(fn func(userID string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onUserDisabled = fn
}

// Open reads the accounts file. A missing file returns an empty Store
// (auth-disabled bootstrap). A legacy v1 single-account file is migrated to
// v2 in memory and persisted back on the next mutation.
func Open(path string) (*Store, error) {
	s := &Store{path: path, sessions: make(map[string]session)}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return s, nil
	}
	file, err := parseAccountsFile(data)
	if err != nil {
		return nil, err
	}
	s.file = file
	if st, err := os.Stat(path); err == nil {
		s.mtime = st.ModTime()
	}
	return s, nil
}

// reloadIfStale re-reads the accounts file when its mtime (or existence)
// diverges from the last-loaded snapshot. Cheap: 1 os.Stat on the hot path,
// no I/O beyond that when the file is unchanged. Called from Enabled() (and
// hence indirectly from every webauth-protected route) so that an external
// `gosidian user setup` is visible to the server without a restart.
//
// Active sessions survive the reload: user IDs are derived deterministically
// from username+created_at and the sessions map is keyed by id, so a session
// cookie issued before the reload remains valid as long as the same user
// still exists in the new file.
func (s *Store) reloadIfStale() {
	st, err := os.Stat(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.mu.Lock()
			if !s.mtime.IsZero() || len(s.file.Users) > 0 {
				s.file = AccountsFile{}
				s.mtime = time.Time{}
			}
			s.mu.Unlock()
		}
		return
	}
	s.mu.RLock()
	current := s.mtime
	s.mu.RUnlock()
	if st.ModTime().Equal(current) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.ModTime().Equal(s.mtime) {
		return // another caller won the race
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	if len(data) == 0 {
		s.file = AccountsFile{}
		s.mtime = st.ModTime()
		return
	}
	file, perr := parseAccountsFile(data)
	if perr != nil {
		return
	}
	s.file = file
	s.mtime = st.ModTime()
}

// parseAccountsFile handles both v2 (current) and legacy v1 shapes.
func parseAccountsFile(data []byte) (AccountsFile, error) {
	// Peek at the shape: v2 has "version"; v1 has "username" as a top-level
	// scalar. If "version" is present and >= 2 we trust the new shape.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return AccountsFile{}, fmt.Errorf("parse auth file: %w", err)
	}
	if _, hasVersion := probe["version"]; hasVersion {
		var f AccountsFile
		if err := json.Unmarshal(data, &f); err != nil {
			return AccountsFile{}, fmt.Errorf("parse v2 auth file: %w", err)
		}
		return f, nil
	}
	// Legacy v1 single-user shape.
	var legacy struct {
		Username  string    `json:"username"`
		Hash      string    `json:"hash"`
		TOTPSec   string    `json:"totp_secret"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return AccountsFile{}, fmt.Errorf("parse legacy auth file: %w", err)
	}
	if legacy.Username == "" {
		// Empty object — treat as no users.
		return AccountsFile{Version: accountsVersion}, nil
	}
	u := User{
		ID:        deriveUserID(legacy.Username, legacy.UpdatedAt),
		Username:  legacy.Username,
		Hash:      legacy.Hash,
		TOTPSec:   legacy.TOTPSec,
		Role:      RoleOwner,
		CreatedAt: legacy.UpdatedAt,
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	return AccountsFile{
		Version: accountsVersion,
		Users:   []User{u},
	}, nil
}

// deriveUserID returns a stable opaque identifier: first 16 hex chars of
// sha256(username + unix nanos). Collision odds are negligible at
// team-ristretto scale.
func deriveUserID(username string, ts time.Time) string {
	seed := fmt.Sprintf("%s|%d", username, ts.UnixNano())
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])[:16]
}

// Enabled reports whether any user has been provisioned. When false, the
// web UI middleware treats all routes as open.
//
// Performs a lazy mtime check so an external `gosidian user setup` is
// visible without a restart. See reloadIfStale.
func (s *Store) Enabled() bool {
	s.reloadIfStale()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.file.Users) > 0
}

// Username returns the owner's username if any, otherwise an empty string.
// Exposed for backward compatibility with handlers that used the old
// single-user api; new code should prefer UserBySession.
func (s *Store) Username() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if u := s.firstOwnerLocked(); u != nil {
		return u.Username
	}
	return ""
}

// TOTPEnabled reports whether the owner's account has a TOTP secret. Used by
// the login template to decide whether to render the TOTP field.
func (s *Store) TOTPEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u := s.firstOwnerLocked()
	return u != nil && u.TOTPSec != ""
}

// firstOwnerLocked returns a pointer into s.file.Users; caller must hold
// s.mu (at least RLock).
func (s *Store) firstOwnerLocked() *User {
	for i := range s.file.Users {
		if s.file.Users[i].Role == RoleOwner && s.file.Users[i].Enabled() {
			return &s.file.Users[i]
		}
	}
	return nil
}

// FirstOwner returns a copy of the first enabled owner, or nil when no owner
// exists. Used by main.go at startup to migrate ownerless tokens.
func (s *Store) FirstOwner() *User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if u := s.firstOwnerLocked(); u != nil {
		cp := *u
		return &cp
	}
	return nil
}

// Setup provisions the owner account, replacing any existing accounts file.
// This is the v1-compatible entry point used by the CLI `gosidian user setup`.
// Returns the TOTP provisioning URI when withTOTP is true.
func (s *Store) Setup(username, password string, withTOTP bool, issuer string) (otpURI string, err error) {
	if username == "" {
		return "", errors.New("username required")
	}
	if len(password) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	u := User{
		ID:        deriveUserID(username, now),
		Username:  username,
		Hash:      string(hash),
		Role:      RoleOwner,
		CreatedAt: now,
	}
	if withTOTP {
		key, err := totp.Generate(totp.GenerateOpts{
			Issuer:      issuer,
			AccountName: username,
		})
		if err != nil {
			return "", err
		}
		u.TOTPSec = key.Secret()
		otpURI = key.URL()
	}

	s.mu.Lock()
	s.file = AccountsFile{Version: accountsVersion, Users: []User{u}}
	s.sessions = make(map[string]session)
	err = s.saveLocked()
	s.mu.Unlock()
	return otpURI, err
}

// Disable removes all accounts + invites and invalidates all sessions. Used
// by tests and by the CLI `gosidian user disable`.
func (s *Store) Disable() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.file = AccountsFile{}
	s.sessions = make(map[string]session)
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Verify checks credentials. When TOTP is enabled on the account, totpCode
// must be valid for the current time window. Returns the matched user on
// success, an error otherwise.
func (s *Store) Verify(username, password, totpCode string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.file.Users) == 0 {
		return nil, errors.New("auth disabled")
	}
	for i := range s.file.Users {
		u := &s.file.Users[i]
		if u.Username != username {
			continue
		}
		if !u.Enabled() {
			return nil, errors.New("account disabled")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(password)); err != nil {
			return nil, errors.New("invalid credentials")
		}
		if u.TOTPSec != "" {
			if !totp.Validate(totpCode, u.TOTPSec) {
				return nil, errors.New("invalid TOTP code")
			}
		}
		cp := *u
		return &cp, nil
	}
	return nil, errors.New("invalid credentials")
}

// CreateSession returns a fresh session cookie value for the given user,
// valid for ttl.
func (s *Store) CreateSession(userID string, ttl time.Duration) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(buf)
	s.mu.Lock()
	s.sessions[id] = session{userID: userID, expires: time.Now().Add(ttl)}
	s.mu.Unlock()
	return id, nil
}

// ValidateSession checks whether the cookie id maps to a live session for a
// still-enabled user. Expired or orphaned sessions are evicted lazily.
func (s *Store) ValidateSession(id string) bool {
	_, ok := s.UserBySession(id)
	return ok
}

// UserBySession returns a copy of the user behind the given session id, or
// (nil, false) if the session is missing, expired, or belongs to a disabled
// user (in which case the session is also evicted).
func (s *Store) UserBySession(id string) (*User, bool) {
	if id == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	if time.Now().After(sess.expires) {
		delete(s.sessions, id)
		return nil, false
	}
	for i := range s.file.Users {
		if s.file.Users[i].ID == sess.userID {
			u := s.file.Users[i]
			if !u.Enabled() {
				delete(s.sessions, id)
				return nil, false
			}
			return &u, true
		}
	}
	// Session references a non-existent user — evict.
	delete(s.sessions, id)
	return nil, false
}

// RevokeSession deletes a session id (used on logout).
func (s *Store) RevokeSession(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// revokeSessionsForUserLocked evicts every session belonging to userID.
// Caller must hold s.mu (write lock).
func (s *Store) revokeSessionsForUserLocked(userID string) {
	for id, sess := range s.sessions {
		if sess.userID == userID {
			delete(s.sessions, id)
		}
	}
}

// ListUsers returns a copy of the users slice (safe to mutate).
func (s *Store) ListUsers() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]User, len(s.file.Users))
	copy(out, s.file.Users)
	return out
}

// UserByID returns a copy of the user with the given id, or (nil, false).
func (s *Store) UserByID(id string) (*User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.file.Users {
		if s.file.Users[i].ID == id {
			u := s.file.Users[i]
			return &u, true
		}
	}
	return nil, false
}

// AddUser creates a new member user. Fails if username is already taken.
// Password must be >= 8 chars. Used by signup via invite.
func (s *Store) AddUser(username, password string, role Role) (*User, error) {
	if username == "" {
		return nil, errors.New("username required")
	}
	if len(password) < 8 {
		return nil, errors.New("password must be at least 8 characters")
	}
	if role != RoleOwner && role != RoleMember {
		return nil, fmt.Errorf("unknown role %q", role)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	u := User{
		ID:        deriveUserID(username, now),
		Username:  username,
		Hash:      string(hash),
		Role:      role,
		CreatedAt: now,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.file.Users {
		if existing.Username == username {
			return nil, fmt.Errorf("username %q already exists", username)
		}
	}
	s.file.Users = append(s.file.Users, u)
	if err := s.saveLocked(); err != nil {
		s.file.Users = s.file.Users[:len(s.file.Users)-1]
		return nil, err
	}
	cp := u
	return &cp, nil
}

// DisableUser marks the user as disabled, evicts their sessions, and invokes
// the cascade hook. An owner cannot be disabled (guards against lock-out).
func (s *Store) DisableUser(id string) error {
	s.mu.Lock()
	var fn func(string)
	var found bool
	for i := range s.file.Users {
		if s.file.Users[i].ID != id {
			continue
		}
		if s.file.Users[i].Role == RoleOwner {
			s.mu.Unlock()
			return errors.New("cannot disable the owner")
		}
		if !s.file.Users[i].Enabled() {
			s.mu.Unlock()
			return nil // already disabled
		}
		now := time.Now().UTC()
		s.file.Users[i].DisabledAt = &now
		found = true
		break
	}
	if !found {
		s.mu.Unlock()
		return fmt.Errorf("user %q not found", id)
	}
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	s.revokeSessionsForUserLocked(id)
	fn = s.onUserDisabled
	s.mu.Unlock()
	if fn != nil {
		fn(id)
	}
	return nil
}

// CreateInvite generates a single-use registration ticket owned by creator.
// Returns the invite with its plaintext Token populated — caller must present
// it to the user exactly once (shown in the UI after POST).
func (s *Store) CreateInvite(creatorID string, ttl time.Duration) (Invite, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return Invite{}, err
	}
	inv := Invite{
		Token:     "inv_" + base64.RawURLEncoding.EncodeToString(buf),
		CreatedBy: creatorID,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(ttl),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.file.Invites = append(s.file.Invites, inv)
	if err := s.saveLocked(); err != nil {
		s.file.Invites = s.file.Invites[:len(s.file.Invites)-1]
		return Invite{}, err
	}
	return inv, nil
}

// FindInvite looks up an invite by its plaintext token. Returns nil when the
// invite is unknown, consumed, or expired.
func (s *Store) FindInvite(token string) *Invite {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.file.Invites {
		if s.file.Invites[i].Token == token && s.file.Invites[i].Pending() {
			inv := s.file.Invites[i]
			return &inv
		}
	}
	return nil
}

// ClaimInvite atomically marks the invite as consumed by consumerID. Returns
// an error if the invite is unknown or no longer pending.
func (s *Store) ClaimInvite(token, consumerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.file.Invites {
		if s.file.Invites[i].Token != token {
			continue
		}
		if !s.file.Invites[i].Pending() {
			return errors.New("invite already consumed or expired")
		}
		now := time.Now().UTC()
		s.file.Invites[i].ConsumedBy = consumerID
		s.file.Invites[i].ConsumedAt = &now
		return s.saveLocked()
	}
	return errors.New("invite not found")
}

// RevokeInvite deletes an invite (owner action). Consumed invites are still
// deletable as a housekeeping operation.
func (s *Store) RevokeInvite(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.file.Invites {
		if s.file.Invites[i].Token == token {
			s.file.Invites = append(s.file.Invites[:i], s.file.Invites[i+1:]...)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("invite %q not found", token)
}

// ListInvites returns a copy of all invites (pending + consumed + expired).
// Owner UI decides rendering; expired invites are filtered by the template.
func (s *Store) ListInvites() []Invite {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Invite, len(s.file.Invites))
	copy(out, s.file.Invites)
	return out
}

// saveLocked writes the accounts JSON to disk. Caller must hold s.mu.Lock().
func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	s.file.Version = accountsVersion
	data, err := json.MarshalIndent(s.file, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	// Record the fresh mtime so reloadIfStale doesn't re-read our own write.
	if st, err := os.Stat(s.path); err == nil {
		s.mtime = st.ModTime()
	}
	return nil
}

// SessionCookie builds an http.Cookie carrying the session id. Use secure=true
// when serving behind TLS (the server decides based on r.TLS or an X-Forwarded
// header — see server package).
const SessionCookieName = "gosidian_session"

func SessionCookie(id string, ttl time.Duration, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    id,
		Path:     "/",
		Expires:  time.Now().Add(ttl),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	}
}

// ClearCookie returns an expired cookie that the browser will drop.
func ClearCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	}
}

// IsSecureRequest reports whether r looks like it came over TLS. It trusts
// the X-Forwarded-Proto header because gosidian is meant to sit behind a
// reverse proxy.
func IsSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}
