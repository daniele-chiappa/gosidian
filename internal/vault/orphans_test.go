package vault

import (
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"
)

// naiveUnreferenced is Unreferenced as it was before BUG-122: a Contains
// per name and text. The matcher must find the same names.
func naiveUnreferenced(names, texts []string) map[string]bool {
	left := map[string][]string{}
	for _, n := range names {
		f := foldRef(n)
		left[n] = []string{f, strings.ReplaceAll(f, " ", "+")}
	}
	for _, t := range texts {
		text, decoded := foldRef(t), foldRef(percentDecode(t))
		for name, forms := range left {
			for _, f := range forms {
				if strings.Contains(text, f) || strings.Contains(decoded, f) {
					delete(left, name)
					break
				}
			}
		}
	}
	out := map[string]bool{}
	for n := range left {
		out[n] = true
	}
	return out
}

func matcherUnreferenced(names, texts []string) map[string]bool {
	m := newRefMatcher(names)
	for _, t := range texts {
		m.match(foldRef(t))
		m.match(foldRef(percentDecode(t)))
	}
	return m.left
}

// The matcher finds what Contains found, however a link writes the name:
// in another case, with %20 or + for a space, an é composed or not, inside
// other words, or as a prefix of a longer name.
func TestRefMatcher_SameAsContains(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	stems := []string{"8b3aadcfcb1e25fd", "fig 1", "Schéma", "a+b", "x", "Image", "fig 10", "rapporto finale"}
	exts := []string{".png", ".PNG", ".jpg", ".webp", ".pdf", ""}
	var names []string
	for _, s := range stems {
		for _, e := range exts {
			names = append(names, s+e)
		}
	}
	write := func(n string) string {
		switch r.IntN(6) {
		case 0:
			return strings.ToUpper(n)
		case 1:
			return url.PathEscape(n)
		case 2:
			return strings.ReplaceAll(n, " ", "+")
		case 3:
			return norm.NFD.String(n)
		case 4:
			return "attachments/" + n + "?v=1"
		}
		return n
	}
	for round := range 200 {
		var texts []string
		for range 1 + r.IntN(4) {
			var b strings.Builder
			for range r.IntN(5) {
				fmt.Fprintf(&b, "testo ![](%s) e %s. ", write(names[r.IntN(len(names))]), "parole")
			}
			texts = append(texts, b.String())
		}
		want, got := naiveUnreferenced(names, texts), matcherUnreferenced(names, texts)
		if len(want) != len(got) {
			t.Fatalf("round %d: %d left, want %d\ntexts %q", round, len(got), len(want), texts)
		}
		for n := range want {
			if !got[n] {
				t.Fatalf("round %d: %q found, Contains did not\ntexts %q", round, n, texts)
			}
		}
	}
}

// 2,000 notes naming 10,000 of 20,000 attachments: well under a second where
// Contains took minutes (BUG-122).
func TestUnreferenced_Large(t *testing.T) {
	v := New(t.TempDir())
	var names []string
	for i := range 20000 {
		names = append(names, fmt.Sprintf("%016x.png", i*7919))
	}
	dir := filepath.Join(v.Root, "p", "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for f := range 2000 {
		var b strings.Builder
		for i := f * 5; i < f*5+5; i++ {
			fmt.Fprintf(&b, "![](/vault-files/p/attachments/%s)\n\nSome text about the picture.\n\n", names[i])
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("n%d.md", f)), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	left, err := v.Unreferenced(names)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 10000 || left[names[0]] || !left[names[19999]] {
		t.Fatalf("%d left", len(left))
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %s", d)
	}
}
