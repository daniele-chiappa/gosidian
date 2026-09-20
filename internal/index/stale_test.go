package index

import "testing"

// StaleNotes and MaintenanceCounts must agree on what "closed" means: the
// tool with exclude_closed and the bootstrap digest are two views of the same
// query, and the Closed flag lets callers see the difference in the raw list.
func TestStaleNotes_ClosedFlagAndExclusion(t *testing.T) {
	idx := openTest(t)
	mustUpsert := func(path, body string, mtime int64) {
		t.Helper()
		if err := idx.Upsert(NoteDoc{Path: path, Title: path, Body: body, ModTime: mtime, Size: int64(len(body))}); err != nil {
			t.Fatalf("upsert %s: %v", path, err)
		}
	}
	mustUpsert("proj/plans/done.md", "---\ntags: [type:plan, status:done]\n---\n\n# done", 1000)
	mustUpsert("proj/plans/archived.md", "---\ntags: [type:plan, status:archived]\n---\n\n# archived", 1100)
	mustUpsert("proj/memory/open.md", "---\ntags: [type:memory]\n---\n\n# open", 1200)
	mustUpsert("proj/memory/fresh.md", "---\ntags: [type:memory]\n---\n\n# fresh", 9000)
	mustUpsert("other/old.md", "---\ntags: [status:done]\n---\n\n# other", 500)

	const before = 5000

	got, err := idx.StaleNotes("proj", before, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("raw stale len = %d, want 3: %+v", len(got), got)
	}
	closed := map[string]bool{}
	for _, n := range got {
		closed[n.Path] = n.Closed
	}
	if !closed["proj/plans/done.md"] || !closed["proj/plans/archived.md"] || closed["proj/memory/open.md"] {
		t.Errorf("closed flags wrong: %+v", closed)
	}
	if got[0].Path != "proj/plans/done.md" || got[2].Path != "proj/memory/open.md" {
		t.Errorf("order should be oldest first: %+v", got)
	}

	got, err = idx.StaleNotes("proj", before, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "proj/memory/open.md" || got[0].Closed {
		t.Errorf("exclude_closed should leave only open.md: %+v", got)
	}

	// The digest counts exactly what exclude_closed lists.
	_, stale, err := idx.MaintenanceCounts("proj", before, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stale != len(got) {
		t.Errorf("MaintenanceCounts stale = %d, want %d (same definition as exclude_closed)", stale, len(got))
	}

	// Vault-wide listing keeps the flag too.
	all, err := idx.StaleNotes("", before, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 || all[0].Path != "other/old.md" || !all[0].Closed {
		t.Errorf("vault-wide stale wrong: %+v", all)
	}
}
