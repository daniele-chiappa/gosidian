package parser

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/alecthomas/chroma/v2"
)

// The languages come from the notes: the cache of lexers keeps short names
// only, up to its cap, and still answers past it.
func TestLexerOf_KeepsShortNamesUpToTheCap(t *testing.T) {
	lexerCache.Lock()
	saved := lexerCache.m
	lexerCache.m = map[string]chroma.Lexer{}
	for i := range lexerCacheMax - 1 {
		lexerCache.m[fmt.Sprint("nosuch", i)] = nil
	}
	lexerCache.Unlock()
	t.Cleanup(func() {
		lexerCache.Lock()
		lexerCache.m = saved
		lexerCache.Unlock()
	})

	long := strings.Repeat("g", lexerCacheNameMax+1)
	if lexerOf(long) != nil {
		t.Fatalf("a lexer for %q", long)
	}
	if lexerOf("go") == nil || lexerOf("go") == nil {
		t.Fatal("no lexer for go")
	}
	if lexerOf("python") == nil {
		t.Fatal("no lexer for python past the cap")
	}
	lexerCache.Lock()
	defer lexerCache.Unlock()
	if _, ok := lexerCache.m[long]; ok {
		t.Error("the cache kept a long name")
	}
	if _, ok := lexerCache.m["go"]; !ok {
		t.Error("the cache did not keep go")
	}
	if len(lexerCache.m) != lexerCacheMax {
		t.Errorf("the cache holds %d names, want %d", len(lexerCache.m), lexerCacheMax)
	}
}

// The renderer serves several requests at once: the cache of lexers and
// chroma's shared lexers give each the same HTML.
func TestRenderer_CodeBlocksConcurrently(t *testing.T) {
	src, err := os.ReadFile("testdata/code_blocks.md")
	if err != nil {
		t.Fatal(err)
	}
	r := NewRenderer()
	want, err := r.Render(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if got, err := r.Render(src, nil); err != nil || got != want {
				t.Errorf("a concurrent render differs (err %v)", err)
			}
		})
	}
	wg.Wait()
}
