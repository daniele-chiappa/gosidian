// Package statedir resolves and migrates gosidian's machine-owned state
// directory (ADR-023): credentials, configuration, project flags, audit log
// and the SQLite index. By default it is <vault>/.gosidian, unchanged since
// v1; an explicit --state-dir / GOSIDIAN_STATE_DIR moves those files out of
// the vault root — out of reach of any path that resolves inside the vault
// and out of the vault's git repository by construction — with a one-time
// migration at boot. templates/ and trash/ are vault content and stay under
// <vault>/.gosidian.
package statedir

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// EnvVar is the environment variable read by every gosidian command.
	EnvVar = "GOSIDIAN_STATE_DIR"
	// DefaultSubdir is the legacy location, relative to the vault root.
	DefaultSubdir = ".gosidian"
	// MarkerFile is written into the legacy directory after a migration so a
	// human (or an older binary) sees where the files went.
	MarkerFile = "STATE-MOVED-TO"
)

// Files lists the entries that belong to the state directory and migrate
// out of <vault>/.gosidian when a custom state dir is configured. The index
// files travel together (WAL/SHM sidecars are only meaningful next to the
// database they belong to).
var Files = []string{
	"auth.json",
	"tokens.json",
	"spa_tokens.json",
	"gitsync.json",
	"config.toml",
	"projects.json",
	"audit.jsonl",
	"index.db",
	"index.db-wal",
	"index.db-shm",
}

// Resolve picks the state directory: flag value, then env value, then the
// default <vault>/.gosidian. isDefault reports whether the default applies
// (in which case no migration ever runs).
func Resolve(vault, flagValue, envValue string) (dir string, isDefault bool, err error) {
	v := strings.TrimSpace(flagValue)
	if v == "" {
		v = strings.TrimSpace(envValue)
	}
	if v == "" {
		return filepath.Join(vault, DefaultSubdir), true, nil
	}
	abs, err := filepath.Abs(v)
	if err != nil {
		return "", false, fmt.Errorf("state dir %q: %w", v, err)
	}
	return abs, false, nil
}

// Migrate moves every known state file present in legacy and absent in
// target. Files present in both are left untouched and reported through logf
// (the operator resolves the conflict by hand); unknown files and
// directories in legacy (templates/, trash/, ad-hoc backups) are never
// touched. It is idempotent: a second call finds nothing to move. When at
// least one file moved, a marker file is written into legacy.
func Migrate(legacy, target string, logf func(format string, args ...any)) (moved []string, err error) {
	if filepath.Clean(legacy) == filepath.Clean(target) {
		return nil, nil
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return nil, err
	}
	for _, name := range Files {
		src := filepath.Join(legacy, name)
		dst := filepath.Join(target, name)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		if _, err := os.Lstat(dst); err == nil {
			logf("state dir: %s exists in both %s and %s — left untouched, resolve by hand", name, legacy, target)
			continue
		}
		if err := moveFile(src, dst); err != nil {
			return moved, fmt.Errorf("move %s: %w", name, err)
		}
		moved = append(moved, name)
	}
	if len(moved) > 0 {
		note := fmt.Sprintf("gosidian moved its state files to %s on %s\nmoved: %s\n"+
			"This directory now holds vault content only (templates/, trash/).\n",
			target, time.Now().UTC().Format(time.RFC3339), strings.Join(moved, ", "))
		if err := os.WriteFile(filepath.Join(legacy, MarkerFile), []byte(note), 0o644); err != nil {
			logf("state dir: cannot write marker %s: %v", MarkerFile, err)
		}
	}
	return moved, nil
}

// rename is a seam for tests (forcing the cross-device copy path).
var rename = os.Rename

// moveFile renames src to dst, falling back to copy+fsync+remove when the
// two paths sit on different filesystems (a bind mount next to a volume is
// the common Docker case). The copy keeps the source's permission bits, so
// 0600 credential files stay 0600.
func moveFile(src, dst string) error {
	err := rename(src, dst)
	if err == nil {
		return nil
	}
	var le *os.LinkError
	if !errors.As(err, &le) || !errors.Is(le.Err, syscall.EXDEV) {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return os.Remove(src)
}
