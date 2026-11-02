package index

import "testing"

// UpsertMany stores what upserting the notes one at a time does: the same
// rows, and the same ids.
func TestUpsertMany_StoresWhatUpsertsDo(t *testing.T) {
	one, many := openTest(t), openTest(t)
	var docs []Extracted
	for _, d := range goldenCorpus {
		d.ModTime, d.Size = 1, int64(len(d.Body))
		if err := one.UpsertUnresolved(d); err != nil {
			t.Fatalf("upsert %s: %v", d.Path, err)
		}
		docs = append(docs, Extract(d))
	}
	if err := many.UpsertMany(docs[:3]); err != nil {
		t.Fatal(err)
	}
	if err := many.UpsertMany(docs[3:]); err != nil {
		t.Fatal(err)
	}
	if got, want := extractionDigest(t, many.db), extractionDigest(t, one.db); got != want {
		t.Errorf("rows digest %s, want %s", got, want)
	}
	for _, d := range docs {
		var a, b int64
		if err := one.db.QueryRow(`SELECT id FROM notes WHERE path = ?`, d.Path).Scan(&a); err != nil {
			t.Fatal(err)
		}
		if err := many.db.QueryRow(`SELECT id FROM notes WHERE path = ?`, d.Path).Scan(&b); err != nil {
			t.Fatal(err)
		}
		if a != b {
			t.Errorf("%s: id %d, want %d", d.Path, b, a)
		}
	}
	if err := many.UpsertMany(nil); err != nil {
		t.Errorf("an empty batch: %v", err)
	}
}
