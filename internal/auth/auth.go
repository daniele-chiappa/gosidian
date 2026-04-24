// Package auth provides Bearer token authentication and project scoping for
// gosidian's HTTP API and MCP server. Tokens are stored as a JSON file under
// <vault>/.gosidian/tokens.json. The plaintext token is returned to the user
// only at creation time; only a SHA-256 hash is persisted.
//
// Auth is opt-in: if the token store is empty (or the file doesn't exist),
// every request is treated as an implicit admin. As soon as one token is
// provisioned, unauthenticated requests are rejected.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	ScopeRead  = "read"
	ScopeWrite = "write"

	tokenPrefix = "gosidian_"
)

// Token is the persisted record of a provisioned token. The plaintext is never
// stored; only Hash.
type Token struct {
	ID          string    `json:"id"`                     // short display id (first 8 hex of hash)
	Name        string    `json:"name"`                   // human-readable label
	Hash        string    `json:"hash"`                   // hex-encoded sha256 of the full token
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`   // zero = no expiry
	Project     string    `json:"project,omitempty"`      // empty = admin, sees everything
	Scopes      []string  `json:"scopes"`                 // read, write
	OwnerUserID string    `json:"owner_user_id,omitempty"` // webauth user id; empty = admin-owned (CLI)
}

// HasScope reports whether the token carries the given scope.
func (t *Token) HasScope(s string) bool {
	for _, v := range t.Scopes {
		if v == s {
			return true
		}
	}
	return false
}

// Expired reports whether the token has reached its expiration.
func (t *Token) Expired() bool {
	return !t.ExpiresAt.IsZero() && time.Now().After(t.ExpiresAt)
}

// AllowsPath reports whether the token's project scope allows access to the
// given vault-relative note path. Admin tokens (empty Project) allow all.
func (t *Token) AllowsPath(path string) bool {
	if t.Project == "" {
		return true
	}
	return path == t.Project || strings.HasPrefix(path, t.Project+"/")
}

// ProjectFilter returns the project prefix to apply to list/search operations,
// or an empty string for admin tokens.
func (t *Token) ProjectFilter() string { return t.Project }

// Store is a concurrent-safe token store backed by a JSON file. It
// transparently re-reads the file when its modification time changes, so
// tokens created through the CLI or written by an external process while the
// server is running become effective without a restart (IMP-006 / BUG-004).
type Store struct {
	path   string
	mu     sync.RWMutex
	tokens []Token
	mtime  time.Time // mtime observed at last (re)load; zero when file absent
}

type storeFile struct {
	Tokens []Token `json:"tokens"`
}

// Open loads the token store from the given file path. If the file does not
// exist, an empty store is returned (auth disabled until first token).
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// load reads tokens.json from disk and replaces the in-memory snapshot. A
// missing file is not an error — it resets the store to empty. Caller must
// hold s.mu (write) or be in an initialization context.
func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.tokens = nil
			s.mtime = time.Time{}
			return nil
		}
		return err
	}
	var sf storeFile
	if len(data) > 0 {
		if err := json.Unmarshal(data, &sf); err != nil {
			return fmt.Errorf("parse token file: %w", err)
		}
	}
	s.tokens = sf.Tokens
	if st, err := os.Stat(s.path); err == nil {
		s.mtime = st.ModTime()
	}
	return nil
}

// reloadIfStale re-reads the file when its mtime (or existence) diverges from
// the last-loaded snapshot. Cheap: 1 os.Stat on the hot path, no I/O beyond
// that unless something actually changed. Caller must hold s.mu.Lock() or
// enter through RLock()+upgrade; the lockless call below handles the upgrade
// itself.
func (s *Store) reloadIfStale() {
	st, err := os.Stat(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !s.mtime.IsZero() || len(s.tokens) > 0 {
				// File was deleted after being loaded — drop the in-memory copy.
				s.tokens = nil
				s.mtime = time.Time{}
			}
		}
		return
	}
	if st.ModTime().Equal(s.mtime) {
		return
	}
	_ = s.load()
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(storeFile{Tokens: s.tokens}, "", "  ")
	if err != nil {
		return err
	}
	// Write + rename for atomicity.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	// Record the fresh mtime so reloadIfStale() doesn't re-read our own write.
	if st, err := os.Stat(s.path); err == nil {
		s.mtime = st.ModTime()
	}
	return nil
}

// Empty reports whether the store has no tokens. When empty, auth is disabled.
// Also triggers a lazy reload so a newly-created first token flips auth on
// without a restart (closing BUG-004's "latent admin mode" edge case).
func (s *Store) Empty() bool {
	s.mu.Lock()
	s.reloadIfStale()
	empty := len(s.tokens) == 0
	s.mu.Unlock()
	return empty
}

// List returns a copy of the current tokens (without plaintext).
func (s *Store) List() []Token {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Token, len(s.tokens))
	copy(out, s.tokens)
	return out
}

// Create generates a new token, stores its hash, and returns the plaintext
// (shown only at creation time) together with the stored record. ownerUserID
// binds the token to a webauth user when the web UI mints it; CLI-created
// tokens pass "" and behave as admin-owned.
func (s *Store) Create(name, project string, scopes []string, ttl time.Duration, ownerUserID string) (plaintext string, tok Token, err error) {
	if name == "" {
		return "", Token{}, errors.New("token name required")
	}
	if len(scopes) == 0 {
		return "", Token{}, errors.New("at least one scope required")
	}
	for _, sc := range scopes {
		if sc != ScopeRead && sc != ScopeWrite {
			return "", Token{}, fmt.Errorf("unknown scope %q", sc)
		}
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", Token{}, err
	}
	plaintext = tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(plaintext))
	hashHex := hex.EncodeToString(hash[:])

	tok = Token{
		ID:          hashHex[:8],
		Name:        name,
		Hash:        hashHex,
		CreatedAt:   time.Now().UTC(),
		Project:     project,
		Scopes:      append([]string(nil), scopes...),
		OwnerUserID: ownerUserID,
	}
	if ttl != 0 {
		tok.ExpiresAt = tok.CreatedAt.Add(ttl)
	}

	s.mu.Lock()
	s.tokens = append(s.tokens, tok)
	if err := s.save(); err != nil {
		s.tokens = s.tokens[:len(s.tokens)-1]
		s.mu.Unlock()
		return "", Token{}, err
	}
	s.mu.Unlock()
	return plaintext, tok, nil
}

// Revoke deletes a token identified by its ID prefix (first 8 hex of hash).
func (s *Store) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.tokens {
		if t.ID == id {
			s.tokens = append(s.tokens[:i], s.tokens[i+1:]...)
			return s.save()
		}
	}
	return fmt.Errorf("token %q not found", id)
}

// RevokeByOwner deletes all tokens whose OwnerUserID matches userID. Returns
// the number of tokens revoked. Used by the webauth DisableUser cascade to
// invalidate every MCP credential of a disabled collaborator in one shot.
func (s *Store) RevokeByOwner(userID string) int {
	if userID == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.tokens[:0]
	removed := 0
	for _, t := range s.tokens {
		if t.OwnerUserID == userID {
			removed++
			continue
		}
		kept = append(kept, t)
	}
	if removed == 0 {
		return 0
	}
	s.tokens = kept
	_ = s.save()
	return removed
}

// AssignOwnerToOrphans fills the OwnerUserID field of every token that
// doesn't have one with the provided userID. Used on startup after v1.4
// migration to retro-assign legacy tokens to the owner. Returns the number
// of tokens updated.
func (s *Store) AssignOwnerToOrphans(userID string) int {
	if userID == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	updated := 0
	for i := range s.tokens {
		if s.tokens[i].OwnerUserID == "" {
			s.tokens[i].OwnerUserID = userID
			updated++
		}
	}
	if updated == 0 {
		return 0
	}
	_ = s.save()
	return updated
}

// Validate takes a plaintext Bearer token and returns the matching stored
// Token. Returns an error if the token is missing, unknown, or expired. A
// lazy mtime-check picks up tokens.json edits made by the CLI after Open()
// (IMP-006).
func (s *Store) Validate(plaintext string) (*Token, error) {
	if plaintext == "" {
		return nil, errors.New("missing token")
	}
	hash := sha256.Sum256([]byte(plaintext))
	hashHex := hex.EncodeToString(hash[:])

	s.mu.Lock()
	s.reloadIfStale()
	s.mu.Unlock()

	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.tokens {
		t := &s.tokens[i]
		// Constant-time comparison to avoid timing oracles on the hash.
		if subtle.ConstantTimeCompare([]byte(t.Hash), []byte(hashHex)) == 1 {
			if t.Expired() {
				return nil, errors.New("token expired")
			}
			out := *t
			return &out, nil
		}
	}
	return nil, errors.New("invalid token")
}

// AdminToken returns a synthetic admin token used when the store is empty
// (auth-disabled bootstrap mode). This token has all scopes and no project
// filter; it is never persisted.
func AdminToken() *Token {
	return &Token{
		ID:     "admin",
		Name:   "implicit-admin",
		Scopes: []string{ScopeRead, ScopeWrite},
	}
}

// ExtractBearer returns the token from an Authorization: Bearer <token>
// header value. Empty string if not present.
func ExtractBearer(header string) string {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}
