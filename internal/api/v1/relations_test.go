package v1

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
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
