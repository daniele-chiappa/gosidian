package index

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// importDoc is the i-th note of a document import (BUG-121): an image note
// named after its place in the article, 001.md to 050.md, the same names in
// every article's folder, whose frontmatter links its article and the image
// in the IN folder. Neither resolves: there is no article note, and a file of
// the IN folder is no note.
func importDoc(i int) NoteDoc {
	art := fmt.Sprintf("p/wiki/a%04d", i/50)
	name := fmt.Sprintf("%03d", i%50+1)
	body := fmt.Sprintf("---\ntype: image\narticle: \"[[%s]]\"\nsource: \"[[p/IN/wiki/a%04d/%s.png]]\"\n---\n\nAn image of the article.\n", art, i/50, name)
	return NoteDoc{Path: art + "_img/" + name + ".md", Title: name, Body: body, ModTime: 1, Size: int64(len(body))}
}

func medianOf(d []time.Duration) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)
	return s[len(s)/2]
}

// Writing a note costs as much with thousands of notes in the index as with
// a few hundred, when every note has links that never resolve (BUG-121).
// Each write re-resolved every unresolved link whose target contained the
// note's name, each through queries that scanned notes: a write cost more as
// the import went on, and the import as a whole went quadratic (the median
// write took 60 times longer at 2,000 notes than at 500). The sizes keep the
// test short under the race detector, which slows SQLite more than tenfold.
func TestUpsert_CostStaysFlatWithUnresolvedLinks(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 1,000 notes")
	}
	idx := openTest(t)
	const notes, window = 1000, 125
	next := 0
	write := func(k int) []time.Duration {
		out := make([]time.Duration, k)
		for j := range out {
			t0 := time.Now()
			if err := idx.Upsert(importDoc(next)); err != nil {
				t.Fatal(err)
			}
			out[j] = time.Since(t0)
			next++
		}
		return out
	}
	write(window) // warm up: the first writes grow the file
	early := medianOf(write(window))
	write(notes - 3*window)
	late := medianOf(write(window))
	// A machine busy elsewhere slows one window more than the other: a late
	// window that looks slow is measured again before failing.
	if late > 3*early {
		late = min(late, medianOf(write(window)))
	}
	t.Logf("median write: %v at %d notes, %v at %d", early, 2*window, late, next)
	if late > 3*early {
		t.Errorf("a write takes %v with %d notes in the index, %v with %d: its cost grows with the index", late, next, early, 2*window)
	}
}

// BenchmarkUpsert_UnresolvedLinks writes import notes into an index that
// already holds 1,000, 5,000 or 10,000 of them: ns/op stays the same.
func BenchmarkUpsert_UnresolvedLinks(b *testing.B) {
	for _, size := range []int{1000, 5000, 10000} {
		b.Run(fmt.Sprintf("notes=%d", size), func(b *testing.B) {
			idx, err := Open(filepath.Join(b.TempDir(), "bench.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer idx.Close()
			for i := range size {
				if err := idx.UpsertUnresolved(importDoc(i)); err != nil {
					b.Fatal(err)
				}
			}
			if err := idx.ResolveAll(); err != nil {
				b.Fatal(err)
			}
			for i := 0; b.Loop(); i++ {
				if err := idx.Upsert(importDoc(size + i)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// The queries a write runs for each link, and the one that finds the links
// a new note answers, are served by an index: none scans notes or links,
// which made the cost of a write grow with the vault (BUG-121).
func TestResolveQueriesUseIndexes(t *testing.T) {
	idx := openTest(t)
	scan := regexp.MustCompile(`\bSCAN (notes|links)\b`)
	keys := noteKeys("p/wiki/a0001_img/001.md", "001", "An image of the article")
	inboundArgs := make([]any, len(keys))
	for k, key := range keys {
		inboundArgs[k] = key
	}
	for _, q := range []struct {
		name, sql, index string
		args             []any
	}{
		{"path", qNoteByPath, "sqlite_autoindex_notes_1", []any{"p/a.md"}},
		{"title", qNoteByTitle, "notes_title_lower", []any{"a"}},
		{"name", qNoteByName, "notes_name", []any{"a.md", "%/a.md", "a.md"}},
		{"inbound", inboundQuery(len(keys)), "links_target_key", inboundArgs},
	} {
		rows, err := idx.db.Query(`EXPLAIN QUERY PLAN `+q.sql, q.args...)
		if err != nil {
			t.Fatalf("%s: %v", q.name, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		if p := strings.Join(plan, "; "); scan.MatchString(p) || !strings.Contains(p, "INDEX "+q.index+" ") {
			t.Errorf("%s query plan: %s, want a search through %s", q.name, p, q.index)
		}
	}
}

// A link can resolve to a note only if its key is one of the note's keys:
// resolveInbound looks up only those links, so every target resolveTarget
// sends to a note must have one of its keys, in all the ways a target
// matches (path, title, file name from any folder down; any case, a
// fragment, spaces, LIKE wildcards, a letter beyond ASCII).
func TestNoteKeys_CoverEveryTargetThatResolves(t *testing.T) {
	idx := openTest(t)
	notes := []struct{ path, body string }{
		{"Proj/Plans/2026-x.md", "---\ntitle: Ship The Thing\n---\n"},
		{"proj/docs/Über_50%.md", "# body"},
		{"proj/report.html", "<!--\n---\ntitle: Quarter Report\n---\n-->\n<p>r</p>"},
		{"proj/a/b/c.md", "# c"},
	}
	titles := map[string][]string{}
	for _, n := range notes {
		title := strings.TrimSuffix(filepath.Base(n.path), filepath.Ext(n.path))
		upsert(t, idx, n.path, title, n.body)
		var stored string
		if err := idx.db.QueryRow(`SELECT title FROM notes WHERE path = ?`, n.path).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		titles[n.path] = []string{title, stored}
	}
	targets := []string{
		"Proj/Plans/2026-x", "Proj/Plans/2026-x.md", "proj/plans/2026-x", "Plans/2026-X", "2026-x",
		"2026-x#Steps", " 2026-x # Steps ", "ship the thing", "SHIP THE THING#Goal",
		"Über_50%", "über_50%", "docs/ÜBER_50%", "proj/docs/Über_50%.md", "uber_50",
		"report", "Quarter Report", "proj/report.html", "proj/report",
		"c", "b/c", "a/b/c", "proj/a/b/c", "A/B/C#x", "x/b/c", "c.md",
	}
	resolved := 0
	for _, target := range targets {
		p := idx.Resolve(target)
		if p == "" {
			continue
		}
		resolved++
		if !slices.Contains(noteKeys(p, titles[p]...), targetKey(target)) {
			t.Errorf("[[%s]] resolves to %s, but its key %q is not among the note's keys %q",
				target, p, targetKey(target), noteKeys(p, titles[p]...))
		}
	}
	if resolved < 20 {
		t.Errorf("only %d of %d targets resolve: the cases are not exercised", resolved, len(targets))
	}
}

// Links written before their note resolve when it comes, in every form,
// and the links whose key the note does not have are not touched: the
// IN-folder file and the other note's name keep their NULL.
func TestUpsert_InboundByKey(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "p/src.md", "src", "[[P/IMG_001]] [[img_001#h]] [[Photo One]] [[p/IN/doc/img_001.png]] [[img_0011]] [[p/img_001.md]]")
	upsert(t, idx, "p/img_001.md", "img_001", "---\ntitle: Photo one\n---\n")
	outs, err := idx.Outlinks("p/src.md")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, o := range outs {
		got[o.Target] = o.TargetPath
	}
	want := map[string]string{
		"P/IMG_001": "p/img_001.md", "img_001#h": "p/img_001.md", "Photo One": "p/img_001.md", "p/img_001.md": "p/img_001.md",
		"p/IN/doc/img_001.png": "", "img_0011": "",
	}
	for target, w := range want {
		if got[target] != w {
			t.Errorf("[[%s]] resolved to %q, want %q", target, got[target], w)
		}
	}
}

// An index from v5 has no notes.name and no links.target_key: the migration
// adds them, fills them for the rows already there, so a note written next
// resolves the links that were waiting for it, and clears every hash.
func TestOpen_MigratesV5Index(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v5.db")
	idx, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	upsert(t, idx, "p/src.md", "src", "[[later]] and [[p/kept]]")
	upsert(t, idx, "p/kept.md", "kept", "# k")
	if _, err := idx.db.Exec(`DROP INDEX notes_name; DROP INDEX links_target_key;
		ALTER TABLE notes DROP COLUMN name; ALTER TABLE links DROP COLUMN target_key; PRAGMA user_version = 5`); err != nil {
		t.Fatal(err)
	}

	idx = reopen(t, idx, path)
	var v, missing int
	if err := idx.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != schemaVersion {
		t.Fatalf("user_version = %d (%v), want %d", v, err, schemaVersion)
	}
	if err := idx.db.QueryRow(`SELECT (SELECT COUNT(*) FROM notes WHERE name IS NULL) + (SELECT COUNT(*) FROM links WHERE target_key IS NULL)`).Scan(&missing); err != nil || missing != 0 {
		t.Fatalf("%d rows without their key after the migration (%v)", missing, err)
	}
	if s := stampOf(t, idx, "p/kept.md"); s.Hash != "" {
		t.Errorf("migrated note has a hash: %+v", s)
	}
	if p := idx.Resolve("KEPT"); p != "p/kept.md" {
		t.Errorf("[[KEPT]] resolves to %q after the migration, want p/kept.md", p)
	}
	upsert(t, idx, "p/later.md", "later", "# l")
	outs, _ := idx.Outlinks("p/src.md")
	sort.Slice(outs, func(a, b int) bool { return outs[a].Target < outs[b].Target })
	if len(outs) != 2 || outs[0].TargetPath != "p/later.md" || outs[1].TargetPath != "p/kept.md" {
		t.Errorf("outlinks after the migration = %+v", outs)
	}
}

// A release from before v6, after a rollback, writes rows without keys and
// leaves the hash that makes the boot scan skip them: the next start fills
// the keys.
func TestOpen_FillsKeysOfRowsWrittenWithout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollback.db")
	idx, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	upsert(t, idx, "p/src.md", "src", "[[later]]")
	upsert(t, idx, "p/old.md", "old", "# o")
	if _, err := idx.db.Exec(`UPDATE notes SET name = NULL; UPDATE links SET target_key = NULL`); err != nil {
		t.Fatal(err)
	}
	idx = reopen(t, idx, path)
	if p := idx.Resolve("OLD"); p != "p/old.md" {
		t.Errorf("[[OLD]] resolves to %q, want p/old.md", p)
	}
	upsert(t, idx, "p/later.md", "later", "# l")
	if outs, _ := idx.Outlinks("p/src.md"); len(outs) != 1 || outs[0].TargetPath != "p/later.md" {
		t.Errorf("outlinks = %+v, want [[later]] resolved", outs)
	}
}
