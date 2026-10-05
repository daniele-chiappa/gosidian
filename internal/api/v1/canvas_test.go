package v1

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testCanvas = `{"nodes":[
	{"id":"g","type":"group","x":0,"y":0,"width":1000,"height":500,"label":"Fase 1"},
	{"id":"t","type":"text","x":20,"y":20,"width":300,"height":100,"text":"Vedi [[p/plans/a]] e #fase"},
	{"id":"f","type":"file","x":400,"y":20,"width":300,"height":200,"file":"p/plans/a.md","subpath":"#Context"},
	{"id":"i","type":"file","x":20,"y":300,"width":200,"height":100,"file":"p/attachments/shot.png"},
	{"id":"m","type":"file","x":2000,"y":0,"width":200,"height":100,"file":"p/plans/gone.md"},
	{"id":"x","type":"file","x":2000,"y":200,"width":200,"height":100,"file":"privproj/secret.md"},
	{"id":"b","type":"file","x":2000,"y":400,"width":200,"height":100,"file":"p/plans/b.md"}
],"edges":[{"id":"e","fromNode":"t","toNode":"f","label":"poi"}]}`

// An Obsidian canvas reads as a read-only note: its cards for the web UI,
// within the reader's scope, its text for agents, its JSON as source; it
// shows in the tree and is never written (IMP-144).
func TestCanvas_REST(t *testing.T) {
	f := newNotesFixture(t)
	guest := f.seedTwoProjects(t)
	f.seedNote(t, "p/plans/a.md", "---\ntitle: Plan A\ntags: [p]\n---\n\n# Plan A\n\nIntro.\n\n## Context\n\nWhy we do it.\n\n## Steps\n\nNot shown.\n")
	f.seedNote(t, "p/plans/b.md", "---\ntitle: Plan B\ntags: [p]\n---\n\n# Plan B\n\nThe body of B.\n")
	for rel, data := range map[string]string{"p/attachments/shot.png": "\x89PNG\r\n", "p/roadmap.canvas": testCanvas, "p/broken.canvas": "{nodes"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(f.vaultRoot, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.vaultRoot, rel), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/p/roadmap.canvas", "", nil)
	var note noteResponse
	if err := json.Unmarshal([]byte(r.body), &note); err != nil || r.code != http.StatusOK {
		t.Fatalf("GET = %d %s", r.code, r.body)
	}
	if note.Kind != "canvas" || note.Source != testCanvas || note.Title != "roadmap" || note.Canvas == nil || len(note.Canvas.Nodes) != 7 || len(note.Canvas.Edges) != 1 {
		t.Fatalf("canvas = %+v", note)
	}
	for _, want := range []string{"- **Group** Fase 1", "  - **File** [[p/plans/a]] #Context", "`p/plans/gone.md`", "- «Vedi [[p/plans/a]] e #fase» → [[p/plans/a]] #Context: poi"} {
		if !strings.Contains(note.Content, want) {
			t.Errorf("content lacks %q:\n%s", want, note.Content)
		}
	}
	cards := map[string]canvasCard{}
	for _, c := range note.Canvas.Nodes {
		cards[c.ID] = c
	}
	if c := cards["t"]; !strings.Contains(c.HTML, `data-preview-path="p/plans/a.md"`) || !strings.Contains(c.HTML, `href="/tags/fase"`) || c.Parent != "g" {
		t.Errorf("text card = %+v", c)
	}
	if c := cards["f"]; c.Path != "p/plans/a.md" || c.Title != "Plan A" || !strings.Contains(c.HTML, "Why we do it.") || strings.Contains(c.HTML, "Not shown") || strings.Contains(c.HTML, "Intro") {
		t.Errorf("file card = %+v", c)
	}
	// The card shows the title: the note's first heading, the same, is not
	// repeated under it.
	if c := cards["b"]; c.Title != "Plan B" || !strings.Contains(c.HTML, "The body of B.") || strings.Contains(c.HTML, "<h1") {
		t.Errorf("file card without subpath = %+v", c)
	}
	if c := cards["i"]; c.Image != "/vault-files/p/attachments/shot.png" || c.Path != "" {
		t.Errorf("image card = %+v", c)
	}
	if c := cards["m"]; c.Path != "" || c.HTML != "" || c.File != "p/plans/gone.md" {
		t.Errorf("missing file card = %+v", c)
	}

	// A guest who may not see p does not see the canvas; one who sees the
	// canvas does not see the cards of a project it may not read.
	if err := os.WriteFile(filepath.Join(f.vaultRoot, "pubproj/map.canvas"), []byte(testCanvas), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := f.req(t, http.MethodGet, "/api/v1/notes/p/roadmap.canvas", "", guest); r.code != http.StatusNotFound {
		t.Errorf("guest on a private canvas = %d", r.code)
	}
	r = f.req(t, http.MethodGet, "/api/v1/notes/pubproj/map.canvas", "", guest)
	note = noteResponse{}
	if err := json.Unmarshal([]byte(r.body), &note); err != nil || r.code != http.StatusOK {
		t.Fatalf("guest GET = %d %s", r.code, r.body)
	}
	for _, c := range note.Canvas.Nodes {
		if c.Path != "" || c.Image != "" || c.Type == "file" && c.HTML != "" {
			t.Errorf("guest sees card %+v", c)
		}
	}
	if strings.Contains(note.Content, "**File** [[") || !strings.Contains(note.Content, "`privproj/secret.md`") {
		t.Errorf("guest content:\n%s", note.Content)
	}

	r = f.doAuthRecorder(http.MethodGet, "/api/v1/notes/p/broken.canvas", "", nil)
	note = noteResponse{}
	if err := json.Unmarshal([]byte(r.body), &note); err != nil || r.code != http.StatusOK || note.Canvas == nil || !strings.Contains(note.Canvas.Error, "not a JSON canvas") {
		t.Errorf("broken = %d %+v", r.code, note)
	}
	if r := f.doAuthRecorder(http.MethodPut, "/api/v1/notes/p/roadmap.canvas", `{"content":"x"}`, nil); r.code < 400 {
		t.Errorf("PUT = %d: a canvas is never written", r.code)
	}
	if data, _ := os.ReadFile(filepath.Join(f.vaultRoot, "p/roadmap.canvas")); string(data) != testCanvas {
		t.Errorf("the canvas changed: %q", data)
	}
	tree := f.doAuthRecorder(http.MethodGet, "/api/v1/tree?project=p", "", nil)
	if !strings.Contains(tree.body, `"path":"p/roadmap.canvas","is_dir":false,"kind":"canvas"`) {
		t.Errorf("tree = %s", tree.body)
	}
}
