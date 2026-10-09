package vault

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

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

// A folder-qualified link to another note with the same name stays: renaming
// proj/a/Old turned [[proj/b/Old]] into a link to nothing (BUG-115, S5-12).
// A move to another folder gives the link the new path.
func TestVault_RenameNote_SameNameElsewhere(t *testing.T) {
	v := newTestVault(t)
	idx := openIndex(t)
	write(t, v.Root, "proj/a/Old.md", "# A")
	write(t, v.Root, "proj/b/Old.md", "# B")
	write(t, v.Root, "proj/ref.md", "[[proj/a/Old]], [[proj/b/Old]], [[a/Old]] and [[b/Old]].")
	_ = v.ScanInto(idx)
	_ = idx.ResolveAll()
	if _, err := v.RenameNote(idx, "proj/a/Old.md", "proj/a/New.md"); err != nil {
		t.Fatal(err)
	}
	ref, _ := v.Load("proj/ref.md")
	if want := "[[proj/a/New]], [[proj/b/Old]], [[a/New]] and [[b/Old]]."; string(ref.Content) != want {
		t.Errorf("body = %q, want %q", ref.Content, want)
	}
	if _, err := v.RenameNote(idx, "proj/a/New.md", "proj/c/New.md"); err != nil {
		t.Fatal(err)
	}
	ref, _ = v.Load("proj/ref.md")
	if want := "[[proj/c/New]], [[proj/b/Old]], [[proj/c/New]] and [[b/Old]]."; string(ref.Content) != want {
		t.Errorf("after the move: body = %q, want %q", ref.Content, want)
	}
}

// BUG-085: links that cite a section of the renamed note must follow it.
func TestVault_RenameNote_AnchoredLinks(t *testing.T) {
	v := newTestVault(t)
	idx := openIndex(t)
	write(t, v.Root, "p/docs/improvements.md", "# Improvements\n\n## IMP-126 — x\n")
	write(t, v.Root, "p/ref.md", "See [[p/docs/improvements#IMP-126]] and [[improvements#IMP-126|that one]].")
	_ = v.ScanInto(idx)
	_ = idx.ResolveAll()

	rewritten, err := v.RenameNote(idx, "p/docs/improvements.md", "p/docs/backlog.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(rewritten) != 1 {
		t.Fatalf("rewritten = %v, want [p/ref.md]", rewritten)
	}
	ref, _ := v.Load("p/ref.md")
	want := "See [[p/docs/backlog#IMP-126]] and [[backlog#IMP-126|that one]]."
	if string(ref.Content) != want {
		t.Errorf("body = %q, want %q", ref.Content, want)
	}
	if backs, _ := idx.Backlinks("p/docs/backlog.md"); len(backs) != 1 {
		t.Errorf("backlinks post-rename = %+v", backs)
	}
}

func TestRewriteWikiLinks(t *testing.T) {
	const fence = "```"
	cases := []struct{ name, in, want string }{
		{"heading fragment", "[[d/old#H 1]]", "[[d/new#H 1]]"},
		{"block fragment with alias", "[[old#^b1|blk]]", "[[new#^b1|blk]]"},
		{"table escape kept", `| [[d/old#IMP-1\|first]] |`, `| [[d/new#IMP-1\|first]] |`},
		{"plain alias keeps plain pipe", "[[old|x]]", "[[new|x]]"},
		{"embed", "![[old]]", "![[new]]"},
		{"same-note anchor untouched", "[[#old]]", "[[#old]]"},
		{"other note untouched", "[[older#old]]", "[[older#old]]"},
		{"inline code untouched", "`[[old]]` and [[old]]", "`[[old]]` and [[new]]"},
		{"fenced code untouched", fence + "\n[[old]]\n" + fence + "\n[[old]]", fence + "\n[[old]]\n" + fence + "\n[[new]]"},
		{"unclosed fence runs to the end", "[[old]]\n" + fence + "\n[[old]]", "[[new]]\n" + fence + "\n[[old]]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(rewriteWikiLinks([]byte(tc.in), "old", "new", "d/old.md", "d/new.md"))
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
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

func TestProjectNotes_StatsNotesWithoutReading(t *testing.T) {
	v := New(t.TempDir())
	for _, p := range []string{"proj/a.md", "proj/sub/b.md", "other/c.md"} {
		if err := v.Save(p, []byte("# "+p)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(v.Root, "proj", ".hidden.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.Root, "proj", "image.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	stats, err := v.ProjectNotes("proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("stats = %+v, want proj/a.md and proj/sub/b.md", stats)
	}
	for _, st := range stats {
		n, err := v.Load(st.Path)
		if err != nil {
			t.Fatal(err)
		}
		if st.ETag() != n.ETag() {
			t.Errorf("%s: stat etag %s != note etag %s", st.Path, st.ETag(), n.ETag())
		}
	}
	if _, err := v.ProjectNotes("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing project err = %v, want fs.ErrNotExist", err)
	}
	if _, err := v.ProjectNotes("proj/sub"); err == nil {
		t.Error("a nested folder is not a project")
	}
}

// A rescan re-indexes only what changed (IMP-104): unchanged notes are
// skipped, a file rewritten with the same bytes only moves its mtime, and a
// note created while the server was down still gets its links resolved.
func TestVault_ScanSkipsUnchanged(t *testing.T) {
	v := newTestVault(t)
	idx := openIndex(t)

	write(t, v.Root, "p/a.md", "# a\n\nSee [[New]].")
	write(t, v.Root, "p/b.md", "# b\n")
	write(t, v.Root, "p/c.md", "# c\n")
	st, err := v.Scan(idx)
	if err != nil {
		t.Fatal(err)
	}
	if st != (ScanStats{Notes: 3, Reindexed: 3}) {
		t.Fatalf("first scan = %+v, want 3 notes all re-indexed", st)
	}

	st, err = v.Scan(idx)
	if err != nil {
		t.Fatal(err)
	}
	if st != (ScanStats{Notes: 3}) {
		t.Fatalf("rescan of an unchanged vault = %+v, want nothing re-indexed", st)
	}

	// b changes, c is only touched, New appears, and a's link to it must
	// resolve although a itself is skipped.
	write(t, v.Root, "p/b.md", "# b\n\nchanged #fresh\n")
	old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(filepath.Join(v.Root, "p/c.md"), old, old); err != nil {
		t.Fatal(err)
	}
	write(t, v.Root, "p/New.md", "# New\n")
	st, err = v.Scan(idx)
	if err != nil {
		t.Fatal(err)
	}
	if st != (ScanStats{Notes: 4, Reindexed: 2}) {
		t.Fatalf("rescan after edits = %+v, want b and New re-indexed", st)
	}
	if backs, _ := idx.Backlinks("p/New.md"); len(backs) != 1 || backs[0].Path != "p/a.md" {
		t.Errorf("link from the skipped note to the new one: backlinks = %+v", backs)
	}
	if got, _ := idx.NotesByTag("fresh"); len(got) != 1 {
		t.Errorf("changed note not re-indexed: notes with #fresh = %+v", got)
	}
	stamps, _ := idx.Stamps()
	if stamps["p/c.md"].ModTime != old.Unix() {
		t.Errorf("touched note mtime = %d, want %d", stamps["p/c.md"].ModTime, old.Unix())
	}
}

// The boot scan drops indexed notes whose file is gone (deleted while the
// server was down, or a delete commit lost in a crash), and the links that
// pointed at them go back to unresolved.
func TestVault_ScanIntoDropsVanishedNotes(t *testing.T) {
	v := newTestVault(t)
	idx := openIndex(t)

	write(t, v.Root, "Keep.md", "# Keep\n\nSee [[Gone]].")
	write(t, v.Root, "Gone.md", "# Gone\n")
	if err := v.ScanInto(idx); err != nil {
		t.Fatal(err)
	}
	if backs, _ := idx.Backlinks("Gone.md"); len(backs) != 1 {
		t.Fatalf("setup: Gone.md backlinks = %v", backs)
	}

	if err := os.Remove(filepath.Join(v.Root, "Gone.md")); err != nil {
		t.Fatal(err)
	}
	if err := v.ScanInto(idx); err != nil {
		t.Fatal(err)
	}
	all, _ := idx.AllNotes()
	if len(all) != 1 || all[0].Path != "Keep.md" {
		t.Errorf("index after rescan = %+v, want only Keep.md", all)
	}
	if backs, _ := idx.Backlinks("Gone.md"); len(backs) != 0 {
		t.Errorf("link to a vanished note still resolved: %v", backs)
	}
}

// A note that links to the renamed one only from its frontmatter (a
// relation, IMP-127 iteration 2) is a backlink too, and its link follows.
func TestVault_RenameNote_FrontmatterLinks(t *testing.T) {
	v := newTestVault(t)
	idx := openIndex(t)
	write(t, v.Root, "p/bugs/BUG-1.md", "# Bug")
	write(t, v.Root, "p/plans/fix.md", "---\ntitle: Fix\nrelated: [\"[[p/bugs/BUG-1]]\", \"[[other]]\"]\norigin: \"[[BUG-1|the bug]]\"\n---\n\nNo link in the body.\n")
	_ = v.ScanInto(idx)
	_ = idx.ResolveAll()

	rewritten, err := v.RenameNote(idx, "p/bugs/BUG-1.md", "p/bugs/BUG-001.md")
	if err != nil {
		t.Fatal(err)
	}
	fix, _ := v.Load("p/plans/fix.md")
	want := "---\ntitle: Fix\nrelated: [\"[[p/bugs/BUG-001]]\", \"[[other]]\"]\norigin: \"[[BUG-001|the bug]]\"\n---\n\nNo link in the body.\n"
	if string(fix.Content) != want || len(rewritten) != 1 {
		t.Errorf("rewritten %v, content:\n%s", rewritten, fix.Content)
	}
	if bl, _ := idx.Backlinks("p/bugs/BUG-001.md"); len(bl) != 1 || bl[0].Path != "p/plans/fix.md" {
		t.Errorf("backlinks after rename: %+v", bl)
	}
}
