package v1

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/infolder"
)

// The IN folder comes with the pipelines and stays when they go; a folder
// named IN that holds notes keeps use_in off; turned off, its notes come
// back into the index (IMP-172).
func TestProjectFlags_IN(t *testing.T) {
	f := newAdminFixture(t)
	f.seedNote(t, "Alpha/a.md", "x")
	put := func(body string) *recorder {
		t.Helper()
		return f.doAuthRecorder(http.MethodPut, "/api/v1/projects/Alpha", body, nil)
	}

	w := put(`{"use_pipelines":true}`)
	if w.code != http.StatusOK || !strings.Contains(w.body, `"use_in":true`) {
		t.Fatalf("use_pipelines on: %d %s", w.code, w.body)
	}
	if st, err := os.Stat(filepath.Join(f.vaultRoot, "Alpha", "IN")); err != nil || !st.IsDir() {
		t.Fatalf("no IN folder: %v", err)
	}
	if w := put(`{"use_pipelines":false}`); w.code != http.StatusOK || !f.projects.UsesIN("Alpha") {
		t.Fatalf("use_pipelines off took use_in: %d %s", w.code, w.body)
	}

	if err := os.WriteFile(filepath.Join(f.vaultRoot, "Alpha", "IN", "n.md"), []byte("# N"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := put(`{"use_in":false}`); w.code != http.StatusOK || !strings.Contains(w.body, `"use_in":false`) {
		t.Fatalf("use_in off: %d %s", w.code, w.body)
	}
	if n, _ := f.idx.Note("Alpha/IN/n.md"); n == nil {
		t.Error("the IN folder closed, its note is not indexed")
	}
	w = put(`{"use_in":true}`)
	if w.code != http.StatusConflict || !strings.Contains(w.body, "Alpha/IN/n.md") || f.projects.UsesIN("Alpha") {
		t.Fatalf("use_in on over a folder with notes: %d %s", w.code, w.body)
	}

	if w := put(`{"track_in_git":true}`); w.code != http.StatusOK || !f.projects.Get("Alpha").TrackINInGit {
		t.Fatalf("track_in_git: %d %s", w.code, w.body)
	}
}

func TestIN_API(t *testing.T) {
	f := newAdminFixture(t)
	f.seedNote(t, "Alpha/a.md", "x")
	f.seedNote(t, "Beta/b.md", "x")
	if w := f.doAuthRecorder(http.MethodPut, "/api/v1/projects/Alpha", `{"use_in":true}`, nil); w.code != http.StatusOK {
		t.Fatalf("use_in: %d %s", w.code, w.body)
	}
	for rel, content := range map[string]string{"Alpha/IN/a/x.pdf": "%PDF x", "Alpha/IN/y.txt": "y"} {
		full := filepath.Join(f.vaultRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.in.Scan("Alpha"); err != nil {
		t.Fatal(err)
	}
	get := func(q string) map[string]any {
		t.Helper()
		w := f.doAuthRecorder(http.MethodGet, "/api/v1/in/Alpha"+q, "", nil)
		if w.code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", q, w.code, w.body)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(w.body), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	names := func(out map[string]any, key, field string) []string {
		var got []string
		for _, x := range out[key].([]any) {
			got = append(got, x.(map[string]any)[field].(string))
		}
		return got
	}
	root := get("")
	if c := root["counts"].(map[string]any); c["files"] != float64(2) {
		t.Errorf("counts %v", c)
	}
	if got := names(root, "folders", "path"); len(got) != 1 || got[0] != "a" {
		t.Errorf("folders %v", got)
	}
	if got := names(root, "files", "path"); len(got) != 1 || got[0] != "Alpha/IN/y.txt" {
		t.Errorf("files %v", got)
	}
	if got := names(get("?dir=a"), "files", "name"); len(got) != 1 || got[0] != "x.pdf" {
		t.Errorf("files of a %v", got)
	}
	if got := names(get("?recursive=true&kinds=pdf"), "files", "name"); len(got) != 1 || got[0] != "x.pdf" {
		t.Errorf("pdfs %v", got)
	}

	post := func(action, body string) *recorder {
		t.Helper()
		return f.doAuthRecorder(http.MethodPost, "/api/v1/in/Alpha/"+action, body, nil)
	}
	if w := post("move", `{"from":"a/x.pdf","to":"done/"}`); w.code != http.StatusOK || !strings.Contains(w.body, `"path":"Alpha/IN/done/x.pdf"`) {
		t.Fatalf("move: %d %s", w.code, w.body)
	}
	if _, err := os.Stat(filepath.Join(f.vaultRoot, "Alpha/IN/done/x.pdf")); err != nil {
		t.Error("not moved on disk")
	}
	if w := post("move", `{"from":"y.txt","to":"../Beta/y.txt"}`); w.code == http.StatusOK {
		t.Errorf("a move out of the IN folder: %s", w.body)
	}
	if w := post("status", `{"paths":["y.txt"],"status":"ignored"}`); w.code != http.StatusOK {
		t.Fatalf("status: %d %s", w.code, w.body)
	}
	if file, _ := f.in.Store().File("Alpha", "Alpha/IN/y.txt"); file.Status != infolder.StatusIgnored {
		t.Errorf("status %+v", file)
	}
	if w := post("status", `{"paths":["y.txt"],"status":"bogus"}`); w.code != http.StatusBadRequest {
		t.Errorf("a bad status: %d", w.code)
	}
	if w := post("delete", `{"paths":["done"]}`); w.code != http.StatusOK || !strings.Contains(w.body, `"trash":true`) {
		t.Fatalf("delete: %d %s", w.code, w.body)
	}
	if _, err := os.Stat(filepath.Join(f.vaultRoot, "Alpha/IN/done")); !os.IsNotExist(err) {
		t.Error("the folder is still there")
	}
	if entries, _ := f.trashBin.List(); len(entries) != 1 {
		t.Errorf("trash %+v", entries)
	}
	if w := post("delete", `{"paths":[""]}`); w.code == http.StatusOK {
		t.Error("deleted the IN folder")
	}
	if w := f.doAuthRecorder(http.MethodDelete, "/api/v1/folders/Alpha/IN", "", nil); w.code != http.StatusBadRequest {
		t.Errorf("the folder delete of IN itself: %d %s", w.code, w.body)
	}
	if w := post("run", `{"pipeline":"Alpha/p.md","paths":["y.txt"]}`); w.code != http.StatusNotFound {
		t.Errorf("a run with the pipelines off: %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/in/Beta", "", nil); w.code != http.StatusNotFound {
		t.Errorf("a project without IN: %d", w.code)
	}
	tree := f.doAuthRecorder(http.MethodGet, "/api/v1/tree", "", nil)
	if !strings.Contains(tree.body, `"path":"Alpha/IN"`) || !strings.Contains(tree.body, `"kind":"in"`) || !strings.Contains(tree.body, `"in":{"files":1`) {
		t.Errorf("tree %s", tree.body)
	}
}
