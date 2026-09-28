package index

import (
	"path/filepath"
	"strconv"
	"testing"
)

// reopen closes idx and opens the same file again, as a restart does.
func reopen(t *testing.T, idx *Index, path string) *Index {
	t.Helper()
	idx.Close()
	idx, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx
}

func stampOf(t *testing.T, idx *Index, path string) Stamp {
	t.Helper()
	st, err := idx.Stamps()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := st[path]
	if !ok {
		t.Fatalf("no stamp for %s", path)
	}
	return s
}

func TestStamps_HashAndTouch(t *testing.T) {
	idx := openTest(t)
	doc := NoteDoc{Path: "p/a.md", Title: "a", Body: "# a\n\n[[p/b]]\n", ModTime: 100, Size: 13}
	if err := idx.Upsert(doc); err != nil {
		t.Fatal(err)
	}
	s := stampOf(t, idx, "p/a.md")
	if s.Hash == "" || s.Hash != ContentHash(doc) || s.ModTime != 100 || s.Size != 13 {
		t.Fatalf("stamp after upsert = %+v, want hash %s, mtime 100, size 13", s, ContentHash(doc))
	}

	if err := idx.Touch("p/a.md", 200, 14); err != nil {
		t.Fatal(err)
	}
	s2 := stampOf(t, idx, "p/a.md")
	if s2.Hash != s.Hash || s2.ModTime != 200 || s2.Size != 14 {
		t.Errorf("stamp after touch = %+v, want same hash, mtime 200, size 14", s2)
	}
	// Touch changes the stamp only: links and title stay as indexed.
	if outs, _ := idx.Outlinks("p/a.md"); len(outs) != 1 {
		t.Errorf("touch dropped the links: %+v", outs)
	}

	// The title is part of the hash: the vault derives it from the path.
	other := doc
	other.Title = "renamed"
	if ContentHash(other) == ContentHash(doc) {
		t.Error("ContentHash ignores the title")
	}
}

// A restart with the same ContentVersion keeps the hashes; a different one
// clears them all, so the next scan re-indexes every note.
func TestOpen_ContentVersionChangeClearsHashes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idx.db")
	idx, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	upsert(t, idx, "p/a.md", "a", "body")

	idx = reopen(t, idx, path)
	if s := stampOf(t, idx, "p/a.md"); s.Hash == "" {
		t.Fatal("hash cleared by a restart with the same ContentVersion")
	}

	if _, err := idx.db.Exec(`UPDATE meta SET value = '0' WHERE key = 'content_version'`); err != nil {
		t.Fatal(err)
	}
	idx = reopen(t, idx, path)
	if s := stampOf(t, idx, "p/a.md"); s.Hash != "" {
		t.Errorf("hash kept across a ContentVersion change: %+v", s)
	}
	var v string
	if err := idx.db.QueryRow(`SELECT value FROM meta WHERE key = 'content_version'`).Scan(&v); err != nil || v != strconv.Itoa(ContentVersion) {
		t.Errorf("content_version = %q (%v), want %d", v, err, ContentVersion)
	}
}

// An index from before v3 has no hash column and no meta table: the
// migration adds them and leaves every hash empty.
func TestOpen_MigratesV2Index(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	idx, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	upsert(t, idx, "p/a.md", "a", "body")
	if _, err := idx.db.Exec(`DROP TRIGGER notes_hash_guard; ALTER TABLE notes DROP COLUMN hash; DROP TABLE meta; PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}

	idx = reopen(t, idx, path)
	var v int
	if err := idx.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != schemaVersion {
		t.Fatalf("user_version = %d (%v), want %d", v, err, schemaVersion)
	}
	if s := stampOf(t, idx, "p/a.md"); s.Hash != "" {
		t.Errorf("migrated note has a hash: %+v", s)
	}
	upsert(t, idx, "p/a.md", "a", "body")
	if s := stampOf(t, idx, "p/a.md"); s.Hash == "" {
		t.Error("upsert after migration stored no hash")
	}
}

// A release from before v3 (a rollback) upserts without touching the hash:
// the guard trigger clears it, so the next scan re-indexes the note instead
// of trusting rows this release did not extract.
func TestHashGuard_UpsertThatKeepsTheHash(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "p/a.md", "a", "body")

	// Our own upsert of new content stores the new hash.
	upsert(t, idx, "p/a.md", "a", "new body")
	if s := stampOf(t, idx, "p/a.md"); s.Hash != ContentHash(NoteDoc{Title: "a", Body: "new body"}) {
		t.Fatalf("changed content: stamp %+v", s)
	}
	// The statement an older release runs on upsert.
	if _, err := idx.db.Exec(`UPDATE notes SET title = 'a', mtime = 2, size = 3, importance = 3 WHERE path = 'p/a.md'`); err != nil {
		t.Fatal(err)
	}
	if s := stampOf(t, idx, "p/a.md"); s.Hash != "" {
		t.Errorf("an upsert that left the hash alone kept it: %+v", s)
	}
	// Touch does not set importance: the hash survives it.
	upsert(t, idx, "p/a.md", "a", "again")
	if err := idx.Touch("p/a.md", 5, 5); err != nil {
		t.Fatal(err)
	}
	if s := stampOf(t, idx, "p/a.md"); s.Hash == "" {
		t.Error("Touch cleared the hash")
	}
}
