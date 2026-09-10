package statedir

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestResolve_Precedence(t *testing.T) {
	vault := t.TempDir()
	dir, def, err := Resolve(vault, "", "")
	if err != nil || !def || dir != filepath.Join(vault, DefaultSubdir) {
		t.Fatalf("default: dir=%q def=%v err=%v", dir, def, err)
	}
	dir, def, _ = Resolve(vault, "", "/tmp/from-env")
	if def || dir != "/tmp/from-env" {
		t.Errorf("env: dir=%q def=%v", dir, def)
	}
	dir, def, _ = Resolve(vault, "/tmp/from-flag", "/tmp/from-env")
	if def || dir != "/tmp/from-flag" {
		t.Errorf("flag wins: dir=%q def=%v", dir, def)
	}
}

func seedLegacy(t *testing.T, legacy string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(legacy, "templates", "minimal"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{
		"auth.json": 0o600, "tokens.json": 0o600, "config.toml": 0o644, "index.db": 0o644, "index.db-wal": 0o644,
		"templates/minimal/_template.toml": 0o644, "git-backup.tgz": 0o644,
	} {
		if err := os.WriteFile(filepath.Join(legacy, name), []byte(name), mode); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrate_MovesKnownFilesOnly(t *testing.T) {
	vault := t.TempDir()
	legacy := filepath.Join(vault, DefaultSubdir)
	target := filepath.Join(t.TempDir(), "state")
	seedLegacy(t, legacy)

	var logs []string
	moved, err := Migrate(legacy, target, func(f string, a ...any) { logs = append(logs, f) })
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 5 {
		t.Errorf("moved=%v", moved)
	}
	for _, name := range []string{"auth.json", "tokens.json", "config.toml", "index.db", "index.db-wal"} {
		if _, err := os.Stat(filepath.Join(target, name)); err != nil {
			t.Errorf("%s not in target: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(legacy, name)); err == nil {
			t.Errorf("%s still in legacy", name)
		}
	}
	if st, _ := os.Stat(filepath.Join(target, "auth.json")); st.Mode().Perm() != 0o600 {
		t.Errorf("auth.json perm=%v", st.Mode().Perm())
	}
	for _, keep := range []string{"templates/minimal/_template.toml", "git-backup.tgz"} {
		if _, err := os.Stat(filepath.Join(legacy, keep)); err != nil {
			t.Errorf("%s should stay in legacy: %v", keep, err)
		}
	}
	marker, err := os.ReadFile(filepath.Join(legacy, MarkerFile))
	if err != nil || !strings.Contains(string(marker), target) {
		t.Errorf("marker: %v %q", err, marker)
	}
	// Idempotent.
	again, err := Migrate(legacy, target, func(string, ...any) {})
	if err != nil || len(again) != 0 {
		t.Errorf("second run moved=%v err=%v", again, err)
	}
	if len(logs) != 0 {
		t.Errorf("unexpected logs: %v", logs)
	}
}

func TestMigrate_ConflictLeftUntouched(t *testing.T) {
	vault := t.TempDir()
	legacy := filepath.Join(vault, DefaultSubdir)
	target := filepath.Join(t.TempDir(), "state")
	seedLegacy(t, legacy)
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "auth.json"), []byte("target-copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	var warned bool
	moved, err := Migrate(legacy, target, func(f string, a ...any) { warned = true })
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range moved {
		if m == "auth.json" {
			t.Errorf("conflicting file was moved")
		}
	}
	if b, _ := os.ReadFile(filepath.Join(target, "auth.json")); string(b) != "target-copy" {
		t.Errorf("target auth.json overwritten: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(legacy, "auth.json")); string(b) != "auth.json" {
		t.Errorf("legacy auth.json changed: %q", b)
	}
	if !warned {
		t.Errorf("conflict should be logged")
	}
}

func TestMigrate_SameDirNoop(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), DefaultSubdir)
	seedLegacy(t, legacy)
	moved, err := Migrate(legacy, legacy+string(filepath.Separator)+".", func(string, ...any) {})
	if err != nil || len(moved) != 0 {
		t.Errorf("same dir: moved=%v err=%v", moved, err)
	}
}

func TestMoveFile_CrossDeviceFallsBackToCopy(t *testing.T) {
	orig := rename
	rename = func(string, string) error { return &os.LinkError{Op: "rename", Err: syscall.EXDEV} }
	t.Cleanup(func() { rename = orig })

	dir := t.TempDir()
	src := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(src, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "state", "auth.json")
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := moveFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "secret" {
		t.Errorf("copied content: %q", b)
	}
	if st, _ := os.Stat(dst); st.Mode().Perm() != 0o600 {
		t.Errorf("copied perm=%v", st.Mode().Perm())
	}
	if _, err := os.Stat(src); err == nil {
		t.Errorf("source not removed after copy")
	}
}
