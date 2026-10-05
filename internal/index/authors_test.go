package index

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func authorsIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	for p, body := range map[string]string{
		"p/a.md":           "---\ntitle: A\nstatus: open\n---\n",
		"p/b.md":           "---\ntitle: B\n---\n",
		"p/handoffs/h.md":  "---\ntitle: H\ncreated_by: claude-cli@1234\n---\n",
		"q/docs/report.md": "---\ntitle: Report\n---\n",
	} {
		if err := idx.Upsert(NoteDoc{Path: p, Title: strings.TrimSuffix(filepath.Base(p), ".md"), Body: body, ModTime: 1, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
	}
	return idx
}

func authorFields(t *testing.T, idx *Index, path string) (string, string) {
	t.Helper()
	hits, _, err := idx.Query(QueryOptions{Paths: []string{path}, Fields: []string{CreatedByField, ModifiedByField}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		return "-", "-"
	}
	return strings.Join(hits[0].Fields[CreatedByField], ","), strings.Join(hits[0].Fields[ModifiedByField], ",")
}

// The audit replayed at start gives each note its creator and last
// modifier, as fields queries read, filter and sort on; a frontmatter field
// of that name wins (a handoff's own created_by).
func TestAuthors_Rebuild(t *testing.T) {
	idx := authorsIndex(t)
	t0 := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	ev := func(min int, action, path, to, by string) AuthorEvent {
		return AuthorEvent{TS: t0.Add(time.Duration(min) * time.Minute), Action: action, Path: path, To: to, By: by}
	}
	err := idx.RebuildAuthors([]AuthorEvent{
		ev(0, AuthorCreate, "p/a.md", "", "claude-cli (admin)"),
		ev(1, AuthorUpdate, "p/a.md", "", "daniele"),
		ev(2, AuthorAppend, "p/b.md", "", "odoo-agent (admin)"), // no create seen: no creator
		ev(3, AuthorCreate, "p/handoffs/h.md", "", "claude-cli (admin)"),
		ev(4, AuthorCreate, "p/gone.md", "", "x"),
		ev(5, AuthorDelete, "p/gone.md", "", "x"),
		ev(6, AuthorCreate, "q/docs/old.md", "", "daniele"),
		ev(7, AuthorRename, "q/docs/old.md", "q/docs/report.md", "claude-cli (admin)"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"p/a.md":           "claude-cli (admin) | daniele",
		"p/b.md":           " | odoo-agent (admin)",
		"p/handoffs/h.md":  "claude-cli@1234 | claude-cli (admin)",
		"q/docs/report.md": "daniele | claude-cli (admin)",
	} {
		if c, m := authorFields(t, idx, path); c+" | "+m != want {
			t.Errorf("%s: %s | %s, want %s", path, c, m, want)
		}
	}
	if a, ok, _ := idx.AuthorOf("p/gone.md"); ok {
		t.Errorf("a deleted note keeps no author: %+v", a)
	}
	if a, ok, _ := idx.AuthorOf("p/a.md"); !ok || a.ModifiedAt != "2026-10-04T09:01:00Z" || a.CreatedAt != "2026-10-04T09:00:00Z" {
		t.Errorf("times: %+v", a)
	}
	// The times are fields too, dates that compare as such.
	hits, _, err := idx.Query(QueryOptions{Where: []FieldCond{{Field: ModifiedAtField, Op: OpGte, Values: []string{"2026-10-04T09:05"}}}, Sort: ModifiedAtField, Fields: []string{ModifiedAtField, CreatedAtField}})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Path != "q/docs/report.md" || hits[0].Fields[ModifiedAtField][0] != "2026-10-04T09:07:00Z" || hits[0].Fields[CreatedAtField][0] != "2026-10-04T09:06:00Z" {
		t.Errorf("modified_at >= 09:05: %+v", hits)
	}
	// As fields of a query: a condition and a sort.
	hits, _, err = idx.Query(QueryOptions{Where: []FieldCond{{Field: ModifiedByField, Op: OpContains, Values: []string{"claude-cli"}}}, Sort: "path", Fields: []string{ModifiedByField}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range hits {
		got = append(got, h.Path)
	}
	if strings.Join(got, ",") != "p/handoffs/h.md,q/docs/report.md" {
		t.Errorf("modified_by contains claude-cli: %v", got)
	}
	// A re-index of the note keeps them.
	body := "---\ntitle: A\nstatus: done\n---\n"
	if err := idx.Upsert(NoteDoc{Path: "p/a.md", Title: "A", Body: body, ModTime: 2, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if c, m := authorFields(t, idx, "p/a.md"); c != "claude-cli (admin)" || m != "daniele" {
		t.Errorf("after a re-index: %s | %s", c, m)
	}
}

// Writes recorded one by one as the audit log takes them, project actions
// included.
func TestAuthors_Record(t *testing.T) {
	idx := authorsIndex(t)
	now := time.Now()
	rec := func(action, path, to, by string) {
		t.Helper()
		if err := idx.RecordAuthor(AuthorEvent{TS: now, Action: action, Path: path, To: to, By: by}); err != nil {
			t.Fatal(err)
		}
	}
	rec(AuthorCreate, "p/a.md", "", "alice")
	rec(AuthorUpdate, "p/a.md", "", "bob")
	if c, m := authorFields(t, idx, "p/a.md"); c != "alice" || m != "bob" {
		t.Errorf("a: %s | %s", c, m)
	}
	rec(AuthorCreate, "q/docs/report.md", "", "carol")
	rec(AuthorRenameProject, "q", "r", "dave")
	if _, ok, _ := idx.AuthorOf("q/docs/report.md"); ok {
		t.Error("the old path keeps no row after a project rename")
	}
	if a, ok, _ := idx.AuthorOf("r/docs/report.md"); !ok || a.CreatedBy != "carol" {
		t.Errorf("project rename: %+v %v", a, ok)
	}
	rec(AuthorDeleteProject, "r", "", "dave")
	if _, ok, _ := idx.AuthorOf("r/docs/report.md"); ok {
		t.Error("project delete keeps a row")
	}
	rec(AuthorDelete, "p/a.md", "", "bob")
	if c, m := authorFields(t, idx, "p/a.md"); c != "" || m != "" {
		t.Errorf("deleted: %s | %s", c, m)
	}
	rec("upload_attachment", "p/attachments/x.png", "", "bob") // ignored
}
