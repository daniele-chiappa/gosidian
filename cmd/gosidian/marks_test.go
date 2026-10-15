package main

import (
	"path/filepath"
	"testing"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/marks"
)

// The marks follow their notes through the audit log (IMP-188): a note,
// a folder or a project renamed takes them along, deleted loses them.
func TestWireMarks(t *testing.T) {
	dir := t.TempDir()
	al, err := audit.Open(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := marks.Open(filepath.Join(dir, "marks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	wireMarks(m, al)
	put := func(p string) {
		t.Helper()
		if err := m.Put(marks.Set{Path: p, Name: "cut", Hash: "h", Marks: []marks.Mark{{Color: "red"}}}); err != nil {
			t.Fatal(err)
		}
	}
	has := func(p string) bool {
		sets, _ := m.Sets(p)
		return len(sets) > 0
	}
	for _, p := range []string{"p/a.md", "p/docs/b.md", "q/c.md", "r/d.md"} {
		put(p)
	}
	for _, e := range []audit.Entry{
		{Action: audit.ActionRename, Path: "p/a.md", To: "p/a2.md"},
		{Action: audit.ActionRename, Path: "p/docs", To: "p/manuals"},
		{Action: audit.ActionRenameProject, Path: "q", To: "q2"},
		{Action: audit.ActionDeleteProject, Path: "r"},
	} {
		if err := al.Write(e); err != nil {
			t.Fatal(err)
		}
	}
	for p, want := range map[string]bool{"p/a.md": false, "p/a2.md": true, "p/docs/b.md": false, "p/manuals/b.md": true, "q/c.md": false, "q2/c.md": true, "r/d.md": false} {
		if has(p) != want {
			t.Errorf("%s: marks %v, want %v", p, !want, want)
		}
	}
	if err := al.Write(audit.Entry{Action: audit.ActionDelete, Path: "p/a2.md"}); err != nil {
		t.Fatal(err)
	}
	if has("p/a2.md") {
		t.Error("a deleted note kept its marks")
	}
}
