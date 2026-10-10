package secrets

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestStore_SealsAndOpens(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	s, err := Open(path, key(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("openrouter", "sk-or-very-secret"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("openrouter")
	if err != nil || got != "sk-or-very-secret" {
		t.Fatalf("%q %v", got, err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "very-secret") {
		t.Fatal("the value is in clear in the file")
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	list, _ := s.List()
	if len(list) != 1 || list[0].Name != "openrouter" || list[0].Updated.IsZero() {
		t.Fatalf("list %+v", list)
	}
	if _, err := s.Get("other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing secret: %v", err)
	}
	if err := s.Delete("openrouter"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("openrouter"); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted, still there")
	}
}

func TestStore_AnotherKeyIsRefusedAtOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	s, _ := Open(path, key(1))
	_ = s.Set("a", "x")
	if _, err := Open(path, key(2)); err == nil || !strings.Contains(err.Error(), EnvVar) {
		t.Fatalf("another key: %v", err)
	}
}

func TestStore_AValueMovedUnderAnotherNameDoesNotOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	s, _ := Open(path, key(1))
	_ = s.Set("a", "x")
	raw, _ := os.ReadFile(path)
	_ = os.WriteFile(path, bytes.Replace(raw, []byte(`"a"`), []byte(`"b"`), 1), 0o600)
	if _, err := Open(path, key(1)); err == nil {
		t.Fatal("a value moved to another name opened")
	}
}

func TestParseKey(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(key(7))
	if k, err := ParseKey(" " + good + "\n"); err != nil || len(k) != 32 {
		t.Fatal(err)
	}
	if _, err := ParseKey(""); !errors.Is(err, ErrNoKey) {
		t.Fatal("an empty key")
	}
	if _, err := ParseKey(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("a short key")
	}
	if _, err := ParseKey("not base64 at all!"); err == nil {
		t.Fatal("not base64")
	}
	s, _ := Open(filepath.Join(t.TempDir(), FileName), key(1))
	if err := s.Set("Bad Name", "x"); err == nil {
		t.Fatal("a bad name")
	}
}
