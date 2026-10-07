package index

import "testing"

// FolderStats counts the notes under a folder at any depth, never the note
// named like the folder nor a sibling whose name only starts the same way.
func TestFolderStats(t *testing.T) {
	idx := openTest(t)
	for path, size := range map[string]int64{
		"p/skills/big.md":                   100,
		"p/skills/big/flow/step.md":         10,
		"p/skills/big/references/a.md":      30,
		"p/skills/big/references/deep/b.md": 50,
		"p/skills/bigger/x.md":              999,
		"p/skills/small.md":                 5,
	} {
		if err := idx.Upsert(NoteDoc{Path: path, Title: path, Body: "x", ModTime: 1, Size: size}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := idx.FolderStats("p/skills/big", 20)
	if err != nil {
		t.Fatal(err)
	}
	if got != (FolderStats{Notes: 3, Bytes: 90, Over: 2}) {
		t.Errorf("FolderStats(big) = %+v, want 3 notes, 90 bytes, 2 over 20", got)
	}
	if got, _ := idx.FolderStats("p/skills/small", 20); got != (FolderStats{}) {
		t.Errorf("FolderStats(small) = %+v, want zero", got)
	}
}
