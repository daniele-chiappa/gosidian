package index

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// extractionDigests pins, for each ContentVersion, the digest of the rows an
// upsert stores for goldenCorpus. When the parser or the extraction change
// what gets stored, TestContentVersion_Golden fails: bump ContentVersion and
// add its digest here (keep the old ones, they document what changed when).
var extractionDigests = map[int]string{
	1: "0e5de1601a3a29c6a327b48a787955b77441b9fc022868d9426e5ebf0086cf1a",
	// 2: a value is unquoted only when it opens and closes with the same
	// quote (BUG-089); the corpus gained such a value.
	2: "7950a173024cbf8b1b8f01c3e9f9e17312bd57aab9f865034018abc34825d2b6",
	// 3: the note title is unquoted the same way (BUG-089); the corpus
	// gained a note whose title ends with a quoted phrase.
	3: "0644b06a40dd8b5dda7504f0c024af4dbc1eb24edc364fbb7d5161dc7cacea59",
	// 4: one frontmatter reader, YAML with the line reader as a fallback
	// (IMP-138 P3); the corpus gained a note with the cases it reads its own
	// way.
	4: "068ae4ad97d624b328d22c54be740248378c98e48e7c0e34f205ab14ea627b1a",
	// 5: the wikilinks of the frontmatter's values are links, with their
	// field (IMP-127 iteration 2); the corpus gained a note with relations.
	// The body's links dump as before, so without the fmlink rows the
	// digest is still the one of 4.
	5: "e53f97b421217f141c235fb8f684764e173dcebe2727fb3ad2b39f72fba18717",
	// 6: a hex color code is not a tag (IMP-145); the corpus gained a note
	// with colors and with tags of their look that are not colors.
	6: "ca9060ccb40ea22719d12d265005a2a8b13b1b21d8f6ee7bdeb3b836d64066d3",
	// 7: a color followed by a slash is a color too (#FAFAFA/#EFEFEF,
	// IMP-145); the corpus gained such a list.
	7: "3637b94e7932d7766cf23201dcd41e0755559311fb6e5f2bc594b54d75538378",
}

// goldenCorpus covers every extracted row kind: title from frontmatter and
// from H1, tags inline and in frontmatter, namespaced tags, importance, list
// and scalar fields, dates and numbers, a field and a title ending with a
// quoted phrase (BUG-089), wikilinks with alias, fragment, same-note anchor
// and embed, an HTML note, and the cases the YAML reader reads its own way
// (IMP-138): an unquoted [[wikilink]], a comma inside a quoted list item, a
// trailing comment, a block scalar, a number kept as text; and relations,
// links in frontmatter values (a list with an alias, a link in inline code
// and one in a nested map, which are not links); and hex colors, which are
// not tags (IMP-145).
var goldenCorpus = []NoteDoc{
	{Path: "p/plan.md", Title: "plan", Body: "---\ntitle: The plan\ntags: [p, type:plan, status:draft]\nimportance: 4\nupdated: 2026-09-28\nimplements_imp:\n  - IMP-104\n  - IMP-099\nratio: 0.5\nsubtitle: Sync v1.12 \"Agent workflow\"\n---\n\n# Heading\n\nSee [[p/bugs#BUG-065|the bug]], [[#Heading]], [[Other Note]] and ![[img.webp]].\n\nInline #topic/sub and #todo.\n"},
	{Path: "p/bugs.md", Title: "bugs", Body: "# Bug tracker\n\n## BUG-065\n\nNo frontmatter, [[p/plan]] back.\n"},
	{Path: "p/sync.md", Title: "sync", Body: "---\ntitle: Sync v1.12 \"Agent workflow\"\n---\n\nBody.\n"},
	{Path: "p/reader.md", Title: "reader", Body: "---\ntitle: Reader cases\ntags: [p, type:doc]\nrelated: [[p/plan]]\naliases: [\"a, b\", c]\nstatus: open # a comment\nsummary: |\n  two\n  lines\ncode: 007\n---\n\nBody.\n"},
	{Path: "p/relations.md", Title: "relations", Body: "---\ntitle: Relations\ntags: [p, type:doc]\nrelated: [\"[[p/plan|The plan]]\", \"[[p/bugs#BUG-065]]\"]\norigin: \"[[p/sync]]\"\ndescription: \"a `[[p/plan]]` sample\"\nharness:\n  link: \"[[p/reader]]\"\n---\n\nBody with [[p/plan]].\n"},
	{Path: "p/brand.md", Title: "brand", Body: "# Brand\n\nRed #AC1F24, white #fff, #topic/brand and #cafebabe1; palette #FAFAFA/#EFEFEF.\n"},
	{Path: "p/report.html", Title: "report", Body: "<!--\n---\ntitle: Report\ntags: [p, type:doc]\n---\n-->\n<html><body><h1>Report</h1><p>Links <a href=\"x\">out</a> and [[p/plan]].</p></body></html>\n"},
}

func TestContentVersion_Golden(t *testing.T) {
	idx := openTest(t)
	for _, d := range goldenCorpus {
		d.ModTime, d.Size = 1, int64(len(d.Body))
		if err := idx.UpsertUnresolved(d); err != nil {
			t.Fatalf("upsert %s: %v", d.Path, err)
		}
	}
	got := extractionDigest(t, idx.db)
	want, ok := extractionDigests[ContentVersion]
	if !ok || got != want {
		t.Fatalf("the rows extracted from goldenCorpus changed (digest %s, pinned for ContentVersion %d: %q).\n"+
			"If the change is intended, bump index.ContentVersion to %d and add %d: %q to extractionDigests,\n"+
			"so that existing indexes re-extract every note at the next start.",
			got, ContentVersion, want, ContentVersion+1, ContentVersion+1, got)
	}
}

// extractionDigest hashes every row derived from the notes' content, in a
// stable order. Resolution (links.target_path) is left out on purpose: the
// boot scan re-resolves every link at each start.
func extractionDigest(t *testing.T, db *sql.DB) string {
	t.Helper()
	var b strings.Builder
	dump := func(label, q string) {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]sql.NullString, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			b.WriteString(label)
			for _, v := range vals {
				fmt.Fprintf(&b, "\x1f%v:%s", v.Valid, v.String)
			}
			b.WriteByte('\n')
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
	}
	dump("note", `SELECT path, title, importance FROM notes ORDER BY path`)
	dump("link", `SELECT n.path, l.target, l.alias FROM links l JOIN notes n ON n.id = l.src_id WHERE l.field IS NULL ORDER BY n.path, l.target, l.alias`)
	dump("fmlink", `SELECT n.path, l.field, l.target, l.alias FROM links l JOIN notes n ON n.id = l.src_id WHERE l.field IS NOT NULL ORDER BY n.path, l.field, l.target, l.alias`)
	dump("tag", `SELECT n.path, t.tag FROM tags t JOIN notes n ON n.id = t.note_id ORDER BY n.path, t.tag`)
	dump("field", `SELECT n.path, f.key, f.value, f.num, f.date, f.source FROM note_fields f JOIN notes n ON n.id = f.note_id ORDER BY n.path, f.key, f.value, f.source`)
	dump("fts", `SELECT n.path, f.title, f.meta, f.body FROM notes_fts f JOIN notes n ON n.id = f.rowid ORDER BY n.path`)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
