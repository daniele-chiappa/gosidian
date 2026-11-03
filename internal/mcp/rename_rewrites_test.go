package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
)

// seedLinked writes a note on disk and in the index, links included, past
// every token's scope.
func seedLinked(t *testing.T, s *Server, rel, body string) {
	t.Helper()
	full := filepath.Join(s.vault.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.index.Upsert(index.NoteDoc{Path: rel, Title: rel, Body: body, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
}

// recordedUnderLock watches the audit log: each write recorded, and those
// recorded while no lock of what they name was held.
func recordedUnderLock(t *testing.T, s *Server) (seen, free func() []string) {
	t.Helper()
	al, err := audit.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetAuditLog(al)
	var mu sync.Mutex
	var all, unlocked []string
	al.OnWrite(func(e audit.Entry) {
		paths := []string{e.Path}
		if e.Action == audit.ActionRename {
			paths = append(paths, e.To)
		}
		mu.Lock()
		defer mu.Unlock()
		for _, p := range paths {
			all = append(all, string(e.Action)+" "+p)
			if !s.vault.PathBusy(p) {
				unlocked = append(unlocked, string(e.Action)+" "+p)
			}
		}
	})
	get := func(xs *[]string) func() []string {
		return func() []string {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(*xs)
		}
	}
	return get(&all), get(&unlocked)
}

// A rename records an update of each note whose links it rewrote, in the
// renamer's name, while the locks of the two ends and of those notes are
// held (BUG-148): they had no record and no lock, and went in the auto
// commit. A move does the same.
func TestMCP_RenameRecordsTheNotesItRewrites(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedLinked(t, s, "p/x.md", "# X\n")
	seedLinked(t, s, "p/y.md", "See [[x]].\n")
	seedLinked(t, s, "q/z.md", "See [[p/x]] too.\n")
	seen, free := recordedUnderLock(t, s)
	ctx := context.Background()

	res, _ := s.handleRenameNote(ctx, call(map[string]any{"from": "p/x.md", "to": "p/w.md"}))
	resultText(t, res)
	// The move keeps the name: only the link with the project changes.
	res, _ = s.handleMoveNote(ctx, call(map[string]any{"path": "p/w.md", "project": "r"}))
	resultText(t, res)

	want := []string{"rename p/x.md", "rename p/w.md", "update p/y.md", "update q/z.md", "rename p/w.md", "rename r/w.md", "update q/z.md"}
	if got := seen(); !slices.Equal(got, want) {
		t.Errorf("recorded %q, want %q", got, want)
	}
	if f := free(); len(f) > 0 {
		t.Errorf("recorded with no lock held: %q", f)
	}
}

// A token rewrites the links of the notes it may write only (BUG-150): the
// others keep the old link, and the answer says how many, without naming
// them. Before, it rewrote the notes of projects it could not write, and
// named them.
func TestMCP_RenameRewritesOnlyWhereTheTokenWrites(t *testing.T) {
	s, ctx := newScopedServer(t, "a", []string{auth.ScopeRead, auth.ScopeWrite})
	seedLinked(t, s, "a/x.md", "# X\n")
	seedLinked(t, s, "a/y.md", "See [[x]].\n")
	seedLinked(t, s, "b/z.md", "See [[x]] too.\n")
	seen, _ := recordedUnderLock(t, s)

	res, _ := s.handleRenameNote(ctx, call(map[string]any{"from": "a/x.md", "to": "a/w.md"}))
	body := resultText(t, res)
	var out struct {
		Rewritten    []string `json:"rewritten"`
		NotRewritten int      `json:"not_rewritten"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if !slices.Equal(out.Rewritten, []string{"a/y.md"}) || out.NotRewritten != 1 {
		t.Errorf("answer = %+v, want a/y.md rewritten and 1 not", out)
	}
	if strings.Contains(body, "b/z.md") {
		t.Errorf("the answer names a note of a project the token cannot write: %s", body)
	}
	if raw, _ := os.ReadFile(filepath.Join(s.vault.Root, "b", "z.md")); string(raw) != "See [[x]] too.\n" {
		t.Errorf("b/z.md rewritten: %q", raw)
	}
	if raw, _ := os.ReadFile(filepath.Join(s.vault.Root, "a", "y.md")); !strings.Contains(string(raw), "[[w]]") {
		t.Errorf("a/y.md not rewritten: %q", raw)
	}
	if got := seen(); slices.Contains(got, "update b/z.md") {
		t.Errorf("an update of b/z.md recorded: %q", got)
	}
}
