package vault

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWalkExport(t *testing.T) {
	v := newTestVault(t)
	write(t, v.Root, "p/a.md", "a")
	write(t, v.Root, "p/board.canvas", "{}")
	write(t, v.Root, "p/.hidden.md", "h")
	write(t, v.Root, "p/.obsidian/app.json", "{}")
	write(t, v.Root, "p/node_modules/x.js", "x")
	write(t, v.Root, "p/state/tokens.json", "{}")
	write(t, v.Root, "q/b.md", "b")
	if err := os.Symlink(filepath.Join(v.Root, "q", "b.md"), filepath.Join(v.Root, "p", "link.md")); err != nil {
		t.Fatal(err)
	}

	walk := func(dir string, exclude ...string) string {
		t.Helper()
		var got []string
		if err := v.WalkExport(dir, exclude, func(rel string, _ fs.FileInfo) error {
			got = append(got, rel)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return strings.Join(got, ",")
	}
	if got := walk("p", filepath.Join(v.Root, "p", "state")); got != "p/a.md,p/board.canvas" {
		t.Errorf("project walk = %s", got)
	}
	if got := walk(""); got != "p/a.md,p/board.canvas,p/state/tokens.json,q/b.md" {
		t.Errorf("vault walk without exclude = %s", got)
	}
	if err := v.WalkExport("../outside", nil, func(string, fs.FileInfo) error { return nil }); err == nil {
		t.Error("a dir outside the vault was walked")
	}
}
