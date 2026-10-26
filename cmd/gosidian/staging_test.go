package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gosidian/gosidian/internal/vault"
)

// The bridge dir and the upload roots inside the vault are the folders git
// sync leaves out; the vault itself and a folder outside it are not
// (BUG-138).
func TestStagingInVault(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{".staging", "imports", "p"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	v := vault.New(root)
	outside := t.TempDir()
	got := stagingInVault(v, filepath.Join(root, ".staging"),
		[]string{root, outside, filepath.Join(root, "imports"), filepath.Join(root, ".staging")})
	if want := []string{".staging", "imports"}; !slices.Equal(got, want) {
		t.Errorf("staging = %v, want %v", got, want)
	}
	if got := stagingInVault(v, "", nil); len(got) != 0 {
		t.Errorf("nothing configured: %v", got)
	}
}
