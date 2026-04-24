package vault

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/gosidian/gosidian/internal/index"
)

func openIndex(t *testing.T) *index.Index {
	t.Helper()
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx
}

func newTestVault(t *testing.T) *Vault {
	t.Helper()
	dir := t.TempDir()
	return New(dir)
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVault_ListAndLoad(t *testing.T) {
	v := newTestVault(t)
	write(t, v.Root, "a.md", "# A")
	write(t, v.Root, "sub/b.md", "# B")
	write(t, v.Root, "notes.txt", "ignored")
	write(t, v.Root, ".hidden/c.md", "hidden")

	paths, err := v.List()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	wantContains := []string{"a.md", "sub/b.md"}
	for _, w := range wantContains {
		found := false
		for _, p := range paths {
			if p == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing %q in %v", w, paths)
		}
	}
	for _, p := range paths {
		if p == "notes.txt" || p == ".hidden/c.md" {
			t.Errorf("unexpected %q in list", p)
		}
	}

	note, err := v.Load("a.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(note.Content) != "# A" {
		t.Errorf("content = %q", note.Content)
	}
	if note.Title != "a" {
		t.Errorf("title = %q", note.Title)
	}
}

func TestVault_SaveCreatesDirs(t *testing.T) {
	v := newTestVault(t)
	if err := v.Save("deep/sub/new.md", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(v.Root, "deep/sub/new.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q", data)
	}
}

func TestVault_ProjectsAndCreate(t *testing.T) {
	v := newTestVault(t)
	write(t, v.Root, "loose.md", "# loose")

	projs, err := v.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projs) != 0 {
		t.Errorf("expected no projects, got %+v", projs)
	}

	clean, err := v.CreateProject("  Lavoro  ")
	if err != nil {
		t.Fatal(err)
	}
	if clean != "Lavoro" {
		t.Errorf("sanitized name = %q, want %q", clean, "Lavoro")
	}
	if _, err := os.Stat(filepath.Join(v.Root, "Lavoro")); err != nil {
		t.Errorf("project dir not created: %v", err)
	}

	// duplicate should fail
	if _, err := v.CreateProject("Lavoro"); err == nil {
		t.Errorf("duplicate create should fail")
	}

	// note inside project should be counted
	write(t, v.Root, "Lavoro/a.md", "# a")
	write(t, v.Root, "Lavoro/b.md", "# b")
	projs, _ = v.Projects()
	if len(projs) != 1 || projs[0].Name != "Lavoro" || projs[0].NoteCount != 2 {
		t.Errorf("projects = %+v", projs)
	}

	// invalid names
	bad := []string{"", " ", "..", ".hidden", "a/b", "a:b", "a\\b"}
	for _, b := range bad {
		if _, err := v.CreateProject(b); err == nil {
			t.Errorf("CreateProject(%q) should fail", b)
		}
	}
}

func TestVault_DeleteProject(t *testing.T) {
	v := newTestVault(t)

	// Empty project
	if _, err := v.CreateProject("Empty"); err != nil {
		t.Fatal(err)
	}
	removed, err := v.DeleteProject("Empty")
	if err != nil {
		t.Fatalf("delete empty: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed notes from empty project: %+v", removed)
	}
	if _, err := os.Stat(filepath.Join(v.Root, "Empty")); err == nil {
		t.Errorf("dir should be gone")
	}

	// Project with notes — recursive delete returns the list
	if _, err := v.CreateProject("Work"); err != nil {
		t.Fatal(err)
	}
	write(t, v.Root, "Work/a.md", "# a")
	write(t, v.Root, "Work/sub/b.md", "# b")
	write(t, v.Root, "Work/notes.txt", "not markdown — ignored by the caller")

	removed, err = v.DeleteProject("Work")
	if err != nil {
		t.Fatalf("delete work: %v", err)
	}
	gotSet := map[string]bool{}
	for _, p := range removed {
		gotSet[p] = true
	}
	for _, want := range []string{"Work/a.md", "Work/sub/b.md"} {
		if !gotSet[want] {
			t.Errorf("missing %q in removed list (got %+v)", want, removed)
		}
	}
	if _, err := os.Stat(filepath.Join(v.Root, "Work")); err == nil {
		t.Errorf("dir should be gone")
	}

	// Invalid names
	bad := []string{"", "..", ".hidden", "a/b", "a\\b"}
	for _, b := range bad {
		if _, err := v.DeleteProject(b); err == nil {
			t.Errorf("DeleteProject(%q) should fail", b)
		}
	}

	// Non-existent
	if _, err := v.DeleteProject("Ghost"); err == nil {
		t.Errorf("DeleteProject on missing dir should fail")
	}
}

func TestVault_RenameNote(t *testing.T) {
	v := newTestVault(t)
	idx := openIndex(t)

	write(t, v.Root, "Foo.md", "# Foo\n\nBody")
	write(t, v.Root, "ref.md", "See [[Foo]] and [[Foo|the thing]] plus [[other]].")
	if err := v.ScanInto(idx); err != nil {
		t.Fatal(err)
	}
	if err := idx.ResolveAll(); err != nil {
		t.Fatal(err)
	}

	// Sanity: ref.md is a backlink of Foo.md
	backs, _ := idx.Backlinks("Foo.md")
	if len(backs) != 1 || backs[0].Path != "ref.md" {
		t.Fatalf("backlinks pre-rename = %+v", backs)
	}

	rewritten, err := v.RenameNote(idx, "Foo.md", "Bar.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(rewritten) != 1 || rewritten[0] != "ref.md" {
		t.Errorf("rewritten = %+v, want [ref.md]", rewritten)
	}

	// Filesystem
	if _, err := os.Stat(filepath.Join(v.Root, "Foo.md")); err == nil {
		t.Errorf("old file should be gone")
	}
	if _, err := os.Stat(filepath.Join(v.Root, "Bar.md")); err != nil {
		t.Errorf("new file missing: %v", err)
	}

	// ref.md body rewritten
	ref, _ := v.Load("ref.md")
	body := string(ref.Content)
	for _, want := range []string{"[[Bar]]", "[[Bar|the thing]]"} {
		if !stringContains(body, want) {
			t.Errorf("missing %q in rewritten body: %s", want, body)
		}
	}
	if stringContains(body, "[[Foo]]") || stringContains(body, "[[Foo|") {
		t.Errorf("old wiki-link still present: %s", body)
	}
	// Unrelated links untouched
	if !stringContains(body, "[[other]]") {
		t.Errorf("unrelated link lost: %s", body)
	}

	// Index reflects the new path
	if n, _ := idx.Note("Bar.md"); n == nil {
		t.Errorf("new note missing from index")
	}
	if n, _ := idx.Note("Foo.md"); n != nil {
		t.Errorf("old note still in index")
	}

	// Backlinks now point at Bar.md
	backs, _ = idx.Backlinks("Bar.md")
	if len(backs) != 1 || backs[0].Path != "ref.md" {
		t.Errorf("backlinks post-rename = %+v", backs)
	}
}

func TestVault_RenameNote_Conflict(t *testing.T) {
	v := newTestVault(t)
	idx := openIndex(t)
	write(t, v.Root, "a.md", "A")
	write(t, v.Root, "b.md", "B")
	_ = v.ScanInto(idx)
	if _, err := v.RenameNote(idx, "a.md", "b.md"); err == nil {
		t.Errorf("rename to existing path should fail")
	}
	if _, err := v.RenameNote(idx, "missing.md", "x.md"); err == nil {
		t.Errorf("rename from missing source should fail")
	}
}

func TestVault_RenameNote_FolderQualified(t *testing.T) {
	v := newTestVault(t)
	idx := openIndex(t)
	write(t, v.Root, "sub/foo.md", "# Foo")
	write(t, v.Root, "ref.md", "Link [[sub/foo]] here.")
	_ = v.ScanInto(idx)
	_ = idx.ResolveAll()

	if _, err := v.RenameNote(idx, "sub/foo.md", "sub/bar.md"); err != nil {
		t.Fatal(err)
	}
	ref, _ := v.Load("ref.md")
	if !stringContains(string(ref.Content), "[[sub/bar]]") {
		t.Errorf("folder-qualified link not rewritten: %s", ref.Content)
	}
}

func TestVault_MoveNote(t *testing.T) {
	v := newTestVault(t)
	idx := openIndex(t)
	_, _ = v.CreateProject("Inbox")
	_, _ = v.CreateProject("Done")
	write(t, v.Root, "Inbox/task.md", "# task")
	write(t, v.Root, "ref.md", "Linking [[task]]")
	_ = v.ScanInto(idx)
	_ = idx.ResolveAll()

	rewritten, err := v.MoveNote(idx, "Inbox/task.md", "Done")
	if err != nil {
		t.Fatal(err)
	}
	_ = rewritten
	if _, err := os.Stat(filepath.Join(v.Root, "Done", "task.md")); err != nil {
		t.Errorf("note not at new location: %v", err)
	}
	if _, err := os.Stat(filepath.Join(v.Root, "Inbox", "task.md")); err == nil {
		t.Errorf("note still at old location")
	}
	if n, _ := idx.Note("Done/task.md"); n == nil {
		t.Errorf("new path not in index")
	}
	if n, _ := idx.Note("Inbox/task.md"); n != nil {
		t.Errorf("old path still in index")
	}
}

func TestVault_RenameProject(t *testing.T) {
	v := newTestVault(t)
	idx := openIndex(t)
	_, _ = v.CreateProject("Work")
	write(t, v.Root, "Work/a.md", "# a")
	write(t, v.Root, "Work/sub/b.md", "# b")
	_ = v.ScanInto(idx)

	if err := v.RenameProject(idx, "Work", "Lavoro"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(v.Root, "Work")); err == nil {
		t.Errorf("old dir should be gone")
	}
	if _, err := os.Stat(filepath.Join(v.Root, "Lavoro", "a.md")); err != nil {
		t.Errorf("file not under new dir: %v", err)
	}
	// Index paths updated
	if n, _ := idx.Note("Lavoro/a.md"); n == nil {
		t.Errorf("new path missing from index")
	}
	if n, _ := idx.Note("Work/a.md"); n != nil {
		t.Errorf("old path still in index")
	}

	// Conflict
	_, _ = v.CreateProject("Other")
	if err := v.RenameProject(idx, "Lavoro", "Other"); err == nil {
		t.Errorf("rename to existing project should fail")
	}
}

func stringContains(s, sub string) bool { return len(s) > 0 && len(sub) > 0 && indexOf(s, sub) >= 0 }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestVault_RelRejectsEscape(t *testing.T) {
	v := newTestVault(t)
	bad := []string{"../evil.md", "a/../../../etc/passwd", ""}
	for _, b := range bad {
		if _, err := v.Rel(b); err == nil {
			t.Errorf("Rel(%q) should fail", b)
		}
	}
}
