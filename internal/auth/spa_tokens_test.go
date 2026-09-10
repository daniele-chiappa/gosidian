package auth

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSpaTokenStore_CreateAndValidate(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenSpaTokens(filepath.Join(dir, "spa.json"))
	if err != nil {
		t.Fatal(err)
	}
	plain, tok, err := s.Create("user-1", "test-agent")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, spaTokenPrefix) {
		t.Errorf("plaintext missing prefix: %q", plain)
	}
	if tok.Hash == "" || tok.ID == "" || tok.UserID != "user-1" {
		t.Errorf("token shape unexpected: %+v", tok)
	}
	if tok.UserAgent != "test-agent" {
		t.Errorf("UA = %q", tok.UserAgent)
	}

	got, err := s.Validate(plain)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.UserID != "user-1" {
		t.Errorf("Validate UserID = %q", got.UserID)
	}
}

func TestSpaTokenStore_ValidateRejectsUnknown(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenSpaTokens(filepath.Join(dir, "spa.json"))
	if _, err := s.Validate("gsp_unknown"); err == nil {
		t.Errorf("expected error on unknown token")
	}
}

func TestSpaTokenStore_ValidateRejectsExpired(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenSpaTokens(filepath.Join(dir, "spa.json"))
	s.SetTTL(1*time.Millisecond, 1*time.Hour)
	plain, _, _ := s.Create("user-1", "")
	time.Sleep(5 * time.Millisecond)
	_, err := s.Validate(plain)
	if err == nil {
		t.Errorf("expected expired error")
	}
}

func TestSpaTokenStore_ValidateRejectsHardExpired(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenSpaTokens(filepath.Join(dir, "spa.json"))
	s.SetTTL(1*time.Hour, 1*time.Millisecond)
	plain, _, _ := s.Create("user-1", "")
	time.Sleep(5 * time.Millisecond)
	_, err := s.Validate(plain)
	if err == nil || !strings.Contains(err.Error(), "hard-expired") {
		t.Errorf("expected hard-expired error, got %v", err)
	}
}

func TestSpaTokenStore_Refresh(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenSpaTokens(filepath.Join(dir, "spa.json"))
	// Wide windows on purpose (BUG-032): the assertion is bidirectional
	// ("still has time left"), so the margin must dwarf any scheduling
	// hiccup on a shared CI runner. Expiry-only checks below keep tiny
	// TTLs because waiting longer can only make them pass.
	s.SetTTL(2*time.Second, 1*time.Hour)
	plain, _, _ := s.Create("user-1", "")
	time.Sleep(20 * time.Millisecond)
	got, err := s.Refresh(plain)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got.ExpiresAt.Sub(time.Now().UTC()) < 1*time.Second {
		t.Errorf("refresh did not extend ExpiresAt")
	}
	// Refresh after hard expiry should fail
	s.SetTTL(50*time.Millisecond, 5*time.Millisecond)
	plain2, _, _ := s.Create("user-2", "")
	time.Sleep(10 * time.Millisecond)
	if _, err := s.Refresh(plain2); err == nil {
		t.Errorf("Refresh past hard-expiry should fail")
	}
}

func TestSpaTokenStore_Revoke(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenSpaTokens(filepath.Join(dir, "spa.json"))
	plain, _, _ := s.Create("user-1", "")
	if err := s.Revoke(plain); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Validate(plain); err == nil {
		t.Errorf("revoked token should be invalid")
	}
	// Idempotent
	if err := s.Revoke("gsp_unknown"); err != nil {
		t.Errorf("revoke missing should be no-op: %v", err)
	}
}

func TestSpaTokenStore_RevokeByID(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenSpaTokens(filepath.Join(dir, "spa.json"))
	_, tok, _ := s.Create("user-1", "")
	if err := s.RevokeByID(tok.ID); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 0 {
		t.Errorf("RevokeByID did not remove entry, count=%d", s.Count())
	}
}

func TestSpaTokenStore_RevokeByUser(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenSpaTokens(filepath.Join(dir, "spa.json"))
	_, _, _ = s.Create("user-1", "")
	_, _, _ = s.Create("user-1", "")
	_, _, _ = s.Create("user-2", "")
	if revoked := s.RevokeByUser("user-1"); revoked != 2 {
		t.Errorf("expected 2 revocations, got %d", revoked)
	}
	if s.Count() != 1 {
		t.Errorf("expected 1 remaining, got %d", s.Count())
	}
}

func TestSpaTokenStore_PruneExpired(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenSpaTokens(filepath.Join(dir, "spa.json"))
	s.SetTTL(1*time.Millisecond, 1*time.Hour)
	_, _, _ = s.Create("user-1", "")
	_, _, _ = s.Create("user-2", "")
	time.Sleep(5 * time.Millisecond)
	if err := s.PruneExpired(); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 0 {
		t.Errorf("expected 0 after prune, got %d", s.Count())
	}
}

func TestSpaTokenStore_ListByUserSorted(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenSpaTokens(filepath.Join(dir, "spa.json"))
	_, t1, _ := s.Create("u", "agent-1")
	time.Sleep(2 * time.Millisecond)
	_, t2, _ := s.Create("u", "agent-2")
	list := s.ListByUser("u")
	if len(list) != 2 {
		t.Fatalf("len=%d", len(list))
	}
	if list[0].ID != t2.ID || list[1].ID != t1.ID {
		t.Errorf("not sorted desc by IssuedAt: %+v", list)
	}
}

func TestSpaTokenStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spa.json")
	s1, _ := OpenSpaTokens(path)
	plain, _, _ := s1.Create("user-1", "")
	s2, err := OpenSpaTokens(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Validate(plain); err != nil {
		t.Errorf("token lost across reopens: %v", err)
	}
}
