package vault

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// BenchmarkScanIntoWarm measures a restart on an unchanged vault: the index
// already holds every note, so the scan only compares hashes and resolves
// the links (IMP-104).
func BenchmarkScanIntoWarm(b *testing.B) {
	root := os.Getenv("GOSIDIAN_BENCH_VAULT")
	if root == "" {
		root = syntheticVault(b, 300, 10)
	}
	v := New(root)
	idx, err := index.Open(filepath.Join(b.TempDir(), "index.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer idx.Close()
	if err := v.ScanInto(idx); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if err := v.ScanInto(idx); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSearch runs the queries of level B (bench/queries.jsonl) on the
// index of GOSIDIAN_BENCH_VAULT, one sub-benchmark each, asking for the
// eleven hits memory_search asks for ten: the search on a large vault,
// query by query, without the MCP around it.
func BenchmarkSearch(b *testing.B) {
	root := os.Getenv("GOSIDIAN_BENCH_VAULT")
	if root == "" {
		b.Skip("GOSIDIAN_BENCH_VAULT is not set")
	}
	idx, err := index.Open(filepath.Join(b.TempDir(), "index.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer idx.Close()
	if err := New(root).ScanInto(idx); err != nil {
		b.Fatal(err)
	}
	f, err := os.Open("../../bench/queries.jsonl")
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	var queries []string
	for sc := bufio.NewScanner(f); sc.Scan(); {
		var q struct{ ID, Query string }
		if err := json.Unmarshal(sc.Bytes(), &q); err != nil {
			b.Fatal(err)
		}
		queries = append(queries, q.Query)
		b.Run(q.ID, func(b *testing.B) {
			for n := 0; n < b.N; n++ {
				if _, err := idx.SearchWith(q.Query, index.SearchOptions{Limit: 11}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	// All of them in turn, as level B sends them: no query finds what the
	// one before left warm. An op is a round of all the queries; p50 and
	// p95 are of the single searches.
	b.Run("mixed", func(b *testing.B) {
		var took []time.Duration
		for n := 0; n < b.N; n++ {
			for _, q := range queries {
				t0 := time.Now()
				if _, err := idx.SearchWith(q, index.SearchOptions{Limit: 11}); err != nil {
					b.Fatal(err)
				}
				took = append(took, time.Since(t0))
			}
		}
		slices.Sort(took)
		b.ReportMetric(float64(took[len(took)/2].Microseconds())/1000, "p50-ms")
		b.ReportMetric(float64(took[(len(took)*95+99)/100-1].Microseconds())/1000, "p95-ms")
	})
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
