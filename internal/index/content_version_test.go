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
}

// goldenCorpus covers every extracted row kind: title from frontmatter and
// from H1, tags inline and in frontmatter, namespaced tags, importance, list
// and scalar fields, dates and numbers, wikilinks with alias, fragment,
// same-note anchor and embed, and an HTML note.
var goldenCorpus = []NoteDoc{
	{Path: "p/plan.md", Title: "plan", Body: "---\ntitle: The plan\ntags: [p, type:plan, status:draft]\nimportance: 4\nupdated: 2026-09-28\nimplements_imp:\n  - IMP-104\n  - IMP-099\nratio: 0.5\n---\n\n# Heading\n\nSee [[p/bugs#BUG-065|the bug]], [[#Heading]], [[Other Note]] and ![[img.webp]].\n\nInline #topic/sub and #todo.\n"},
	{Path: "p/bugs.md", Title: "bugs", Body: "# Bug tracker\n\n## BUG-065\n\nNo frontmatter, [[p/plan]] back.\n"},
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
	dump("link", `SELECT n.path, l.target, l.alias FROM links l JOIN notes n ON n.id = l.src_id ORDER BY n.path, l.target, l.alias`)
	dump("tag", `SELECT n.path, t.tag FROM tags t JOIN notes n ON n.id = t.note_id ORDER BY n.path, t.tag`)
	dump("field", `SELECT n.path, f.key, f.value, f.num, f.date, f.source FROM note_fields f JOIN notes n ON n.id = f.note_id ORDER BY n.path, f.key, f.value, f.source`)
	dump("fts", `SELECT n.path, f.title, f.meta, f.body FROM notes_fts f JOIN notes n ON n.id = f.rowid ORDER BY n.path`)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
