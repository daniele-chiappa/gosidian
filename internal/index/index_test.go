package index

import (
	"context"
	"database/sql"
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

func TestIndex_FragmentLinkResolution(t *testing.T) {
	idx := openTest(t)

	// [[note#heading]] must resolve to the note (fragment is
	// presentation-level, Obsidian semantics) — BUG-025: it used to stay
	// unresolved, invisible to backlinks/graph and flagged by lint.
	upsert(t, idx, "proj/bugs.md", "Bug tracker", "# Bugs\n\n## BUG-001\n\nbody")
	upsert(t, idx, "proj/plan.md", "Plan", "See [[proj/bugs#BUG-001]] and [[Bug tracker#BUG-001]]")

	outs, err := idx.Outlinks("proj/plan.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 2 {
		t.Fatalf("outlinks = %+v, want 2", outs)
	}
	for _, o := range outs {
		if o.TargetPath != "proj/bugs.md" {
			t.Errorf("fragment link %q should resolve to proj/bugs.md, got %q", o.Target, o.TargetPath)
		}
	}
	backs, _ := idx.Backlinks("proj/bugs.md")
	if len(backs) != 1 || backs[0].Path != "proj/plan.md" {
		t.Errorf("backlinks = %+v, want [proj/plan.md]", backs)
	}

	// Pure self-fragment [[#heading]] records no cross-note edge.
	upsert(t, idx, "proj/self.md", "Self", "Jump to [[#BUG-001]]")
	outs, _ = idx.Outlinks("proj/self.md")
	for _, o := range outs {
		if o.TargetPath != "" {
			t.Errorf("self-fragment link should stay unresolved, got %+v", o)
		}
	}
}

// "_" and "%" in a wikilink target are literal characters, not LIKE
// wildcards: [[api_reference]] must not resolve to apixreference.md
// (BUG-042).
func TestIndex_BasenameResolutionEscapesWildcards(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "docs/apixreference.md", "X", "decoy")
	upsert(t, idx, "docs/api_reference.md", "Y", "real")
	upsert(t, idx, "a.md", "A", "[[api_reference]] [[apixreference]] [[%]]")
	outs, err := idx.Outlinks("a.md")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, o := range outs {
		got[o.Target] = o.TargetPath
	}
	if got["api_reference"] != "docs/api_reference.md" {
		t.Errorf("[[api_reference]] resolved to %q", got["api_reference"])
	}
	if got["apixreference"] != "docs/apixreference.md" {
		t.Errorf("[[apixreference]] resolved to %q", got["apixreference"])
	}
	if got["%"] != "" {
		t.Errorf("[[%%]] must stay unresolved, got %q", got["%"])
	}
}

// A note whose name is empty once its extension goes (p/.md) is indexed:
// the links to re-resolve were selected with "AND ()" (IMP-162, S4-12).
func TestUpsert_EmptyName(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "p/.md", "", "body")
	if n, err := idx.Note("p/.md"); err != nil || n == nil {
		t.Errorf("p/.md not indexed: %v", err)
	}
}

// Every connection of the pool reads the index through a memory map, not
// only the first one.
func TestOpen_EveryConnectionMapsTheFile(t *testing.T) {
	idx := openTest(t)
	ctx := context.Background()
	var conns []*sql.Conn
	for range 3 {
		c, err := idx.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	for k, c := range conns {
		var n int64
		if err := c.QueryRowContext(ctx, `PRAGMA mmap_size`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != mmapSize {
			t.Errorf("connection %d: mmap_size %d, want %d", k, n, mmapSize)
		}
		c.Close()
	}
}

// An index from v6 has a full-text table without the prefix index, which
// only a new table gets: the migration recreates it and clears every hash,
// so the boot scan fills it again.
func TestOpen_MigratesV6Index(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v6.db")
	idx, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	upsert(t, idx, "p/a.md", "a", "di stampa")
	if _, err := idx.db.Exec(`DROP TABLE notes_fts;
		CREATE VIRTUAL TABLE notes_fts USING fts5(title, meta, body, tokenize='unicode61 remove_diacritics 2');
		PRAGMA user_version = 6`); err != nil {
		t.Fatal(err)
	}

	idx = reopen(t, idx, path)
	var v int
	if err := idx.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != schemaVersion {
		t.Fatalf("user_version = %d (%v), want %d", v, err, schemaVersion)
	}
	var ddl string
	if err := idx.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'notes_fts'`).Scan(&ddl); err != nil || !strings.Contains(ddl, "prefix='1 2 3'") {
		t.Fatalf("notes_fts after the migration: %q (%v)", ddl, err)
	}
	if s := stampOf(t, idx, "p/a.md"); s.Hash != "" {
		t.Errorf("migrated note has a hash: %+v", s)
	}
	upsert(t, idx, "p/a.md", "a", "di stampa")
	if hits, err := idx.SearchWith("d stam", SearchOptions{Limit: 5}); err != nil || len(hits) != 1 || hits[0].Path != "p/a.md" {
		t.Errorf("search after the migration = %+v (%v)", hits, err)
	}
}

// BulkLoad turns FTS5's merging off for a load and back to its defaults.
func TestBulkLoad_SwitchesMerging(t *testing.T) {
	idx := openTest(t)
	merging := func() string {
		t.Helper()
		var a, c string
		if err := idx.db.QueryRow(`SELECT (SELECT v FROM notes_fts_config WHERE k = 'automerge'), (SELECT v FROM notes_fts_config WHERE k = 'crisismerge')`).Scan(&a, &c); err != nil {
			t.Fatal(err)
		}
		return a + " " + c
	}
	for _, c := range []struct {
		on   bool
		want string
	}{{true, "0 64"}, {false, "4 16"}} {
		if err := idx.BulkLoad(c.on); err != nil {
			t.Fatal(err)
		}
		if got := merging(); got != c.want {
			t.Errorf("BulkLoad(%v): automerge, crisismerge = %s, want %s", c.on, got, c.want)
		}
	}
}

// A note is searched by the text SetSearchText gives, and is the note as it
// is in all the rest: its title, tags and hash, so a scan finds it unchanged
// (IMP-201). A note it has nothing for is searched by its own text.
func TestSearchText(t *testing.T) {
	idx := openTest(t)
	asked := []string{}
	idx.SetSearchText(func(path, body string) string {
		asked = append(asked, path)
		if path == "p/a.md" {
			return "---\ntitle: Old\n---\nvalidated words\n"
		}
		return ""
	})
	a := NoteDoc{Path: "p/a.md", Title: "a", Body: "---\ntitle: Draft title\ntags: [draftag]\n---\nunapproved words\n", ModTime: 1, Size: 9}
	if err := idx.Upsert(a); err != nil {
		t.Fatal(err)
	}
	if err := idx.UpsertUnresolved(NoteDoc{Path: "p/b.md", Title: "b", Body: "plain words\n", ModTime: 1, Size: 9}); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 2 {
		t.Errorf("asked %v", asked)
	}
	found := func(q string) string {
		t.Helper()
		hits, err := idx.Search(q, 10)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, h := range hits {
			out = append(out, h.Path)
		}
		return strings.Join(out, " ")
	}
	for q, want := range map[string]string{"validated": "p/a.md", "unapproved": "", "plain": "p/b.md", "Draft": "p/a.md", "draftag": "p/a.md"} {
		if got := found(q); got != want {
			t.Errorf("%q found %q, want %q", q, got, want)
		}
	}
	st, ok, err := idx.StampOf("p/a.md")
	if err != nil || !ok || !st.Holds(a) {
		t.Errorf("the stamp %+v %v %v does not hold the note", st, ok, err)
	}
}
