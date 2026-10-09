package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The source and the destination of import-vault are separate folders, and
// a link in the source is not copied (BUG-115, S5-13).
func TestImportVault_SeparateDirsAndLinks(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "obsidian")
	if err := os.MkdirAll(filepath.Join(src, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "notes", "a.md"), []byte("# a"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "secret.md")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(src, "notes", "link.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	for _, dst := range []string{src, filepath.Join(src, "inside", "new"), root, filepath.Join(root, "alias")} {
		if err := separateDirs(src, dst); err == nil {
			t.Errorf("separateDirs(%s, %s) accepted", src, dst)
		}
	}
	dst := filepath.Join(root, "vault")
	if err := separateDirs(src, dst); err != nil {
		t.Fatalf("separate folders refused: %v", err)
	}
	copied, skipped := importDir(src, dst, false)
	if copied != 1 || skipped != 1 {
		t.Errorf("copied %d, skipped %d; want 1 and 1", copied, skipped)
	}
	if _, err := os.Lstat(filepath.Join(dst, "notes", "link.md")); err == nil {
		t.Error("the link was copied")
	}
}
