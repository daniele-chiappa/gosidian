package index

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExtractFields(t *testing.T) {
	meta := strings.Join([]string{
		"title: Payments v2",
		"description: Paylane v2: 3-D Secure, behind a flag", // unquoted colon: strict YAML rejects it
		"type: plan",
		"importance: 4",
		"updated: 2026-09-21",
		"claimed_at: 2026-09-21T10:30:00Z",
		"related_commits: [abc123, def456]",
		"implements_imp:",
		"  - IMP-095",
		"  - IMP-016",
		"harness:",
		"  cli: claude",
		"tags: [tidewater, type:plan, status:in-progress, topic:payments]",
	}, "\n")
	tags := []string{"tidewater", "type:plan", "status:in-progress", "topic:payments"}
	got := map[string][]string{}
	sources := map[string]string{}
	var nums, dates []string
	for _, f := range extractFields(meta, tags) {
		got[f.key] = append(got[f.key], f.value)
		sources[f.key] = f.source
		if f.num != nil {
			nums = append(nums, f.key)
		}
		if f.date != nil {
			dates = append(dates, f.key+"="+f.date.(string))
		}
	}
	want := map[string][]string{
		"title":           {"Payments v2"},
		"description":     {"Paylane v2: 3-D Secure, behind a flag"},
		"type":            {"plan"},
		"importance":      {"4"},
		"updated":         {"2026-09-21"},
		"claimed_at":      {"2026-09-21T10:30:00Z"},
		"related_commits": {"abc123", "def456"},
		"implements_imp":  {"IMP-095", "IMP-016"},
		"tags":            {"tidewater", "type:plan", "status:in-progress", "topic:payments"},
		"status":          {"in-progress"}, // from the tag: no status field
		"topic":           {"payments"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fields:\n got %v\nwant %v", got, want)
	}
	if sources["status"] != "tag" || sources["type"] != "field" || sources["implements_imp"] != "list" {
		t.Errorf("sources = %v", sources)
	}
	if !reflect.DeepEqual(nums, []string{"importance"}) {
		t.Errorf("numeric fields = %v", nums)
	}
	if !reflect.DeepEqual(dates, []string{"updated=2026-09-21", "claimed_at=2026-09-21T10:30:00Z"}) {
		t.Errorf("date fields = %v", dates)
	}
}

func TestExtractFields_FieldWinsOverTag(t *testing.T) {
	got := map[string][]string{}
	for _, f := range extractFields("status: done\ntags: [status:draft]", []string{"status:draft"}) {
		got[f.key] = append(got[f.key], f.value)
	}
	if !reflect.DeepEqual(got["status"], []string{"done"}) {
		t.Errorf("status = %v, want the field's value only", got["status"])
	}
}

// seedQuery builds a small vault: plans in two projects, a skill, a note
// with status only as a tag and one without any status.
func seedQuery(t *testing.T) *Index {
	t.Helper()
	idx := openTest(t)
	notes := []struct {
		path, body string
		mtime      int64
	}{
		{"alpha/plans/a.md", "---\ntitle: Plan A\ntype: plan\nstatus: draft\nimportance: 4\nupdated: 2026-09-01\nimplements_imp: [IMP-001]\ntags: [type:plan, status:draft]\n---\n\n# A\n", 10},
		{"alpha/plans/b.md", "---\ntitle: Plan B\ntype: plan\nstatus: in-progress\nimportance: 2\nupdated: 2026-09-20\ntags: [type:plan, status:in-progress]\n---\n\n# B\n", 30},
		{"alpha/plans/c.md", "---\ntitle: Plan C\ntype: plan\nupdated: 2026-08-15\ntags: [type:plan, status:draft, topic:search]\n---\n\n# C\n", 20},
		{"alpha/skills/deploy.md", "---\ntitle: Deploy\ntype: skill\ndescription: Deploy the stack to staging\nupdated: 2026-09-21T09:00:00Z\ntags: [type:skill]\n---\n\n# Deploy\n", 40},
		{"beta/plans/d.md", "---\ntitle: Plan D\ntype: plan\nstatus: draft\nimportance: 5\nupdated: 2026-09-25\ntags: [type:plan]\n---\n\n# D\n", 50},
		{"hidden/plans/e.md", "---\ntitle: Plan E\ntype: plan\nstatus: draft\ntags: [type:plan]\n---\n\n# E\n", 60},
	}
	for _, n := range notes {
		if err := idx.Upsert(NoteDoc{Path: n.path, Title: n.path, Body: n.body, ModTime: n.mtime, Size: int64(len(n.body))}); err != nil {
			t.Fatal(err)
		}
	}
	return idx
}

func queryPaths(t *testing.T, idx *Index, opts QueryOptions) ([]string, int) {
	t.Helper()
	hits, total, err := idx.Query(opts)
	if err != nil {
		t.Fatalf("query %+v: %v", opts, err)
	}
	var out []string
	for _, h := range hits {
		out = append(out, h.Path)
	}
	return out, total
}

func cond(field, op string, values ...string) FieldCond {
	return FieldCond{Field: field, Op: op, Values: values}
}

func TestQuery_Conditions(t *testing.T) {
	idx := seedQuery(t)
	scope := QueryOptions{Exclude: []string{"hidden"}, Sort: "path"}
	cases := []struct {
		name  string
		where []FieldCond
		want  []string
	}{
		{"status from field or tag", []FieldCond{cond("status", "eq", "draft")},
			[]string{"alpha/plans/a.md", "alpha/plans/c.md", "beta/plans/d.md"}},
		{"eq ignores case", []FieldCond{cond("type", "eq", "SKILL")}, []string{"alpha/skills/deploy.md"}},
		{"ne includes notes without the field", []FieldCond{cond("status", "ne", "draft")},
			[]string{"alpha/plans/b.md", "alpha/skills/deploy.md"}},
		{"in", []FieldCond{cond("status", "in", "in-progress", "done")}, []string{"alpha/plans/b.md"}},
		{"exists", []FieldCond{cond("importance", "exists")},
			[]string{"alpha/plans/a.md", "alpha/plans/b.md", "beta/plans/d.md"}},
		{"exists false", []FieldCond{cond("type", "eq", "plan"), cond("importance", "exists", "false")},
			[]string{"alpha/plans/c.md"}},
		{"number", []FieldCond{cond("importance", "gte", "4")}, []string{"alpha/plans/a.md", "beta/plans/d.md"}},
		{"date after", []FieldCond{cond("updated", "gt", "2026-09-19")},
			[]string{"alpha/plans/b.md", "alpha/skills/deploy.md", "beta/plans/d.md"}},
		{"bare day covers a datetime", []FieldCond{cond("updated", "lte", "2026-09-21")},
			[]string{"alpha/plans/a.md", "alpha/plans/b.md", "alpha/plans/c.md", "alpha/skills/deploy.md"}},
		{"list element", []FieldCond{cond("implements_imp", "eq", "IMP-001")}, []string{"alpha/plans/a.md"}},
		{"tags as a list", []FieldCond{cond("tags", "eq", "topic:search")}, []string{"alpha/plans/c.md"}},
		{"contains", []FieldCond{cond("description", "contains", "STAGING")}, []string{"alpha/skills/deploy.md"}},
		{"conditions combine", []FieldCond{cond("type", "eq", "plan"), cond("status", "eq", "draft"), cond("importance", "gte", "4")},
			[]string{"alpha/plans/a.md", "beta/plans/d.md"}},
	}
	for _, tc := range cases {
		opts := scope
		opts.Where = tc.where
		got, total := queryPaths(t, idx, opts)
		if !reflect.DeepEqual(got, tc.want) || total != len(tc.want) {
			t.Errorf("%s: got %v (total %d), want %v", tc.name, got, total, tc.want)
		}
	}
}

func TestQuery_ScopeSortLimitFields(t *testing.T) {
	idx := seedQuery(t)
	plans := []FieldCond{cond("type", "eq", "plan")}

	got, _ := queryPaths(t, idx, QueryOptions{Projects: []string{"alpha"}, Where: plans, Sort: "path"})
	if !reflect.DeepEqual(got, []string{"alpha/plans/a.md", "alpha/plans/b.md", "alpha/plans/c.md"}) {
		t.Errorf("project scope: %v", got)
	}
	if got, total := queryPaths(t, idx, QueryOptions{Projects: []string{}, Where: plans}); got != nil || total != 0 {
		t.Errorf("empty project list must match nothing: %v %d", got, total)
	}
	if got, _ := queryPaths(t, idx, QueryOptions{Exclude: []string{"hidden"}, Where: plans}); len(got) != 4 || got[0] != "beta/plans/d.md" {
		t.Errorf("default sort is newest modification first: %v", got)
	}

	// A field sort puts the notes without it last, in both directions.
	got, _ = queryPaths(t, idx, QueryOptions{Exclude: []string{"hidden"}, Where: plans, Sort: "importance", Desc: true})
	if !reflect.DeepEqual(got, []string{"beta/plans/d.md", "alpha/plans/a.md", "alpha/plans/b.md", "alpha/plans/c.md"}) {
		t.Errorf("importance desc: %v", got)
	}
	got, _ = queryPaths(t, idx, QueryOptions{Exclude: []string{"hidden"}, Where: plans, Sort: "importance"})
	if !reflect.DeepEqual(got, []string{"alpha/plans/b.md", "alpha/plans/a.md", "beta/plans/d.md", "alpha/plans/c.md"}) {
		t.Errorf("importance asc: %v", got)
	}
	got, _ = queryPaths(t, idx, QueryOptions{Exclude: []string{"hidden"}, Where: plans, Sort: "updated", Desc: true})
	if !reflect.DeepEqual(got, []string{"beta/plans/d.md", "alpha/plans/b.md", "alpha/plans/a.md", "alpha/plans/c.md"}) {
		t.Errorf("updated desc: %v", got)
	}

	hits, total, err := idx.Query(QueryOptions{Exclude: []string{"hidden"}, Where: plans, Sort: "updated", Desc: true,
		Limit: 2, Fields: []string{"status", "updated", "implements_imp", "missing"}})
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(hits) != 2 {
		t.Fatalf("limit: %d hits, total %d", len(hits), total)
	}
	if !reflect.DeepEqual(hits[0].Fields, map[string][]string{"status": {"draft"}, "updated": {"2026-09-25"}}) {
		t.Errorf("fields of d: %v", hits[0].Fields)
	}
	if hits[0].Lists != nil {
		t.Errorf("d has no list among the fields asked: %v", hits[0].Lists)
	}
	if hits[1].Title != "Plan B" || hits[1].ModTime != 30 {
		t.Errorf("hit b: %+v", hits[1])
	}
}

func TestQuery_ListsStayLists(t *testing.T) {
	idx := seedQuery(t)
	hits, _, err := idx.Query(QueryOptions{Where: []FieldCond{cond("implements_imp", "exists")}, Fields: []string{"implements_imp", "status"}})
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits %v, err %v", hits, err)
	}
	if !hits[0].Lists["implements_imp"] || hits[0].Lists["status"] || !reflect.DeepEqual(hits[0].Fields["implements_imp"], []string{"IMP-001"}) {
		t.Errorf("lists %v fields %v", hits[0].Lists, hits[0].Fields)
	}
}

func TestQuery_BadConditions(t *testing.T) {
	idx := seedQuery(t)
	for _, c := range []FieldCond{
		cond("status", "like", "d%"),
		cond("", "eq", "x"),
		cond("status", "eq"),
		cond("status", "eq", "a", "b"),
		cond("importance", "gt"),
		cond("status", "exists", "maybe"),
		cond("description", "contains", " "),
	} {
		if _, _, err := idx.Query(QueryOptions{Where: []FieldCond{c}}); !errors.Is(err, ErrBadQuery) {
			t.Errorf("%+v: err = %v, want ErrBadQuery", c, err)
		}
	}
	many := make([]FieldCond, MaxQueryConds+1)
	for k := range many {
		many[k] = cond("type", "exists")
	}
	if _, _, err := idx.Query(QueryOptions{Where: many}); !errors.Is(err, ErrBadQuery) {
		t.Errorf("too many conditions: %v", err)
	}
}

func TestQuery_FieldsFollowTheNote(t *testing.T) {
	idx := seedQuery(t)
	upsert(t, idx, "alpha/plans/a.md", "A", "---\ntype: plan\nstatus: done\n---\n\n# A\n")
	if got, _ := queryPaths(t, idx, QueryOptions{Where: []FieldCond{cond("status", "eq", "done")}}); !reflect.DeepEqual(got, []string{"alpha/plans/a.md"}) {
		t.Errorf("after update: %v", got)
	}
	if err := idx.Delete("alpha/plans/a.md"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := idx.db.QueryRow(`SELECT COUNT(*) FROM note_fields f LEFT JOIN notes n ON n.id = f.note_id WHERE n.id IS NULL`).Scan(&n); err != nil || n != 0 {
		t.Errorf("orphan field rows after delete: %d (%v)", n, err)
	}
}

func TestOpen_MigratesV1Index(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	idx, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.db.Exec(`DROP TABLE note_fields; PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	idx.Close()

	idx, err = Open(path)
	if err != nil {
		t.Fatalf("open v1 index: %v", err)
	}
	defer idx.Close()
	var v int
	if err := idx.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != schemaVersion {
		t.Fatalf("user_version = %d (%v), want %d", v, err, schemaVersion)
	}
	upsert(t, idx, "p/a.md", "A", "---\nstatus: done\n---\n\nbody")
	if got, _ := queryPaths(t, idx, QueryOptions{Where: []FieldCond{cond("status", "eq", "done")}}); len(got) != 1 {
		t.Errorf("query after migration: %v", got)
	}
	var tbl sql.NullString
	_ = idx.db.QueryRow(`SELECT name FROM sqlite_master WHERE name = 'note_fields'`).Scan(&tbl)
	if tbl.String != "note_fields" {
		t.Error("note_fields missing after migration")
	}
}
