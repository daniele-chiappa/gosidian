package vault

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// inVault is a vault where project p has an IN folder, q has none.
func inVault(t *testing.T) *Vault {
	t.Helper()
	v := newTestVault(t)
	v.SetINProjects(func(project string) bool { return project == "p" })
	for _, d := range []string{"p", "q"} {
		if err := os.MkdirAll(filepath.Join(v.Root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return v
}

func TestInArea(t *testing.T) {
	v := inVault(t)
	for rel, want := range map[string]bool{
		"p/IN": true, "p/IN/a": true, "p/IN/a/b.md": true,
		"p": false, "p/INX": false, "p/in": false, "p/x/IN": false, "q/IN": false, "IN": false, "IN/a": false,
	} {
		if got := v.InArea(rel); got != want {
			t.Errorf("InArea(%q) = %v, want %v", rel, got, want)
		}
	}
	if New(t.TempDir()).InArea("p/IN/a") {
		t.Error("a vault told of no IN folders has none")
	}
}

// In an IN folder nothing is a note: the scan, the project's count and the
// export leave it out, the note operations refuse it.
func TestIN_NoNotes(t *testing.T) {
	v := inVault(t)
	write(t, v.Root, "p/b.md", "# B")
	write(t, v.Root, "p/IN/a.md", "# A")
	write(t, v.Root, "p/IN/sub/c.md", "# C")
	write(t, v.Root, "q/IN/d.md", "# D") // q has no IN folder: a folder like any other

	paths, err := v.List()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"p/b.md", "q/IN/d.md"}; !slices.Equal(paths, want) {
		t.Fatalf("List = %v, want %v", paths, want)
	}
	notes, err := v.ProjectNotes("p")
	if err != nil || len(notes) != 1 {
		t.Fatalf("ProjectNotes = %v, %v", notes, err)
	}
	projs, _ := v.Projects()
	if projs[0].Name != "p" || projs[0].NoteCount != 1 {
		t.Fatalf("Projects = %+v", projs)
	}

	if _, err := v.Load("p/IN/a.md"); !errors.Is(err, ErrNotNote) {
		t.Errorf("Load in IN: %v", err)
	}
	if err := v.Save("p/IN/new.md", []byte("x")); !errors.Is(err, ErrNotNote) {
		t.Errorf("Save in IN: %v", err)
	}
	if err := v.Delete("p/IN/a.md"); !errors.Is(err, ErrNotNote) {
		t.Errorf("Delete in IN: %v", err)
	}
	if _, err := os.Stat(filepath.Join(v.Root, "p/IN/a.md")); err != nil {
		t.Fatal("the refused delete removed the file")
	}
	idx := openIndex(t)
	if _, err := v.RenameNote(idx, "p/b.md", "p/IN/b.md"); !errors.Is(err, ErrNotNote) {
		t.Errorf("RenameNote into IN: %v", err)
	}
	allowed := map[string]bool{".png": true}
	if err := v.SaveAttachment("p/IN/attachments/x.png", []byte("x"), allowed); !errors.Is(err, ErrNotAttachment) {
		t.Errorf("SaveAttachment in IN: %v", err)
	}
	if _, err := v.Load("q/IN/d.md"); err != nil {
		t.Errorf("a folder IN of a project without one holds notes: %v", err)
	}

	if !v.skipDir(v.Root, filepath.Join(v.Root, "p/IN/sub")) || v.skipDir(v.Root, filepath.Join(v.Root, "q/IN")) {
		t.Error("skipDir: the IN folder of p only")
	}
}

func exported(t *testing.T, v *Vault, dir string) []string {
	t.Helper()
	var out []string
	if err := v.WalkExport(dir, func(rel string, _ fs.FileInfo) error {
		out = append(out, rel)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// The zip of the vault or of the project leaves the IN folder out; the zip
// of the IN folder, or of a folder in it, has all of it.
func TestIN_Export(t *testing.T) {
	v := inVault(t)
	write(t, v.Root, "p/b.md", "# B")
	write(t, v.Root, "p/IN/a.pdf", "a")
	write(t, v.Root, "p/IN/sub/deep/c.pdf", "c")
	if got, want := exported(t, v, ""), []string{"p/b.md"}; !slices.Equal(got, want) {
		t.Errorf("vault export = %v, want %v", got, want)
	}
	if got, want := exported(t, v, "p"), []string{"p/b.md"}; !slices.Equal(got, want) {
		t.Errorf("project export = %v, want %v", got, want)
	}
	if got, want := exported(t, v, "p/IN"), []string{"p/IN/a.pdf", "p/IN/sub/deep/c.pdf"}; !slices.Equal(got, want) {
		t.Errorf("IN export = %v, want %v", got, want)
	}
	if got, want := exported(t, v, "p/IN/sub"), []string{"p/IN/sub/deep/c.pdf"}; !slices.Equal(got, want) {
		t.Errorf("IN subfolder export = %v, want %v", got, want)
	}
}

func TestIN_StatReadDirWalk(t *testing.T) {
	v := inVault(t)
	write(t, v.Root, "p/IN/a.pdf", "aaaa")
	write(t, v.Root, "p/IN/sub/b.xml", "bb")
	write(t, v.Root, "p/IN/sub/.hidden", "h")
	write(t, v.Root, "p/IN/.uploads/part", "p")

	e, err := v.INStat("p/IN/a.pdf")
	if err != nil || e.Dir || e.Size != 4 || e.Path != "p/IN/a.pdf" {
		t.Fatalf("INStat = %+v, %v", e, err)
	}
	if _, err := v.INStat("p/b.pdf"); !errors.Is(err, ErrNotIN) {
		t.Errorf("INStat outside IN: %v", err)
	}
	if _, err := v.INStat("q/IN/x"); !errors.Is(err, ErrNotIN) {
		t.Errorf("INStat in a project without IN: %v", err)
	}
	if _, err := v.INStat("p/IN/../b"); err == nil {
		t.Error("INStat with ..")
	}

	des, err := v.INReadDir("p/IN")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range des {
		names = append(names, d.Path)
	}
	if want := []string{"p/IN/a.pdf", "p/IN/sub"}; !slices.Equal(names, want) {
		t.Errorf("INReadDir = %v, want %v", names, want)
	}

	var walked []string
	if err := v.INWalk("p/IN", func(e INEntry) error {
		walked = append(walked, e.Path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"p/IN/a.pdf", "p/IN/sub/b.xml"}; !slices.Equal(walked, want) {
		t.Errorf("INWalk = %v, want %v", walked, want)
	}

	data, err := v.INRead("p/IN/a.pdf", 4)
	if err != nil || string(data) != "aaaa" {
		t.Fatalf("INRead = %q, %v", data, err)
	}
	if _, err := v.INRead("p/IN/a.pdf", 3); err == nil {
		t.Error("INRead over max")
	}
}

// A link in an IN folder is not there: not stated, opened, listed or
// walked into (the folder fills from outside: rsync, a sync client).
func TestIN_Links(t *testing.T) {
	v := inVault(t)
	outside := t.TempDir()
	write(t, outside, "secret.txt", "s")
	write(t, outside, "dir/inner.txt", "i")
	write(t, v.Root, "p/IN/ok.txt", "o")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(v.Root, "p/IN/link.txt")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if err := os.Symlink(filepath.Join(outside, "dir"), filepath.Join(v.Root, "p/IN/linkdir")); err != nil {
		t.Fatal(err)
	}
	if _, err := v.INStat("p/IN/link.txt"); err == nil {
		t.Error("INStat follows a link")
	}
	if _, _, err := v.INOpen("p/IN/link.txt"); err == nil {
		t.Error("INOpen follows a link")
	}
	if _, _, err := v.INOpen("p/IN/linkdir/inner.txt"); err == nil {
		t.Error("INOpen goes through a linked folder")
	}
	if _, err := v.INReadDir("p/IN/linkdir"); err == nil {
		t.Error("INReadDir of a linked folder")
	}
	des, _ := v.INReadDir("p/IN")
	if len(des) != 1 || des[0].Path != "p/IN/ok.txt" {
		t.Errorf("INReadDir lists links: %+v", des)
	}
	var walked []string
	_ = v.INWalk("p/IN", func(e INEntry) error { walked = append(walked, e.Path); return nil })
	if want := []string{"p/IN/ok.txt"}; !slices.Equal(walked, want) {
		t.Errorf("INWalk = %v, want %v", walked, want)
	}
	if _, err := v.INMove("p/IN/ok.txt", "p/IN/linkdir/ok.txt"); err == nil {
		t.Error("INMove into a linked folder")
	}
	if _, err := os.Stat(filepath.Join(v.Root, "p/IN/ok.txt")); err != nil {
		t.Error("the refused move took the file")
	}
}

func TestIN_MoveRemove(t *testing.T) {
	v := inVault(t)
	write(t, v.Root, "p/IN/a/b/c.pdf", "c")
	write(t, v.Root, "p/IN/x.pdf", "x")

	e, err := v.INMove("p/IN/a/b/c.pdf", "p/IN/done/c.pdf")
	if err != nil || e.Path != "p/IN/done/c.pdf" {
		t.Fatalf("INMove = %+v, %v", e, err)
	}
	if _, err := os.Stat(filepath.Join(v.Root, "p/IN/a")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("the folders the move emptied stay")
	}
	if _, err := v.INMove("p/IN/x.pdf", "p/IN/done/c.pdf"); err == nil {
		t.Error("INMove over a file")
	}
	if _, err := v.INMove("p/IN/done", "p/IN/done/inner"); err == nil {
		t.Error("INMove of a folder into itself")
	}
	if _, err := v.INMove("p/IN", "p/IN/x"); err == nil {
		t.Error("INMove of the IN folder")
	}
	if _, err := v.INMove("p/IN/x.pdf", "p/x.pdf"); !errors.Is(err, ErrNotIN) {
		t.Errorf("INMove out of IN: %v", err)
	}

	if err := v.INRemove("p/IN"); err == nil {
		t.Error("INRemove of the IN folder")
	}
	if err := v.INRemove("p/IN/x.pdf"); err != nil {
		t.Fatal(err)
	}
	if err := v.INRemove("p/IN/done"); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(v.Root, "p/IN")); err != nil || !st.IsDir() {
		t.Error("the IN folder went with its last file")
	}
	if err := v.INMkdir("p/IN/new/deep"); err != nil {
		t.Fatal(err)
	}
}

func TestPruneEmptyParents_StopsAtIN(t *testing.T) {
	root := t.TempDir()
	write(t, root, "p/IN/a/b.pdf", "b")
	if err := os.Remove(filepath.Join(root, "p/IN/a/b.pdf")); err != nil {
		t.Fatal(err)
	}
	PruneEmptyParents(root, "p/IN/a/b.pdf")
	if _, err := os.Stat(filepath.Join(root, "p/IN/a")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("the emptied folder stays")
	}
	if _, err := os.Stat(filepath.Join(root, "p/IN")); err != nil {
		t.Error("the IN folder was pruned")
	}
}

func TestEnsureINAndNotesInIN(t *testing.T) {
	v := inVault(t)
	if err := v.EnsureIN("p"); err != nil {
		t.Fatal(err)
	}
	if err := v.EnsureIN("p"); err != nil {
		t.Fatal("EnsureIN twice:", err)
	}
	write(t, v.Root, "q/IN", "a file")
	if err := v.EnsureIN("q"); err == nil {
		t.Error("EnsureIN over a file named IN")
	}
	write(t, v.Root, "p/IN/z.md", "# Z")
	write(t, v.Root, "p/IN/s/a.md", "# A")
	write(t, v.Root, "p/IN/s/a.pdf", "a")
	got, err := v.NotesInIN("p", 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"p/IN/s/a.md", "p/IN/z.md"}; !slices.Equal(got, want) {
		t.Errorf("NotesInIN = %v, want %v", got, want)
	}
	if got, _ := v.NotesInIN("p", 1); len(got) != 1 {
		t.Errorf("NotesInIN with max 1 = %v", got)
	}
}

// The watcher leaves the IN folders out: a note written there is not
// indexed, one written beside it is (and tells the order of events).
func TestIN_WatcherSkips(t *testing.T) {
	v := inVault(t)
	if err := os.MkdirAll(filepath.Join(v.Root, "p/IN/sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	idx := openIndex(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- v.Watch(ctx, idx, nil) }()
	time.Sleep(150 * time.Millisecond) // the initial watches; see TestWatcher_NewSubdirRace
	write(t, v.Root, "p/IN/sub/hidden.md", "# H")
	write(t, v.Root, "p/IN/new/deep.md", "# D")
	time.Sleep(50 * time.Millisecond)
	write(t, v.Root, "p/seen.md", "# S")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n, _ := idx.Note("p/seen.md"); n != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	if n, _ := idx.Note("p/seen.md"); n == nil {
		t.Fatal("the note beside the IN folder was not indexed")
	}
	for _, p := range []string{"p/IN/sub/hidden.md", "p/IN/new/deep.md"} {
		if n, _ := idx.Note(p); n != nil {
			t.Errorf("%s was indexed", p)
		}
	}
}
