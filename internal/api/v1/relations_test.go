package v1

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/index"
)

// Relations in the REST API (IMP-127 iteration 2): the row views of a row
// (GET .../row-views, IMP-139), the fields of a backlink, and link
// conditions in POST /query.
func TestRelations_REST(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "p/docs/tasks.md", strings.Replace(tasksDatabase, "tags: [p, type:index]\n",
		"row_views:\n  - title: Notes about it\n    from: p/notes\n    where: [about contains this]\n    columns: [title, about]\n"+
			"  - title: Nothing\n    from: p/notes\n    where: [about contains this, title = none]\ntags: [p, type:index]\n", 1))
	f.seedNote(t, "p/docs/tasks/T-1.md", taskRow)
	f.seedNote(t, "p/notes/a.md", "---\ntitle: A\nabout: \"[[T-1]]\"\ntags: [p]\n---\n")
	f.seedNote(t, "p/notes/b.md", "---\ntitle: B\ntags: [p]\n---\n\nSee [[p/docs/tasks/T-1]].\n")

	rec := f.request(http.MethodGet, "/api/v1/notes/p/docs/tasks/T-1.md/row-views", "", map[string]string{"Authorization": "Bearer " + f.bearer})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET row-views = %d %s", rec.Code, rec.Body.String())
	}
	var rv rowViewsResponse
	if err := json.NewDecoder(rec.Body).Decode(&rv); err != nil {
		t.Fatal(err)
	}
	if rv.Database != "p/docs/tasks.md" || len(rv.Views) != 2 || rv.Views[0].Title != "Notes about it" ||
		len(rv.Views[0].View.Rows) != 1 || rv.Views[0].View.Rows[0].Path != "p/notes/a.md" || rv.Views[1].View.Total != 0 {
		t.Errorf("row views = %+v", rv)
	}
	if links := rv.Views[0].View.Rows[0].Links["about"]; len(links) != 1 || links[0].Path != "p/docs/tasks/T-1.md" {
		t.Errorf("the relation's link resolves: %+v", rv.Views[0].View.Rows[0].Links)
	}
	rec = f.request(http.MethodGet, "/api/v1/notes/p/notes/a.md/row-views", "", map[string]string{"Authorization": "Bearer " + f.bearer})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"views":[]`) {
		t.Errorf("not a row: %d %s", rec.Code, rec.Body.String())
	}

	r := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/p/docs/tasks/T-1.md/backlinks", "", nil)
	if r.code != http.StatusOK || !strings.Contains(r.body, `{"path":"p/notes/a.md","title":"A","fields":["about"]}`) ||
		!strings.Contains(r.body, `{"path":"p/notes/b.md","title":"B"}`) {
		t.Errorf("backlinks = %d %s", r.code, r.body)
	}

	r = f.doAuthRecorder(http.MethodPost, "/api/v1/query", `{"where":[{"field":"links","op":"contains","value":"[[T-1]]"}],"sort":"path"}`, nil)
	if r.code != http.StatusOK || !strings.Contains(r.body, "p/notes/a.md") || !strings.Contains(r.body, "p/notes/b.md") {
		t.Errorf("query links = %d %s", r.code, r.body)
	}
	r = f.doAuthRecorder(http.MethodPost, "/api/v1/query", `{"where":[{"field":"about","op":"contains","value":"[[p/none]]"}]}`, nil)
	if r.code != http.StatusBadRequest || !strings.Contains(r.body, "names no note") {
		t.Errorf("a link to no note = %d %s", r.code, r.body)
	}
}

// A wikilink to a note's file name resolves in the preview as in the index,
// whose backlinks count it, even when the title differs (BUG-090).
func TestPreview_ResolvesFileName(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "p/docs/tasks/T-1.md", taskRow)
	r := f.doAuthRecorder(http.MethodPost, "/api/v1/preview", `{"markdown":"See [[T-1]] and [[T-1#Align the icons|the task]]."}`, nil)
	if r.code != http.StatusOK || strings.Contains(r.body, "unresolved") || strings.Count(r.body, `data-preview-path=\"p/docs/tasks/T-1.md\"`) != 2 {
		t.Errorf("preview = %d %s", r.code, r.body)
	}
}

// A database with rows: {type: plan}: its index is not a row (no panel), a
// new row gets type: plan.
func TestDatabaseRows_REST(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "p/plans.md", "---\ntitle: Plans\ntype: database\nsource: p/plans\nrows: {type: plan}\nfields:\n  title: {type: text}\n  status: {type: select, options: [draft, done]}\ntags: [p, type:index]\n---\n")
	f.seedNote(t, "p/plans/README.md", "---\ntitle: Plans index\ntags: [p, type:index]\n---\n")
	auth := map[string]string{"Authorization": "Bearer " + f.bearer}
	rec := f.request(http.MethodGet, "/api/v1/notes/p/plans/README.md/fields", "", auth)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"database"`) {
		t.Errorf("the index is not a row: %d %s", rec.Code, rec.Body.String())
	}
	r := f.doAuthRecorder(http.MethodPost, "/api/v1/notes/p/plans.md/rows", `{"name":"20261004-x","title":"X","values":{"status":"draft"}}`, nil)
	if r.code != http.StatusCreated {
		t.Fatalf("create row = %d %s", r.code, r.body)
	}
	if c := f.content(t, "p/plans/20261004-x.md"); !strings.Contains(c, "type: plan") || !strings.Contains(c, "status: draft") {
		t.Errorf("new row:\n%s", c)
	}
}

// A rollup in the REST API (IMP-127 iteration 3): computed in the fields of
// a row (the property panel) and in POST /query over the rows, refused in a
// PATCH of the frontmatter.
func TestRollups_REST(t *testing.T) {
	f := newNotesFixture(t)
	db := strings.Replace(tasksDatabase, "fields:\n", "fields:\n  notes:\n    type: rollup\n    from: p/notes\n    where: [about contains this]\n", 1)
	f.seedNote(t, "p/docs/tasks.md", db)
	f.seedNote(t, "p/docs/tasks/T-1.md", taskRow)
	f.seedNote(t, "p/notes/a.md", "---\ntitle: A\nabout: \"[[T-1]]\"\ntags: [p]\n---\n")
	f.seedNote(t, "p/notes/c.md", "---\ntitle: C\nabout: \"[[T-1]]\"\ntags: [p]\n---\n")

	r := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/p/docs/tasks/T-1.md/fields", "", nil)
	if r.code != http.StatusOK || !strings.Contains(r.body, `"notes":"2"`) || !strings.Contains(r.body, `"type":"rollup"`) {
		t.Errorf("row fields = %d %s", r.code, r.body)
	}
	r = f.doAuthRecorder(http.MethodPost, "/api/v1/query", `{"from":["p/docs/tasks"],"fields":["notes"]}`, nil)
	if r.code != http.StatusOK || !strings.Contains(r.body, `"notes":"2"`) {
		t.Errorf("query = %d %s", r.code, r.body)
	}
	r = f.doAuthRecorder(http.MethodPatch, "/api/v1/notes/p/docs/tasks/T-1.md/frontmatter", `{"set":{"notes":5}}`, nil)
	if r.code == http.StatusOK || !strings.Contains(r.body, "computed when the row is read") {
		t.Errorf("PATCH a rollup = %d %s", r.code, r.body)
	}
}

// The property panel says who created and last modified a row, from the
// authors the audit log gives the index (IMP-127 iteration 3).
func TestRowFields_Authors(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "p/docs/tasks.md", tasksDatabase)
	f.seedNote(t, "p/docs/tasks/T-1.md", taskRow)
	for _, e := range []index.AuthorEvent{
		{TS: time.Now(), Action: index.AuthorCreate, Path: "p/docs/tasks/T-1.md", By: "claude-cli (admin)"},
		{TS: time.Now(), Action: index.AuthorUpdate, Path: "p/docs/tasks/T-1.md", By: "daniele"},
	} {
		if err := f.idx.RecordAuthor(e); err != nil {
			t.Fatal(err)
		}
	}
	r := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/p/docs/tasks/T-1.md/fields", "", nil)
	if r.code != http.StatusOK || !strings.Contains(r.body, `"created_by":"claude-cli (admin)"`) || !strings.Contains(r.body, `"modified_by":"daniele"`) {
		t.Errorf("row fields = %d %s", r.code, r.body)
	}
	r = f.doAuthRecorder(http.MethodPost, "/api/v1/query", `{"from":["p/docs/tasks"],"fields":["modified_by"],"where":[{"field":"created_by","op":"contains","value":"claude"}]}`, nil)
	if r.code != http.StatusOK || !strings.Contains(r.body, `"modified_by":"daniele"`) {
		t.Errorf("query by author = %d %s", r.code, r.body)
	}
}

// POST /notes/{path}/snapshot writes the frozen note beside it (IMP-127
// iteration 3, phase 3).
func TestSnapshot_REST(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "p/docs/tasks.md", tasksDatabase)
	f.seedNote(t, "p/docs/tasks/T-1.md", taskRow)
	f.seedNote(t, "p/hot.md", "---\ntitle: Hot\ntags: [p]\n---\n\n# Hot\n\nTasks: `=count(p/docs/tasks)`.\n")
	r := f.doAuthRecorder(http.MethodPost, "/api/v1/notes/p/hot.md/snapshot", "", nil)
	day := time.Now().UTC().Format("2006-01-02")
	if r.code != http.StatusCreated || !strings.Contains(r.body, `"path":"p/hot.snapshots/`+day+`.md"`) || !strings.Contains(r.body, `"values":1`) {
		t.Fatalf("snapshot = %d %s", r.code, r.body)
	}
	data, err := os.ReadFile(filepath.Join(f.vaultRoot, "p/hot.snapshots", day+".md"))
	if err != nil || !strings.Contains(string(data), "Tasks: 1 (`` `=count(p/docs/tasks)` ``).") {
		t.Errorf("snapshot note: %v\n%s", err, data)
	}
	if r := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/p/hot.md/snapshot", "", nil); r.code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d", r.code)
	}
}
