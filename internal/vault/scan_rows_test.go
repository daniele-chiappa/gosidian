package vault

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/index"
)

// scanCorpus covers what an upsert stores: titles from the frontmatter, the
// H1 and the path; tags inline and in the frontmatter; fields, lists, dates
// and numbers; author fields, one shadowed by the frontmatter; links by
// path, title, basename, with an alias, a fragment, an embed, to the note
// itself and to no note; relations in the frontmatter; a diagram's link;
// an HTML note; nested folders.
var scanCorpus = map[string]string{
	"p/plan.md":           "---\ntitle: The plan\ntags: [p, type:plan, status:draft]\nimportance: 4\nupdated: 2026-09-28\nratio: 0.5\n---\n\n# Heading\n\nSee [[p/bugs#BUG-065|the bug]], [[#Heading]], [[Research notes]] and ![[img.webp]].\n\nInline #topic/sub and #todo.\n",
	"p/bugs.md":           "# Bug tracker\n\n## BUG-065\n\nNo frontmatter, [[p/plan]] back, and [[Nowhere]].\n",
	"p/fields.md":         "---\ntitle: Fields\ncreated_by: dave\nstatus: open # a comment\naliases: [\"a, b\", c]\nsummary: |\n  two\n  lines\ncode: 007\n---\n\nLinks [[The plan]] and [[fields]] (itself).\n",
	"p/relations.md":      "---\ntitle: Relations\nrelated: [\"[[p/plan|The plan]]\", \"[[p/bugs#BUG-065]]\"]\norigin: \"[[p/sync]]\"\n---\n\nBody with [[P/Plan]].\n",
	"p/sync.md":           "---\ntitle: Sync v1.12 \"Agent workflow\"\n---\n\nBody, see [[bugs]].\n",
	"p/diagram.md":        "# Diagram\n\n```diagram\nkind: flow\nnodes:\n  - {id: a, label: Start, link: \"[[p/plan]]\"}\nedges: []\n```\n",
	"p/report.html":       "<!--\n---\ntitle: Report\ntags: [p, type:doc]\n---\n-->\n<html><body><h1>Report</h1><p>Links [[p/plan]] and [[Research notes]].</p></body></html>\n",
	"p/deep/a/b/leaf.md":  "Leaf without a heading, #deep, to [[relations]].\n",
	"p/old.md":            "# Old\n\nTo [[p/plan]].\n",
	"q/research notes.md": "# Research notes\n\nFrom q, to [[p/report]] and [[The plan]].\n",
	"q/meeting.md":        "---\ntags: [q, type:meeting]\ndate: 2026-10-01\nattendees: [Ana, Bo]\n---\n\n# Meeting\n\n[[Old]] and [[new]].\n",
	"q/empty.md":          "",
	"r/one.md":            "# One\n\n#r [[r/two]]\n",
	"r/two.md":            "# Two\n\n#r [[r/one]] [[two]]\n",
}

// The boot scan stores the rows that indexing each note on its own stores:
// the same notes with the same ids, the same links resolved the same way,
// tags, fields, author fields and FTS text, in the same order, on a first
// scan and on a rescan after an edit, a touch, a delete and a new note.
func TestVault_ScanStoresWhatUpsertsDo(t *testing.T) {
	defer func(n int) { scanBatch = n }(scanBatch)
	scanBatch = 3 // several batches, the last one part full
	v := newTestVault(t)
	v.SetHTMLNotes(true)
	for p, body := range scanCorpus {
		write(t, v.Root, p, body)
	}
	scanned, scannedDB := openIndexAt(t, "scan.db")
	byNote, byNoteDB := openIndexAt(t, "notes.db")
	ts := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	authors := []index.AuthorEvent{
		{TS: ts, Action: index.AuthorCreate, Path: "p/plan.md", By: "alice"},
		{TS: ts.Add(time.Hour), Action: index.AuthorUpdate, Path: "p/plan.md", By: "bob"},
		{TS: ts, Action: index.AuthorCreate, Path: "p/fields.md", By: "erin"},
		{TS: ts, Action: index.AuthorCreate, Path: "q/new.md", By: "carol"},
	}
	for _, idx := range []*index.Index{scanned, byNote} {
		if err := idx.RebuildAuthors(authors); err != nil {
			t.Fatal(err)
		}
	}
	check := func(when string, wantReindexed int) {
		t.Helper()
		st, err := v.Scan(scanned)
		if err != nil {
			t.Fatalf("%s: %v", when, err)
		}
		if st.Reindexed != wantReindexed {
			t.Errorf("%s: %d re-indexed, want %d", when, st.Reindexed, wantReindexed)
		}
		upsertEach(t, v, byNote)
		if got, want := dumpIndex(t, scannedDB), dumpIndex(t, byNoteDB); got != want {
			t.Errorf("%s: the scan's rows differ from the upserts'\n%s", when, firstDiff(got, want))
		}
		for _, q := range []string{"plan", "the plan", "research notes", "bug", "deep", "INC", "nowhere at all"} {
			got, err := scanned.SearchWith(q, index.SearchOptions{Limit: 11})
			if err != nil {
				t.Fatal(err)
			}
			want, err := byNote.SearchWith(q, index.SearchOptions{Limit: 11})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s: search %q = %+v, the upserts' index finds %+v", when, q, got, want)
			}
		}
	}
	check("first scan", len(scanCorpus))

	write(t, v.Root, "p/plan.md", strings.Replace(scanCorpus["p/plan.md"], "#todo", "#done [[q/meeting]]", 1))
	old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(filepath.Join(v.Root, "r/one.md"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(v.Root, "p/old.md")); err != nil {
		t.Fatal(err)
	}
	write(t, v.Root, "q/new.md", "# New\n\nBack to [[The plan]] and [[Old]].\n")
	check("rescan", 2)
}

// upsertEach brings idx in line with the vault one note at a time, as the
// watcher would: the oracle the scan is held to.
func upsertEach(t *testing.T, v *Vault, idx *index.Index) {
	t.Helper()
	paths, err := v.List()
	if err != nil {
		t.Fatal(err)
	}
	stamps, err := idx.Stamps()
	if err != nil {
		t.Fatal(err)
	}
	onDisk := map[string]bool{}
	for _, p := range paths {
		n, err := loadNote(v.Root, p)
		if err != nil {
			t.Fatal(err)
		}
		onDisk[n.Path] = true
		doc := toIndexNote(n)
		if old, ok := stamps[n.Path]; ok && old.Holds(doc) {
			if err := idx.Touch(doc.Path, doc.ModTime, doc.Size); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := idx.UpsertUnresolved(doc); err != nil {
			t.Fatal(err)
		}
	}
	for p := range stamps {
		if !onDisk[p] {
			if err := idx.Delete(p); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := idx.ResolveAll(); err != nil {
		t.Fatal(err)
	}
}

// openIndexAt opens an index in a file of the test's own, and a second
// handle on the file to read its tables with.
func openIndexAt(t *testing.T, name string) (*index.Index, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	idx, err := index.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return idx, db
}

// dumpIndex is every row the notes leave in the index, ids and order
// included.
func dumpIndex(t *testing.T, db *sql.DB) string {
	t.Helper()
	var b strings.Builder
	for _, q := range []string{
		`SELECT id, path, name, title, mtime, size, importance, hash FROM notes ORDER BY id`,
		`SELECT rowid, src_id, target, target_key, target_path, alias, field FROM links ORDER BY rowid`,
		`SELECT rowid, note_id, tag FROM tags ORDER BY rowid`,
		`SELECT rowid, note_id, key, value, num, date, source FROM note_fields ORDER BY rowid`,
		`SELECT rowid, title, meta, body FROM notes_fts ORDER BY rowid`,
		`SELECT term, doc, cnt FROM notes_vocab ORDER BY term`,
		`SELECT path, created_by, created_at, modified_by, modified_at FROM authors ORDER BY path`,
	} {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]sql.NullString, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			b.WriteString(q[:strings.Index(q, " FROM")])
			for _, v := range vals {
				fmt.Fprintf(&b, " | %v:%q", v.Valid, v.String)
			}
			b.WriteByte('\n')
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return b.String()
}

// firstDiff is the first line where two dumps part, with the line before.
func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return fmt.Sprintf("line %d\n got: %s\nwant: %s", i+1, gl, wl)
		}
	}
	return ""
}

// An empty vault scans to nothing.
func TestVault_ScanEmpty(t *testing.T) {
	st, err := newTestVault(t).Scan(openIndex(t))
	if err != nil || st != (ScanStats{}) {
		t.Fatalf("scan of an empty vault = %+v, %v", st, err)
	}
}

// A scan that writes most of the vault holds FTS5's merging back while it
// writes and gives the defaults back at the end; any scan sets them again,
// so a load a crash cut short does not leave the merging off.
func TestVault_ScanMergesOnceWhenItLoadsTheVault(t *testing.T) {
	v := newTestVault(t)
	for k := 0; k < 6; k++ {
		write(t, v.Root, fmt.Sprintf("p/n%d.md", k), fmt.Sprintf("# n%d\n\ndi stampa %d\n", k, k))
	}
	idx, db := openIndexAt(t, "bulk.db")
	merging := func() string {
		t.Helper()
		var a, c string
		if err := db.QueryRow(`SELECT (SELECT v FROM notes_fts_config WHERE k = 'automerge'), (SELECT v FROM notes_fts_config WHERE k = 'crisismerge')`).Scan(&a, &c); err != nil {
			t.Fatal(err)
		}
		return a + " " + c
	}
	if st, err := v.Scan(idx); err != nil || st.Reindexed != 6 {
		t.Fatalf("first scan = %+v, %v", st, err)
	}
	if got := merging(); got != "4 16" {
		t.Errorf("merging after the first scan = %s, want the defaults 4 16", got)
	}
	if hits, err := idx.SearchWith("d stam", index.SearchOptions{Limit: 11}); err != nil || len(hits) != 6 {
		t.Errorf("search after the first scan: %d hits, %v", len(hits), err)
	}
	if _, err := db.Exec(`INSERT INTO notes_fts(notes_fts, rank) VALUES('automerge', 0)`); err != nil {
		t.Fatal(err)
	}
	write(t, v.Root, "p/n0.md", "# n0\n\ndi stampa again\n")
	if st, err := v.Scan(idx); err != nil || st.Reindexed != 1 {
		t.Fatalf("second scan = %+v, %v", st, err)
	}
	if got := merging(); got != "4 16" {
		t.Errorf("merging after a scan of one change = %s, want 4 16", got)
	}
}
