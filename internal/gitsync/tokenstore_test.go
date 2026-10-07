package gitsync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gosidian/gosidian/internal/config"
)

func TestTokenStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gitsync.json")
	ts, err := OpenTokenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := ts.Get(); got != "" {
		t.Errorf("empty store should yield empty token, got %q", got)
	}
	if err := ts.Set("glpat-1234567890ABCDE"); err != nil {
		t.Fatal(err)
	}
	if got := ts.Get(); got != "glpat-1234567890ABCDE" {
		t.Errorf("Get = %q", got)
	}

	// Reopen from disk
	ts2, err := OpenTokenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := ts2.Get(); got != "glpat-1234567890ABCDE" {
		t.Errorf("after reopen Get = %q", got)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("file perms = %o, want 0600", st.Mode().Perm())
	}
}

func TestTokenStore_Clear(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gitsync.json")
	ts, _ := OpenTokenStore(path)
	_ = ts.Set("glpat-secret")
	if err := ts.Clear(); err != nil {
		t.Fatal(err)
	}
	if got := ts.Get(); got != "" {
		t.Errorf("after Clear Get = %q", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be removed: stat err = %v", err)
	}
	// Idempotent: Clear on already-cleared store is fine.
	if err := ts.Clear(); err != nil {
		t.Errorf("Clear on missing file should be no-op: %v", err)
	}
}

func TestTokenStore_SetEmptyRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gitsync.json")
	ts, _ := OpenTokenStore(path)
	if err := ts.Set(""); err == nil {
		t.Errorf("Set(\"\") should be rejected")
	}
}

func TestTokenStore_OpenMissingDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "missing", "gitsync.json")
	ts, err := OpenTokenStore(path)
	if err != nil {
		t.Fatalf("Open on missing parent dir should succeed: %v", err)
	}
	if got := ts.Get(); got != "" {
		t.Errorf("missing file should yield empty token, got %q", got)
	}
	// Set creates the parent directory.
	if err := ts.Set("token123"); err != nil {
		t.Fatalf("Set should create parent dir: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("after Set file should exist: %v", err)
	}
}

func TestAuthToken_EnvWinsOverFile(t *testing.T) {
	dir := t.TempDir()
	ts, _ := OpenTokenStore(filepath.Join(dir, "gitsync.json"))
	_ = ts.Set("from-file")

	cfg := config.GitConfig{TokenEnv: "GOSIDIAN_TEST_TOKEN_PRECEDENCE"}
	s := New(dir, cfg)
	s.SetTokenStore(ts)

	// 1. env unset → file wins
	t.Setenv("GOSIDIAN_TEST_TOKEN_PRECEDENCE", "")
	if got := s.authToken(); got != "from-file" {
		t.Errorf("env unset: got %q, want from-file", got)
	}

	// 2. env set → env wins
	t.Setenv("GOSIDIAN_TEST_TOKEN_PRECEDENCE", "from-env")
	if got := s.authToken(); got != "from-env" {
		t.Errorf("env set: got %q, want from-env", got)
	}

	// 3. env unset, file cleared → empty
	t.Setenv("GOSIDIAN_TEST_TOKEN_PRECEDENCE", "")
	_ = ts.Clear()
	if got := s.authToken(); got != "" {
		t.Errorf("env unset + file cleared: got %q, want empty", got)
	}
}

func TestAuthToken_FileFallbackWithoutTokenEnv(t *testing.T) {
	dir := t.TempDir()
	ts, _ := OpenTokenStore(filepath.Join(dir, "gitsync.json"))
	_ = ts.Set("file-only")

	// No TokenEnv configured at all → file is the only source.
	cfg := config.GitConfig{}
	s := New(dir, cfg)
	s.SetTokenStore(ts)

	if got := s.authToken(); got != "file-only" {
		t.Errorf("got %q, want file-only", got)
	}
}
