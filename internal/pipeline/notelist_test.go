package pipeline

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/parser"
)

// ListNotes makes the fake a NoteLister: the notes under the folder (or
// directly in it), by path, a page of them, with the fields asked as the
// index keeps them, as text. It keeps the last query.
func (h *fakeHost) ListNotes(project string, q NoteQuery) ([]NoteRow, int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastQuery = q
	var paths []string
	for p := range h.notes {
		if !strings.HasPrefix(p, q.Folder+"/") {
			continue
		}
		if !q.Recursive && strings.Contains(strings.TrimPrefix(p, q.Folder+"/"), "/") {
			continue
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	total := len(paths)
	paths = paths[min(q.Offset, total):]
	paths = paths[:min(q.Limit, len(paths))]
	rows := make([]NoteRow, 0, len(paths))
	for _, p := range paths {
		row := NoteRow{Path: p, Title: p}
		fm := parser.ParseFrontmatterFields(parser.ExtractFrontmatterRaw([]byte(h.notes[p])))
		for _, k := range q.Fields {
			if v, ok := fm[k]; ok {
				if row.Fields == nil {
					row.Fields = map[string]any{}
				}
				row.Fields[k] = v
			}
		}
		rows = append(rows, row)
	}
	return rows, total, nil
}

// note.list resolves its folder inside the project, sorts by path by
// default, and pages with cursor and next as in.list does.
func TestNoteList_FolderDefaultsAndPages(t *testing.T) {
	e, host := newEngine(t, Config{})
	for _, p := range []string{"p/docs/a.md", "p/docs/b.md", "p/docs/sub/c.md", "p/other/d.md"} {
		host.notes[p] = "# note"
	}
	host.notes["p/list.md"] = pipelineNote(`
nodes:
  - {id: first, use: note.list, with: {folder: docs, limit: 2, where: [{field: type, op: ne, value: image}]}}
  - {id: second, use: note.list, with: {folder: docs, limit: 2, cursor: "{{ first.next }}"}}
  - {id: flat, use: note.list, with: {folder: docs, recursive: false}}
`)
	run, _, _ := e.Start(StartRequest{Path: "p/list.md", Trigger: TriggerManual})
	waitStatus(t, e, run.ID, jobs.Done)
	page := func(name string) (paths []string, total float64, next string) {
		m := outputMap(t, stepOf(t, e, run.ID, name))
		for _, n := range m["notes"].([]any) {
			paths = append(paths, n.(map[string]any)["path"].(string))
		}
		return paths, m["total"].(float64), m["next"].(string)
	}
	if p, total, next := page("first"); strings.Join(p, " ") != "p/docs/a.md p/docs/b.md" || total != 3 || next != "2" {
		t.Errorf("first page %v %v %q", p, total, next)
	}
	if p, _, next := page("second"); strings.Join(p, " ") != "p/docs/sub/c.md" || next != "" {
		t.Errorf("second page %v %q", p, next)
	}
	if p, _, _ := page("flat"); len(p) != 2 {
		t.Errorf("not recursive %v", p)
	}
	host.mu.Lock()
	q := host.lastQuery
	host.mu.Unlock()
	if q.Folder != "p/docs" || q.Sort != "path" || q.Recursive || q.Limit != noteListDefault {
		t.Errorf("last query %+v", q)
	}
}

func TestNoteList_Refusals(t *testing.T) {
	e, host := newEngine(t, Config{})
	for name, with := range map[string]string{
		"outside": `{folder: ../q}`,
		"cursor":  `{cursor: page-two}`,
	} {
		host.put("p/"+name+".md", pipelineNote("nodes:\n  - {id: list, use: note.list, with: "+with+"}\n"))
		run, _, _ := e.Start(StartRequest{Path: "p/" + name + ".md", Trigger: TriggerManual})
		if got := waitStatus(t, e, run.ID, jobs.Failed); got.Error == "" {
			t.Errorf("%s: no error", name)
		}
	}
	// The whole project, and the limit capped.
	host.put("p/all.md", pipelineNote("nodes:\n  - {id: list, use: note.list, with: {limit: 100000}}\n"))
	run, _, _ := e.Start(StartRequest{Path: "p/all.md", Trigger: TriggerManual})
	waitStatus(t, e, run.ID, jobs.Done)
	var out struct{ Notes []NoteRow }
	_ = json.Unmarshal(stepOf(t, e, run.ID, "list").Output, &out)
	host.mu.Lock()
	q := host.lastQuery
	host.mu.Unlock()
	if q.Folder != "p" || q.Limit != 5000 || len(out.Notes) == 0 {
		t.Errorf("query %+v, %d notes", q, len(out.Notes))
	}
}
