package infolder

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/vault"
)

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (f *fixture) open(t *testing.T, dir, policy string, files ...UploadFile) Upload {
	t.Helper()
	u, err := f.s.OpenUpload(UploadRequest{Project: "p", Dir: dir, Policy: policy, Owner: "ann", OwnerID: "u1", Channel: "web", Files: files})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (f *fixture) put(t *testing.T, u Upload, n int, offset int64, data string) UploadFile {
	t.Helper()
	got, err := f.s.WriteChunk("p", u.ID, "u1", n, offset, strings.NewReader(data))
	if err != nil {
		t.Fatalf("piece %d at %d: %v", n, offset, err)
	}
	return got
}

// finish completes an upload and waits for its end.
func (f *fixture) finish(t *testing.T, u Upload, policy string, drop ...int) Upload {
	t.Helper()
	if _, err := f.s.CompleteUpload("p", u.ID, "u1", policy, drop); err != nil {
		t.Fatal(err)
	}
	return f.waitDone(t, u.ID)
}

func (f *fixture) waitDone(t *testing.T, id string) Upload {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		u, err := f.s.GetUpload("p", id, "u1", true)
		if err != nil {
			t.Fatal(err)
		}
		if u.State == UploadDone {
			return u
		}
		if u.State == UploadOpen && u.Error != "" {
			t.Fatalf("the completion stopped: %s", u.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the upload is still %s", u.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *fixture) read(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.v.Root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (f *fixture) staged(id string) string {
	return filepath.Join(f.v.Root, "p", "IN", vault.INUploads, id)
}

// Files arrive in pieces at the offset received, hashed as they arrive;
// the completion places them, with their rows and their hash, in one
// batch closed at once, and the upload's folder goes.
func TestUploadPiecesAndComplete(t *testing.T) {
	f := newFixture(t)
	mod := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	u := f.open(t, "lotto", "", UploadFile{Path: "a.txt", Size: 10, Modified: mod.UnixMilli()}, UploadFile{Path: "sub/b.txt", Size: 3})
	if u.State != UploadOpen || u.Policy != PolicyKeep || len(u.Files) != 2 || u.ChunkSize != ChunkSize {
		t.Fatalf("opened: %+v", u)
	}
	if u.Files[0].Exists == nil || *u.Files[0].Exists {
		t.Fatalf("a.txt is not there yet: %+v", u.Files[0])
	}
	f.put(t, u, 0, 0, "hello")
	var oe *OffsetError
	if _, err := f.s.WriteChunk("p", u.ID, "u1", 0, 7, strings.NewReader("xx")); !errors.As(err, &oe) || oe.Received != 5 {
		t.Fatalf("a piece past the bytes received: %v", err)
	}
	// A piece sent again from before the bytes received: what the server
	// has is skipped, the rest written.
	if got := f.put(t, u, 0, 3, "lowo"); got.Received != 7 {
		t.Fatalf("a piece sent again: %+v", got)
	}
	if got := f.put(t, u, 0, 2, "ll"); got.Received != 7 {
		t.Fatalf("a piece the server has whole: %+v", got)
	}
	got := f.put(t, u, 0, 7, "rld")
	if got.Received != 10 || got.SHA256 != sha("helloworld") {
		t.Fatalf("whole: %+v", got)
	}
	if _, err := f.s.CompleteUpload("p", u.ID, "u1", "", nil); !errors.As(err, new(*IncompleteError)) {
		t.Fatalf("b.txt has not arrived: %v", err)
	}
	f.put(t, u, 1, 0, "abc")
	if _, err := f.s.WriteChunk("p", u.ID, "u1", 1, 3, strings.NewReader("more")); err != nil {
		t.Fatalf("a piece of nothing at the end is no error: %v", err)
	}
	done := f.finish(t, u, "")
	if r := done.Files[0].Result; r == nil || r.Result != ResultNew || r.Path != "p/IN/lotto/a.txt" {
		t.Fatalf("a.txt: %+v", r)
	}
	if f.read(t, "p/IN/lotto/a.txt") != "helloworld" || f.read(t, "p/IN/lotto/sub/b.txt") != "abc" {
		t.Fatal("the files are not in their places")
	}
	if st, _ := os.Stat(filepath.Join(f.v.Root, "p/IN/lotto/a.txt")); !st.ModTime().Equal(mod) {
		t.Fatalf("a.txt keeps the time it was given: %v", st.ModTime())
	}
	row, err := f.s.Store().File("p", "p/IN/lotto/a.txt")
	if err != nil || row.SHA256 != sha("helloworld") || row.Status != StatusNew || row.Batch == "" {
		t.Fatalf("its row: %+v %v", row, err)
	}
	batches, files := f.sink.take()
	if len(batches) != 1 || batches[0].Dir != "lotto" || batches[0].Files != 2 || batches[0].Closed.IsZero() {
		t.Fatalf("one batch closed: %+v", batches)
	}
	if got := paths(files[0]); !slices.Equal(got, []string{"p/IN/lotto/a.txt", "p/IN/lotto/sub/b.txt"}) {
		t.Fatalf("its files: %v", got)
	}
	if !slices.Equal(done.Batches, []string{batches[0].ID}) {
		t.Fatalf("the upload's batches: %v", done.Batches)
	}
	if _, err := os.Stat(f.staged(u.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the upload's folder is still there: %v", err)
	}
	// The scan finds the files as they are: nothing new, no other batch.
	f.now = f.now.Add(time.Hour)
	f.scan(t)
	if b, _ := f.sink.take(); len(b) != 0 {
		t.Fatalf("the scan made a batch of the uploaded files: %+v", b)
	}
	if _, err := f.s.WriteChunk("p", u.ID, "u1", 0, 10, strings.NewReader("")); !errors.Is(err, ErrUploadState) {
		t.Fatalf("a piece of a done upload: %v", err)
	}
}

// An upload resumes after a restart: the bytes and the state of the hash
// were saved; the same files opened again are the same upload.
func TestUploadResumesAfterRestart(t *testing.T) {
	f := newFixture(t)
	db := filepath.Join(t.TempDir(), "in.db")
	open := func() *Service {
		s, err := Open(db, f.v, func() []string { return []string{"p"} }, Options{Now: func() time.Time { return f.now }})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	f.s.Close()
	f.s = open()
	u := f.open(t, "", "", UploadFile{Path: "big.bin", Size: 9})
	f.put(t, u, 0, 0, "abcd")
	f.s.Close()
	f.s = open()
	defer f.s.Close()
	again := f.open(t, "", "", UploadFile{Path: "big.bin", Size: 9})
	if again.ID != u.ID || again.Files[0].Received != 4 {
		t.Fatalf("the same files are the same upload: %+v", again)
	}
	// A piece written and not recorded (a crash between the two) is
	// dropped.
	part := filepath.Join(f.staged(u.ID), partName(0))
	if err := os.WriteFile(part, []byte("abcdXYZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := f.put(t, u, 0, 4, "efghi")
	if got.SHA256 != sha("abcdefghi") {
		t.Fatalf("the hash resumed: %+v", got)
	}
	f.finish(t, u, "")
	if f.read(t, "p/IN/big.bin") != "abcdefghi" {
		t.Fatal("big.bin")
	}
}

// A file whose bytes the database counts are not on the disk starts again.
func TestUploadLostPartStartsAgain(t *testing.T) {
	f := newFixture(t)
	u := f.open(t, "", "", UploadFile{Path: "a.bin", Size: 6})
	f.put(t, u, 0, 0, "abc")
	if err := os.Remove(filepath.Join(f.staged(u.ID), partName(0))); err != nil {
		t.Fatal(err)
	}
	var oe *OffsetError
	if _, err := f.s.WriteChunk("p", u.ID, "u1", 0, 3, strings.NewReader("def")); !errors.As(err, &oe) || oe.Received != 0 {
		t.Fatalf("lost bytes: %v", err)
	}
	if got := f.put(t, u, 0, 0, "abcdef"); got.SHA256 != sha("abcdef") {
		t.Fatalf("%+v", got)
	}
}

// What to do with a file whose place is taken: keep both, skip, replace
// (into the trash); the same file is not placed again.
func TestUploadPolicies(t *testing.T) {
	f := newFixture(t)
	f.write(t, "p/IN/x.txt", "old")
	f.write(t, "p/IN/same.txt", "same")
	f.write(t, "p/IN/d/inner.txt", "i")
	f.scan(t)
	var discarded []string
	f.s.SetDiscard(func(rel string) error {
		discarded = append(discarded, rel)
		return os.Remove(filepath.Join(f.v.Root, filepath.FromSlash(rel)))
	})
	run := func(policy string, files map[string]string) Upload {
		t.Helper()
		var list []UploadFile
		names := make([]string, 0, len(files))
		for n := range files {
			names = append(names, n)
		}
		slices.Sort(names)
		for _, n := range names {
			list = append(list, UploadFile{Path: n, Size: int64(len(files[n]))})
		}
		u := f.open(t, "", policy, list...)
		for i, n := range names {
			f.put(t, u, i, 0, files[n])
		}
		return f.finish(t, u, "")
	}
	u := run(PolicyKeep, map[string]string{"x.txt": "new", "same.txt": "same", "d": "file"})
	results := map[string]*FileResult{}
	for _, fl := range u.Files {
		results[fl.Path] = fl.Result
	}
	if r := results["x.txt"]; r.Result != ResultRenamed || r.Path != "p/IN/x (2).txt" || f.read(t, "p/IN/x (2).txt") != "new" {
		t.Fatalf("keep: %+v", r)
	}
	if r := results["same.txt"]; r.Result != ResultSame {
		t.Fatalf("the same file: %+v", r)
	}
	if r := results["d"]; r.Result != ResultRenamed || r.Path != "p/IN/d (2)" {
		t.Fatalf("a folder there, keep: %+v", r)
	}
	u = run(PolicySkip, map[string]string{"x.txt": "newer"})
	if r := u.Files[0].Result; r.Result != ResultSkipped || f.read(t, "p/IN/x.txt") != "old" {
		t.Fatalf("skip: %+v", r)
	}
	u = run(PolicyReplace, map[string]string{"x.txt": "newest", "d": "f"})
	results = map[string]*FileResult{}
	for _, fl := range u.Files {
		results[fl.Path] = fl.Result
	}
	if r := results["x.txt"]; r.Result != ResultReplaced || f.read(t, "p/IN/x.txt") != "newest" || !slices.Equal(discarded, []string{"p/IN/x.txt"}) {
		t.Fatalf("replace: %+v %v", r, discarded)
	}
	if r := results["d"]; r.Result != ResultError {
		t.Fatalf("a folder is not replaced: %+v", r)
	}
	row, _ := f.s.Store().File("p", "p/IN/x.txt")
	if row.SHA256 != sha("newest") || row.Status != StatusNew {
		t.Fatalf("the replaced file's row: %+v", row)
	}
}

// The reserve: an upload that would take the disk under it is refused, at
// the start and at each piece.
func TestUploadReserve(t *testing.T) {
	f := newFixture(t)
	free := int64(1000)
	f.s.opts.FreeBytes = func(string) (int64, error) { return free, nil }
	f.s.SetUploadLimits(UploadLimits{Reserve: 800})
	if _, err := f.s.OpenUpload(UploadRequest{Project: "p", OwnerID: "u1", Files: []UploadFile{{Path: "a", Size: 300}}}); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("over the reserve: %v", err)
	}
	u := f.open(t, "", "", UploadFile{Path: "a", Size: 150})
	free = 900
	if _, err := f.s.WriteChunk("p", u.ID, "u1", 0, 0, strings.NewReader(strings.Repeat("x", 150))); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("a piece over the reserve: %v", err)
	}
	f.s.SetUploadLimits(UploadLimits{Reserve: 0})
	f.put(t, u, 0, 0, strings.Repeat("x", 150))
}

// The caps of a way in, and of the IN folder.
func TestUploadCaps(t *testing.T) {
	f := newFixture(t)
	f.write(t, "p/IN/have.bin", strings.Repeat("h", 50))
	f.scan(t)
	f.s.SetUploadLimits(UploadLimits{MaxIN: 100, Channels: map[string]ChannelLimits{"web": {MaxFile: 40, MaxUpload: 60, Extensions: []string{"pdf"}}}})
	try := func(files ...UploadFile) error {
		_, err := f.s.OpenUpload(UploadRequest{Project: "p", OwnerID: "u1", Channel: "web", Files: files})
		return err
	}
	for name, files := range map[string][]UploadFile{
		"a file over its cap":  {{Path: "a.pdf", Size: 41}},
		"an upload over":       {{Path: "a.pdf", Size: 40}, {Path: "b.pdf", Size: 21}},
		"a kind not taken":     {{Path: "a.txt", Size: 1}},
		"the IN folder's over": {{Path: "a.pdf", Size: 30}, {Path: "b.pdf", Size: 30}},
	} {
		if err := try(files...); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := try(UploadFile{Path: "a.zip", Size: 10, Unzip: true}); err != nil {
		t.Errorf("a zip to unpack is taken whatever the kinds: %v", err)
	}
	if err := try(UploadFile{Path: "a.PDF", Size: 40}); err != nil {
		t.Errorf("the kinds ignore case: %v", err)
	}
}

// The paths an upload refuses.
func TestUploadBadPaths(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"../x", ".hidden", "a/.git/x", "a//b", "/abs", `a\b`, "a/./b", "nul\x00", " lead", "trail ", "", strings.Repeat("n", 256)} {
		_, err := f.s.OpenUpload(UploadRequest{Project: "p", OwnerID: "u1", Files: []UploadFile{{Path: p, Size: 1}}})
		if !errors.Is(err, ErrBadUpload) {
			t.Errorf("%q: %v", p, err)
		}
	}
	if _, err := f.s.OpenUpload(UploadRequest{Project: "p", OwnerID: "u1", Files: []UploadFile{{Path: "a", Size: 1}, {Path: "a", Size: 2}}}); !errors.Is(err, ErrBadUpload) {
		t.Errorf("twice: %v", err)
	}
	if _, err := f.s.OpenUpload(UploadRequest{Project: "p", OwnerID: "u1", Dir: "../up", Files: []UploadFile{{Path: "a", Size: 1}}}); !errors.Is(err, ErrBadUpload) {
		t.Errorf("a folder out of IN: %v", err)
	}
	// NFD names (macOS) arrive in NFC.
	u := f.open(t, "", "", UploadFile{Path: "café.txt", Size: 1})
	if u.Files[0].Path != "café.txt" {
		t.Errorf("NFC: %q", u.Files[0].Path)
	}
}

// An upload is its opener's; one dropped goes with its folder.
func TestUploadOwnerAndCancel(t *testing.T) {
	f := newFixture(t)
	u := f.open(t, "", "", UploadFile{Path: "a", Size: 2})
	if _, err := f.s.WriteChunk("p", u.ID, "u2", 0, 0, strings.NewReader("ab")); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("another account's piece: %v", err)
	}
	if _, err := f.s.GetUpload("q", u.ID, "u1", true); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("another project: %v", err)
	}
	if list, _ := f.s.Uploads("p", "u2"); len(list) != 0 {
		t.Fatalf("another account's list: %+v", list)
	}
	if list, _ := f.s.Uploads("p", "u1"); len(list) != 1 || list[0].FileCount != 1 || list[0].Bytes != 2 || list[0].Folders || list[0].Zips {
		t.Fatalf("the list: %+v", list)
	}
	f.now = f.now.Add(time.Second)
	f.open(t, "", "", UploadFile{Path: "d/a.zip", Size: 2, Unzip: true})
	if list, _ := f.s.Uploads("p", "u1"); len(list) != 2 || !list[0].Folders || !list[0].Zips {
		t.Fatalf("a folder's zips: %+v", list[0])
	}
	f.put(t, u, 0, 0, "a")
	if err := f.s.CancelUpload("p", u.ID, "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.staged(u.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("its folder: %v", err)
	}
	if _, err := f.s.GetUpload("p", u.ID, "u1", true); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("dropped: %v", err)
	}
}

// A file dropped is left out; one of no bytes needs no piece.
func TestUploadDropAndEmpty(t *testing.T) {
	f := newFixture(t)
	u := f.open(t, "", "", UploadFile{Path: "gone.bin", Size: 5}, UploadFile{Path: "empty.txt", Size: 0})
	done := f.finish(t, u, "", 0)
	if r := done.Files[0].Result; r == nil || r.Result != ResultDropped {
		t.Fatalf("dropped: %+v", r)
	}
	if r := done.Files[1].Result; r == nil || r.Result != ResultNew || f.read(t, "p/IN/empty.txt") != "" {
		t.Fatalf("empty: %+v", r)
	}
	if row, _ := f.s.Store().File("p", "p/IN/empty.txt"); row.SHA256 != emptySHA256 {
		t.Fatalf("its row: %+v", row)
	}
}

func zipOf(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(files[n]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func (f *fixture) uploadZip(t *testing.T, dir, name, data string) FileResult {
	t.Helper()
	u := f.open(t, dir, "", UploadFile{Path: name, Size: int64(len(data)), Unzip: true})
	f.put(t, u, 0, 0, data)
	done := f.finish(t, u, "")
	return *done.Files[0].Result
}

// A zip is unpacked on the server: into its own folder when its files are
// in one, else into a folder named after it; what it should not hold is
// left out, and what would leave the folder is refused.
func TestUploadZip(t *testing.T) {
	f := newFixture(t)
	r := f.uploadZip(t, "in", "pack.zip", zipOf(t, map[string]string{
		"f/a.txt": "A", "f/b/c.txt": "C", "f/.DS_Store": "x", "__MACOSX/f/._a.txt": "x",
	}))
	if r.Result != ResultUnzipped || r.Path != "p/IN/in/f" || r.Counts[ResultNew] != 2 || r.Ignored != 2 {
		t.Fatalf("a zip of a folder: %+v", r)
	}
	if f.read(t, "p/IN/in/f/b/c.txt") != "C" {
		t.Fatal("c.txt")
	}
	if row, _ := f.s.Store().File("p", "p/IN/in/f/a.txt"); row.SHA256 != sha("A") {
		t.Fatalf("a.txt's row: %+v", row)
	}
	r = f.uploadZip(t, "", "loose.zip", zipOf(t, map[string]string{"a.txt": "A", "../evil.txt": "E", "/etc/x": "X", "b/c.txt": "C"}))
	if r.Path != "p/IN/loose" || r.Counts[ResultNew] != 2 || len(r.Refused) != 2 {
		t.Fatalf("a zip of loose files: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(f.v.Root, "p", "evil.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("zip-slip")
	}
	// The same zip again: its files are the same.
	r = f.uploadZip(t, "", "loose.zip", zipOf(t, map[string]string{"a.txt": "A", "b/c.txt": "C"}))
	if r.Counts[ResultSame] != 2 {
		t.Fatalf("the same zip again: %+v", r)
	}
	// Not a zip.
	if r = f.uploadZip(t, "", "fake.zip", "not a zip"); r.Result != ResultError {
		t.Fatalf("not a zip: %+v", r)
	}
	batches, _ := f.sink.take()
	if len(batches) != 2 {
		t.Fatalf("a batch for each zip that placed files: %+v", batches)
	}
}

// The caps of a zip: its files, their bytes, their ratio to its size.
func TestUploadZipCaps(t *testing.T) {
	f := newFixture(t)
	f.s.SetUploadLimits(UploadLimits{ZipMaxEntries: 2})
	if r := f.uploadZip(t, "", "many.zip", zipOf(t, map[string]string{"a": "1", "b": "2", "c": "3"})); r.Result != ResultError || !strings.Contains(r.Error, "3 files") {
		t.Fatalf("over the files: %+v", r)
	}
	f.s.SetUploadLimits(UploadLimits{ZipMaxBytes: 5})
	if r := f.uploadZip(t, "", "big.zip", zipOf(t, map[string]string{"a": "123456"})); r.Result != ResultError {
		t.Fatalf("over the bytes: %+v", r)
	}
	f.s.SetUploadLimits(UploadLimits{ZipMaxRatio: 200})
	bomb := zipOf(t, map[string]string{"zeros": strings.Repeat("\x00", ratioFloor+1)})
	if r := f.uploadZip(t, "", "bomb.zip", bomb); r.Result != ResultError || !strings.Contains(r.Error, "times its size") {
		t.Fatalf("a bomb: %+v", r)
	}
	// Room for the zip, not for its files.
	free := int64(1 << 20)
	f.s.opts.FreeBytes = func(string) (int64, error) { return free, nil }
	f.s.SetUploadLimits(UploadLimits{Reserve: 1<<20 - 500})
	data := zipOf(t, map[string]string{"a": strings.Repeat("a", 1000)})
	u := f.open(t, "", "", UploadFile{Path: "fill.zip", Size: int64(len(data)), Unzip: true})
	f.put(t, u, 0, 0, data)
	free = 1<<20 - 100
	if r := f.finish(t, u, "").Files[0].Result; r.Result != ResultError || !strings.Contains(r.Error, "free disk") {
		t.Fatalf("over the reserve: %+v", r)
	}
}

// A zip's file longer than the zip says is not written past it.
func TestUploadZipLies(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	data := strings.Repeat("y", 1000)
	w, err := zw.CreateRaw(&zip.FileHeader{Name: "liar.txt", Method: zip.Store, CRC32: 0, CompressedSize64: 1000, UncompressedSize64: 10})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(data))
	zw.Close()
	f := newFixture(t)
	r := f.uploadZip(t, "", "liar.zip", buf.String())
	if r.Counts[ResultNew] != 0 {
		t.Fatalf("a liar's file was placed: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(f.v.Root, "p/IN/liar/liar.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("liar.txt")
	}
}

// The old uploads go, with their folders; a folder no upload holds goes
// after an hour.
func TestUploadGC(t *testing.T) {
	f := newFixture(t)
	u := f.open(t, "", "", UploadFile{Path: "a", Size: 2})
	f.put(t, u, 0, 0, "a")
	orphan := filepath.Join(f.v.Root, "p", "IN", vault.INUploads, "0123456789abcdef")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	f.s.gcUploads()
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal("an orphan of less than an hour stays")
	}
	old := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(orphan, old, old)
	f.now = f.now.Add(25 * time.Hour)
	f.s.gcUploads()
	if _, err := f.s.GetUpload("p", u.ID, "u1", true); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("an old upload: %v", err)
	}
	for _, d := range []string{f.staged(u.ID), orphan} {
		if _, err := os.Stat(d); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s stays", d)
		}
	}
}

// A completion a restart cut short runs again: the files placed are not
// placed again.
func TestUploadCompletionResumes(t *testing.T) {
	f := newFixture(t)
	u := f.open(t, "", "", UploadFile{Path: "a.txt", Size: 1}, UploadFile{Path: "b.txt", Size: 1})
	f.put(t, u, 0, 0, "a")
	f.put(t, u, 1, 0, "b")
	if ok, err := f.s.store.startCompletion(u.ID, PolicyKeep, nil, f.now); !ok || err != nil {
		t.Fatal(ok, err)
	}
	// a.txt placed, its result recorded, then the server stopped.
	full, _ := f.s.store.upload(u.ID, true)
	rows, err := f.s.place(&full, []item{{staged: partName(0), target: "p/IN/a.txt", size: 1, sum: sha("a")}})
	if err != nil {
		t.Fatal(err)
	}
	rows[0].upload, rows[0].n = u.ID, 0
	if err := f.s.store.recordPlaced(rows, f.now); err != nil {
		t.Fatal(err)
	}
	f.s.resumeUploads()
	done := f.waitDone(t, u.ID)
	if done.Files[0].Result.Result != ResultNew || done.Files[1].Result.Result != ResultNew {
		t.Fatalf("%+v %+v", done.Files[0].Result, done.Files[1].Result)
	}
	if _, err := os.Stat(filepath.Join(f.v.Root, "p/IN/a (2).txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a.txt placed twice")
	}
	batches, _ := f.sink.take()
	if len(batches) != 1 || batches[0].Files != 2 {
		t.Fatalf("one batch of both: %+v", batches)
	}
}
