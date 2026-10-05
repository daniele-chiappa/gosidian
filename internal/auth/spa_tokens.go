package auth

// SpaTokenStore persists short-lived browser session tokens issued by the
// SPA login endpoint (POST /api/v1/login). It is intentionally separate
// from the MCP token Store: agent tokens live in tokens.json, browser
// sessions in spa_tokens.json. The two have different lifecycles
// (sliding-TTL refresh vs. long-lived agent capability) and different
// audiences (vue-i18n localStorage cookie vs. agent runtime config), so
// keeping them apart simplifies audit and rotation.
//
// Threat model: a stolen SPA token grants whatever role the originating
// user holds. Mitigations:
//   - Stored only as SHA-256 hash on disk (the plaintext exists once at
//     create time and ships in the response body).
//   - Sliding TTL 24h, hard expiry 7d. Refresh extends the window without
//     re-entering credentials so usability stays close to "keep me
//     logged in" while bounded.
//   - Idle revocation: a token unused for >24h is considered stale and
//     pruned by the cleanup goroutine.
//   - Owner can revoke any user's tokens via /api/v1/admin/users.
//
// The SPA carries the token in an Authorization: Bearer header. Despite
// the conventional XSS warnings around localStorage storage, the SPA
// runs CSP strict (no unsafe-inline / unsafe-eval) and renders markdown
// through DOMPurify, so realistic XSS exfiltration vectors are limited.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// SPA token defaults. Sliding TTL governs the time-since-last-use window
// after which a token must be refreshed; hard TTL caps the absolute
// lifetime regardless of activity. Both are configurable per-store via
// SpaTokenStore.SetTTL for tests.
const (
	defaultSpaSlidingTTL = 24 * time.Hour
	defaultSpaHardTTL    = 7 * 24 * time.Hour
	spaTokenPrefix       = "gsp_"
)

// SpaToken is the on-disk shape (kept tight; the Hash is a SHA-256 hex
// digest of the plaintext, never the plaintext itself).
type SpaToken struct {
	ID         string    `json:"id"`      // first 8 hex of Hash, displayable
	Hash       string    `json:"hash"`    // sha256 hex of plaintext
	UserID     string    `json:"user_id"` // webauth user.ID
	UserAgent  string    `json:"user_agent,omitempty"`
	IssuedAt   time.Time `json:"issued_at"`
	ExpiresAt  time.Time `json:"expires_at"`  // sliding deadline
	HardExpiry time.Time `json:"hard_expiry"` // absolute deadline
	LastSeenAt time.Time `json:"last_seen_at"`
}

// SpaTokenStore is concurrent-safe and reloads transparently when the
// underlying file's mtime changes (mirrors auth.Store). The cleanup
// goroutine started by Open prunes expired entries every hour.
type SpaTokenStore struct {
	path        string
	mu          sync.RWMutex
	tokens      []SpaToken
	mtime       time.Time
	slidingTTL  time.Duration
	hardTTL     time.Duration
	cleanupStop chan struct{}
}

type spaTokenFile struct {
	Tokens []SpaToken `json:"tokens"`
}

// OpenSpaTokens loads the store from disk. A missing file is not an
// error; the store is initialised empty and the file appears on first
// Create.
func OpenSpaTokens(path string) (*SpaTokenStore, error) {
	s := &SpaTokenStore{
		path:        path,
		slidingTTL:  defaultSpaSlidingTTL,
		hardTTL:     defaultSpaHardTTL,
		cleanupStop: make(chan struct{}),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// SetTTL overrides the sliding/hard TTL pair. Intended for tests; not
// surfaced via env vars to keep the production surface minimal.
func (s *SpaTokenStore) SetTTL(sliding, hard time.Duration) {
	s.mu.Lock()
	s.slidingTTL = sliding
	s.hardTTL = hard
	s.mu.Unlock()
}

// StartCleanup launches a goroutine that prunes expired entries on
// every interval. Call once at startup; pass a context-derived stop
// channel for shutdown.
func (s *SpaTokenStore) StartCleanup(interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				_ = s.PruneExpired()
			case <-s.cleanupStop:
				return
			}
		}
	}()
}

// Close stops the cleanup goroutine. Idempotent.
func (s *SpaTokenStore) Close() {
	select {
	case <-s.cleanupStop:
		return
	default:
		close(s.cleanupStop)
	}
}

// Path returns the on-disk file path. Useful for diagnostics.
func (s *SpaTokenStore) Path() string { return s.path }

func (s *SpaTokenStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.tokens = nil
			s.mtime = time.Time{}
			return nil
		}
		return err
	}
	var sf spaTokenFile
	if len(data) > 0 {
		if err := json.Unmarshal(data, &sf); err != nil {
			return fmt.Errorf("parse spa token file: %w", err)
		}
	}
	s.tokens = sf.Tokens
	if st, err := os.Stat(s.path); err == nil {
		s.mtime = st.ModTime()
	}
	return nil
}

func (s *SpaTokenStore) reloadIfStale() {
	st, err := os.Stat(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !s.mtime.IsZero() || len(s.tokens) > 0 {
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

func (s *SpaTokenStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(spaTokenFile{Tokens: s.tokens}, "", "  ")
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
	if st, err := os.Stat(s.path); err == nil {
		s.mtime = st.ModTime()
	}
	return nil
}

// Create issues a new SPA token for the given user. Returns the
// plaintext (which the caller must ship to the client and never
// persist) plus the stored shape.
func (s *SpaTokenStore) Create(userID, userAgent string) (plaintext string, tok SpaToken, err error) {
	if userID == "" {
		return "", SpaToken{}, errors.New("userID required")
	}
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", SpaToken{}, err
	}
	plaintext = spaTokenPrefix + base64.RawURLEncoding.EncodeToString(raw[:])
	sum := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(sum[:])
	now := time.Now().UTC()
	tok = SpaToken{
		ID:         hash[:8],
		Hash:       hash,
		UserID:     userID,
		UserAgent:  userAgent,
		IssuedAt:   now,
		ExpiresAt:  now.Add(s.slidingTTL),
		HardExpiry: now.Add(s.hardTTL),
		LastSeenAt: now,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	s.tokens = append(s.tokens, tok)
	if err := s.save(); err != nil {
		return "", SpaToken{}, err
	}
	return plaintext, tok, nil
}

// Validate checks the plaintext, advances LastSeenAt (without saving on
// the hot path — saving happens on Refresh) and returns the stored
// token entry. Returns an error for invalid, expired, or
// hard-expired tokens.
func (s *SpaTokenStore) Validate(plaintext string) (*SpaToken, error) {
	sum := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(sum[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	now := time.Now().UTC()
	for i := range s.tokens {
		if s.tokens[i].Hash != hash {
			continue
		}
		if now.After(s.tokens[i].HardExpiry) {
			return nil, errors.New("token hard-expired")
		}
		if now.After(s.tokens[i].ExpiresAt) {
			return nil, errors.New("token expired (refresh required)")
		}
		s.tokens[i].LastSeenAt = now
		t := s.tokens[i]
		return &t, nil
	}
	return nil, errors.New("token not found")
}

// Refresh extends the sliding TTL of an existing token. The hard expiry
// is not extended — it caps absolute lifetime so a refresh chain cannot
// keep a leaked token alive forever. Returns the unchanged plaintext
// (refresh keeps the same token value; only metadata updates).
func (s *SpaTokenStore) Refresh(plaintext string) (*SpaToken, error) {
	sum := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(sum[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	now := time.Now().UTC()
	for i := range s.tokens {
		if s.tokens[i].Hash != hash {
			continue
		}
		if now.After(s.tokens[i].HardExpiry) {
			return nil, errors.New("token hard-expired (login required)")
		}
		s.tokens[i].ExpiresAt = now.Add(s.slidingTTL)
		s.tokens[i].LastSeenAt = now
		if err := s.save(); err != nil {
			return nil, err
		}
		t := s.tokens[i]
		return &t, nil
	}
	return nil, errors.New("token not found")
}

// Revoke removes the token matching the plaintext. No-op if absent.
func (s *SpaTokenStore) Revoke(plaintext string) error {
	sum := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(sum[:])
	return s.RevokeByHash(hash)
}

// RevokeByID removes the token by its display ID (first 8 hex of Hash).
// Used by the admin UI which never sees plaintexts.
func (s *SpaTokenStore) RevokeByID(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	out := s.tokens[:0]
	dirty := false
	for _, t := range s.tokens {
		if t.ID == id {
			dirty = true
			continue
		}
		out = append(out, t)
	}
	s.tokens = out
	if !dirty {
		return nil
	}
	return s.save()
}

// RevokeByHash removes the token by its sha256 hex digest. Used
// internally and by tests.
func (s *SpaTokenStore) RevokeByHash(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	out := s.tokens[:0]
	dirty := false
	for _, t := range s.tokens {
		if t.Hash == hash {
			dirty = true
			continue
		}
		out = append(out, t)
	}
	s.tokens = out
	if !dirty {
		return nil
	}
	return s.save()
}

// RevokeByUser removes every token for the given userID. Returns the
// count revoked. Mirrors the cascade used by auth.Store on user
// disable.
func (s *SpaTokenStore) RevokeByUser(userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	out := s.tokens[:0]
	revoked := 0
	for _, t := range s.tokens {
		if t.UserID == userID {
			revoked++
			continue
		}
		out = append(out, t)
	}
	s.tokens = out
	if revoked > 0 {
		_ = s.save()
	}
	return revoked
}

// RevokeByUserExcept removes every token of userID but the one whose
// plaintext is keep: the other sessions of an account that just changed
// its password (IMP-063). Returns the count revoked.
func (s *SpaTokenStore) RevokeByUserExcept(userID, keep string) int {
	sum := sha256.Sum256([]byte(keep))
	keepHash := hex.EncodeToString(sum[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	out := s.tokens[:0]
	revoked := 0
	for _, t := range s.tokens {
		if t.UserID == userID && t.Hash != keepHash {
			revoked++
			continue
		}
		out = append(out, t)
	}
	s.tokens = out
	if revoked > 0 {
		_ = s.save()
	}
	return revoked
}

// PruneExpired removes hard-expired or sliding-expired tokens from the
// store. Returns the count removed.
func (s *SpaTokenStore) PruneExpired() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	now := time.Now().UTC()
	out := s.tokens[:0]
	pruned := 0
	for _, t := range s.tokens {
		if now.After(t.HardExpiry) || now.After(t.ExpiresAt) {
			pruned++
			continue
		}
		out = append(out, t)
	}
	s.tokens = out
	if pruned == 0 {
		return nil
	}
	return s.save()
}

// ListByUser returns the active (non-expired) tokens for a user, sorted
// by IssuedAt descending. Used by the SPA admin UI to show a "where am
// I logged in" panel.
func (s *SpaTokenStore) ListByUser(userID string) []SpaToken {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now().UTC()
	out := make([]SpaToken, 0)
	for _, t := range s.tokens {
		if t.UserID != userID {
			continue
		}
		if now.After(t.HardExpiry) || now.After(t.ExpiresAt) {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IssuedAt.After(out[j].IssuedAt) })
	return out
}

// Count returns the number of stored entries (including expired ones
// not yet pruned).
func (s *SpaTokenStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tokens)
}
