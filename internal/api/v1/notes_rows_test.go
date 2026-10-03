package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/lint"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/vault"
)

const (
	newRowURL = "/api/v1/notes/p/docs/tasks.md/new-row"
	rowsURL   = "/api/v1/notes/p/docs/tasks.md/rows"
)

const taskTemplate = `---
title: "{{ID}} — {{TITLE}}"
status: todo
due: "{{TODAY}}"
tags: ["{{PROJECT}}", type:doc]
---

# {{ID}} — {{TITLE}}

Opened on {{TODAY}}.
`

func withTemplate(t *testing.T, f *notesFixture) {
	t.Helper()
	f.seedNote(t, "p/templates/task.md", taskTemplate)
	f.seedNote(t, "p/docs/tasks.md", strings.Replace(tasksDatabase, "source: p/docs/tasks\n", "source: p/docs/tasks\ntemplate: p/templates/task\n", 1))
}

func TestNewRow_Suggests(t *testing.T) {
	f := newFrontmatterFixture(t)
	f.seedNote(t, "p/docs/tasks/T-009.md", strings.ReplaceAll(taskRow, "T-1", "T-009"))
	r := f.doAuthRecorder(http.MethodGet, newRowURL, "", nil)
	if r.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", r.code, r.body)
	}
	var got newRowResponse
	if err := json.Unmarshal([]byte(r.body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "T-010" || got.Source != "p/docs/tasks" || got.Template != "" || len(got.Columns) != 6 ||
		got.Columns[1].Name != "status" || !got.Columns[1].Required || len(got.Preset) != 0 {
		t.Errorf("new row = %+v", got)
	}

	withTemplate(t, f)
	r = f.doAuthRecorder(http.MethodGet, newRowURL, "", nil)
	_ = json.Unmarshal([]byte(r.body), &got)
	if got.Template != "p/templates/task.md" || strings.Join(got.Preset, ",") != "title,status,due,tags" {
		t.Errorf("with a template: %+v", got)
	}
}

// Without a template the row is the title, the id, the values and the
// project's tag, over a heading; the title keeps its quotes and colon.
func TestCreateRow_WithoutTemplate(t *testing.T) {
	f := newFrontmatterFixture(t)
	r := f.doAuthRecorder(http.MethodPost, rowsURL,
		`{"name":"T-2","title":"Fix: the \"menu\"","values":{"status":"doing","labels":["ui"],"done":false}}`, nil)
	if r.code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", r.code, r.body)
	}
	content := f.content(t, "p/docs/tasks/T-2.md")
	raw := parser.ExtractFrontmatterRaw([]byte(content))
	fields := parser.ParseFrontmatterFields(raw)
	if !strings.HasPrefix(content, "---\ntitle: ") || fields["title"] != `Fix: the "menu"` || fields["id"] != "T-2" ||
		fields["status"] != "doing" || fields["done"] != "false" || strings.Join(parser.FrontmatterList(raw, "labels"), ",") != "ui" {
		t.Errorf("content:\n%s\nfields %v", content, fields)
	}
	if !strings.HasSuffix(content, "tags: [p]\n---\n\n# Fix: the \"menu\"\n") {
		t.Errorf("tags last, then the heading:\n%s", content)
	}
}

// With a template its placeholders are filled, in the frontmatter as valid
// YAML whatever the title holds, and the row passes lint.
func TestCreateRow_FromTemplate(t *testing.T) {
	f := newFrontmatterFixture(t)
	withTemplate(t, f)
	r := f.doAuthRecorder(http.MethodPost, rowsURL, `{"name":"T-7","title":"Say \"hi\": now","values":{"priority":"high"}}`, nil)
	if r.code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", r.code, r.body)
	}
	today := time.Now().UTC().Format("2006-01-02")
	content := f.content(t, "p/docs/tasks/T-7.md")
	raw := parser.ExtractFrontmatterRaw([]byte(content))
	fields := parser.ParseFrontmatterFields(raw)
	if fields["title"] != `T-7 — Say "hi": now` || fields["status"] != "todo" || fields["due"] != today ||
		fields["id"] != "T-7" || fields["priority"] != "high" || strings.Join(parser.FrontmatterList(raw, "tags"), ",") != "p,type:doc" {
		t.Errorf("fields %v in\n%s", fields, content)
	}
	if !strings.Contains(content, "\n# T-7 — Say \"hi\": now\n\nOpened on "+today+".\n") {
		t.Errorf("body placeholders:\n%s", content)
	}
	// A row written by hand that breaks the schema shows the lint runs.
	f.seedNote(t, "p/docs/tasks/T-8.md", "---\ntitle: Bad\nid: T-8\nstatus: nope\ntags: [p]\n---\n")
	issues, err := lint.New(vault.New(f.vaultRoot), f.idx).Run(context.Background(), "p", []string{"database-field-invalid"}, "")
	if err != nil {
		t.Fatal(err)
	}
	flagged := map[string]bool{}
	for _, is := range issues {
		flagged[is.File] = true
	}
	if flagged["p/docs/tasks/T-7.md"] || !flagged["p/docs/tasks/T-8.md"] {
		t.Errorf("lint: %+v", issues)
	}
}

func TestCreateRow_Refusals(t *testing.T) {
	f := newFrontmatterFixture(t)
	cases := []struct {
		name, url, body string
		want            int
		has             string
	}{
		{"value off the schema", rowsURL, `{"name":"T-3","values":{"status":"nope"}}`, http.StatusUnprocessableEntity, `"problems"`},
		{"required missing", rowsURL, `{"name":"T-3"}`, http.StatusUnprocessableEntity, `required field \"status\" is missing`},
		{"undeclared field", rowsURL, `{"name":"T-3","values":{"status":"todo","owner":"x"}}`, http.StatusUnprocessableEntity, `not in the schema`},
		{"comma in a list item", rowsURL, `{"name":"T-3","values":{"status":"todo","labels":["a,b"]}}`, http.StatusUnprocessableEntity, `"field":"labels"`},
		{"name taken", rowsURL, `{"name":"T-1","values":{"status":"todo"}}`, http.StatusConflict, `"name":"T-2"`},
		{"no name", rowsURL, `{"values":{"status":"todo"}}`, http.StatusBadRequest, "name required"},
		{"name with a folder", rowsURL, `{"name":"x/T-3","values":{"status":"todo"}}`, http.StatusBadRequest, "file name"},
		{"dot name", rowsURL, `{"name":".T-3","values":{"status":"todo"}}`, http.StatusBadRequest, "file name"},
		{"not a database", "/api/v1/notes/p/docs/tasks/T-1.md/rows", `{"name":"x"}`, http.StatusBadRequest, "not a database"},
		{"missing database", "/api/v1/notes/p/docs/none.md/rows", `{"name":"x"}`, http.StatusNotFound, ""},
		{"GET rows", rowsURL, ``, http.StatusMethodNotAllowed, ""},
	}
	for _, c := range cases {
		method := http.MethodPost
		if strings.HasPrefix(c.name, "GET") {
			method = http.MethodGet
		}
		r := f.doAuthRecorder(method, c.url, c.body, nil)
		if r.code != c.want || !strings.Contains(r.body, c.has) {
			t.Errorf("%s: status=%d body=%s, want %d with %q", c.name, r.code, r.body, c.want, c.has)
		}
	}
	if rec := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/p/docs/tasks/T-3.md", "", nil); rec.code != http.StatusNotFound {
		t.Errorf("a refused row was written: %d", rec.code)
	}

	// A template that is gone, or one the schema may not name.
	f.seedNote(t, "p/docs/tasks.md", strings.Replace(tasksDatabase, "source: p/docs/tasks\n", "source: p/docs/tasks\ntemplate: p/templates/gone\n", 1))
	if r := f.doAuthRecorder(http.MethodPost, rowsURL, `{"name":"T-3","values":{"status":"todo"}}`, nil); r.code != http.StatusUnprocessableEntity || !strings.Contains(r.body, "does not exist") {
		t.Errorf("missing template: %d %s", r.code, r.body)
	}
	f.seedNote(t, "p/docs/tasks.md", strings.Replace(tasksDatabase, "source: p/docs/tasks\n", "source: p/docs/tasks\ntemplate: q/templates/task\n", 1))
	if r := f.doAuthRecorder(http.MethodGet, newRowURL, "", nil); r.code != http.StatusUnprocessableEntity || !strings.Contains(r.body, "must be a note of project p") {
		t.Errorf("template of another project: %d %s", r.code, r.body)
	}
}

func TestCreateRow_ReadOnlyMember(t *testing.T) {
	f := newFrontmatterFixture(t)
	u, bearer := f.memberUser(t, "reader")
	if err := f.projects.SetMember("p", u.ID, projects.LevelRead); err != nil {
		t.Fatal(err)
	}
	hdr := map[string]string{"Authorization": "Bearer " + bearer}
	if rec := f.request(http.MethodGet, newRowURL, "", hdr); rec.Code != http.StatusForbidden {
		t.Errorf("new-row: %d, want 403", rec.Code)
	}
	if rec := f.request(http.MethodPost, rowsURL, `{"name":"T-2","values":{"status":"todo"}}`, hdr); rec.Code != http.StatusForbidden {
		t.Errorf("rows: %d, want 403", rec.Code)
	}
}
