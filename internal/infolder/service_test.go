package infolder

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/vault"
)

type sinkRec struct {
	mu      sync.Mutex
	batches []Batch
	files   [][]File
}

func (r *sinkRec) INBatch(b Batch, files []File) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches = append(r.batches, b)
	r.files = append(r.files, files)
}

func (r *sinkRec) take() ([]Batch, [][]File) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, f := r.batches, r.files
	r.batches, r.files = nil, nil
	return b, f
}

type fixture struct {
	v    *vault.Vault
	s    *Service
	sink *sinkRec
	now  time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	v := vault.New(t.TempDir())
	v.SetINProjects(func(p string) bool { return p == "p" })
	if err := os.MkdirAll(filepath.Join(v.Root, "p", "IN"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fixture{v: v, sink: &sinkRec{}, now: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	s, err := Open(filepath.Join(t.TempDir(), "in.db"), v, func() []string { return []string{"p"} },
		Options{Now: func() time.Time { return f.now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.SetSink(f.sink)
	f.s = s
	return f
}

func (f *fixture) write(t *testing.T, rel, content string) {
	t.Helper()
	full := filepath.Join(f.v.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) scan(t *testing.T) {
	t.Helper()
	if err := f.s.Scan("p"); err != nil {
		t.Fatal(err)
	}
}

func paths(files []File) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	slices.Sort(out)
	return out
}

// Files arrive in batches by first-level folder; a batch closes after the
// quiet time, with its files hashed; a changed file is new again, in a new
// batch.
func TestScan_Batches(t *testing.T) {
	f := newFixture(t)
	f.write(t, "p/IN/a/x.pdf", "x")
	f.write(t, "p/IN/a/deep/y.pdf", "yy")
	f.write(t, "p/IN/z.xml", "z")
	f.scan(t)
	if b, _ := f.sink.take(); len(b) != 0 {
		t.Fatalf("closed before the quiet time: %+v", b)
	}
	x, err := f.s.Store().File("p", "p/IN/a/x.pdf")
	if err != nil || x.Status != StatusNew || x.Dir != "a" || x.Name != "x.pdf" || x.Ext != "pdf" || x.Stem != "x" || x.SHA256 == "" {
		t.Fatalf("x = %+v, %v", x, err)
	}

	f.now = f.now.Add(61 * time.Second)
	f.scan(t)
	batches, files := f.sink.take()
	if len(batches) != 2 {
		t.Fatalf("batches = %+v", batches)
	}
	got := map[string][]string{}
	for i, b := range batches {
		got[b.Dir] = paths(files[i])
		if b.Closed.IsZero() || b.Files != len(files[i]) {
			t.Errorf("batch %+v", b)
		}
	}
	if want := []string{"p/IN/a/deep/y.pdf", "p/IN/a/x.pdf"}; !slices.Equal(got["a"], want) {
		t.Errorf("batch a = %v", got["a"])
	}
	if want := []string{"p/IN/z.xml"}; !slices.Equal(got[""], want) {
		t.Errorf("batch at the root = %v", got[""])
	}
	f.scan(t)
	if b, _ := f.sink.take(); len(b) != 0 {
		t.Fatalf("closed twice: %+v", b)
	}

	if err := f.s.SetStatus("p", []string{"p/IN/a/x.pdf"}, StatusDone, "r1"); err != nil {
		t.Fatal(err)
	}
	f.write(t, "p/IN/a/x.pdf", "x changed")
	f.scan(t)
	x, _ = f.s.Store().File("p", "p/IN/a/x.pdf")
	if x.Status != StatusNew || x.Run != "" {
		t.Errorf("a changed file is new again: %+v", x)
	}
	f.now = f.now.Add(61 * time.Second)
	f.scan(t)
	batches, files = f.sink.take()
	if len(batches) != 1 || !slices.Equal(paths(files[0]), []string{"p/IN/a/x.pdf"}) {
		t.Fatalf("the change's batch: %+v %v", batches, files)
	}

	if err := os.Remove(filepath.Join(f.v.Root, "p/IN/z.xml")); err != nil {
		t.Fatal(err)
	}
	f.scan(t)
	if _, err := f.s.Store().File("p", "p/IN/z.xml"); err != ErrNotFound {
		t.Errorf("a file gone stays: %v", err)
	}
}

// A marker closes its folder's batch at once, and goes.
func TestScan_Marker(t *testing.T) {
	f := newFixture(t)
	f.write(t, "p/IN/b/f.pdf", "f")
	f.write(t, "p/IN/b/.done", "")
	f.scan(t)
	batches, files := f.sink.take()
	if len(batches) != 1 || batches[0].Dir != "b" || !slices.Equal(paths(files[0]), []string{"p/IN/b/f.pdf"}) {
		t.Fatalf("the marker did not close the batch: %+v %v", batches, files)
	}
	if _, err := os.Stat(filepath.Join(f.v.Root, "p/IN/b/.done")); !os.IsNotExist(err) {
		t.Error("the marker stays")
	}
}

func TestService_MoveRemoveRunEnded(t *testing.T) {
	f := newFixture(t)
	f.write(t, "p/IN/a/x.pdf", "x")
	f.write(t, "p/IN/a/y.pdf", "y")
	f.scan(t)
	if err := f.s.SetStatus("p", []string{"p/IN/a/x.pdf", "p/IN/a/y.pdf"}, StatusWorking, "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Move("p", "p/IN/a/x.pdf", "p/IN/fatti/x.pdf"); err != nil {
		t.Fatal(err)
	}
	x, err := f.s.Store().File("p", "p/IN/fatti/x.pdf")
	if err != nil || x.Status != StatusWorking || x.Run != "r1" || x.Dir != "fatti" || x.SHA256 == "" {
		t.Fatalf("moved = %+v, %v", x, err)
	}
	f.now = f.now.Add(2 * time.Minute)
	f.scan(t)
	f.sink.take()
	f.scan(t)
	if x, _ := f.s.Store().File("p", "p/IN/fatti/x.pdf"); x.Status != StatusWorking {
		t.Errorf("a move made the file new: %+v", x)
	}
	if err := f.s.SetStatus("p", []string{"p/IN/fatti/x.pdf"}, StatusIgnored, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.RunEnded("r1", false); err != nil {
		t.Fatal(err)
	}
	x, _ = f.s.Store().File("p", "p/IN/fatti/x.pdf")
	y, _ := f.s.Store().File("p", "p/IN/a/y.pdf")
	if x.Status != StatusIgnored || y.Status != StatusError {
		t.Errorf("after the run: x %s, y %s", x.Status, y.Status)
	}
	if err := f.s.Remove("p", "p/IN/a", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Store().File("p", "p/IN/a/y.pdf"); err != ErrNotFound {
		t.Errorf("removed, still known: %v", err)
	}
	if err := f.s.Remove("p", "p/IN", nil); err == nil {
		t.Error("removed the IN folder")
	}
	if err := f.s.SetStatus("p", nil, "wrong", ""); err == nil {
		t.Error("a wrong status")
	}
}

func TestStore_ListFoldersCounts(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"p/IN/a/1.pdf", "p/IN/a/2.xml", "p/IN/a/b/3.pdf", "p/IN/c/4.PDF", "p/IN/5.pdf"} {
		f.write(t, p, p)
	}
	f.scan(t)
	st := f.s.Store()
	list := func(q Query) []string {
		t.Helper()
		page, err := st.List("p", q)
		if err != nil {
			t.Fatal(err)
		}
		return paths(page.Files)
	}
	if got := list(Query{Kinds: []string{"pdf"}}); len(got) != 4 {
		t.Errorf("kinds pdf = %v", got)
	}
	if got, want := list(Query{Folder: "a"}), []string{"p/IN/a/1.pdf", "p/IN/a/2.xml", "p/IN/a/b/3.pdf"}; !slices.Equal(got, want) {
		t.Errorf("folder a = %v", got)
	}
	if got, want := list(Query{Folder: "a", Shallow: true}), []string{"p/IN/a/1.pdf", "p/IN/a/2.xml"}; !slices.Equal(got, want) {
		t.Errorf("folder a, shallow = %v", got)
	}
	if got, want := list(Query{Shallow: true}), []string{"p/IN/5.pdf"}; !slices.Equal(got, want) {
		t.Errorf("the root, shallow = %v", got)
	}
	if got, want := list(Query{Match: "?.xml"}), []string{"p/IN/a/2.xml"}; !slices.Equal(got, want) {
		t.Errorf("match = %v", got)
	}
	if _, err := st.List("p", Query{Match: "["}); err == nil {
		t.Error("a bad glob")
	}
	var all []string
	q := Query{Limit: 2}
	for {
		page, err := st.List("p", q)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, paths(page.Files)...)
		if page.Next == "" {
			break
		}
		q.Cursor = page.Next
	}
	if len(all) != 5 {
		t.Errorf("pages = %v", all)
	}

	folders, err := st.Folders("p", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 2 || folders[0].Path != "a" || folders[0].Files != 3 || folders[1].Path != "c" {
		t.Errorf("folders = %+v", folders)
	}
	if sub, _ := st.Folders("p", "a"); len(sub) != 1 || sub[0].Path != "a/b" || sub[0].Files != 1 {
		t.Errorf("folders of a = %+v", sub)
	}
	c, err := st.Counts("p")
	if err != nil || c.Files != 5 || c.New != 5 {
		t.Errorf("counts = %+v, %v", c, err)
	}

	if err := st.ProjectRenamed("p", "renamed"); err != nil {
		t.Fatal(err)
	}
	if got, err := st.File("renamed", "renamed/IN/a/1.pdf"); err != nil || got.Project != "renamed" {
		t.Errorf("after the rename: %+v, %v", got, err)
	}
}
