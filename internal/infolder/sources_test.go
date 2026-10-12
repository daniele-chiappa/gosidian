package infolder

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// memSource is a source in memory, its files served by an HTTP server.
type memSource struct {
	mu      sync.Mutex
	files   map[string]memFile
	changes bool
	move    bool
	snaps   map[string]map[string]string // cursors → path → etag
	tokens  map[string]string
	lists   int
	gets    int
	fail    error
	srv     *httptest.Server
}

type memFile struct {
	data []byte
	etag string
	mod  time.Time
}

func newMemSource(t *testing.T) *memSource {
	m := &memSource{files: map[string]memFile{}, snaps: map[string]map[string]string{}, tokens: map[string]string{}}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.gets++
		p, ok := m.tokens[strings.TrimPrefix(r.URL.Path, "/f/")]
		f := m.files[p]
		m.mu.Unlock()
		if !ok {
			http.Error(w, "expired", http.StatusForbidden)
			return
		}
		http.ServeContent(w, r, filepath.Base(p), f.mod, strings.NewReader(string(f.data)))
	}))
	t.Cleanup(m.srv.Close)
	return m
}

var etagSeq int

func (m *memSource) put(p, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	etagSeq++
	m.files[p] = memFile{data: []byte(content), etag: fmt.Sprintf("e%d", etagSeq), mod: time.Date(2026, 10, 1, 0, 0, etagSeq, 0, time.UTC)}
}

func (m *memSource) del(p string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, p)
}

func (m *memSource) snapshot() map[string]string {
	out := map[string]string{}
	for p, f := range m.files {
		out[p] = f.etag
	}
	return out
}

func token() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type memSources struct {
	mu       sync.Mutex
	bindings []Binding
	src      *memSource
}

func (s *memSources) Bindings() []Binding {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Binding(nil), s.bindings...)
}

func (s *memSources) Session(_ context.Context, b Binding) (SourceSession, error) {
	s.src.mu.Lock()
	fail := s.src.fail
	s.src.mu.Unlock()
	if fail != nil {
		return nil, fail
	}
	return memSession{s.src}, nil
}

type memSession struct{ m *memSource }

func (s memSession) Info() SourceInfo {
	return SourceInfo{Kind: "mem", Changes: s.m.changes, Move: s.m.move}
}
func (s memSession) Close() error { return nil }

func (s memSession) List(_ context.Context, cursor string, limit int) (RemotePage, error) {
	m := s.m
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lists++
	var paths []string
	for p := range m.files {
		if p > cursor {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	var page RemotePage
	for _, p := range paths {
		if len(page.Files) == 2 { // small pages: the paging is tested too
			page.Next = page.Files[1].Path
			break
		}
		f := m.files[p]
		page.Files = append(page.Files, RemoteFile{Path: p, Size: int64(len(f.data)), Modified: f.mod, ETag: f.etag})
	}
	return page, nil
}

func (s memSession) Changes(_ context.Context, cursor string) (RemoteChanges, error) {
	m := s.m
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.snapshot()
	next := token()
	m.snaps[next] = now
	if cursor == "" {
		return RemoteChanges{Cursor: next}, nil
	}
	before, ok := m.snaps[cursor]
	if !ok {
		return RemoteChanges{Cursor: next, Reset: true}, nil
	}
	var ch []RemoteFile
	for p, e := range now {
		if before[p] != e {
			f := m.files[p]
			ch = append(ch, RemoteFile{Path: p, Size: int64(len(f.data)), Modified: f.mod, ETag: e})
		}
	}
	for p := range before {
		if _, ok := now[p]; !ok {
			ch = append(ch, RemoteFile{Path: p, Deleted: true})
		}
	}
	return RemoteChanges{Changes: ch, Cursor: next}, nil
}

func (s memSession) Open(_ context.Context, p string) (RemoteURL, error) {
	m := s.m
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[p]
	if !ok {
		return RemoteURL{}, errors.New("no file " + p)
	}
	tok := token()
	m.tokens[tok] = p
	return RemoteURL{URL: m.srv.URL + "/f/" + tok, Size: int64(len(f.data)), ETag: f.etag}, nil
}

func (s memSession) Move(_ context.Context, from, to string) (RemoteFile, error) {
	m := s.m
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[from]
	if !ok {
		return RemoteFile{}, errors.New("no file " + from)
	}
	delete(m.files, from)
	m.files[to] = f
	return RemoteFile{Path: to, Size: int64(len(f.data)), ETag: f.etag}, nil
}

func (f *fixture) bind(t *testing.T, m *memSource, b Binding) *memSources {
	t.Helper()
	srcs := &memSources{bindings: []Binding{b}, src: m}
	f.s.SetSources(srcs)
	return srcs
}

func (f *fixture) check(t *testing.T) {
	t.Helper()
	f.s.checkSources(context.Background(), "p/")
}

func (f *fixture) files(t *testing.T) map[string]File {
	t.Helper()
	rows, err := f.s.store.projectFiles("p")
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestSource_ListBatchesAndRemovals(t *testing.T) {
	f := newFixture(t)
	m := newMemSource(t)
	m.put("a.txt", "a")
	m.put("2024/b.pdf", "bb")
	m.put("2024/c.xml", "ccc")
	m.put(".hidden/x.txt", "x")
	m.put("2024/../escape.txt", "x")
	f.write(t, "p/IN/local.txt", "local")
	f.bind(t, m, Binding{Project: "p", Folder: "nc", Plugin: "mem", Source: "docs"})
	f.scan(t)
	f.check(t)

	rows := f.files(t)
	for _, p := range []string{"p/IN/nc/a.txt", "p/IN/nc/2024/b.pdf", "p/IN/nc/2024/c.xml", "p/IN/local.txt"} {
		if _, ok := rows[p]; !ok {
			t.Errorf("no row %s: %v", p, keys(rows))
		}
	}
	if len(rows) != 4 {
		t.Errorf("rows %v: the hidden and the unclean paths are left out", keys(rows))
	}
	b := rows["p/IN/nc/2024/b.pdf"]
	if b.Source != "mem/docs" || b.Dir != "nc/2024" || b.ETag == "" || b.SHA256 != "" || b.Status != StatusNew || !b.Remote() || b.Version() != b.ETag {
		t.Errorf("b.pdf %+v", b)
	}
	if m.lists < 2 {
		t.Errorf("lists %d: four files in pages of two", m.lists)
	}
	st, err := f.s.SourceStates("p")
	if err != nil || len(st) != 1 || st[0].Status != "ok" || st[0].Folder != "nc" || st[0].Kind != "mem" {
		t.Fatalf("states %+v %v", st, err)
	}

	// The local scan leaves the source's rows alone; quiet, the batches close.
	f.now = f.now.Add(61 * time.Second)
	f.scan(t)
	batches, files := f.sink.take()
	dirs := map[string][]string{}
	for i, bt := range batches {
		dirs[bt.Dir] = paths(files[i])
	}
	if len(dirs["nc"]) != 1 || len(dirs["nc/2024"]) != 2 || len(dirs[""]) != 1 {
		t.Fatalf("batches %v", dirs)
	}

	// A file that changes is new again, in a new batch; one gone is forgotten.
	if err := f.s.SetStatus("p", []string{"p/IN/nc/2024/b.pdf"}, StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	m.put("2024/b.pdf", "b2")
	m.del("2024/c.xml")
	f.check(t)
	rows = f.files(t)
	if r := rows["p/IN/nc/2024/b.pdf"]; r.Status != StatusNew || r.ETag == b.ETag || r.Batch == b.Batch {
		t.Errorf("b.pdf changed: %+v", r)
	}
	if _, ok := rows["p/IN/nc/2024/c.xml"]; ok {
		t.Error("c.xml is gone from the source, and still in in.db")
	}
	// Same size, same etag: not a change.
	before := rows["p/IN/nc/a.txt"]
	f.s.checkSources(context.Background(), "p/nc")
	if r := f.files(t)["p/IN/nc/a.txt"]; r.Updated != before.Updated || r.Batch != before.Batch {
		t.Errorf("a.txt unchanged, its row changed: %+v", r)
	}
}

func keys(m map[string]File) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestSource_Changes(t *testing.T) {
	f := newFixture(t)
	m := newMemSource(t)
	m.changes = true
	m.put("a.txt", "a")
	f.bind(t, m, Binding{Project: "p", Folder: "nc", Plugin: "mem", Source: "docs"})
	f.check(t)
	if m.lists != 1 {
		t.Fatalf("the first check lists the source: %d", m.lists)
	}
	m.put("b.txt", "b")
	m.del("a.txt")
	f.check(t)
	rows := f.files(t)
	if _, ok := rows["p/IN/nc/b.txt"]; !ok || len(rows) != 1 {
		t.Fatalf("after the changes: %v", keys(rows))
	}
	if m.lists != 1 {
		t.Errorf("the changes were read with a list: %d", m.lists)
	}
	// A cursor the source no longer knows: a list in full.
	m.mu.Lock()
	m.snaps = map[string]map[string]string{}
	m.mu.Unlock()
	m.put("c.txt", "c")
	f.check(t)
	if _, ok := f.files(t)["p/IN/nc/c.txt"]; !ok || m.lists != 2 {
		t.Errorf("after a reset: %v, lists %d", keys(f.files(t)), m.lists)
	}
	// An hour on, a list in full all the same.
	f.now = f.now.Add(fullListEvery + time.Minute)
	f.check(t)
	if m.lists != 3 {
		t.Errorf("lists %d after an hour", m.lists)
	}
}

func TestSource_Marker(t *testing.T) {
	f := newFixture(t)
	m := newMemSource(t)
	m.put("2024/a.pdf", "a")
	m.put("2024/.done", "")
	m.put("2025/b.pdf", "b")
	f.bind(t, m, Binding{Project: "p", Folder: "nc", Plugin: "mem", Source: "docs"})
	f.check(t)
	batches, _ := f.sink.take()
	if len(batches) != 1 || batches[0].Dir != "nc/2024" {
		t.Fatalf("the marker closes its batch at once: %+v", batches)
	}
	if _, ok := f.files(t)["p/IN/nc/2024/.done"]; ok {
		t.Error("the marker is a file of the IN folder")
	}
	// It stays in the source: it counts once.
	m.put("2024/c.pdf", "c")
	f.check(t)
	if batches, _ := f.sink.take(); len(batches) != 0 {
		t.Errorf("the marker that stays closed another batch: %+v", batches)
	}
}

func TestSource_OpenAndRead(t *testing.T) {
	f := newFixture(t)
	m := newMemSource(t)
	m.put("doc.txt", "0123456789")
	f.bind(t, m, Binding{Project: "p", Folder: "nc", Plugin: "mem", Source: "docs"})
	f.check(t)
	ctx := context.Background()

	data, err := f.s.Read(ctx, "p/IN/nc/doc.txt", 100)
	if err != nil || string(data) != "0123456789" {
		t.Fatalf("read %q %v", data, err)
	}
	if _, err := f.s.Read(ctx, "p/IN/nc/doc.txt", 5); err == nil {
		t.Error("a file over max was read")
	}
	e, err := f.s.Stat("p", "p/IN/nc/doc.txt")
	if err != nil || e.Size != 10 || e.Dir {
		t.Errorf("stat %+v %v", e, err)
	}
	if e, err := f.s.Stat("p", "p/IN/nc"); err != nil || !e.Dir {
		t.Errorf("stat of the folder %+v %v", e, err)
	}

	// Served as a file of the disk is: ranges, through ServeContent.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rf, e, err := f.s.Open(r.Context(), "p/IN/nc/doc.txt")
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		defer rf.Close()
		http.ServeContent(w, r, "doc.txt", e.ModTime, rf)
	}))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Range", "bytes=3-5")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(body) != "345" {
		t.Errorf("range: %d %q", resp.StatusCode, body)
	}

	// Seeks back and forth, a GET each time it reads elsewhere.
	rf, _, err := f.s.Open(ctx, "p/IN/nc/doc.txt")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	if _, err := rf.Seek(7, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(rf, buf); err != nil || string(buf) != "78" {
		t.Errorf("at 7: %q %v", buf, err)
	}
	if _, err := rf.Seek(1, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(rf, buf); err != nil || string(buf) != "12" {
		t.Errorf("at 1: %q %v", buf, err)
	}
	// The URL expires: another is asked for.
	m.mu.Lock()
	m.tokens = map[string]string{}
	m.mu.Unlock()
	if _, err := rf.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	all, err := io.ReadAll(rf)
	if err != nil || string(all) != "0123456789" {
		t.Errorf("after the URL expired: %q %v", all, err)
	}
	rf.Close()

	// The file changed in the source since the check: said, not served.
	m.put("doc.txt", "longer than before")
	if _, err := f.s.Read(ctx, "p/IN/nc/doc.txt", 100); !errors.Is(err, errChanged) {
		t.Errorf("changed: %v", err)
	}
	// Without the sources (the pipelines off): not read.
	f.s.SetSources(nil)
	if _, err := f.s.Read(ctx, "p/IN/nc/doc.txt", 100); !errors.Is(err, ErrRemote) {
		t.Errorf("no sources: %v", err)
	}
}

func TestSource_MoveAndRemove(t *testing.T) {
	f := newFixture(t)
	m := newMemSource(t)
	m.put("a.pdf", "a")
	srcs := f.bind(t, m, Binding{Project: "p", Folder: "nc", Plugin: "mem", Source: "docs"})
	f.check(t)
	f.write(t, "p/IN/local.txt", "l")
	f.scan(t)

	if _, err := f.s.Move("p", "p/IN/nc/a.pdf", "p/IN/nc/done/a.pdf"); !errors.Is(err, ErrRemote) || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("read-only: %v", err)
	}
	srcs.mu.Lock()
	srcs.bindings[0].Write = true
	srcs.mu.Unlock()
	if _, err := f.s.Move("p", "p/IN/nc/a.pdf", "p/IN/nc/done/a.pdf"); !errors.Is(err, ErrRemote) || !strings.Contains(err.Error(), "cannot move") {
		t.Errorf("a source that does not move: %v", err)
	}
	m.move = true
	e, err := f.s.Move("p", "p/IN/nc/a.pdf", "p/IN/nc/done/a.pdf")
	if err != nil || e.Path != "p/IN/nc/done/a.pdf" {
		t.Fatalf("move: %+v %v", e, err)
	}
	if _, ok := m.files["done/a.pdf"]; !ok {
		t.Error("not moved in the source")
	}
	rows := f.files(t)
	if r, ok := rows["p/IN/nc/done/a.pdf"]; !ok || r.Dir != "nc/done" {
		t.Errorf("rows %v", keys(rows))
	}
	f.check(t)
	if r := f.files(t)["p/IN/nc/done/a.pdf"]; r.Status != StatusNew || r.Run != "" {
		t.Errorf("after the next check: %+v", r)
	}
	// A target outside the source is read inside it.
	e, err = f.s.Move("p", "p/IN/nc/done/a.pdf", "p/IN/fatti/a.pdf")
	if err != nil || e.Path != "p/IN/nc/fatti/a.pdf" {
		t.Fatalf("a target outside the source: %+v %v", e, err)
	}
	if _, ok := m.files["fatti/a.pdf"]; !ok {
		t.Error("not moved to the source's fatti/")
	}
	for _, mv := range [][2]string{{"p/IN/local.txt", "p/IN/nc/local.txt"}, {"p/IN/nc/fatti/a.pdf", "p/IN/nc/.done"}, {"p/IN/nc/fatti/a.pdf", "q/IN/a.pdf"}} {
		if _, err := f.s.Move("p", mv[0], mv[1]); err == nil {
			t.Errorf("moved %s to %s", mv[0], mv[1])
		}
	}
	if err := f.s.Remove("p", "p/IN/nc/fatti/a.pdf", nil); !errors.Is(err, ErrRemote) {
		t.Errorf("remove: %v", err)
	}
	if _, ok := m.files["fatti/a.pdf"]; !ok {
		t.Error("removed from the source")
	}
}

func TestSource_LocalFolderAndUnbind(t *testing.T) {
	f := newFixture(t)
	m := newMemSource(t)
	m.put("a.pdf", "a")
	f.write(t, "p/IN/nc/mine.txt", "mine")
	f.scan(t)
	srcs := f.bind(t, m, Binding{Project: "p", Folder: "nc", Plugin: "mem", Source: "docs"})
	f.check(t)
	st, _ := f.s.SourceStates("p")
	if len(st) != 1 || st[0].Status != "error" || !strings.Contains(st[0].Error, "local folder") {
		t.Errorf("states %+v", st)
	}
	f.scan(t)
	rows := f.files(t)
	if _, ok := rows["p/IN/nc/mine.txt"]; !ok || len(rows) != 1 {
		t.Errorf("the local folder stays local: %v", keys(rows))
	}

	// Bound elsewhere, then unbound: forgotten.
	srcs.mu.Lock()
	srcs.bindings[0].Folder = "cloud"
	srcs.mu.Unlock()
	f.check(t)
	if _, ok := f.files(t)["p/IN/cloud/a.pdf"]; !ok {
		t.Fatalf("rows %v", keys(f.files(t)))
	}
	srcs.mu.Lock()
	srcs.bindings = nil
	srcs.mu.Unlock()
	f.check(t)
	rows = f.files(t)
	if _, ok := rows["p/IN/cloud/a.pdf"]; ok || len(rows) != 1 {
		t.Errorf("unbound, still there: %v", keys(rows))
	}
	if st, _ := f.s.SourceStates("p"); len(st) != 0 {
		t.Errorf("states %+v", st)
	}
}

func TestSource_FailedCheckAndPipelinesOff(t *testing.T) {
	f := newFixture(t)
	m := newMemSource(t)
	m.put("a.pdf", "a")
	m.fail = errors.New("the archive is down")
	f.bind(t, m, Binding{Project: "p", Folder: "nc", Plugin: "mem", Source: "docs"})
	f.check(t)
	st, _ := f.s.SourceStates("p")
	if len(st) != 1 || st[0].Status != "error" || st[0].Error != "the archive is down" {
		t.Fatalf("states %+v", st)
	}
	m.mu.Lock()
	m.fail = nil
	m.mu.Unlock()
	f.check(t)
	st, _ = f.s.SourceStates("p")
	if st[0].Status != "ok" || len(f.files(t)) != 1 {
		t.Fatalf("states %+v rows %v", st, keys(f.files(t)))
	}
	// The pipelines off: the folder keeps what it held, and says why.
	f.s.SetSources(nil)
	f.check(t)
	st, _ = f.s.SourceStates("p")
	if len(st) != 1 || st[0].Status != "off" || len(f.files(t)) != 1 {
		t.Errorf("off: %+v %v", st, keys(f.files(t)))
	}
}

func TestContentRange(t *testing.T) {
	for in, want := range map[string][3]int64{
		"bytes 3-5/10": {3, 10, 1}, "bytes 0-0/*": {0, -1, 1}, "bytes 3-5": {0, 0, 0}, "3-5/10": {0, 0, 0}, "bytes x-5/10": {0, 0, 0},
	} {
		s, total, ok := contentRange(in)
		if s != want[0] || total != want[1] || ok != (want[2] == 1) {
			t.Errorf("%q: %d %d %v", in, s, total, ok)
		}
	}
}

func TestValidFolder(t *testing.T) {
	for name, want := range map[string]bool{"nc": true, "Nextcloud 2024": true, "": false, ".nc": false, "a/b": false, "IN": false, " a": false} {
		if ValidFolder(name) != want {
			t.Errorf("%q: %v", name, !want)
		}
	}
}
