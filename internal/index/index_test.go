package index

import (
	"path/filepath"
	"strings"
	"testing"
)

func openTest(t *testing.T) *Index {
	t.Helper()
	dir := t.TempDir()
	idx, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx
}

func upsert(t *testing.T, idx *Index, path, title, body string) {
	t.Helper()
	err := idx.Upsert(NoteDoc{
		Path: path, Title: title, Body: body,
		ModTime: 1, Size: int64(len(body)),
	})
	if err != nil {
		t.Fatalf("upsert %s: %v", path, err)
	}
}

func TestIndex_UpsertAndBacklinks(t *testing.T) {
	idx := openTest(t)

	upsert(t, idx, "other.md", "Other", "# Other\nContent here")
	upsert(t, idx, "hello.md", "Hello", "# Hello\nLinks to [[Other]] #demo")

	backs, err := idx.Backlinks("other.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(backs) != 1 || backs[0].Path != "hello.md" {
		t.Errorf("backlinks = %+v, want [hello.md]", backs)
	}
}

func TestIndex_InboundResolution(t *testing.T) {
	idx := openTest(t)

	// Note that links to yet-nonexistent "Target" is inserted first.
	upsert(t, idx, "source.md", "Source", "Ref [[Target]]")

	outs, err := idx.Outlinks("source.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 1 || outs[0].TargetPath != "" {
		t.Errorf("pre-target outlink should be unresolved: %+v", outs)
	}

	// Now create the target — inbound resolution should kick in.
	upsert(t, idx, "target.md", "Target", "# Target")

	outs, _ = idx.Outlinks("source.md")
	if len(outs) != 1 || outs[0].TargetPath != "target.md" {
		t.Errorf("outlink should be resolved to target.md, got %+v", outs)
	}

	backs, _ := idx.Backlinks("target.md")
	if len(backs) != 1 || backs[0].Path != "source.md" {
		t.Errorf("backlinks = %+v, want [source.md]", backs)
	}
}

func TestIndex_SearchFTS(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "a.md", "Alpha", "The quick brown fox jumps over the lazy dog")
	upsert(t, idx, "b.md", "Bravo", "Gopher language rocks for web services")

	hits, err := idx.Search("gopher", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Path != "b.md" {
		t.Errorf("hits = %+v, want [b.md]", hits)
	}
	// prefix search
	hits, _ = idx.Search("broW", 10)
	if len(hits) != 1 || hits[0].Path != "a.md" {
		t.Errorf("prefix hits = %+v, want [a.md]", hits)
	}
}

func TestIndex_TagsQuery(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "a.md", "A", "#demo content")
	upsert(t, idx, "b.md", "B", "#demo #other content")
	upsert(t, idx, "c.md", "C", "no tags")

	tags, err := idx.Tags()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, t := range tags {
		counts[t.Tag] = t.Count
	}
	if counts["demo"] != 2 || counts["other"] != 1 {
		t.Errorf("tag counts = %v", counts)
	}

	notes, _ := idx.NotesByTag("demo")
	if len(notes) != 2 {
		t.Errorf("notes by tag demo = %+v", notes)
	}
}

func TestIndex_Delete(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "a.md", "A", "hello world")
	upsert(t, idx, "b.md", "B", "see [[A]]")

	if err := idx.Delete("a.md"); err != nil {
		t.Fatal(err)
	}
	if n, _ := idx.Note("a.md"); n != nil {
		t.Error("note a.md should be gone")
	}
	// outgoing link from b should now be unresolved
	outs, _ := idx.Outlinks("b.md")
	if len(outs) != 1 || outs[0].TargetPath != "" {
		t.Errorf("after delete, b→A should be unresolved: %+v", outs)
	}
}

func TestIndex_Reindex(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "a.md", "A", "first #tagone")
	upsert(t, idx, "a.md", "A", "second #tagtwo")

	tags, _ := idx.Tags()
	for _, tc := range tags {
		if tc.Tag == "tagone" {
			t.Errorf("old tag should be removed after reindex")
		}
	}
	hits, _ := idx.Search("second", 10)
	if len(hits) != 1 {
		t.Errorf("FTS should contain new body, got %+v", hits)
	}
	hits, _ = idx.Search("first", 10)
	if len(hits) != 0 {
		t.Errorf("FTS should not contain old body, got %+v", hits)
	}
}

func TestRecentNotes(t *testing.T) {
	idx := openTest(t)
	// upsert populates mtime via NoteDoc.ModTime. The helper `upsert` sets a
	// static time — we set distinct values directly to ensure ordering.
	mustUpsert := func(path, title, body string, mtime int64) {
		if err := idx.Upsert(NoteDoc{Path: path, Title: title, Body: body, ModTime: mtime, Size: int64(len(body))}); err != nil {
			t.Fatalf("upsert %s: %v", path, err)
		}
	}
	mustUpsert("a.md", "A", "alpha", 1000)
	mustUpsert("proj/b.md", "B", "beta", 2000)
	mustUpsert("proj/c.md", "C", "gamma", 3000)

	// No filter — all 3 notes, sorted by mtime desc.
	got, err := idx.RecentNotes("", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Path != "proj/c.md" || got[1].Path != "proj/b.md" || got[2].Path != "a.md" {
		t.Errorf("order wrong: %+v", got)
	}

	// Project filter.
	got, err = idx.RecentNotes("proj", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("project filter len = %d, want 2", len(got))
	}
	for _, n := range got {
		if !strings.HasPrefix(n.Path, "proj/") {
			t.Errorf("path %q should start with proj/", n.Path)
		}
	}

	// Since filter.
	got, err = idx.RecentNotes("", 2500, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "proj/c.md" {
		t.Errorf("since filter wrong: %+v", got)
	}

	// Limit.
	got, err = idx.RecentNotes("", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("limit = %d, want 1", len(got))
	}
}

func TestIndex_GraphData(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "a.md", "A", "[[B]] [[C]]")
	upsert(t, idx, "b.md", "B", "[[C]]")
	upsert(t, idx, "c.md", "C", "leaf")

	nodes, edges, err := idx.GraphData("", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 {
		t.Errorf("nodes = %d, want 3", len(nodes))
	}
	// 3 unique undirected edges: a-b, a-c, b-c
	if len(edges) != 3 {
		t.Errorf("edges = %d, want 3", len(edges))
	}
	for _, e := range edges {
		if e.Count < 1 {
			t.Errorf("edge %s-%s has zero count", e.From, e.To)
		}
	}
	// Degree: a links to b and c => 2; b links to a and c => 2; c links to a and b => 2
	for _, n := range nodes {
		if n.Degree != 2 {
			t.Errorf("node %s degree = %d, want 2", n.Path, n.Degree)
		}
	}
}
