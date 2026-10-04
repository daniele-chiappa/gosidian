package index

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

// seedRelations: a target note and rows that reach it from a frontmatter
// field, from the body, from another field, or not at all.
func seedRelations(t *testing.T) *Index {
	t.Helper()
	idx := openTest(t)
	upsert(t, idx, "p/docs/BUG-1.md", "BUG-1", "---\ntitle: BUG-1\n---\n\nThe bug.\n")
	upsert(t, idx, "p/plans/r1.md", "r1", "---\ntitle: r1\nrelated: [\"[[BUG-1]]\"]\n---\n\nBody.\n")
	upsert(t, idx, "p/plans/r2.md", "r2", "---\ntitle: r2\n---\n\nFixes [[p/docs/BUG-1]].\n")
	upsert(t, idx, "p/plans/r3.md", "r3", "---\ntitle: r3\norigin: \"[[p/docs/BUG-1|the bug]]\"\nrelated: [\"[[p/docs/BUG-1]]\"]\n---\n\nAnd [[BUG-1]].\n")
	upsert(t, idx, "p/plans/r4.md", "r4", "---\ntitle: r4\nrelated: plain text\n---\n\nNothing.\n")
	return idx
}

func TestRelations_BacklinksAndOutlinks(t *testing.T) {
	idx := seedRelations(t)
	bl, err := idx.Backlinks("p/docs/BUG-1.md")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, b := range bl {
		got[b.Path] = b.Fields
	}
	want := map[string][]string{"p/plans/r1.md": {"related"}, "p/plans/r2.md": nil, "p/plans/r3.md": {"origin", "related"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("backlinks = %v, want %v", got, want)
	}
	outs, err := idx.Outlinks("p/plans/r3.md")
	if err != nil {
		t.Fatal(err)
	}
	var fields []string
	for _, o := range outs {
		if o.TargetPath != "p/docs/BUG-1.md" {
			t.Errorf("outlink %+v does not resolve", o)
		}
		fields = append(fields, o.Field)
	}
	if !reflect.DeepEqual(fields, []string{"origin", "related", ""}) {
		t.Errorf("outlink fields = %v: the frontmatter's first, then the body's", fields)
	}
	// A rename keeps them: the old path's links resolve again, to nothing.
	if err := idx.Delete("p/docs/BUG-1.md"); err != nil {
		t.Fatal(err)
	}
	if bl, _ := idx.Backlinks("p/docs/BUG-1.md"); len(bl) != 0 {
		t.Errorf("backlinks of a deleted note: %v", bl)
	}
}

func TestRelations_LinkConds(t *testing.T) {
	idx := seedRelations(t)
	resolve := idx.Resolve
	q := func(where ...FieldCond) []string {
		t.Helper()
		where, err := ResolveLinkConds(where, resolve)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := queryPaths(t, idx, QueryOptions{Folders: []string{"p/plans"}, Where: where, Sort: "path"})
		return got
	}
	for name, c := range map[string]struct {
		where []FieldCond
		want  []string
	}{
		"field contains a link":      {[]FieldCond{cond("related", "contains", "[[p/docs/BUG-1]]")}, []string{"p/plans/r1.md", "p/plans/r3.md"}},
		"written another way":        {[]FieldCond{cond("related", "eq", "[[BUG-1|alias]]")}, []string{"p/plans/r1.md", "p/plans/r3.md"}},
		"another field":              {[]FieldCond{cond("origin", "contains", "[[BUG-1]]")}, []string{"p/plans/r3.md"}},
		"links: from anywhere":       {[]FieldCond{cond("links", "contains", "[[BUG-1]]")}, []string{"p/plans/r1.md", "p/plans/r2.md", "p/plans/r3.md"}},
		"links takes a path":         {[]FieldCond{cond("links", "eq", "p/docs/BUG-1.md")}, []string{"p/plans/r1.md", "p/plans/r2.md", "p/plans/r3.md"}},
		"ne: no link from the field": {[]FieldCond{cond("related", "ne", "[[BUG-1]]")}, []string{"p/plans/r2.md", "p/plans/r4.md"}},
		"text conditions unchanged":  {[]FieldCond{cond("related", "eq", "plain text")}, []string{"p/plans/r4.md"}},
	} {
		if got := q(c.where...); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
	for name, where := range map[string][]FieldCond{
		"no such note": {cond("related", "contains", "[[p/none]]")},
		"bad op":       {cond("links", "gt", "[[BUG-1]]")},
		"mixed values": {cond("related", "in", "[[BUG-1]]", "text")},
		"links alone":  {{Field: "links", Op: "contains"}},
	} {
		if _, err := ResolveLinkConds(where, resolve); !errors.Is(err, ErrBadQuery) {
			t.Errorf("%s: err = %v, want ErrBadQuery", name, err)
		}
	}
	// The pseudo-field has no values to show by default; a relation does.
	where, _ := ResolveLinkConds([]FieldCond{cond("links", "contains", "[[BUG-1]]"), cond("related", "contains", "[[BUG-1]]")}, resolve)
	if f := DefaultQueryFields(where, ""); !reflect.DeepEqual(f, []string{"related"}) {
		t.Errorf("DefaultQueryFields = %v", f)
	}
}

// An index from before links.field gains the column, and the boot scan
// re-extracts every note (the hashes are cleared).
func TestOpen_MigratesV3Index(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3.db")
	idx, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	upsert(t, idx, "p/a.md", "A", "---\nrelated: \"[[p/b]]\"\n---\n")
	if _, err := idx.db.Exec(`DROP TABLE links; CREATE TABLE links (src_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
		target TEXT NOT NULL, target_path TEXT, alias TEXT); PRAGMA user_version = 3`); err != nil {
		t.Fatal(err)
	}
	idx.Close()
	idx, err = Open(path)
	if err != nil {
		t.Fatalf("open v3 index: %v", err)
	}
	defer idx.Close()
	var hash sql.NullString
	if err := idx.db.QueryRow(`SELECT hash FROM notes WHERE path = 'p/a.md'`).Scan(&hash); err != nil || hash.Valid {
		t.Errorf("hash after migration = %v (%v), want NULL", hash, err)
	}
	upsert(t, idx, "p/a.md", "A", "---\nrelated: \"[[p/b]]\"\n---\n")
	var field string
	if err := idx.db.QueryRow(`SELECT field FROM links`).Scan(&field); err != nil || field != "related" {
		t.Errorf("links.field = %q (%v)", field, err)
	}
}
