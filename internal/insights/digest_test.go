package insights

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/clock/clocktest"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/vault"
)

func newDigester(t *testing.T, cfg DigestConfig) (*Digester, *vault.Vault, *index.Index) {
	t.Helper()
	v := vault.New(t.TempDir())
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	return New(v, idx, cfg, nil), v, idx
}

func seedInsight(t *testing.T, v *vault.Vault, idx *index.Index, path, content string) {
	t.Helper()
	if err := v.Save(path, []byte(content)); err != nil {
		t.Fatal(err)
	}
	note, err := v.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Upsert(index.NoteDoc{
		Path:    note.Path,
		Title:   note.Title,
		Body:    string(note.Content),
		ModTime: note.ModTime.Unix(),
		Size:    note.Size,
	}); err != nil {
		t.Fatal(err)
	}
}

func pendingNote(title string) string {
	return "---\ntitle: " + title + "\ntags: [insights, type:insight, status:pending]\ntype: insight\nstatus: pending\n---\n\n# " + title + "\n"
}

func TestDigest_CompileWritesNote(t *testing.T) {
	d, v, idx := newDigester(t, DigestConfig{Project: "insights"})
	seedInsight(t, v, idx, "insights/2026-06-08-a-aaaa.md", pendingNote("Aaa"))
	seedInsight(t, v, idx, "insights/2026-06-08-b-bbbb.md", pendingNote("Bbb"))
	// An already-triaged insight (status:done) must not be counted.
	seedInsight(t, v, idx, "insights/2026-06-07-c-cccc.md",
		"---\ntitle: Ccc\ntags: [insights, type:insight, status:done]\ntype: insight\nstatus: done\n---\n\n# Ccc\n")

	path, count, err := d.Compile(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 pending, got %d", count)
	}
	if path != "insights/digest-2026-06-08.md" {
		t.Fatalf("unexpected digest path: %s", path)
	}
	note, err := v.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(note.Content)
	for _, want := range []string{"# Insights digest — 2026-06-08", "Aaa", "Bbb", "type: doc"} {
		if !strings.Contains(body, want) {
			t.Errorf("digest missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "Ccc") {
		t.Errorf("triaged insight leaked into digest:\n%s", body)
	}
}

func TestDigest_CompileEmptyNoop(t *testing.T) {
	d, _, _ := newDigester(t, DigestConfig{Project: "insights"})
	path, count, err := d.Compile(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 || path != "" {
		t.Errorf("empty pending set should write nothing, got path=%q count=%d", path, count)
	}
}

func TestDigest_RunNoEmailWhenUnconfigured(t *testing.T) {
	// SMTP host empty → Run compiles the note but never tries to email
	// (so the test does not hang on a network dial).
	d, v, idx := newDigester(t, DigestConfig{Project: "insights", NotifyEmail: "x@example.com"})
	seedInsight(t, v, idx, "insights/2026-06-08-a-aaaa.md", pendingNote("A"))
	d.Run(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC))
	if _, err := v.Load("insights/digest-2026-06-08.md"); err != nil {
		t.Errorf("digest note not written by Run: %v", err)
	}
}

// The digest is named and dated with the day of the server's zone
// (IMP-208): past midnight in Rome, the 9th.
func TestDigest_CompileNoteZone(t *testing.T) {
	clocktest.Rome(t)
	d, v, idx := newDigester(t, DigestConfig{Project: "insights"})
	seedInsight(t, v, idx, "insights/2026-06-08-a-aaaa.md", pendingNote("Aaa"))
	path, _, err := d.Compile(time.Date(2026, 6, 8, 22, 30, 0, 0, time.UTC))
	if err != nil || path != "insights/digest-2026-06-09.md" {
		t.Fatalf("path = %s, %v", path, err)
	}
	if note, err := v.Load(path); err != nil || !strings.Contains(string(note.Content), "\ncreated: 2026-06-09\n") {
		t.Errorf("digest: %v", err)
	}
}
