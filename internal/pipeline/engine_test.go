package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/marks"
	"github.com/gosidian/gosidian/internal/parser"
)

// fakeHost is a vault in memory: notes, attachments, pipeline notes found
// by a ```pipeline block, and one project that runs pipelines.
type fakeHost struct {
	mu      sync.Mutex
	notes   map[string]string
	att     map[string][]byte
	enabled map[string]bool
	types   Types
	changed int
	stored  []string // "actor path" of each StoreAttachment
	deleted []string // the notes DeleteNote deleted
	// lastQuery is the last note.list asked (notelist_test.go).
	lastQuery NoteQuery
	// writers are who wrote each note last (commit_test.go).
	writers map[string]string
	// marks are the sets note.mark put, by path#set (marknode_test.go).
	marks map[string]marks.Set
	// tagging are the projects with pipeline_tags (vaultcommit_test.go).
	tagging map[string]bool
}

func newFakeHost() *fakeHost {
	return &fakeHost{notes: map[string]string{}, att: map[string][]byte{}, enabled: map[string]bool{"p": true}}
}

func (h *fakeHost) Pipelines(project string) ([]Pipeline, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Pipeline
	for path, c := range h.notes {
		proj := path[:strings.Index(path, "/")]
		if (project != "" && proj != project) || !strings.Contains(c, "```pipeline") || !h.enabled[proj] {
			continue
		}
		out = append(out, FromNote(path, proj, path, []byte(c), h.types))
	}
	return out, nil
}

func (h *fakeHost) Pipeline(path string) (Pipeline, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.notes[path]
	if !ok {
		return Pipeline{}, ErrNoNote
	}
	return FromNote(path, path[:strings.Index(path, "/")], path, []byte(c), h.types), nil
}

func (h *fakeHost) Enabled(project string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enabled[project]
}

func (h *fakeHost) ReadNote(project, path string) (NoteData, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !strings.HasPrefix(path, project+"/") {
		return NoteData{}, errors.New("outside the project")
	}
	c, ok := h.notes[path]
	if !ok {
		return NoteData{}, ErrNoNote
	}
	body := c
	if strings.HasPrefix(c, "---\n") {
		if i := strings.Index(c[4:], "---\n"); i >= 0 {
			body = c[4+i+4:]
		}
	}
	fm := parser.ParseFrontmatterFields(parser.ExtractFrontmatterRaw([]byte(c)))
	return NoteData{Path: path, Title: path, Frontmatter: fm, Body: body, ETag: fmt.Sprint(len(c)), Raw: []byte(c)}, nil
}

func (h *fakeHost) WriteNote(project, path string, content []byte, mode WriteMode, actor string) (WriteResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if actor != "pipeline:"+h.runRef(path) && !strings.HasSuffix(path, "-runs.md") {
		return WriteResult{}, fmt.Errorf("written as %q", actor)
	}
	if !strings.HasPrefix(path, project+"/") {
		return WriteResult{}, errors.New("outside the project")
	}
	_, exists := h.notes[path]
	if mode == WriteCreate && exists {
		return WriteResult{}, errors.New("exists")
	}
	if mode == WriteReplace && !exists {
		return WriteResult{}, ErrNoNote
	}
	h.notes[path] = string(content)
	return WriteResult{Path: path, ETag: fmt.Sprint(len(content)), Created: !exists}, nil
}

func (h *fakeHost) ReadAttachment(project, path string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, ok := h.att[path]
	if !ok || !strings.HasPrefix(path, project+"/") {
		return nil, errors.New("no such attachment")
	}
	return b, nil
}

func (h *fakeHost) StatFile(project, path string) (FileStat, error) {
	data, err := h.ReadAttachment(project, path)
	if err != nil {
		return FileStat{}, err
	}
	sum := sha256.Sum256(data)
	return FileStat{Path: path, Name: path[strings.LastIndex(path, "/")+1:], Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}, nil
}

func (h *fakeHost) OpenFile(project, path string) (io.ReadSeekCloser, FileStat, error) {
	st, err := h.StatFile(project, path)
	if err != nil {
		return nil, FileStat{}, err
	}
	data, _ := h.ReadAttachment(project, path)
	return readSeekNopCloser{bytes.NewReader(data)}, st, nil
}

type readSeekNopCloser struct{ *bytes.Reader }

func (readSeekNopCloser) Close() error { return nil }

func (h *fakeHost) StoreAttachment(project, name string, data []byte, actor string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sum := sha256.Sum256(data)
	rel := project + "/attachments/" + hex.EncodeToString(sum[:8]) + strings.ToLower(path.Ext(name))
	if h.att == nil {
		h.att = map[string][]byte{}
	}
	h.att[rel] = data
	h.stored = append(h.stored, actor+" "+rel)
	return rel, nil
}

func (h *fakeHost) Changed(jobs.Run) {
	h.mu.Lock()
	h.changed++
	h.mu.Unlock()
}

// runRef is the pipeline the tests' notes are written by: they have one
// pipeline that writes each, at the paths below. Call it with h.mu held.
func (h *fakeHost) runRef(string) string {
	for p, c := range h.notes {
		if strings.Contains(c, "```pipeline") && (strings.Contains(c, "note.write") ||
			strings.Contains(c, "in.import") || strings.Contains(c, "llm.describe_media")) {
			return p
		}
	}
	return ""
}

// NotesIn and DeleteNote make the fake a Folders.
func (h *fakeHost) NotesIn(project, folder string) ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for p := range h.notes {
		if path.Dir(p) == folder && strings.HasPrefix(p, project+"/") {
			out = append(out, p)
		}
	}
	return out, nil
}

func (h *fakeHost) DeleteNote(project, p, actor string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if actor != "pipeline:"+h.runRef(p) {
		return fmt.Errorf("deleted as %q", actor)
	}
	if _, ok := h.notes[p]; !ok || !strings.HasPrefix(p, project+"/") {
		return ErrNoNote
	}
	delete(h.notes, p)
	h.deleted = append(h.deleted, p)
	return nil
}

func (h *fakeHost) note(path string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.notes[path]
}

type fakeSecrets map[string]string

func (f fakeSecrets) Get(name string) (string, error) {
	if v, ok := f[name]; ok {
		return v, nil
	}
	return "", fmt.Errorf("no secret %q", name)
}

func newEngine(t *testing.T, cfg Config) (*Engine, *fakeHost) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	runner := jobs.NewRunner(st, jobs.Options{Workers: 2, Poll: 10 * time.Millisecond, Lease: time.Minute})
	host := newFakeHost()
	e := New(host, runner, cfg, fakeSecrets{"or_key": "sk-test"}, nil)
	host.types = e.Types()
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { runner.Start(ctx); close(stopped) }()
	t.Cleanup(func() { cancel(); <-stopped; st.Close() })
	return e, host
}

func pipelineNote(src string) string {
	return "---\ntype: pipeline\n---\n\n# A pipeline\n\n```pipeline\n" + src + "\n```\n"
}

func waitStatus(t *testing.T, e *Engine, id string, want jobs.Status) jobs.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		run, err := e.Runner().Store().Run(id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == want {
			return run
		}
		if time.Now().After(deadline) {
			steps, _ := e.Runner().Store().Steps(id)
			t.Fatalf("run is %s (%s), want %s; steps %+v", run.Status, run.Error, want, steps)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func stepOf(t *testing.T, e *Engine, run, name string) jobs.Step {
	t.Helper()
	steps, _ := e.Runner().Store().Steps(run)
	for _, st := range steps {
		if st.Name == name {
			return st
		}
	}
	t.Fatalf("no step %s", name)
	return jobs.Step{}
}

// eventually waits for cond, as Ended runs right after the run's status.
func eventually(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestEngine_RunsReadStarlarkWrite(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/in/doc.md"] = "---\ntitle: Doc\n---\n\nHello world.\n"
	host.notes["p/pipelines/copy.md"] = pipelineNote(`
trigger: {on: external}
nodes:
  - id: read
    use: note.read
    with: {path: "{{ trigger.path }}"}
  - id: upper
    use: starlark
    with:
      input: "{{ read.body }}"
      code: |
        output = {"text": input.upper().strip(), "words": len(input.split())}
  - id: write
    use: note.write
    with:
      path: "out/{{ trigger.name }}"
      title: "Copy of {{ trigger.name }}"
      body: "{{ upper.text }} ({{ upper.words }} words)"
`)
	run, existing, err := e.Start(StartRequest{Path: "p/pipelines/copy.md", Trigger: TriggerExternal,
		Input: map[string]any{"path": "in/doc.md", "name": "doc"}, Actor: "alice"})
	if err != nil || existing {
		t.Fatal(err, existing)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	out := host.note("p/out/doc.md")
	for _, want := range []string{"title: Copy of doc", "pipeline: '[[p/pipelines/copy]]'", "pipeline_run: " + run.ID, "HELLO WORLD. (2 words)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the note written lacks %q:\n%s", want, out)
		}
	}
	eventually(t, func() bool { return strings.Contains(host.note("p/pipelines/copy-runs.md"), ": 1 run, 0 failed") }, "the runs note")
	if host.changed == 0 {
		t.Fatal("no change was published")
	}
}

// A document that arrived is named by note.write: an attachment by a
// wikilink in source, a file of the IN folder, which no wikilink reaches,
// by its path as text in in_file (ADR-035 addendum, BUG-121).
func TestEngine_WriteNamesTheFileItCameFrom(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/pipelines/arrive.md"] = pipelineNote(`
trigger: {on: external}
nodes:
  - id: write
    use: note.write
    with: {path: "out/{{ trigger.name }}.md", body: "x"}
`)
	for name, c := range map[string]struct{ file, want, not string }{
		"attachment": {"p/attachments/a.pdf", "source: '[[p/attachments/a.pdf]]'", "in_file"},
		"in":         {"p/IN/fatture/b.pdf", "in_file: p/IN/fatture/b.pdf", "source"},
		"in short":   {"IN/c.pdf", "in_file: p/IN/c.pdf", "source"},
	} {
		run, _, err := e.Start(StartRequest{Path: "p/pipelines/arrive.md", Trigger: TriggerExternal,
			Input: map[string]any{"file": c.file, "name": strings.ReplaceAll(name, " ", "-")}, Actor: "alice"})
		if err != nil {
			t.Fatal(err)
		}
		waitStatus(t, e, run.ID, jobs.Done)
		out := host.note("p/out/" + strings.ReplaceAll(name, " ", "-") + ".md")
		if !strings.Contains(out, c.want) || strings.Contains(out, c.not+":") {
			t.Errorf("%s: want %q and no %s:\n%s", name, c.want, c.not, out)
		}
	}
}

func TestEngine_StartRefusals(t *testing.T) {
	e, host := newEngine(t, Config{MaxInputBytes: 64})
	host.notes["p/pl.md"] = pipelineNote("trigger: {on: manual}\nnodes: [{id: a, use: note.read, with: {path: x.md}}]")
	host.notes["q/pl.md"] = pipelineNote("nodes: [{id: a, use: note.read, with: {path: x.md}}]")
	host.notes["p/ingest.md"] = pipelineNote("trigger: {on: ingest, kinds: [pdf]}\nnodes: [{id: a, use: note.read, with: {path: x.md}}]")
	host.notes["p/broken.md"] = pipelineNote("nodes: [{id: a, use: shell.run}]")
	cases := map[string]StartRequest{
		"disabled project":   {Path: "q/pl.md", Trigger: TriggerManual},
		"not external":       {Path: "p/ingest.md", Trigger: TriggerExternal},
		"broken spec":        {Path: "p/broken.md", Trigger: TriggerManual},
		"missing":            {Path: "p/none.md", Trigger: TriggerManual},
		"input too large":    {Path: "p/pl.md", Trigger: TriggerManual, Input: strings.Repeat("x", 100)},
		"callback forbidden": {Path: "p/pl.md", Trigger: TriggerManual, CallbackURL: "http://evil.example/"},
	}
	for name, req := range cases {
		if _, _, err := e.Start(req); err == nil {
			t.Errorf("%s: started", name)
		}
	}
}

func TestEngine_WhenEachAndContinue(t *testing.T) {
	e, host := newEngine(t, Config{})
	fails := TypeFunc(func(context.Context, *Env, map[string]any) (any, error) { return nil, errors.New("nope") })
	e.Types().Register("test.fail", fails)
	e.Types().Register("test.echo", TypeFunc(func(_ context.Context, _ *Env, with map[string]any) (any, error) { return with, nil }))
	host.notes["p/pl.md"] = pipelineNote(`
nodes:
  - {id: list, use: starlark, with: {code: "output = [1, 2, 3]"}}
  - {id: each, use: test.echo, each: "{{ list }}", with: {n: "{{ item }}", i: "{{ index }}"}}
  - {id: never, use: test.echo, after: [list], when: "{{ trigger.go }}"}
  - {id: then, use: test.echo, after: [never]}
  - {id: flaky, use: test.fail, after: [list], on_error: continue}
  - {id: tail, use: test.echo, after: [flaky], with: {got: "{{ flaky }}"}}
`)
	run, _, err := e.Start(StartRequest{Path: "p/pl.md", Trigger: TriggerManual, Input: map[string]any{"go": false}})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	var each []map[string]any
	_ = json.Unmarshal(stepOf(t, e, run.ID, "each").Output, &each)
	if len(each) != 3 || each[2]["n"] != float64(3) || each[1]["i"] != float64(1) {
		t.Fatalf("each %v", each)
	}
	if s := stepOf(t, e, run.ID, "never").Status; s != jobs.Skipped {
		t.Fatalf("never %s", s)
	}
	if s := stepOf(t, e, run.ID, "then").Status; s != jobs.Skipped {
		t.Fatalf("then, after a skipped node only, is %s", s)
	}
	if s := stepOf(t, e, run.ID, "flaky").Status; s != jobs.Failed {
		t.Fatalf("flaky %s", s)
	}
	if s := stepOf(t, e, run.ID, "tail").Status; s != jobs.Done {
		t.Fatalf("tail after a node that continues on error is %s", s)
	}
}

func TestEngine_AFailureFailsTheRunAndRetriesFirst(t *testing.T) {
	e, host := newEngine(t, Config{})
	var calls int
	var mu sync.Mutex
	e.Types().Register("test.flaky", TypeFunc(func(context.Context, *Env, map[string]any) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return nil, errors.New("down")
	}))
	host.notes["p/pl.md"] = pipelineNote(`
nodes:
  - {id: a, use: test.flaky, retry: {attempts: 3, backoff: 0s}}
  - {id: b, use: starlark, with: {code: "output = 1"}}
`)
	run, _, _ := e.Start(StartRequest{Path: "p/pl.md", Trigger: TriggerManual})
	got := waitStatus(t, e, run.ID, jobs.Failed)
	if !strings.Contains(got.Error, "a: down") {
		t.Fatalf("error %q", got.Error)
	}
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 3 {
		t.Fatalf("%d calls, want 3", n)
	}
	if s := stepOf(t, e, run.ID, "b").Status; s != jobs.Canceled {
		t.Fatalf("b %s", s)
	}
	eventually(t, func() bool { return strings.Contains(host.note("p/pl-runs.md"), "1 failed") }, "a failure in the runs note")
}

func TestEngine_WaitCallIsResumedFromOutside(t *testing.T) {
	e, host := newEngine(t, Config{PublicURL: "https://g.example"})
	host.notes["p/pl.md"] = pipelineNote(`
nodes:
  - {id: ask, use: starlark, with: {code: "output = ctx['run']['resume_url']"}}
  - {id: wait, use: wait.call, with: {timeout: 1h}}
  - {id: done, use: starlark, with: {input: "{{ wait.answer }}", code: "output = input"}}
`)
	run, _, _ := e.Start(StartRequest{Path: "p/pl.md", Trigger: TriggerManual})
	got := waitStatus(t, e, run.ID, jobs.Waiting)
	var url string
	_ = json.Unmarshal(stepOf(t, e, run.ID, "ask").Output, &url)
	if url != "https://g.example/api/v1/runs/"+run.ID+"/resume?key="+got.ResumeKey {
		t.Fatalf("resume url %q", url)
	}
	if err := e.Runner().Resume(run.ID, "", json.RawMessage(`{"answer":"yes"}`)); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	if out := string(stepOf(t, e, run.ID, "done").Output); out != `"yes"` {
		t.Fatalf("done %s", out)
	}
}

func TestEngine_IngestAndScheduleTriggers(t *testing.T) {
	e, host := newEngine(t, Config{Location: time.UTC})
	host.notes["p/pipelines/arrival.md"] = pipelineNote(`
trigger: {on: ingest, kinds: [pdf], folder: inbox}
nodes: [{id: a, use: starlark, with: {input: "{{ trigger }}", code: "output = input['stem'] + '/' + input['folder']"}}]
`)
	host.notes["p/pipelines/weekly.md"] = pipelineNote(`
trigger: {on: schedule, every: "day 09:00"}
nodes: [{id: a, use: starlark, with: {code: "output = 1"}}]
`)
	e.OnAttachment("p", "p/attachments/ab.pdf", "Report 1.pdf", "p/inbox/report.md", "alice")
	e.OnAttachment("p", "p/attachments/ab.pdf", "Report 1.pdf", "p/inbox/report.md", "alice") // the same document again
	e.OnAttachment("p", "p/attachments/cd.png", "x.png", "p/inbox/x.md", "alice")             // another kind
	e.OnAttachment("p", "p/attachments/ef.pdf", "y.pdf", "p/other/y.md", "alice")             // another folder
	runs, _ := e.Runner().Store().Runs(jobs.RunFilter{Ref: "p/pipelines/arrival.md"})
	if len(runs) != 1 {
		t.Fatalf("%d ingest runs, want 1", len(runs))
	}
	waitStatus(t, e, runs[0].ID, jobs.Done)
	if out := string(stepOf(t, e, runs[0].ID, "a").Output); out != `"Report 1/inbox"` {
		t.Fatalf("trigger input %s", out)
	}

	e.SetClock(func() time.Time { return time.Date(2026, 10, 10, 9, 3, 0, 0, time.UTC) })
	e.TickSchedules(10 * time.Minute)
	e.TickSchedules(10 * time.Minute)
	if runs, _ := e.Runner().Store().Runs(jobs.RunFilter{Ref: "p/pipelines/weekly.md"}); len(runs) != 1 {
		t.Fatalf("%d schedule runs for one slot", len(runs))
	}
	e.SetClock(func() time.Time { return time.Date(2026, 10, 10, 11, 0, 0, 0, time.UTC) })
	e.TickSchedules(10 * time.Minute)
	if runs, _ := e.Runner().Store().Runs(jobs.RunFilter{Ref: "p/pipelines/weekly.md"}); len(runs) != 1 {
		t.Fatal("a slot two hours old ran late")
	}
}

func TestEngine_LLMDescribeSendsTheImage(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		_, _ = io.WriteString(w, `{"model":"vis-1","choices":[{"message":{"content":" A red square. "}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer srv.Close()
	e, host := newEngine(t, Config{Providers: []Provider{{Name: "or", BaseURL: srv.URL + "/v1", APIKeySecret: "or_key", Model: "vis-1"}}, MaxLLMTokens: 1100})
	host.att["p/attachments/a.png"] = []byte("PNGDATA")
	host.notes["p/pl.md"] = pipelineNote(`
nodes:
  - {id: d, use: llm.describe, with: {image: attachments/a.png, max_tokens: 1000}}
  - {id: again, use: llm.chat, with: {prompt: "more", max_tokens: 1100}}
`)
	run, _, _ := e.Start(StartRequest{Path: "p/pl.md", Trigger: TriggerManual})
	failed := waitStatus(t, e, run.ID, jobs.Failed)
	if auth != "Bearer sk-test" {
		t.Fatalf("auth %q", auth)
	}
	msgs, _ := got["messages"].([]any)
	content := msgs[0].(map[string]any)["content"].([]any)
	url := content[1].(map[string]any)["image_url"].(map[string]any)["url"].(string)
	if url != "data:image/png;base64,UE5HREFUQQ==" {
		t.Fatalf("image url %q", url)
	}
	var d map[string]any
	_ = json.Unmarshal(stepOf(t, e, run.ID, "d").Output, &d)
	if d["text"] != "A red square." || d["image"] != "p/attachments/a.png" {
		t.Fatalf("describe output %v", d)
	}
	// 15 tokens used, and 1100 more could go beyond the cap of 1100.
	if !strings.Contains(failed.Error, "again:") || !strings.Contains(failed.Error, "max_llm_tokens") {
		t.Fatalf("run error %q", failed.Error)
	}
}

// llm.chat passes reasoning_effort on, and gives the finish reason and a
// thinking model's reasoning: an empty text that ran out of max_tokens
// while the model thought shows as such.
func TestEngine_LLMChatReasoning(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		_, _ = io.WriteString(w, `{"model":"think-1","choices":[{"message":{"content":"","reasoning":"Let me think..."},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":600,"total_tokens":610}}`)
	}))
	defer srv.Close()
	e, host := newEngine(t, Config{Providers: []Provider{{Name: "default", BaseURL: srv.URL + "/v1", Model: "think-1"}}})
	host.notes["p/think.md"] = pipelineNote(`
nodes:
  - {id: c, use: llm.chat, with: {prompt: "judge", json: true, reasoning_effort: none, max_tokens: 600}}
`)
	run, _, _ := e.Start(StartRequest{Path: "p/think.md", Trigger: TriggerManual})
	waitStatus(t, e, run.ID, jobs.Done)
	if got["reasoning_effort"] != "none" {
		t.Errorf("the request has reasoning_effort %v", got["reasoning_effort"])
	}
	var out map[string]any
	_ = json.Unmarshal(stepOf(t, e, run.ID, "c").Output, &out)
	if out["finish_reason"] != "length" || out["reasoning"] != "Let me think..." || out["text"] != "" {
		t.Errorf("output %v", out)
	}
}

func TestEngine_HTTPRequestAllowlist(t *testing.T) {
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()
	e, host := newEngine(t, Config{HTTPAllowlist: []string{srv.URL + "/api/"}})
	host.notes["p/ok.md"] = pipelineNote("nodes: [{id: a, use: http.request, with: {url: '" + srv.URL + "/api/x', method: post, json: {q: 1}}}]")
	host.notes["p/no.md"] = pipelineNote("nodes: [{id: a, use: http.request, with: {url: '" + srv.URL + "/admin'}}]")
	ok, _, _ := e.Start(StartRequest{Path: "p/ok.md", Trigger: TriggerManual})
	waitStatus(t, e, ok.ID, jobs.Done)
	var out map[string]any
	_ = json.Unmarshal(stepOf(t, e, ok.ID, "a").Output, &out)
	if out["status"] != float64(200) || out["json"].(map[string]any)["ok"] != true {
		t.Fatalf("output %v", out)
	}
	no, _, _ := e.Start(StartRequest{Path: "p/no.md", Trigger: TriggerManual})
	got := waitStatus(t, e, no.ID, jobs.Failed)
	if !strings.Contains(got.Error, "http_allowlist") {
		t.Fatalf("error %q", got.Error)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Fatalf("%d hits, the refused call went out", hits)
	}
}

func TestEngine_CallbackGetsTheSummary(t *testing.T) {
	got := make(chan RunSummary, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var s RunSummary
		_ = json.NewDecoder(r.Body).Decode(&s)
		got <- s
	}))
	defer srv.Close()
	e, host := newEngine(t, Config{HTTPAllowlist: []string{srv.URL}})
	host.notes["p/pl.md"] = pipelineNote("trigger: {on: external}\nnodes: [{id: a, use: starlark, with: {code: 'output = 1'}}]")
	run, _, err := e.Start(StartRequest{Path: "p/pl.md", Trigger: TriggerExternal, CallbackURL: srv.URL + "/done"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s.Run != run.ID || s.Status != jobs.Done || s.Steps["a"] != "done" {
			t.Fatalf("summary %+v", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no callback")
	}
}

func TestStarlark_Sandbox(t *testing.T) {
	e, host := newEngine(t, Config{StarlarkSteps: 10_000})
	host.notes["p/load.md"] = pipelineNote("nodes: [{id: a, use: starlark, with: {code: \"load('x.star', 'y')\\noutput = 1\"}}]")
	host.notes["p/loop.md"] = pipelineNote("nodes: [{id: a, use: starlark, with: {code: \"def f():\\n  n = 0\\n  for i in range(100000000):\\n    n += i\\n  return n\\noutput = f()\"}}]")
	host.notes["p/none.md"] = pipelineNote("nodes: [{id: a, use: starlark, with: {code: 'x = 1'}}]")
	host.notes["p/while.md"] = pipelineNote("nodes: [{id: a, use: starlark, with: {code: \"n = 0\\nwhile True:\\n  n += 1\\noutput = n\"}}]")
	for path, want := range map[string]string{"p/load.md": "load is not available", "p/loop.md": "too many steps", "p/none.md": "sets no output", "p/while.md": "too many steps"} {
		run, _, err := e.Start(StartRequest{Path: path, Trigger: TriggerManual})
		if err != nil {
			t.Fatal(err)
		}
		got := waitStatus(t, e, run.ID, jobs.Failed)
		if !strings.Contains(got.Error, want) {
			t.Errorf("%s: error %q, want %q", path, got.Error, want)
		}
	}
}

// A node's code is a script: loops and ifs at the top level, a name set
// again, while (lab, R2: md = md.replace(…) in a for over the files).
func TestStarlark_ScriptAtTopLevel(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/script.md"] = pipelineNote(`nodes:
  - id: a
    use: starlark
    with:
      input: {md: "a fig-1.png b fig-2.png", files: [{name: fig-1.png, path: x/1.png}, {name: fig-2.png, path: x/2.png}]}
      code: |
        md = input["md"]
        for f in input["files"]:
            md = md.replace(f["name"], f["path"])
        n = 0
        while n < 3:
            n += 1
        if n == 3:
            output = {"md": md, "seen": set([1, 1, 2]) == set([1, 2])}`)
	run, _, err := e.Start(StartRequest{Path: "p/script.md", Trigger: TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	var out map[string]any
	_ = json.Unmarshal(stepOf(t, e, run.ID, "a").Output, &out)
	if out["md"] != "a x/1.png b x/2.png" || out["seen"] != true {
		t.Errorf("output = %v", out)
	}
}

// A node reads when its run was made: a report dates what it writes.
func TestEngine_RunCreated(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/when.md"] = pipelineNote(`nodes:
  - {id: a, use: starlark, with: {input: {at: "{{ run.created }}"}, code: "output = input['at']"}}`)
	before := time.Now().UTC().Truncate(time.Second)
	run, _, err := e.Start(StartRequest{Path: "p/when.md", Trigger: TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	var at string
	_ = json.Unmarshal(stepOf(t, e, run.ID, "a").Output, &at)
	got, err := time.Parse(time.RFC3339, at)
	if err != nil || got.Before(before) || got.After(time.Now().UTC()) {
		t.Errorf("run.created = %q (%v), want a time from %v on", at, err, before)
	}
}

// A node skipped when nothing else runs decides the nodes waiting on it
// at once: one waiting on it alone is skipped too, one waiting also on a
// node done runs, and the run ends done (BUG-126; it stopped failed with
// "it could not start").
func TestEngine_SkipReleasesTheNodesAfter(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/pipelines/skip.md"] = pipelineNote(`
trigger: {on: manual}
nodes:
  - {id: a, use: starlark, with: {code: "output = 1"}}
  - {id: b, use: starlark, when: "{{ trigger.go }}", with: {code: "output = 2"}}
  - {id: c, use: starlark, with: {code: "output = 3"}}
  - {id: d, use: starlark, after: [a, b], with: {code: "output = 4"}}
  - {id: f, use: starlark, after: [c], with: {code: "output = 5"}}
`)
	run, _, err := e.Start(StartRequest{Path: "p/pipelines/skip.md", Trigger: TriggerManual, Input: map[string]any{"go": false}})
	if err != nil {
		t.Fatal(err)
	}
	run = waitStatus(t, e, run.ID, jobs.Done)
	for name, want := range map[string]jobs.Status{"a": jobs.Done, "b": jobs.Skipped, "c": jobs.Skipped, "d": jobs.Done, "f": jobs.Skipped} {
		if st := stepOf(t, e, run.ID, name); st.Status != want {
			t.Errorf("%s: %s, want %s", name, st.Status, want)
		}
	}
}

// A condition may name a node skipped in the same turn: it reads nothing,
// as it would a turn later, and does not fail the node.
func TestEngine_ConditionReadsANodeJustSkipped(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/pipelines/skip.md"] = pipelineNote(`
trigger: {on: manual}
nodes:
  - {id: a, use: starlark, with: {code: "output = 1"}}
  - {id: b, use: starlark, after: [a], when: "{{ trigger.go }}", with: {code: "output = 2"}}
  - {id: d, use: starlark, after: [a, b], unless: "{{ b }}", with: {code: "output = 4"}}
`)
	run, _, err := e.Start(StartRequest{Path: "p/pipelines/skip.md", Trigger: TriggerManual, Input: map[string]any{"go": false}})
	if err != nil {
		t.Fatal(err)
	}
	run = waitStatus(t, e, run.ID, jobs.Done)
	for name, want := range map[string]jobs.Status{"a": jobs.Done, "b": jobs.Skipped, "d": jobs.Done} {
		if st := stepOf(t, e, run.ID, name); st.Status != want {
			t.Errorf("%s: %s, want %s (%s)", name, st.Status, want, st.Error)
		}
	}
}
