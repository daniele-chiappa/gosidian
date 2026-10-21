package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/vault"
)

// IMP-185: what a note's write costs as the vault grows. A synthetic vault
// of n notes over ten projects, each with frontmatter, tags, three links to
// other notes and one that resolves to nothing; then notes written through
// memory_create, as an import writes them.

// scaleVault writes n notes under dir and indexes them into a fresh index.
func scaleVault(tb testing.TB, n int) (*Server, string) {
	tb.Helper()
	dir := tb.TempDir()
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("p%d", i%10), fmt.Sprintf("n%06d.md", i))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(scaleNote(i, n)), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	idx, err := index.Open(filepath.Join(tb.TempDir(), "idx.db"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { idx.Close() })
	v := vault.New(dir)
	if err := v.ScanInto(idx); err != nil {
		tb.Fatal(err)
	}
	s := New(v, idx, nil)
	s.SetWriteLimits(1_000_000, 0) // an import, not an agent: no per-minute cap
	return s, dir
}

// scaleNote is note i of a vault of n: frontmatter, tags, three links to
// other notes of the vault and one to a note that does not exist.
func scaleNote(i, n int) string {
	link := func(k int) string { return fmt.Sprintf("[[p%d/n%06d]]", k%10, k) }
	return fmt.Sprintf("---\ntitle: Nota %d\ntype: doc\nstatus: open\ncreated: 2026-10-%02d\ntags: [t%d, scale]\n---\n\n# Nota %d\n\nVedi %s, %s e %s; manca [[assente-%d]].\n",
		i, 1+i%28, i%50, i, link((i*7+1)%n), link((i*13+5)%n), link((i*31+11)%n), i%500)
}

func writeScale(tb testing.TB, s *Server, n, from, m int) time.Duration {
	tb.Helper()
	ctx := context.Background()
	start := time.Now()
	for i := from; i < from+m; i++ {
		res, err := s.handleCreate(ctx, call(map[string]any{
			"path":    fmt.Sprintf("p0/new/w%06d.md", i),
			"content": scaleNote(n+i, n),
		}))
		if err != nil || res.IsError {
			tb.Fatalf("create %d: %v %+v", i, err, res)
		}
	}
	return time.Since(start)
}

// TestWriteScale logs the time of a write at three sizes of the vault: a
// cost that grows with the vault shows here. GOSIDIAN_WRITE_SCALE=1 runs it.
func TestWriteScale(t *testing.T) {
	if os.Getenv("GOSIDIAN_WRITE_SCALE") == "" {
		t.Skip("GOSIDIAN_WRITE_SCALE unset")
	}
	const m = 200
	for _, n := range []int{1000, 5000, 20000} {
		start := time.Now()
		s, _ := scaleVault(t, n)
		built := time.Since(start)
		took := writeScale(t, s, n, 0, m)
		t.Logf("vault %6d notes (built in %v): %d writes in %v, %.2f ms each", n, built.Round(time.Millisecond), m, took.Round(time.Millisecond), float64(took.Microseconds())/1000/m)
	}
}

// BenchmarkWriteNote writes notes through memory_create into a vault of
// GOSIDIAN_WRITE_SCALE_N notes (default 5000), for a CPU profile:
// go test -run x -bench WriteNote -cpuprofile cpu.out ./internal/mcp/
func BenchmarkWriteNote(b *testing.B) {
	n := 5000
	if v, err := strconv.Atoi(os.Getenv("GOSIDIAN_WRITE_SCALE_N")); err == nil && v > 0 {
		n = v
	}
	s, _ := scaleVault(b, n)
	b.ResetTimer()
	writeScale(b, s, n, 0, b.N)
}
