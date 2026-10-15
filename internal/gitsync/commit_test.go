package gitsync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// OnCommit tells each listener what a commit changed: added, modified,
// deleted and renamed files, with their paths as written (accents and
// spaces too), in the order of the commits.
func TestSync_OnCommit(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	s := New(dir, testCfg())
	var got []CommitChanges
	s.OnCommit(func(c CommitChanges) { got = append(got, c) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	long := "Una procedura abbastanza lunga perché git riconosca lo spostamento come una rinomina.\n"
	write("rag/docs/Contabilità generale.md", "# Contabilità\n"+long)
	write("rag/docs/b.md", "# B\n"+long+long)
	write("rag/docs/c.md", "# C\n")
	s.TriggerCommit()
	s.Flush()
	if len(got) != 1 || len(got[0].Files) != 3 || len(got[0].SHA) != 40 || got[0].Date.IsZero() {
		t.Fatalf("first commit: %+v", got)
	}
	for _, f := range got[0].Files {
		if f.Status != "added" {
			t.Errorf("first commit: %+v", f)
		}
	}
	if got[0].Files[0].Path != "rag/docs/Contabilità generale.md" {
		t.Errorf("a path with an accent: %q", got[0].Files[0].Path)
	}

	write("rag/docs/c.md", "# C changed\n")
	if err := os.Remove(filepath.Join(dir, "rag/docs/Contabilità generale.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "rag/docs/b.md"), filepath.Join(dir, "rag/docs/sub/b moved.md")); err != nil {
		if err := os.MkdirAll(filepath.Join(dir, "rag/docs/sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(dir, "rag/docs/b.md"), filepath.Join(dir, "rag/docs/sub/b moved.md")); err != nil {
			t.Fatal(err)
		}
	}
	s.TriggerCommit()
	s.Flush()
	if len(got) != 2 {
		t.Fatalf("%d commits told", len(got))
	}
	want := map[string]Change{
		"rag/docs/Contabilità generale.md": {Path: "rag/docs/Contabilità generale.md", Status: "deleted"},
		"rag/docs/sub/b moved.md":          {Path: "rag/docs/sub/b moved.md", From: "rag/docs/b.md", Status: "renamed"},
		"rag/docs/c.md":                    {Path: "rag/docs/c.md", Status: "modified"},
	}
	if len(got[1].Files) != len(want) {
		t.Fatalf("second commit: %+v", got[1].Files)
	}
	for _, f := range got[1].Files {
		if want[f.Path] != f {
			t.Errorf("second commit: %+v, want %+v", f, want[f.Path])
		}
	}
	if got[1].SHA == got[0].SHA {
		t.Error("the same SHA twice")
	}
	// Nothing to commit: no listener is told.
	s.TriggerCommit()
	s.Flush()
	if len(got) != 2 {
		t.Errorf("an empty flush told %d", len(got)-2)
	}
}

func TestParseNameStatus(t *testing.T) {
	got := parseNameStatus("M\x00a.md\x00R087\x00old.md\x00new.md\x00D\x00gone.md\x00A\x00x y.md\x00C100\x00src.md\x00copy.md\x00T\x00link\x00")
	want := []Change{{Path: "a.md", Status: "modified"}, {Path: "new.md", From: "old.md", Status: "renamed"}, {Path: "gone.md", Status: "deleted"},
		{Path: "x y.md", Status: "added"}, {Path: "copy.md", From: "src.md", Status: "added"}, {Path: "link", Status: "modified"}}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %+v, want %+v", i, got[i], want[i])
		}
	}
}
