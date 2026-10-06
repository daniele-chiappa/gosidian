package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/trash"
)

type gcResult struct {
	Orphans []struct {
		Path    string `json:"path"`
		TrashID string `json:"trash_id"`
	} `json:"orphans"`
	OrphansCount int  `json:"orphans_count"`
	TooYoung     int  `json:"too_young"`
	Removed      int  `json:"removed"`
	Trash        bool `json:"trash"`
}

func gcCall(t *testing.T, s *Server, ctx context.Context, args map[string]any) gcResult {
	t.Helper()
	res, err := s.handleGCAttachments(ctx, call(args))
	if err != nil {
		t.Fatal(err)
	}
	var out gcResult
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func gcPaths(r gcResult) map[string]bool {
	out := map[string]bool{}
	for _, o := range r.Orphans {
		out[o.Path] = true
	}
	return out
}

// gcVault seeds attachments of project p that are referenced in every way
// that counts, one that is not, and one too young to count (IMP-033).
func gcVault(t *testing.T) (*Server, string) {
	t.Helper()
	s, _, dir := newTestServer(t)
	files := map[string]string{
		"p/attachments/by-note.png":                    "x",
		"p/attachments/by-other.png":                   "x",
		"p/attachments/by-canvas.png":                  "x",
		"p/attachments/by-trash.png":                   "x",
		"p/attachments/with space.png":                 "x",
		"p/docs/attachments/nested-o.png":              "x",
		"p/attachments/orphan.png":                     "x",
		"p/attachments/young.png":                      "x",
		"p/note.md":                                    "# N\n\n![[by-note.png]]\n\n![](attachments/with%20space.png)\n",
		"q/elsewhere.md":                               "# E\n\n![x](/vault-files/p/attachments/by-other.png)\n",
		"p/board.canvas":                               `{"nodes":[{"id":"a","type":"file","file":"p/attachments/by-canvas.png","x":0,"y":0,"width":10,"height":10}]}`,
		".gosidian/trash/20261001-120000__p%2Fgone.md": "# Gone\n\n![[by-trash.png]]\n",
	}
	for rel, body := range files {
		writeVaultFile(t, dir, rel, body)
	}
	old := time.Now().Add(-72 * time.Hour)
	for rel := range files {
		if rel != "p/attachments/young.png" {
			_ = os.Chtimes(filepath.Join(dir, filepath.FromSlash(rel)), old, old)
		}
	}
	return s, dir
}

// The dry run lists only what nothing names, old enough, and touches
// nothing.
func TestGCAttachments_DryRun(t *testing.T) {
	s, dir := gcVault(t)
	out := gcCall(t, s, context.Background(), map[string]any{"project": "p"})
	got := gcPaths(out)
	want := map[string]bool{"p/attachments/orphan.png": true, "p/docs/attachments/nested-o.png": true}
	if len(got) != len(want) || !got["p/attachments/orphan.png"] || !got["p/docs/attachments/nested-o.png"] {
		t.Errorf("orphans = %v, want %v", got, want)
	}
	if out.TooYoung != 1 {
		t.Errorf("too_young = %d, want 1", out.TooYoung)
	}
	if _, err := os.Stat(filepath.Join(dir, "p", "attachments", "orphan.png")); err != nil {
		t.Error("the dry run removed a file")
	}
	// min_age_hours 0 counts the young one too.
	if out := gcCall(t, s, context.Background(), map[string]any{"project": "p", "min_age_hours": 0}); out.OrphansCount != 3 {
		t.Errorf("with min_age_hours 0: %d orphans, want 3", out.OrphansCount)
	}
}

// Removing: into the trash when it is on (restorable), deleted otherwise;
// the referenced files stay.
func TestGCAttachments_Remove(t *testing.T) {
	s, dir := gcVault(t)
	s.SetTrash(trash.New(dir, time.Hour))
	out := gcCall(t, s, context.Background(), map[string]any{"project": "p", "dry_run": false})
	if out.Removed != 2 || !out.Trash {
		t.Fatalf("removed = %d, trash = %v", out.Removed, out.Trash)
	}
	for _, o := range out.Orphans {
		if o.TrashID == "" {
			t.Errorf("%s has no trash id", o.Path)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "p", "attachments", "orphan.png")); err == nil {
		t.Error("the orphan is still there")
	}
	if _, err := os.Stat(filepath.Join(dir, "p", "docs", "attachments")); err == nil {
		t.Error("the emptied docs/attachments folder was left (IMP-129)")
	}
	for _, kept := range []string{"by-note.png", "by-other.png", "by-canvas.png", "by-trash.png", "with space.png", "young.png"} {
		if _, err := os.Stat(filepath.Join(dir, "p", "attachments", kept)); err != nil {
			t.Errorf("%s was removed", kept)
		}
	}

	// Without the trash the file is deleted.
	s2, dir2 := gcVault(t)
	if out := gcCall(t, s2, context.Background(), map[string]any{"project": "p", "dry_run": false}); out.Removed != 2 || out.Trash {
		t.Errorf("no trash: removed = %d, trash = %v", out.Removed, out.Trash)
	}
	if _, err := os.Stat(filepath.Join(dir2, "p", "attachments", "orphan.png")); err == nil {
		t.Error("no trash: the orphan is still there")
	}
}

// A read-only token may look but not remove; another project is out of a
// scoped token's reach.
func TestGCAttachments_Scope(t *testing.T) {
	s, ctx := newScopedServer(t, "p", []string{auth.ScopeRead})
	writeVaultFile(t, s.vault.Root, "p/attachments/o.png", "x")
	if res, _ := s.handleGCAttachments(ctx, call(map[string]any{"project": "p"})); res.IsError {
		t.Errorf("read-only dry run: %s", expectError(t, res))
	}
	if res, _ := s.handleGCAttachments(ctx, call(map[string]any{"project": "p", "dry_run": false})); !res.IsError {
		t.Error("a read-only token removed attachments")
	}
	if res, _ := s.handleGCAttachments(ctx, call(map[string]any{"project": "q"})); !res.IsError {
		t.Error("a token scoped to p looked into q")
	}
}
