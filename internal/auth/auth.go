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
//
// Project scope: Projects carries the full list a token may span (the
// orchestrator-bus case: one orchestrator token over N agent projects).
// Project remains the legacy single-project field — it is kept populated with
// the first entry so tokens.json written by this version stays readable by
// older binaries (which then see a MORE restrictive single-project token,
// never a wider one). Both empty = admin, sees everything. Always read the
// scope through ProjectList(), never the raw fields.
type Token struct {
	ID               string    `json:"id"`   // short display id (first 8 hex of hash)
	Name             string    `json:"name"` // human-readable label
	Hash             string    `json:"hash"` // hex-encoded sha256 of the full token
	CreatedAt        time.Time `json:"created_at"`
	ExpiresAt        time.Time `json:"expires_at,omitempty"`          // zero = no expiry
	Project          string    `json:"project,omitempty"`             // legacy single project; see ProjectList
	Projects         []string  `json:"projects,omitempty"`            // multi-project scope; see ProjectList
	Scopes           []string  `json:"scopes"`                        // read, write
	OwnerUserID      string    `json:"owner_user_id,omitempty"`       // webauth user id; empty = admin-owned (CLI)
	SelfImproveOptIn bool      `json:"self_improve_opt_in,omitempty"` // opt-in to the self-improve nudge loop (per-token)
	ToolProfile      string    `json:"tool_profile,omitempty"`        // MCP tool surface: "" | "full" (everything) or "core" (worker subset)
	// Kind distinguishes a static bearer ("" — the plaintext is the credential)
	// from an OAuth grant (KindOAuth — minted by the consent flow, IMP-092).
	// A grant is never presented directly: short-lived access tokens resolve
	// to it in memory and the refresh token below renews them. Revoking the
	// record (admin UI, CLI, user-disable cascade) kills both.
	Kind string `json:"kind,omitempty"`
	// ClientID is the OAuth client the grant was issued to (DCR id or CIMD
	// URL); empty for static bearers.
	ClientID string `json:"client_id,omitempty"`
	// RefreshHash is the sha256 of the current refresh token; rotated on every
	// use. RefreshExpiresAt bounds it independently of ExpiresAt.
	RefreshHash      string    `json:"refresh_hash,omitempty"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at,omitempty"`
}

// KindOAuth marks a token record minted by the OAuth consent flow (a grant).
const KindOAuth = "oauth"

// IsOAuthGrant reports whether the record is an OAuth grant rather than a
// static bearer.
func (t *Token) IsOAuthGrant() bool { return t.Kind == KindOAuth }

// Tool profiles: which MCP tool surface a token sees. Empty means full —
// existing tokens keep the whole catalogue (backward compatible). "core"
// exposes only the worker subset (read/write/search/upload/handoff), cutting
// the per-session schema cost for sub-agents.
const (
	ToolProfileFull = "full"
	ToolProfileCore = "core"
)

// ValidToolProfile reports whether p is an accepted tool_profile value.
func ValidToolProfile(p string) bool {
	return p == "" || p == ToolProfileFull || p == ToolProfileCore
}

// IsCoreProfile reports whether the token is restricted to the core tool
// subset. Empty/"full" (and admin tokens, which have no profile) see all.
func (t *Token) IsCoreProfile() bool { return t.ToolProfile == ToolProfileCore }

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

// ProjectList returns the token's normalized project scope: the multi-project
// list when set, the legacy single Project as a one-element list otherwise,
// nil for admin tokens. All scope decisions go through this accessor so
// records written before the multi-project era keep working unchanged.
func (t *Token) ProjectList() []string {
	if len(t.Projects) > 0 {
		return t.Projects
	}
	if t.Project != "" {
		return []string{t.Project}
	}
	return nil
}

// IsAdmin reports whether the token has no project scope (sees everything).
func (t *Token) IsAdmin() bool { return len(t.ProjectList()) == 0 }

// AllowsProject reports whether the token may operate on the given top-level
// project. Admin tokens allow all.
func (t *Token) AllowsProject(project string) bool {
	list := t.ProjectList()
	if len(list) == 0 {
		return true
	}
	for _, p := range list {
		if p == project {
			return true
		}
	}
	return false
}

// AllowsPath reports whether the token's project scope allows access to the
// given vault-relative note path. Admin tokens allow all; scoped tokens match
// any of their projects as a path prefix.
func (t *Token) AllowsPath(path string) bool {
	list := t.ProjectList()
	if len(list) == 0 {
		return true
	}
	for _, p := range list {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// ScopeLabel renders the project scope for error messages and displays:
// comma-joined projects, or "(admin)" for unscoped tokens.
func (t *Token) ScopeLabel() string {
	list := t.ProjectList()
	if len(list) == 0 {
		return "(admin)"
	}
	return strings.Join(list, ",")
}

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
// the last-loaded snapshot. Every mutator calls it before touching s.tokens:
// save() rewrites the whole file, so mutating a stale snapshot would silently
// undo whatever the CLI (or another process) wrote in the meantime — a
// revoked token coming back to life is the worst case. Cheap: 1 os.Stat on
// the hot path, no I/O beyond that unless something actually changed. Caller
// must hold s.mu.Lock() or enter through RLock()+upgrade; the lockless call
// below handles the upgrade itself.
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
// tokens pass "" and behave as admin-owned. projects is the scope list (nil
// or empty = admin); entries are trimmed and deduplicated, order preserved.
func (s *Store) Create(name string, projects []string, scopes []string, ttl time.Duration, ownerUserID string) (plaintext string, tok Token, err error) {
	if err := validateCreate(name, scopes); err != nil {
		return "", Token{}, err
	}
	cleanProjects, err := cleanProjectList(projects)
	if err != nil {
		return "", Token{}, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", Token{}, err
	}
	plaintext = tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	tok, err = s.mint(plaintext, name, cleanProjects, scopes, ttl, ownerUserID)
	if err != nil {
		return "", Token{}, err
	}
	return plaintext, tok, nil
}

// CreateGrant persists an OAuth grant: a token record with no presentable
// plaintext (its hash is derived from random bytes that are discarded, so
// Validate can never match it). Access tokens resolve to it by ID and the
// refresh token set with SetRefresh renews them; see internal/oauth. Same
// validation and project normalization as Create.
func (s *Store) CreateGrant(name string, projects []string, scopes []string, ttl time.Duration, ownerUserID, clientID string) (Token, error) {
	if err := validateCreate(name, scopes); err != nil {
		return Token{}, err
	}
	cleanProjects, err := cleanProjectList(projects)
	if err != nil {
		return Token{}, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Token{}, err
	}
	// Not a presentable credential: the "plaintext" hashed here is never
	// returned to anyone.
	tok, err := s.mint("grant:"+base64.RawURLEncoding.EncodeToString(raw), name, cleanProjects, scopes, ttl, ownerUserID)
	if err != nil {
		return Token{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	for i := range s.tokens {
		if s.tokens[i].ID == tok.ID {
			s.tokens[i].Kind = KindOAuth
			s.tokens[i].ClientID = clientID
			tok = s.tokens[i]
			return tok, s.save()
		}
	}
	return Token{}, errors.New("grant vanished after mint")
}

// validateCreate holds the argument checks shared by Create and CreateGrant.
func validateCreate(name string, scopes []string) error {
	if name == "" {
		return errors.New("token name required")
	}
	if len(scopes) == 0 {
		return errors.New("at least one scope required")
	}
	for _, sc := range scopes {
		if sc != ScopeRead && sc != ScopeWrite {
			return fmt.Errorf("unknown scope %q", sc)
		}
	}
	return nil
}

// cleanProjectList normalizes and validates a project scope list.
func cleanProjectList(projects []string) ([]string, error) {
	cleanProjects := normalizeProjects(projects)
	for _, p := range cleanProjects {
		if strings.ContainsAny(p, "/\\:") || p == "." || p == ".." || strings.HasPrefix(p, ".") {
			return nil, fmt.Errorf("invalid project name %q", p)
		}
	}
	return cleanProjects, nil
}

// mint builds the record for plaintext, appends it and saves. cleanProjects
// must already be normalized.
func (s *Store) mint(plaintext, name string, cleanProjects, scopes []string, ttl time.Duration, ownerUserID string) (Token, error) {
	hash := sha256.Sum256([]byte(plaintext))
	hashHex := hex.EncodeToString(hash[:])
	tok := Token{
		ID:          hashHex[:8],
		Name:        name,
		Hash:        hashHex,
		CreatedAt:   time.Now().UTC(),
		Scopes:      append([]string(nil), scopes...),
		OwnerUserID: ownerUserID,
	}
	// Legacy field carries the first project so older binaries reading this
	// tokens.json see a narrower (never wider) scope; the full list is only
	// persisted when it actually is a list.
	if len(cleanProjects) > 0 {
		tok.Project = cleanProjects[0]
	}
	if len(cleanProjects) > 1 {
		tok.Projects = cleanProjects
	}
	if ttl != 0 {
		tok.ExpiresAt = tok.CreatedAt.Add(ttl)
	}

	s.mu.Lock()
	s.reloadIfStale()
	s.tokens = append(s.tokens, tok)
	if err := s.save(); err != nil {
		s.tokens = s.tokens[:len(s.tokens)-1]
		s.mu.Unlock()
		return Token{}, err
	}
	s.mu.Unlock()
	return tok, nil
}

// ByID returns a copy of the token with the given ID, reloading the file
// first so a revocation made by the CLI is honoured. Used by the OAuth
// access-token resolver on every request.
func (s *Store) ByID(id string) (*Token, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	for i := range s.tokens {
		if s.tokens[i].ID == id {
			out := s.tokens[i]
			return &out, true
		}
	}
	return nil, false
}

// ByRefreshHash returns a copy of the OAuth grant whose current refresh token
// hashes to hash (hex sha256). Constant-time compare per record.
func (s *Store) ByRefreshHash(hash string) (*Token, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	for i := range s.tokens {
		t := &s.tokens[i]
		if t.RefreshHash != "" && subtle.ConstantTimeCompare([]byte(t.RefreshHash), []byte(hash)) == 1 {
			out := *t
			return &out, true
		}
	}
	return nil, false
}

// SetRefresh stores the hash of a grant's current refresh token and its
// expiry, replacing the previous one (rotation). An empty hash clears it.
func (s *Store) SetRefresh(id, refreshHash string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	for i := range s.tokens {
		if s.tokens[i].ID == id {
			s.tokens[i].RefreshHash = refreshHash
			s.tokens[i].RefreshExpiresAt = expiresAt
			return s.save()
		}
	}
	return fmt.Errorf("token %q not found", id)
}

// Revoke deletes a token identified by its ID prefix (first 8 hex of hash).
func (s *Store) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
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
	s.reloadIfStale()
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
	s.reloadIfStale()
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

// SetSelfImproveOptIn toggles the self-improve opt-in flag on the token
// identified by its ID prefix and persists the change. Used by the admin UI
// and CLI to enrol/withdraw a token from the self-improvement nudge loop
// (plan 20260608-self-improve-feedback-loop). No migration is needed for
// existing tokens.json files: the field is additive and absent records
// deserialize to false.
func (s *Store) SetSelfImproveOptIn(id string, optIn bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	for i := range s.tokens {
		if s.tokens[i].ID == id {
			s.tokens[i].SelfImproveOptIn = optIn
			return s.save()
		}
	}
	return fmt.Errorf("token %q not found", id)
}

// SetToolProfile assigns the MCP tool profile ("", "full" or "core") to an
// existing token and persists the store.
func (s *Store) SetToolProfile(id, profile string) error {
	if !ValidToolProfile(profile) {
		return fmt.Errorf("invalid tool profile %q (expected core or full)", profile)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	for i := range s.tokens {
		if s.tokens[i].ID == id {
			s.tokens[i].ToolProfile = profile
			return s.save()
		}
	}
	return fmt.Errorf("token %q not found", id)
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

// normalizeProjects trims, drops empties and deduplicates while preserving
// order.
func normalizeProjects(projects []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(projects))
	for _, p := range projects {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
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
