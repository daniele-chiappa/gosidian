package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/plugins"
	"github.com/gosidian/gosidian/internal/projects"
)

const copyPipeline = "---\ntitle: Copy\ntype: pipeline\n---\n\n# Copy\n\n```pipeline\n" + `trigger: {on: external}
nodes:
  - id: write
    use: note.write
    with: {path: "out/{{ trigger.name }}", body: "Hello {{ trigger.name }}"}
` + "```\n"

const waitPipeline = "---\ntitle: Ask\ntype: pipeline\n---\n\n```pipeline\n" + `nodes:
  - {id: ask, use: wait.call, with: {timeout: 1h}}
  - {id: answer, use: note.write, with: {path: "answers/{{ ask.who }}", body: "{{ ask.text }}"}}
` + "```\n"

// pipelineServer is a test server where project p runs pipelines and q
// does not, with a pipeline note in each, and an engine on a running job
// runner.
func pipelineServer(t *testing.T) (*Server, *pipeline.Engine) {
	t.Helper()
	s, _, _ := newTestServer(t)
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pstore.Set("p", projects.Flags{UsePipelines: true}); err != nil {
		t.Fatal(err)
	}
	s.SetProjects(pstore)
	s.SetPipelinesSwitch(true)
	ctx := context.Background()
	for path, content := range map[string]string{
		"p/pipelines/copy.md": copyPipeline,
		"p/pipelines/ask.md":  waitPipeline,
		"p/pipelines/bad.md":  "---\ntype: pipeline\n---\n\n```pipeline\nnodes: [{id: a, use: shell.run}]\n```\n",
		"q/pipelines/copy.md": strings.ReplaceAll(copyPipeline, "Copy", "Q copy"),
		"p/plain.md":          "---\ntitle: Plain\n---\n\nNot a pipeline.\n",
	} {
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": path, "content": content})); err != nil || res.IsError {
			t.Fatalf("create %s: %v %+v", path, err, res)
		}
	}
	store, err := jobs.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	runner := jobs.NewRunner(store, jobs.Options{Workers: 2, Poll: 10 * time.Millisecond, Lease: time.Minute})
	e := pipeline.New(s.PipelineHost(), runner, pipeline.Config{Location: time.UTC}, nil, nil)
	s.SetPipelines(e)
	rctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { runner.Start(rctx); close(stopped) }()
	t.Cleanup(func() { cancel(); <-stopped; store.Close() })
	return s, e
}

func runID(t *testing.T, res string) string {
	t.Helper()
	var out struct {
		Run struct {
			ID     string      `json:"id"`
			Status jobs.Status `json:"status"`
		} `json:"run"`
	}
	if err := json.Unmarshal([]byte(res), &out); err != nil || out.Run.ID == "" {
		t.Fatalf("no run in %s", res)
	}
	return out.Run.ID
}

func TestPipelineRun_StartsAndWaits(t *testing.T) {
	s, e := pipelineServer(t)
	al, err := audit.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetAuditLog(al)
	ctx := context.Background()
	res, err := s.handlePipelineRun(ctx, call(map[string]any{"path": "p/pipelines/copy.md", "input": map[string]any{"name": "doc"}, "wait": 5}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, `"status":"done"`) || !strings.Contains(text, `"name":"write"`) {
		t.Fatalf("result %s", text)
	}
	note, err := s.vault.Load("p/out/doc.md")
	if err != nil || !strings.Contains(string(note.Content), "Hello doc") || !strings.Contains(string(note.Content), "pipeline: '[[p/pipelines/copy]]'") {
		t.Fatalf("written note: %v\n%s", err, note.Content)
	}
	// Written as the pipeline, audited with its source.
	found := false
	for _, en := range auditEntries(t, al) {
		if en.Path == "p/out/doc.md" {
			found = en.Source == audit.SourcePipeline && en.Actor == "pipeline:p/pipelines/copy.md"
		}
	}
	if !found {
		t.Fatalf("no pipeline audit entry for the note: %+v", auditEntries(t, al))
	}
	// The same key again gives the same run.
	id := runID(t, text)
	_ = e
	first, _ := s.handlePipelineRun(ctx, call(map[string]any{"path": "p/pipelines/copy.md", "input": map[string]any{"name": "x"}, "idempotency_key": "k1"}))
	again, _ := s.handlePipelineRun(ctx, call(map[string]any{"path": "p/pipelines/copy.md", "input": map[string]any{"name": "x"}, "idempotency_key": "k1"}))
	if runID(t, resultText(t, first)) != runID(t, resultText(t, again)) || !strings.Contains(resultText(t, again), `"existing":true`) {
		t.Fatalf("idempotency: %s / %s", resultText(t, first), resultText(t, again))
	}
	// memory_pipelines shows the run and its step.
	res, _ = s.handlePipelines(ctx, call(map[string]any{"project": "p", "run": id}))
	if text := resultText(t, res); !strings.Contains(text, `"use":"note.write"`) || !strings.Contains(text, `"status":"done"`) {
		t.Fatalf("run view %s", text)
	}
}

func TestPipelineRun_ResumeAndCancel(t *testing.T) {
	s, _ := pipelineServer(t)
	ctx := context.Background()
	res, _ := s.handlePipelineRun(ctx, call(map[string]any{"path": "p/pipelines/ask.md", "wait": 5}))
	text := resultText(t, res)
	if !strings.Contains(text, `"status":"waiting"`) {
		t.Fatalf("not waiting: %s", text)
	}
	id := runID(t, text)
	res, _ = s.handlePipelineRun(ctx, call(map[string]any{"action": "resume", "run": id, "data": map[string]any{"who": "bob", "text": "yes"}}))
	if res.IsError {
		t.Fatalf("resume: %s", resultText(t, res))
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if n, err := s.vault.Load("p/answers/bob.md"); err == nil && strings.Contains(string(n.Content), "yes") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the resumed run never wrote its answer")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Another run, canceled while it waits.
	res, _ = s.handlePipelineRun(ctx, call(map[string]any{"path": "p/pipelines/ask.md", "wait": 5}))
	id = runID(t, resultText(t, res))
	res, _ = s.handlePipelineRun(ctx, call(map[string]any{"action": "cancel", "run": id}))
	if !strings.Contains(resultText(t, res), `"status":"canceled"`) {
		t.Fatalf("cancel: %s", resultText(t, res))
	}
}

func TestPipelineRun_Refusals(t *testing.T) {
	s, _ := pipelineServer(t)
	ctx := context.Background()
	for name, a := range map[string]map[string]any{
		"project without use_pipelines": {"path": "q/pipelines/copy.md"},
		"a broken pipeline":             {"path": "p/pipelines/bad.md"},
		"a note that is not a pipeline": {"path": "p/plain.md"},
		"a missing note":                {"path": "p/pipelines/none.md"},
		"input not an object":           {"path": "p/pipelines/copy.md", "input": "text"},
		"an unknown action":             {"action": "explode", "path": "p/pipelines/copy.md"},
		"resume without a run":          {"action": "resume"},
	} {
		res, err := s.handlePipelineRun(ctx, call(a))
		if err != nil || !res.IsError {
			t.Errorf("%s: accepted (%v)", name, err)
		}
	}
	// memory_pipelines lists the pipelines with their problems.
	res, _ := s.handlePipelines(ctx, call(map[string]any{"project": "p"}))
	text := resultText(t, res)
	for _, want := range []string{"p/pipelines/copy.md", `"trigger":"external"`, "shell.run"} {
		if !strings.Contains(text, want) {
			t.Errorf("listing lacks %q: %s", want, text)
		}
	}
	res, _ = s.handlePipelines(ctx, call(map[string]any{"project": "q"}))
	if !strings.Contains(resultText(t, res), "use_pipelines is off") {
		t.Errorf("q's pipeline is not marked off: %s", resultText(t, res))
	}
}

func TestPipelineTools_Off(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	for _, fn := range []toolFn{s.handlePipelines, s.handlePipelineRun} {
		res, _ := fn(ctx, call(map[string]any{"project": "p", "path": "p/x.md"}))
		if msg := expectError(t, res); !strings.Contains(msg, "pipelines are off") {
			t.Fatalf("off: %s", msg)
		}
	}
}

func TestPipelineIngest_AttachmentStartsOnce(t *testing.T) {
	s, e := pipelineServer(t)
	ctx := context.Background()
	arrive := "---\ntitle: Arrive\ntype: pipeline\n---\n\n```pipeline\n" + `trigger: {on: ingest, kinds: [csv]}
nodes:
  - {id: write, use: note.write, with: {path: "out/{{ trigger.stem }}", body: "From {{ trigger.file }} ({{ trigger.name }})"}}
` + "```\n"
	for _, path := range []string{"p/pipelines/arrive.md", "q/pipelines/arrive.md"} {
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": path, "content": arrive})); err != nil || res.IsError {
			t.Fatalf("create %s: %v %+v", path, err, res)
		}
	}
	data := base64.StdEncoding.EncodeToString([]byte("a,b\n1,2\n"))
	upload := func(project string) string {
		res, err := s.handleUploadResource(ctx, call(map[string]any{"project": project, "data": data, "filename": "report.csv"}))
		if err != nil || res.IsError {
			t.Fatalf("upload: %v %s", err, resultText(t, res))
		}
		var out struct{ Path string }
		_ = json.Unmarshal([]byte(resultText(t, res)), &out)
		return out.Path
	}
	att := upload("p")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if n, err := s.vault.Load("p/out/report.md"); err == nil {
			if c := string(n.Content); !strings.Contains(c, "From "+att+" (report.csv)") || !strings.Contains(c, "source: '[["+att+"]]'") {
				t.Fatalf("note:\n%s", c)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the ingest pipeline never wrote its note")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The same file again does not start another run; q runs none.
	upload("p")
	upload("q")
	store := e.Runner().Store()
	if runs, _ := store.Runs(jobs.RunFilter{Ref: "p/pipelines/arrive.md"}); len(runs) != 1 || runs[0].Trigger != pipeline.TriggerIngest {
		t.Errorf("runs of p: %+v", runs)
	}
	if runs, _ := store.Runs(jobs.RunFilter{Ref: "q/pipelines/arrive.md"}); len(runs) != 0 {
		t.Errorf("q ran: %+v", runs)
	}
}

func TestPipelineBootstrap_SaysWherePipelinesRun(t *testing.T) {
	s, _ := pipelineServer(t)
	for project, want := range map[string]bool{"p": true, "q": false} {
		if got := s.projectCapabilities(project, false).Pipelines; got != want {
			t.Errorf("%s: pipelines %v, want %v", project, got, want)
		}
	}
	off, _, _ := newTestServer(t)
	if off.projectCapabilities("p", false).Pipelines {
		t.Error("a server without pipelines says it runs them")
	}
}

// A file a node makes (a plugin's image) is stored as an attachment of the
// project, audited as the pipeline, and starts no ingest pipeline: a run
// must not start runs on its own files (IMP-168).
func TestPipelineHost_StoreAttachment(t *testing.T) {
	s, e := pipelineServer(t)
	al, err := audit.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetAuditLog(al)
	ctx := context.Background()
	arrive := "---\ntitle: Images\ntype: pipeline\n---\n\n```pipeline\n" + `trigger: {on: ingest, kinds: [png]}
nodes:
  - {id: write, use: note.write, with: {path: "out/{{ trigger.stem }}", body: "x"}}
` + "```\n"
	if res, err := s.handleCreate(ctx, call(map[string]any{"path": "p/pipelines/images.md", "content": arrive})); err != nil || res.IsError {
		t.Fatalf("create: %v %+v", err, res)
	}
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")
	rel, err := s.PipelineHost().StoreAttachment("p", "fig-1.png", png, "pipeline:p/pipelines/convert.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rel, "p/attachments/") || !strings.HasSuffix(rel, ".png") {
		t.Fatalf("stored at %s", rel)
	}
	if b, err := s.vault.ReadAttachment(rel, 1<<20); err != nil || string(b) != string(png) {
		t.Fatalf("read back: %v", err)
	}
	found := false
	for _, en := range auditEntries(t, al) {
		if en.Path == rel {
			found = en.Source == audit.SourcePipeline && en.Actor == "pipeline:p/pipelines/convert.md" && en.Action == audit.ActionUploadAttachment
		}
	}
	if !found {
		t.Errorf("no pipeline audit entry: %+v", auditEntries(t, al))
	}
	if _, err := s.PipelineHost().StoreAttachment("p", "run.sh", []byte("#!/bin/sh"), "pipeline:x"); err == nil {
		t.Error("a shell script is stored")
	}
	time.Sleep(100 * time.Millisecond)
	if runs, _ := e.Runner().Store().Runs(jobs.RunFilter{Ref: "p/pipelines/images.md"}); len(runs) != 0 {
		t.Errorf("a stored file started runs: %+v", runs)
	}
}

// The image notes of in.import (IMP-179): the host lists the notes of a
// folder, not those below it, reads a note as stored, and deletes one as
// the pipeline.
func TestPipelineHost_NotesInAndDeleteNote(t *testing.T) {
	s, _ := pipelineServer(t)
	al, err := audit.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetAuditLog(al)
	ctx := context.Background()
	for _, p := range []string{"p/docs/a_img/1.md", "p/docs/a_img/sub/2.md", "p/docs/a_img2/3.md"} {
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": p, "content": "---\ntitle: X\ntype: image\n---\n\nText.\n"})); err != nil || res.IsError {
			t.Fatalf("create %s: %v %+v", p, err, res)
		}
	}
	h := s.PipelineHost().(pipeline.Folders)
	got, err := h.NotesIn("p", "p/docs/a_img")
	if err != nil || len(got) != 1 || got[0] != "p/docs/a_img/1.md" {
		t.Fatalf("NotesIn: %v %v", got, err)
	}
	if _, err := h.NotesIn("p", "q/docs"); err == nil {
		t.Error("a folder of another project is listed")
	}
	n, err := s.PipelineHost().ReadNote("p", "p/docs/a_img/1.md")
	if err != nil || string(n.Raw) != "---\ntitle: X\ntype: image\n---\n\nText.\n" || n.Frontmatter["type"] != "image" {
		t.Fatalf("ReadNote: %+v %v", n, err)
	}
	if err := h.DeleteNote("p", "p/docs/a_img/1.md", "pipeline:p/pipelines/wiki.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.vault.Load("p/docs/a_img/1.md"); err == nil {
		t.Error("the note is still there")
	}
	if got, _ := h.NotesIn("p", "p/docs/a_img"); len(got) != 0 {
		t.Errorf("the index still lists %v", got)
	}
	found := false
	for _, en := range auditEntries(t, al) {
		if en.Path == "p/docs/a_img/1.md" && en.Action == audit.ActionDelete {
			found = en.Source == audit.SourcePipeline && en.Actor == "pipeline:p/pipelines/wiki.md"
		}
	}
	if !found {
		t.Errorf("no pipeline audit entry: %+v", auditEntries(t, al))
	}
	if err := h.DeleteNote("q", "p/docs/a_img2/3.md", "pipeline:x"); err == nil {
		t.Error("a note of another project is deleted")
	}
}

// memory_pipelines lists the plugins with their node types, without their
// addresses (IMP-168).
func TestPipelineTools_ListPlugins(t *testing.T) {
	s, _ := pipelineServer(t)
	reg, err := plugins.New([]plugins.Config{{Name: "docling", URL: "http://127.0.0.1:1/mcp"}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	reg.Check(context.Background())
	s.SetPlugins(reg)
	res, _ := s.handlePipelines(context.Background(), call(map[string]any{"project": "p"}))
	text := resultText(t, res)
	if !strings.Contains(text, `"plugins":[{"name":"docling","status":"down","nodes":[]}]`) || strings.Contains(text, "127.0.0.1") {
		t.Errorf("listing: %s", text)
	}
}

// ListNotes gives note.list the notes of a folder from the index, with
// memory_query's conditions, sort and pages, inside the project only
// (IMP-186).
func TestPipelineHost_ListNotes(t *testing.T) {
	s, _ := pipelineServer(t)
	ctx := context.Background()
	for p, typ := range map[string]string{"p/docs/a.md": "doc", "p/docs/a_img/1.md": "image", "p/docs/sub/b.md": "doc", "p/docs2/c.md": "doc"} {
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": p, "content": "---\ntitle: T\ntype: " + typ + "\n---\n\nText.\n"})); err != nil || res.IsError {
			t.Fatalf("create %s: %v %+v", p, err, res)
		}
	}
	h := s.PipelineHost().(pipeline.NoteLister)
	notImage := []any{map[string]any{"field": "type", "op": "ne", "value": "image"}}
	list := func(q pipeline.NoteQuery) ([]string, int) {
		t.Helper()
		rows, total, err := h.ListNotes("p", q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range rows {
			out = append(out, r.Path+"="+fmt.Sprint(r.Fields["type"]))
		}
		return out, total
	}
	if got, total := list(pipeline.NoteQuery{Folder: "p/docs", Recursive: true, Where: notImage, Sort: "path", Fields: []string{"type"}, Limit: 1}); strings.Join(got, " ") != "p/docs/a.md=doc" || total != 2 {
		t.Errorf("first page %v of %d", got, total)
	}
	if got, _ := list(pipeline.NoteQuery{Folder: "p/docs", Recursive: true, Where: notImage, Sort: "path", Fields: []string{"type"}, Limit: 1, Offset: 1}); strings.Join(got, " ") != "p/docs/sub/b.md=doc" {
		t.Errorf("second page %v", got)
	}
	if got, total := list(pipeline.NoteQuery{Folder: "p/docs", Sort: "path", Limit: 10}); total != 1 || !strings.HasPrefix(got[0], "p/docs/a.md") {
		t.Errorf("not recursive %v of %d", got, total)
	}
	if _, total := list(pipeline.NoteQuery{Folder: "p", Recursive: true, Where: notImage, Sort: "path", Limit: 10}); total < 3 {
		t.Errorf("the whole project: %d", total)
	}
	if _, _, err := h.ListNotes("p", pipeline.NoteQuery{Folder: "q/docs", Recursive: true, Sort: "path", Limit: 10}); err == nil {
		t.Error("a folder of another project is listed")
	}
	if _, _, err := h.ListNotes("p", pipeline.NoteQuery{Folder: "p/docs", Where: "type = doc", Sort: "path", Limit: 10}); err == nil {
		t.Error("a where that is no list passed")
	}
}
