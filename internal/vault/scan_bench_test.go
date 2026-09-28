package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/index"
)

// BenchmarkScanInto measures the boot scan into a fresh on-disk index, the
// path that keeps the server from listening at startup. The index lives on
// disk (not :memory:) so commit and fsync costs show up. Set
// GOSIDIAN_BENCH_VAULT to scan a real vault (read only) instead of the
// synthetic one.
func BenchmarkScanInto(b *testing.B) {
	root := os.Getenv("GOSIDIAN_BENCH_VAULT")
	if root == "" {
		root = syntheticVault(b, 300, 10)
	}
	v := New(root)
	for n := 0; n < b.N; n++ {
		idx, err := index.Open(filepath.Join(b.TempDir(), "index.db"))
		if err != nil {
			b.Fatal(err)
		}
		if err := v.ScanInto(idx); err != nil {
			b.Fatal(err)
		}
		idx.Close()
	}
}

// syntheticVault writes notes across a few projects, each linking to
// `links` others, so link resolution weighs as it does on a real vault.
func syntheticVault(b *testing.B, notes, links int) string {
	b.Helper()
	root := b.TempDir()
	for i := 0; i < notes; i++ {
		dir := filepath.Join(root, fmt.Sprintf("p%d", i%5))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		var body strings.Builder
		fmt.Fprintf(&body, "---\ntitle: Note %d\ntags: [bench, type:memory]\n---\n\n# Note %d\n\n", i, i)
		for l := 1; l <= links; l++ {
			fmt.Fprintf(&body, "See [[n%d]] for details. ", (i+l*7)%notes)
		}
		body.WriteString("\n\nLorem ipsum dolor sit amet, consectetur adipiscing elit.\n")
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("n%d.md", i)), []byte(body.String()), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	return root
}
