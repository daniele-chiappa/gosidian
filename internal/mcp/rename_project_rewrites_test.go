package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/projects"
)

// A project renamed rewrites what names its files in the notes the token
// may write, the project's own included, records an update of each note
// outside the project while every lock is held, and counts the notes it
// may not write without naming them (BUG-153). Before, it rewrote none,
// and [[a/x]] led nowhere.
func TestMCP_RenameProjectRewritesWhereTheTokenWrites(t *testing.T) {
	s, _, _ := newTestServer(t)
	ps, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetProjects(ps)
	if err := ps.Set("c", projects.Flags{HiddenFromMCP: true}); err != nil {
		t.Fatal(err)
	}
	seedLinked(t, s, "a/x.md", "# X\nSee [[a/y]].\n")
	seedLinked(t, s, "a/y.md", "# Y\n")
	seedLinked(t, s, "b/z.md", "See [[a/x]].\n")
	seedLinked(t, s, "c/w.md", "See [[a/x]].\n")
	seen, free := recordedUnderLock(t, s)
	ctx := context.WithValue(context.Background(), tokenCtxKey, auth.AdminToken())

	body := resultText(t, mustCall(s.handleRenameProject(ctx, call(map[string]any{"from": "a", "to": "n"}))))
	var out struct {
		Rewritten      []string `json:"rewritten"`
		RewrittenCount int      `json:"rewritten_count"`
		NotRewritten   int      `json:"not_rewritten"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if !slices.Equal(out.Rewritten, []string{"b/z.md", "n/x.md"}) || out.RewrittenCount != 2 || out.NotRewritten != 1 {
		t.Errorf("answer = %+v, want b/z.md and n/x.md rewritten, 1 not", out)
	}
	if strings.Contains(body, "c/w.md") {
		t.Errorf("the answer names a note the token may not write: %s", body)
	}
	for path, want := range map[string]string{"n/x.md": "# X\nSee [[n/y]].\n", "b/z.md": "See [[n/x]].\n", "c/w.md": "See [[a/x]].\n"} {
		if raw, err := os.ReadFile(filepath.Join(s.vault.Root, filepath.FromSlash(path))); err != nil || string(raw) != want {
			t.Errorf("%s = %q (%v), want %q", path, raw, err, want)
		}
	}
	if got, want := seen(), []string{"rename_project a", "update b/z.md"}; !slices.Equal(got, want) {
		t.Errorf("recorded %q, want %q", got, want)
	}
	if f := free(); len(f) > 0 {
		t.Errorf("recorded with no lock held: %q", f)
	}
}
