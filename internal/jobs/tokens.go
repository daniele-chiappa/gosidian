package jobs

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// TokenPrefix starts a run token, so a leaked one is recognisable (and
// caught by secret scanners, like the MCP tokens' gosidian_).
const TokenPrefix = "gosidian_run_"

// RunToken is a token that starts one pipeline from outside and does
// nothing else (IMP-167): for a service that has no account.
type RunToken struct {
	ID        string    `json:"id"`
	Ref       string    `json:"pipeline"`
	Name      string    `json:"name"`
	Created   time.Time `json:"created"`
	CreatedBy string    `json:"created_by"`
	LastUsed  time.Time `json:"last_used,omitzero"`
}

func hashToken(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// CreateToken mints a run token for ref. The secret comes back once:
// only its hash is stored.
func (s *Store) CreateToken(ref, name, createdBy string) (RunToken, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := RunToken{ID: NewID(), Ref: ref, Name: name, Created: time.Now().UTC(), CreatedBy: createdBy}
	secret := TokenPrefix + NewSecret()
	_, err := s.db.Exec(`INSERT INTO tokens (id, ref, hash, name, created, created_by) VALUES (?,?,?,?,?,?)`,
		t.ID, t.Ref, hashToken(secret), t.Name, ms(t.Created), t.CreatedBy)
	if err != nil {
		return RunToken{}, "", err
	}
	return t, secret, nil
}

// Tokens lists the run tokens of ref, or of every pipeline for "".
func (s *Store) Tokens(ref string) ([]RunToken, error) {
	q := `SELECT id, ref, name, created, created_by, last_used FROM tokens`
	var args []any
	if ref != "" {
		q += ` WHERE ref = ?`
		args = append(args, ref)
	}
	rows, err := s.db.Query(q+` ORDER BY created`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunToken
	for rows.Next() {
		var t RunToken
		var created, used int64
		if err := rows.Scan(&t.ID, &t.Ref, &t.Name, &created, &t.CreatedBy, &used); err != nil {
			return nil, err
		}
		t.Created, t.LastUsed = fromMS(created), fromMS(used)
		out = append(out, t)
	}
	return out, rows.Err()
}

// CheckToken says which pipeline a run token starts, and stamps its use.
func (s *Store) CheckToken(secret string) (RunToken, error) {
	if !strings.HasPrefix(secret, TokenPrefix) {
		return RunToken{}, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var t RunToken
	var created, used int64
	err := s.db.QueryRow(`SELECT id, ref, name, created, created_by, last_used FROM tokens WHERE hash = ?`, hashToken(secret)).
		Scan(&t.ID, &t.Ref, &t.Name, &created, &t.CreatedBy, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return RunToken{}, ErrNotFound
	}
	if err != nil {
		return RunToken{}, err
	}
	t.Created = fromMS(created)
	t.LastUsed = time.Now().UTC()
	_, _ = s.db.Exec(`UPDATE tokens SET last_used = ? WHERE id = ?`, ms(t.LastUsed), t.ID)
	return t, nil
}

// RevokeToken deletes a run token.
func (s *Store) RevokeToken(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM tokens WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
