package index

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Projects names the top-level folders that hold a note, each once and in
// name order, whatever the paths around them: a folder whose name is
// another's with more after it, a note at the root named as a folder, a
// path that starts with a slash.
func TestProjects_NamesEachFolderOnceInOrder(t *testing.T) {
	idx := openTest(t)
	for _, p := range []string{
		"a/x.md", "a/b/c.md", "a-b/x.md", "a.md", "a0/x.md", "ab/x.md",
		"README.md", "/abs.md", "Z/x.md", "é/x.md", "a b/x.md", "a%/x.md", "a_/x.md",
	} {
		upsert(t, idx, p, p, "body")
	}
	got, err := idx.Projects()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Z", "a", "a b", "a%", "a-b", "a0", "a_", "ab", "é"}
	if !slices.Equal(got, want) {
		t.Errorf("Projects() = %q, want %q", got, want)
	}
}

func TestProjects_Empty(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "root.md", "Root", "body")
	got, err := idx.Projects()
	if err != nil || len(got) != 0 {
		t.Errorf("Projects() = %q, %v; want none", got, err)
	}
}

// A scope and a folder match a path without case, as LIKE does, and read a
// % or a _ in a name as itself: the index on the paths changes how fast
// their notes are found, not which.
func TestScopeAndFolder_MatchWithoutCase(t *testing.T) {
	idx := openTest(t)
	for _, p := range []string{
		"proj/a.md", "Proj/b.md", "proj/sub/c.md", "PROJ/Sub/d.md", "proj_x/e.md", "projx/f.md",
		"pro%/g.md", "proj-x/h.md", "proj.md", "other/i.md",
	} {
		upsert(t, idx, p, p, "body")
	}
	under := []string{"PROJ/Sub/d.md", "Proj/b.md", "proj/a.md", "proj/sub/c.md"}
	for _, c := range []struct {
		name string
		o    QueryOptions
		want []string
	}{
		{"a project", QueryOptions{Projects: []string{"proj"}}, under},
		{"a project with %", QueryOptions{Projects: []string{"pro%"}}, []string{"pro%/g.md"}},
		{"a project with _", QueryOptions{Projects: []string{"proj_x"}}, []string{"proj_x/e.md"}},
		{"two projects", QueryOptions{Projects: []string{"other", "projx"}}, []string{"other/i.md", "projx/f.md"}},
		{"a folder", QueryOptions{Folders: []string{"proj/sub"}}, []string{"PROJ/Sub/d.md", "proj/sub/c.md"}},
		{"a folder and below", QueryOptions{Folders: []string{"proj/**"}}, under},
		{"a folder in a scope", QueryOptions{Projects: []string{"other", "proj"}, Folders: []string{"PROJ"}}, []string{"Proj/b.md", "proj/a.md"}},
		{"a project left out", QueryOptions{Exclude: []string{"proj"}}, []string{"other/i.md", "pro%/g.md", "proj-x/h.md", "proj.md", "proj_x/e.md", "projx/f.md"}},
	} {
		got, total := queryPaths(t, idx, c.o)
		slices.Sort(got)
		if !slices.Equal(got, c.want) || total != len(c.want) {
			t.Errorf("%s: got %q (total %d), want %q", c.name, got, total, c.want)
		}
	}
	recent, err := idx.RecentNotesIn([]string{"proj"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range recent {
		got = append(got, n.Path)
	}
	slices.Sort(got)
	if !slices.Equal(got, under) {
		t.Errorf("RecentNotesIn(proj) = %q, want %q", got, under)
	}
}

// The scope of a reader and the folder of a view are looked up in the index
// on the paths, as the queries of a project are: none reads every note.
func TestScopeQueriesUseThePathIndex(t *testing.T) {
	idx := openTest(t)
	scope, args := scopeClause([]string{"a", "b", "c"}, nil)
	in := likeUnder("a/plans")
	for _, q := range []struct {
		name, sql string
		args      []any
	}{
		{"scope", `SELECT COUNT(*) FROM notes n WHERE 1=1` + scope, args},
		{"folder", `SELECT COUNT(*) FROM notes n WHERE 1=1 AND (n.path LIKE ? ESCAPE '\' AND n.path NOT LIKE ? ESCAPE '\')`, []any{in, in + "/%"}},
		{"scope and folder", `SELECT COUNT(*) FROM notes n WHERE 1=1` + scope + ` AND (n.path LIKE ? ESCAPE '\' AND n.path NOT LIKE ? ESCAPE '\')`, append(slices.Clone(args), in, in+"/%")},
		{"recent", `SELECT n.path, n.title, n.mtime FROM notes n WHERE 1=1` + scope + ` ORDER BY n.mtime DESC LIMIT 50`, args},
		{"project", `SELECT path, title, mtime FROM notes WHERE path LIKE ? ESCAPE '\' AND mtime >= ? ORDER BY mtime DESC LIMIT 50`, []any{likeUnder("a"), 0}},
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
		if p := strings.Join(plan, "; "); strings.Contains(p, "SCAN n") || !strings.Contains(p, "INDEX notes_path_nocase ") {
			t.Errorf("%s query plan: %s, want a search through notes_path_nocase", q.name, p)
		}
	}
}

// scopeIndex fills an index with notes rows only, the way a large vault
// spreads them: most in one project, the rest over many small ones, a few
// at the root.
func scopeIndex(b *testing.B, notes, projects int) *Index {
	b.Helper()
	idx, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { idx.Close() })
	tx, err := idx.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	big := notes * 85 / 100
	for k := range notes {
		p := "p00"
		if k >= big {
			p = fmt.Sprintf("p%02d", 1+k%(projects-1))
		}
		path := fmt.Sprintf("%s/f%02d/n%05d.md", p, k%20, k)
		if _, err := tx.Exec(`INSERT INTO notes(path, title, mtime, size) VALUES (?, ?, 1, 1)`, path, path); err != nil {
			b.Fatal(err)
		}
	}
	for k := range 5 {
		if _, err := tx.Exec(`INSERT INTO notes(path, title, mtime, size) VALUES (?, ?, 1, 1)`, fmt.Sprintf("root%d.md", k), "root"); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	return idx
}

// BenchmarkProjects lists the projects of a vault of 30,000 notes, as every
// search and view of a reader who is not the owner does first.
func BenchmarkProjects(b *testing.B) {
	idx := scopeIndex(b, 30000, 30)
	for b.Loop() {
		if _, err := idx.Projects(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkQuery_ScopedFolderNoMatch runs the query of a view whose folder
// holds no note: for the owner (no scope) and for a reader who sees all but
// one of the 30 projects.
func BenchmarkQuery_ScopedFolderNoMatch(b *testing.B) {
	idx := scopeIndex(b, 30000, 30)
	projs, err := idx.Projects()
	if err != nil {
		b.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		scope []string
	}{{"owner", nil}, {"member", projs[1:]}} {
		b.Run(c.name, func(b *testing.B) {
			for b.Loop() {
				if _, _, err := idx.Query(QueryOptions{Projects: c.scope, Folders: []string{"x"}}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Notes written at the same time come out in path order, whichever index
// the query reads them through: an import writes thousands at once.
func TestRecentAndStale_TiesInPathOrder(t *testing.T) {
	idx := openTest(t)
	for _, p := range []string{"p/c.md", "P/b.md", "p/a.md"} {
		upsert(t, idx, p, p, "body")
	}
	want := []string{"P/b.md", "p/a.md", "p/c.md"}
	paths := func(rows []RecentNote) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.Path)
		}
		return out
	}
	all, err := idx.RecentNotes("", 0, 10)
	if err != nil || !slices.Equal(paths(all), want) {
		t.Errorf("RecentNotes() = %q, %v; want %q", paths(all), err, want)
	}
	inP, err := idx.RecentNotes("p", 0, 10)
	if err != nil || !slices.Equal(paths(inP), want) {
		t.Errorf("RecentNotes(p) = %q, %v; want %q", paths(inP), err, want)
	}
	in, err := idx.RecentNotesIn([]string{"p"}, 10)
	if err != nil || !slices.Equal(paths(in), want) {
		t.Errorf("RecentNotesIn(p) = %q, %v; want %q", paths(in), err, want)
	}
	stale, err := idx.StaleNotes("p", 2, 10, false)
	var got []string
	for _, s := range stale {
		got = append(got, s.Path)
	}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("StaleNotes(p) = %q, %v; want %q", got, err, want)
	}
}
