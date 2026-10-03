package v1

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/projects"
)

const tasksDatabase = `---
title: Tasks
type: database
source: p/docs/tasks
fields:
  id: {type: text, required: true}
  status: {type: select, required: true, options: [todo, doing, done]}
  priority: {type: select, options: [low, medium, high]}
  due: {type: date}
  done: {type: checkbox}
  labels: {type: multi-select, options: [ui, api]}
tags: [p, type:index]
---

# Tasks
`

const taskRow = `---
title: Align the icons
id: T-1
status: todo
priority: high
tags: [p, type:doc]
---

# Align the icons

status: in the body is not frontmatter
`

func newFrontmatterFixture(t *testing.T) *notesFixture {
	t.Helper()
	f := newNotesFixture(t)
	f.seedNote(t, "p/docs/tasks.md", tasksDatabase)
	f.seedNote(t, "p/docs/tasks/T-1.md", taskRow)
	return f
}

const rowURL = "/api/v1/notes/p/docs/tasks/T-1.md/frontmatter"

func (f *notesFixture) content(t *testing.T, rel string) string {
	t.Helper()
	r := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/"+rel, "", nil)
	if r.code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", rel, r.code, r.body)
	}
	var n struct{ Content string }
	if err := json.Unmarshal([]byte(r.body), &n); err != nil {
		t.Fatal(err)
	}
	return n.Content
}

func TestFrontmatterPatch_RewritesOnlyTheKeys(t *testing.T) {
	f := newFrontmatterFixture(t)
	before := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/p/docs/tasks/T-1.md", "", nil).headers.Get("ETag")
	r := f.doAuthRecorder(http.MethodPatch, rowURL,
		`{"set":{"status":"done","due":"2026-10-10","done":true},"unset":["priority"],"expect":{"status":"todo"}}`, nil)
	if r.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", r.code, r.body)
	}
	want := strings.Replace(taskRow, "status: todo\npriority: high\n", "status: done\n", 1)
	// New keys go before tags, the last key, as the vault's notes have it.
	want = strings.Replace(want, "tags: [p, type:doc]\n---", "due: 2026-10-10\ndone: true\ntags: [p, type:doc]\n---", 1)
	if got := f.content(t, "p/docs/tasks/T-1.md"); got != want {
		t.Errorf("content\n%s\nwant\n%s", got, want)
	}
	if etag := r.headers.Get("ETag"); etag == "" || etag == before {
		t.Errorf("etag %q should rotate (was %q)", etag, before)
	}
	hits, _, err := f.idx.Query(index.QueryOptions{Folders: []string{"p/docs/tasks"},
		Where: []index.FieldCond{{Field: "status", Op: index.OpEq, Values: []string{"done"}}}})
	if err != nil || len(hits) != 1 {
		t.Errorf("index not updated: %v %v", hits, err)
	}
}

// expect guards each field on its own: a change to another field of the
// same note does not get in the way, a change to the field itself does.
func TestFrontmatterPatch_ExpectPerField(t *testing.T) {
	f := newFrontmatterFixture(t)
	// Someone else changes priority after the client read the row.
	if r := f.doAuthRecorder(http.MethodPatch, rowURL, `{"set":{"priority":"low"}}`, nil); r.code != http.StatusOK {
		t.Fatalf("setup: %d %s", r.code, r.body)
	}
	if r := f.doAuthRecorder(http.MethodPatch, rowURL, `{"set":{"status":"doing"},"expect":{"status":"todo"}}`, nil); r.code != http.StatusOK {
		t.Fatalf("another field changed: status=%d body=%s", r.code, r.body)
	}
	r := f.doAuthRecorder(http.MethodPatch, rowURL, `{"set":{"status":"done"},"expect":{"status":"todo"}}`, nil)
	if r.code != http.StatusConflict {
		t.Fatalf("stale field: status=%d, want 409 body=%s", r.code, r.body)
	}
	var e struct {
		Error struct {
			Details struct {
				Fields map[string]any `json:"fields"`
			}
		}
	}
	_ = json.Unmarshal([]byte(r.body), &e)
	if e.Error.Details.Fields["status"] != "doing" {
		t.Errorf("409 should carry the current value: %s", r.body)
	}
	// Numbers, bools and lists compare with what the note holds.
	if r := f.doAuthRecorder(http.MethodPatch, rowURL, `{"set":{"done":true,"labels":["ui"]}}`, nil); r.code != http.StatusOK {
		t.Fatalf("setup: %d %s", r.code, r.body)
	}
	r = f.doAuthRecorder(http.MethodPatch, rowURL,
		`{"set":{"done":false},"expect":{"done":true,"labels":["ui"],"due":null,"priority":"low"}}`, nil)
	if r.code != http.StatusOK {
		t.Errorf("typed expect: status=%d body=%s", r.code, r.body)
	}
}

func TestFrontmatterPatch_IfMatch(t *testing.T) {
	f := newFrontmatterFixture(t)
	r := f.doAuthRecorder(http.MethodPatch, rowURL, `{"set":{"status":"done"}}`, map[string]string{"If-Match": `"stale"`})
	if r.code != http.StatusPreconditionFailed || !strings.Contains(r.body, "current_etag") {
		t.Errorf("status=%d, want 412 with current_etag: %s", r.code, r.body)
	}
	etag := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/p/docs/tasks/T-1.md", "", nil).headers.Get("ETag")
	if r := f.doAuthRecorder(http.MethodPatch, rowURL, `{"set":{"status":"done"},"if_match":`+jsonString(etag)+`}`, nil); r.code != http.StatusOK {
		t.Errorf("fresh if_match: status=%d body=%s", r.code, r.body)
	}
}

func TestFrontmatterPatch_Schema(t *testing.T) {
	f := newFrontmatterFixture(t)
	for _, body := range []string{
		`{"set":{"status":"fixed"}}`,
		`{"set":{"resolved":"2026-10-03"}}`,
		`{"set":{"id":"T-2"}}`,
		`{"set":{"due":"tomorrow"}}`,
		`{"set":{"labels":["web"]}}`,
		`{"unset":["status"]}`,
	} {
		r := f.doAuthRecorder(http.MethodPatch, rowURL, body, nil)
		if r.code != http.StatusUnprocessableEntity || !strings.Contains(r.body, `"problems"`) {
			t.Errorf("%s: status=%d, want 422 with problems: %s", body, r.code, r.body)
		}
	}
	if got := f.content(t, "p/docs/tasks/T-1.md"); got != taskRow {
		t.Errorf("a refused edit changed the note:\n%s", got)
	}
}

func TestFrontmatterPatch_ValuesAndYAML(t *testing.T) {
	f := newFrontmatterFixture(t)
	f.seedNote(t, "p/free.md", "---\ntitle: Free\nmeta:\n  a: 1\n---\nbody\n")
	if r := f.doAuthRecorder(http.MethodPatch, "/api/v1/notes/p/free.md/frontmatter", `{"set":{"aliases":["a, b"]}}`, nil); r.code != http.StatusUnprocessableEntity {
		t.Errorf("comma in a list item: status=%d, want 422 body=%s", r.code, r.body)
	}
	if r := f.doAuthRecorder(http.MethodPatch, "/api/v1/notes/p/free.md/frontmatter", `{"set":{"meta":"x"}}`, nil); r.code != http.StatusBadRequest {
		t.Errorf("nested map: status=%d, want 400 body=%s", r.code, r.body)
	}
	// Outside a database any key may be written.
	if r := f.doAuthRecorder(http.MethodPatch, "/api/v1/notes/p/free.md/frontmatter", `{"set":{"note":"Iterazione 2: editor"}}`, nil); r.code != http.StatusOK {
		t.Errorf("free note: status=%d body=%s", r.code, r.body)
	}
	if got := f.content(t, "p/free.md"); !strings.Contains(got, "meta:\n  a: 1\nnote: \"Iterazione 2: editor\"\n---") {
		t.Errorf("free note content:\n%s", got)
	}
}

func TestFrontmatterPatch_NoChangeKeepsTheNote(t *testing.T) {
	f := newFrontmatterFixture(t)
	etag := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/p/docs/tasks/T-1.md", "", nil).headers.Get("ETag")
	r := f.doAuthRecorder(http.MethodPatch, rowURL, `{"set":{"status":"todo"}}`, nil)
	if r.code != http.StatusOK || r.headers.Get("ETag") != etag {
		t.Errorf("status=%d etag %q, want 200 and %q", r.code, r.headers.Get("ETag"), etag)
	}
}

func TestFrontmatterPatch_ReadOnlyMember(t *testing.T) {
	f := newFrontmatterFixture(t)
	u, bearer := f.memberUser(t, "reader")
	if err := f.projects.SetMember("p", u.ID, projects.LevelRead); err != nil {
		t.Fatal(err)
	}
	rec := f.request(http.MethodPatch, rowURL, `{"set":{"status":"done"}}`, map[string]string{"Authorization": "Bearer " + bearer})
	if rec.Code != http.StatusForbidden {
		t.Errorf("read-only member: status=%d, want 403 body=%s", rec.Code, rec.Body.String())
	}
}

func TestFrontmatterPatch_BadRequests(t *testing.T) {
	f := newFrontmatterFixture(t)
	// HTML notes keep their frontmatter in a comment: not editable this way.
	cases := []struct {
		method, url, body string
		want              int
	}{
		{http.MethodGet, rowURL, "", http.StatusMethodNotAllowed},
		{http.MethodPatch, rowURL, `{}`, http.StatusBadRequest},
		{http.MethodPatch, rowURL, `not json`, http.StatusBadRequest},
		{http.MethodPatch, "/api/v1/notes/p/missing.md/frontmatter", `{"set":{"a":"b"}}`, http.StatusNotFound},
		{http.MethodPatch, "/api/v1/notes/p/page.html/frontmatter", `{"set":{"a":"b"}}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		if r := f.doAuthRecorder(c.method, c.url, c.body, nil); r.code != c.want {
			t.Errorf("%s %s %s: status=%d, want %d (%s)", c.method, c.url, c.body, r.code, c.want, r.body)
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// The preview returns a note's views in the form the web UI's editors use:
// typed columns for a database, and which rows the reader may edit.
func TestPreview_ViewData(t *testing.T) {
	f := newFrontmatterFixture(t)
	f.seedNote(t, "q/docs/secret.md", strings.ReplaceAll(tasksDatabase, "p/docs/tasks", "q/docs/secret"))
	f.seedNote(t, "q/docs/secret/S-1.md", strings.ReplaceAll(taskRow, "T-1", "S-1"))
	md := "# Board\n\n```view\nfrom: p/docs/tasks\ncolumns: [title, status, due]\n```\n\n" +
		"```view\nfrom: q/docs/secret\n```\n\n```view\nfrom: p/docs/tasks\nas: board\ngroup_by: status\n```\n"
	body, _ := json.Marshal(map[string]string{"markdown": md, "path": "p/board.md"})

	preview := func(hdr map[string]string) previewResponse {
		t.Helper()
		rec := f.request(http.MethodPost, "/api/v1/preview", string(body), hdr)
		if rec.Code != http.StatusOK {
			t.Fatalf("preview = %d %s", rec.Code, rec.Body.String())
		}
		var out previewResponse
		if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	owner := preview(map[string]string{"Authorization": "Bearer " + f.bearer})
	if len(owner.Views) != 3 || !strings.Contains(owner.HTML, `data-view="2"`) {
		t.Fatalf("views = %+v\nhtml = %s", owner.Views, owner.HTML)
	}
	v := owner.Views[0]
	if v.Database != "p/docs/tasks.md" || len(v.Columns) != 3 || v.Columns[1].Type != "select" ||
		strings.Join(v.Columns[1].Options, ",") != "todo,doing,done" || v.Columns[2].Type != "date" {
		t.Errorf("table view = %+v", v)
	}
	if len(v.Rows) != 1 || v.Rows[0].Path != "p/docs/tasks/T-1.md" || v.Rows[0].Fields["status"] != "todo" || !v.Rows[0].Writable {
		t.Errorf("owner rows = %+v", v.Rows)
	}
	if b := owner.Views[2]; b.As != "board" || strings.Join(b.Groups, ",") != "todo,doing,done" {
		t.Errorf("board view = %+v", b)
	}

	// A reader of p only: rows of p not writable, nothing of q, not even
	// its schema.
	u, bearer := f.memberUser(t, "reader")
	if err := f.webauth.SetRestricted(u.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := f.projects.SetMember("p", u.ID, projects.LevelRead); err != nil {
		t.Fatal(err)
	}
	reader := preview(map[string]string{"Authorization": "Bearer " + bearer})
	if len(reader.Views) != 3 || len(reader.Views[0].Rows) != 1 || reader.Views[0].Rows[0].Writable {
		t.Errorf("reader table view = %+v", reader.Views[0])
	}
	if q := reader.Views[1]; len(q.Rows) != 0 || q.Database != "" || len(q.Columns) == 0 || q.Columns[0].Type != "" {
		t.Errorf("reader saw the other project: %+v", q)
	}
}

func TestRowFields(t *testing.T) {
	f := newFrontmatterFixture(t)
	f.seedNote(t, "p/docs/tasks/T-2.md", "---\ntitle: Two\nid: T-2\nstatus: doing\nlabels: [ui, api]\nextra: x\nsee: \"[[p/docs/tasks/T-1]]\"\ntags: [p]\n---\n")
	f.seedNote(t, "p/free.md", "---\ntitle: Free\nstatus: x\n---\n")
	get := func(rel string, hdr map[string]string) rowFieldsResponse {
		t.Helper()
		rec := f.request(http.MethodGet, "/api/v1/notes/"+rel+"/fields", "", hdr)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s/fields = %d %s", rel, rec.Code, rec.Body.String())
		}
		var out rowFieldsResponse
		if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	owner := map[string]string{"Authorization": "Bearer " + f.bearer}
	r := get("p/docs/tasks/T-2.md", owner)
	if r.Database != "p/docs/tasks.md" || len(r.Columns) != 6 || r.Columns[1].Name != "status" || r.Columns[1].Type != "select" || !r.Writable {
		t.Errorf("row = %+v", r)
	}
	if r.Values["status"] != "doing" || len(r.Values["labels"].([]any)) != 2 || r.Values["extra"] != "x" {
		t.Errorf("values = %+v", r.Values)
	}
	if strings.Join(r.Others, ",") != "extra,see" {
		t.Errorf("others = %v", r.Others)
	}
	if l := r.Links["see"]; len(l) != 1 || l[0].Path != "p/docs/tasks/T-1.md" {
		t.Errorf("links = %+v", r.Links)
	}
	if free := get("p/free.md", owner); free.Database != "" || len(free.Values) != 0 || free.Writable {
		t.Errorf("a note outside a database: %+v", free)
	}

	u, bearer := f.memberUser(t, "reader")
	if err := f.projects.SetMember("p", u.ID, projects.LevelRead); err != nil {
		t.Fatal(err)
	}
	reader := map[string]string{"Authorization": "Bearer " + bearer}
	if r := get("p/docs/tasks/T-2.md", reader); r.Database == "" || r.Writable {
		t.Errorf("reader: %+v", r)
	}
	if rec := f.request(http.MethodGet, "/api/v1/notes/p/docs/tasks/missing.md/fields", "", owner); rec.Code != http.StatusNotFound {
		t.Errorf("missing note: %d", rec.Code)
	}
}
