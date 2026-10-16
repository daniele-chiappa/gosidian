package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/gosidian/gosidian/internal/chatbot"
	"github.com/gosidian/gosidian/internal/jobs"
	mcpsrv "github.com/gosidian/gosidian/internal/mcp"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/plugins"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/vault"
)

const restCopyPipeline = "---\ntitle: Copy\ntype: pipeline\n---\n\n```pipeline\n" + `trigger: {on: external}
nodes:
  - {id: write, use: note.write, with: {path: "out/{{ trigger.name }}", body: "Hello {{ trigger.name }}"}}
` + "```\n"

const restAskPipeline = "---\ntitle: Ask\ntype: pipeline\n---\n\n```pipeline\n" + `trigger: {on: manual}
nodes:
  - {id: ask, use: wait.call}
  - {id: answer, use: note.write, with: {path: "answers/{{ ask.who }}", body: "{{ ask.text }}"}}
` + "```\n"

const restIngestPipeline = "---\ntitle: Arrivals\ntype: pipeline\n---\n\n```pipeline\n" + `trigger: {on: ingest}
nodes:
  - {id: write, use: note.write, with: {path: "out/x", body: "x"}}
` + "```\n"

type pipelineFixture struct {
	*adminFixture
	engine *pipeline.Engine
}

// newPipelineFixture is the admin fixture with an engine on the MCP
// server's host, over the same vault and index, and project Alpha
// running pipelines.
func newPipelineFixture(t *testing.T) *pipelineFixture {
	t.Helper()
	f := newAdminFixture(t)
	if err := f.projects.Set("Alpha", projects.Flags{UsePipelines: true}); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"Alpha/pipelines/copy.md":   restCopyPipeline,
		"Alpha/pipelines/ask.md":    restAskPipeline,
		"Alpha/pipelines/arrive.md": restIngestPipeline,
		"Private/pipelines/copy.md": restCopyPipeline,
		"Alpha/notes/not-a-pipe.md": "# plain\n",
	} {
		if w := f.doAuthRecorder(http.MethodPost, "/api/v1/notes", mustJSON(t, map[string]string{"path": path, "content": content}), nil); w.code != http.StatusCreated {
			t.Fatalf("seed %s: %d %s", path, w.code, w.body)
		}
	}
	s := mcpsrv.New(vault.New(f.vaultRoot), f.idx, nil)
	s.SetProjects(f.projects)
	s.SetPipelinesSwitch(true)
	store, err := jobs.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	runner := jobs.NewRunner(store, jobs.Options{Workers: 2, Poll: 10 * time.Millisecond, Lease: time.Minute})
	e := pipeline.New(s.PipelineHost(), runner, pipeline.Config{Location: time.UTC, PublicURL: "https://vault.example"}, nil, nil)
	s.SetPipelines(e)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { runner.Start(ctx); close(stopped) }()
	t.Cleanup(func() { cancel(); <-stopped; store.Close() })
	f.router.deps.Pipelines = e
	return &pipelineFixture{adminFixture: f, engine: e}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func runOf(t *testing.T, body string) pipeline.RunBrief {
	t.Helper()
	var out struct {
		Run pipeline.RunBrief `json:"run"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.Run.ID == "" {
		t.Fatalf("no run in %s", body)
	}
	return out.Run
}

// waitStatus polls the run until it reaches want.
func (f *pipelineFixture) waitStatus(t *testing.T, id string, want jobs.Status) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		run, err := f.engine.Runner().Store().Run(id)
		if err == nil && run.Status == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s: %s, want %s (%s)", id, run.Status, want, run.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPipelinesREST_Off(t *testing.T) {
	f := newAdminFixture(t)
	for _, p := range []string{"/api/v1/pipelines", "/api/v1/runs", "/api/v1/runs/x", "/api/v1/plugins"} {
		if w := f.doAuthRecorder(http.MethodGet, p, "", nil); w.code != http.StatusNotFound {
			t.Errorf("%s: %d %s", p, w.code, w.body)
		}
	}
	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/version", "", nil); strings.Contains(w.body, "pipelines_enabled") {
		t.Errorf("version says pipelines: %s", w.body)
	}
}

func TestPipelinesREST_StartListAndShow(t *testing.T) {
	f := newPipelineFixture(t)
	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/version", "", nil); !strings.Contains(w.body, `"pipelines_enabled":true`) {
		t.Errorf("version: %s", w.body)
	}
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/pipelines?project=Alpha", "", nil)
	if w.code != http.StatusOK || !strings.Contains(w.body, "Alpha/pipelines/copy.md") || strings.Contains(w.body, "not-a-pipe") || strings.Contains(w.body, "Private/") {
		t.Fatalf("list: %d %s", w.code, w.body)
	}
	w = f.doAuthRecorder(http.MethodPost, "/api/v1/pipelines/Alpha/pipelines/copy.md/runs", `{"input":{"name":"doc"},"idempotency_key":"k1"}`, nil)
	if w.code != http.StatusAccepted {
		t.Fatalf("start: %d %s", w.code, w.body)
	}
	run := runOf(t, w.body)
	if run.Trigger != pipeline.TriggerManual || run.Actor != "owner" {
		t.Errorf("run %+v", run)
	}
	f.waitStatus(t, run.ID, jobs.Done)
	// The same key: the same run, 200.
	w = f.doAuthRecorder(http.MethodPost, "/api/v1/pipelines/Alpha/pipelines/copy.md/runs", "", map[string]string{"Idempotency-Key": "k1"})
	if w.code != http.StatusOK || runOf(t, w.body).ID != run.ID {
		t.Fatalf("again: %d %s", w.code, w.body)
	}
	w = f.doAuthRecorder(http.MethodGet, "/api/v1/runs/"+run.ID, "", nil)
	if w.code != http.StatusOK || !strings.Contains(w.body, `"use":"note.write"`) || !strings.Contains(w.body, `"status":"done"`) || strings.Contains(w.body, "resume_key") {
		t.Fatalf("show: %d %s", w.code, w.body)
	}
	for _, p := range []string{"/api/v1/runs?project=Alpha", "/api/v1/pipelines/Alpha/pipelines/copy.md/runs", "/api/v1/runs?status=done"} {
		if w := f.doAuthRecorder(http.MethodGet, p, "", nil); w.code != http.StatusOK || !strings.Contains(w.body, run.ID) {
			t.Errorf("%s: %d %s", p, w.code, w.body)
		}
	}
	// Refusals: a broken input, a project without use_pipelines, a note
	// that is not a pipeline, a missing one.
	for p, body := range map[string]string{
		"/api/v1/pipelines/Alpha/pipelines/copy.md/runs":   `{"input":"text"}`,
		"/api/v1/pipelines/Private/pipelines/copy.md/runs": `{}`,
		"/api/v1/pipelines/Alpha/notes/not-a-pipe.md/runs": `{}`,
		"/api/v1/pipelines/Alpha/pipelines/none.md/runs":   `{}`,
		"/api/v1/pipelines/Alpha/pipelines/copy.md":        `{}`,
	} {
		if w := f.doAuthRecorder(http.MethodPost, p, body, nil); w.code < 400 {
			t.Errorf("%s %s accepted: %d %s", p, body, w.code, w.body)
		}
	}
}

func TestPipelinesREST_RunToken(t *testing.T) {
	f := newPipelineFixture(t)
	store := f.engine.Runner().Store()
	_, secret, err := store.CreateToken("Alpha/pipelines/copy.md", "scanner", "test")
	if err != nil {
		t.Fatal(err)
	}
	_, ingestSecret, _ := store.CreateToken("Alpha/pipelines/arrive.md", "scanner", "test")
	bearer := map[string]string{"Authorization": "Bearer " + secret}
	w := f.request(http.MethodPost, "/api/v1/pipelines/Alpha/pipelines/copy.md/runs", `{"input":{"name":"scan"}}`, bearer)
	if w.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	run := runOf(t, w.Body.String())
	if run.Trigger != pipeline.TriggerExternal || run.Actor != "run-token:scanner" {
		t.Errorf("run %+v", run)
	}
	f.waitStatus(t, run.ID, jobs.Done)
	// The token starts its pipeline and nothing else.
	for _, c := range []struct {
		method, path string
		headers      map[string]string
		want         int
	}{
		{http.MethodPost, "/api/v1/pipelines/Alpha/pipelines/ask.md/runs", bearer, http.StatusForbidden},
		{http.MethodGet, "/api/v1/pipelines/Alpha/pipelines/copy.md/runs", bearer, http.StatusUnauthorized},
		{http.MethodGet, "/api/v1/runs/" + run.ID, bearer, http.StatusUnauthorized},
		{http.MethodGet, "/api/v1/notes/Alpha/out/scan.md", bearer, http.StatusUnauthorized},
		{http.MethodPost, "/api/v1/pipelines/Alpha/pipelines/copy.md/runs", map[string]string{"Authorization": "Bearer " + jobs.TokenPrefix + "nope"}, http.StatusUnauthorized},
		// An ingest pipeline does not start from outside.
		{http.MethodPost, "/api/v1/pipelines/Alpha/pipelines/arrive.md/runs", map[string]string{"Authorization": "Bearer " + ingestSecret}, http.StatusBadRequest},
	} {
		if w := f.request(c.method, c.path, "", c.headers); w.Code != c.want {
			t.Errorf("%s %s: %d, want %d (%s)", c.method, c.path, w.Code, c.want, w.Body)
		}
	}
	if err := store.RevokeToken(mustTokenID(t, store, "Alpha/pipelines/copy.md")); err != nil {
		t.Fatal(err)
	}
	if w := f.request(http.MethodPost, "/api/v1/pipelines/Alpha/pipelines/copy.md/runs", "", bearer); w.Code != http.StatusUnauthorized {
		t.Errorf("revoked token: %d", w.Code)
	}
}

func mustTokenID(t *testing.T, store *jobs.Store, ref string) string {
	t.Helper()
	list, err := store.Tokens(ref)
	if err != nil || len(list) != 1 {
		t.Fatalf("tokens of %s: %v %v", ref, list, err)
	}
	return list[0].ID
}

func TestPipelinesREST_ResumeAndCancel(t *testing.T) {
	f := newPipelineFixture(t)
	start := func() pipeline.RunBrief {
		w := f.doAuthRecorder(http.MethodPost, "/api/v1/pipelines/Alpha/pipelines/ask.md/runs", "", nil)
		if w.code != http.StatusAccepted {
			t.Fatalf("start: %d %s", w.code, w.body)
		}
		run := runOf(t, w.body)
		f.waitStatus(t, run.ID, jobs.Waiting)
		return run
	}
	run := start()
	full, err := f.engine.Runner().Store().Run(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A wrong key, then the right one without an account.
	if w := f.request(http.MethodPost, "/api/v1/runs/"+run.ID+"/resume?key=wrong", `{"who":"bob"}`, nil); w.Code != http.StatusNotFound {
		t.Errorf("wrong key: %d %s", w.Code, w.Body)
	}
	if w := f.request(http.MethodPost, "/api/v1/runs/"+run.ID+"/resume?key="+full.ResumeKey, `{"who":"bob","text":"yes"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("resume with key: %d %s", w.Code, w.Body)
	}
	f.waitStatus(t, run.ID, jobs.Done)
	// The key does nothing once the run moved on.
	if w := f.request(http.MethodPost, "/api/v1/runs/"+run.ID+"/resume?key="+full.ResumeKey, `{}`, nil); w.Code != http.StatusConflict {
		t.Errorf("resume of a done run: %d %s", w.Code, w.Body)
	}
	// A member without write access cannot cancel; the owner can.
	run = start()
	member := map[string]string{"Authorization": "Bearer " + f.memberToken(t)}
	if w := f.request(http.MethodPost, "/api/v1/runs/"+run.ID+"/cancel", "", member); w.Code != http.StatusNotFound && w.Code != http.StatusForbidden {
		t.Errorf("member cancel: %d %s", w.Code, w.Body)
	}
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/runs/"+run.ID+"/resume", `not json`, nil); w.code != http.StatusBadRequest {
		t.Errorf("resume with a broken body: %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/runs/"+run.ID+"/cancel", "", nil); w.code != http.StatusOK || !strings.Contains(w.body, `"status":"canceled"`) {
		t.Fatalf("cancel: %d %s", w.code, w.body)
	}
}

// TestProjectFlags_UsePipelines covers the IMP-166 project flag.
func TestProjectFlags_UsePipelines(t *testing.T) {
	f := newAdminFixture(t)
	f.seedNote(t, "Alpha/a.md", "x")
	w := f.doAuthRecorder(http.MethodPut, "/api/v1/projects/Alpha", `{"use_pipelines":true}`, nil)
	if w.code != http.StatusOK || !strings.Contains(w.body, `"use_pipelines":true`) || !f.projects.UsesPipelines("Alpha") {
		t.Fatalf("on: %d %s", w.code, w.body)
	}
	w = f.doAuthRecorder(http.MethodPut, "/api/v1/projects/Alpha", `{"use_globals":true}`, nil)
	if w.code != http.StatusOK || !f.projects.UsesPipelines("Alpha") {
		t.Fatalf("another flag cleared it: %d %s", w.code, w.body)
	}
	if w = f.doAuthRecorder(http.MethodPut, "/api/v1/projects/Alpha", `{"use_pipelines":false}`, nil); f.projects.UsesPipelines("Alpha") {
		t.Fatalf("off: %d %s", w.code, w.body)
	}
}

// GET /api/v1/plugins lists the plugins and their status to every signed-in
// account; only the owner sees their addresses and errors (IMP-168).
func TestPluginsREST(t *testing.T) {
	f := newPipelineFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/plugins", "", nil)
	if w.code != http.StatusOK || !strings.Contains(w.body, `"plugins":[]`) {
		t.Fatalf("no plugins: %d %s", w.code, w.body)
	}
	reg, err := plugins.New([]plugins.Config{{Name: "docling", URL: "http://127.0.0.1:1/mcp"}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	reg.Check(context.Background())
	f.router.deps.Plugins = reg
	w = f.doAuthRecorder(http.MethodGet, "/api/v1/plugins", "", nil)
	if w.code != http.StatusOK || !strings.Contains(w.body, `"status":"down"`) || !strings.Contains(w.body, `"url":"http://127.0.0.1:1/mcp"`) || !strings.Contains(w.body, `"error":`) {
		t.Fatalf("owner: %d %s", w.code, w.body)
	}
	member := f.memberToken(t)
	m := f.req(t, http.MethodGet, "/api/v1/plugins", "", member)
	if m.code != http.StatusOK || !strings.Contains(m.body, `"name":"docling"`) || strings.Contains(m.body, "127.0.0.1") || strings.Contains(m.body, `"error"`) {
		t.Fatalf("member: %d %s", m.code, m.body)
	}
}

// The pipeline editor's routes answer with the pipelines off too: the base
// node types with their schemas, and the checks of a block (IMP-169, F4).
func TestPipelineEditorRoutes_PipelinesOff(t *testing.T) {
	f := newAdminFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/pipeline-nodes", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("nodes: %d %s", w.code, w.body)
	}
	var got struct {
		Enabled bool `json:"pipelines_enabled"`
		Nodes   []struct {
			Type   string          `json:"type"`
			Group  string          `json:"group"`
			Input  json.RawMessage `json:"input_schema"`
			Output json.RawMessage `json:"output_schema"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(w.body), &got); err != nil {
		t.Fatal(err)
	}
	types := map[string]bool{}
	for _, n := range got.Nodes {
		types[n.Type] = true
		var in map[string]any
		if n.Group != "base" || json.Unmarshal(n.Input, &in) != nil || in["type"] != "object" {
			t.Errorf("%s: group %q, input %s", n.Type, n.Group, n.Input)
		}
	}
	for _, want := range pipeline.NewRegistry().Names() {
		if !types[want] {
			t.Errorf("no schema for the base node type %s", want)
		}
	}
	if got.Enabled || len(got.Nodes) != len(pipeline.NewRegistry().Names()) {
		t.Errorf("enabled=%v, %d nodes", got.Enabled, len(got.Nodes))
	}

	check := func(src string) map[string]any {
		t.Helper()
		w := f.doAuthRecorder(http.MethodPost, "/api/v1/pipeline-check", mustJSON(t, map[string]any{"source": src}), nil)
		if w.code != http.StatusOK {
			t.Fatalf("check: %d %s", w.code, w.body)
		}
		var out map[string]any
		_ = json.Unmarshal([]byte(w.body), &out)
		return out
	}
	ok := "nodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}\n  - {id: b, use: note.write, with: {path: x, body: \"{{ a }}\"}}\nlayout:\n  a: {x: 0, y: 0}\n"
	if out := check(ok); out["ok"] != true {
		t.Errorf("a good pipeline, with a layout: %v", out)
	}
	if out := check("nodes:\n  - {id: a, use: docling.convert}\n"); out["ok"] != false || !strings.Contains(out["problem"].(string), `no node type "docling.convert"`) {
		t.Errorf("an unknown type: %v", out)
	}
	if out := check("nodes:\n  - {id: a, use: starlark}\n  - {id: b, use: starlark, after: [], with: {input: \"{{ a.x }}\"}}\n"); out["ok"] != false || !strings.Contains(out["problem"].(string), "does not run before it") {
		t.Errorf("a reference to a node that does not run before: %v", out)
	}
}

// With the pipelines on, the catalog says so; the check uses the engine's types.
func TestPipelineEditorRoutes_PipelinesOn(t *testing.T) {
	f := newPipelineFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/pipeline-nodes", "", nil)
	if w.code != http.StatusOK || !strings.Contains(w.body, `"pipelines_enabled":true`) {
		t.Fatalf("nodes: %d %s", w.code, w.body)
	}
}

const restChatPipeline = "---\ntitle: Bot\ntype: pipeline\n---\n\n```pipeline\n" + `trigger: {on: chat}
answer: reply
nodes:
  - {id: reply, use: starlark, with: {input: "{{ trigger.message }}", code: "output = {'text': 'eco: ' + input}"}}
` + "```\n"

// TestPipelinesREST_StartAndWait: a run token of a chat pipeline starts
// an interactive chat run, and a start that waits gets its answer.
func TestPipelinesREST_StartAndWait(t *testing.T) {
	f := newPipelineFixture(t)
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/notes", mustJSON(t, map[string]string{"path": "Alpha/pipelines/bot.md", "content": restChatPipeline}), nil); w.code != http.StatusCreated {
		t.Fatalf("seed: %d %s", w.code, w.body)
	}
	store := f.engine.Runner().Store()
	_, secret, err := store.CreateToken("Alpha/pipelines/bot.md", "widget", "test")
	if err != nil {
		t.Fatal(err)
	}
	bearer := map[string]string{"Authorization": "Bearer " + secret}
	w := f.request(http.MethodPost, "/api/v1/pipelines/Alpha/pipelines/bot.md/runs", `{"input":{"message":"ciao"},"wait":5}`, bearer)
	if w.Code != http.StatusOK {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	var out struct {
		Run    pipeline.RunBrief `json:"run"`
		Answer map[string]any    `json:"answer"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Run.Status != jobs.Done || out.Run.Trigger != pipeline.TriggerChat || !out.Run.Interactive || out.Answer["text"] != "eco: ciao" {
		t.Errorf("start and wait: %s", w.Body)
	}
	// ?wait= does the same; a start that does not wait answers at once.
	if w := f.request(http.MethodPost, "/api/v1/pipelines/Alpha/pipelines/bot.md/runs?wait=5", `{"input":{"message":"due"}}`, bearer); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"eco: due"`) {
		t.Errorf("?wait: %d %s", w.Code, w.Body)
	}
	if w := f.request(http.MethodPost, "/api/v1/pipelines/Alpha/pipelines/bot.md/runs", `{"input":{"message":"tre"}}`, bearer); w.Code != http.StatusAccepted || strings.Contains(w.Body.String(), "answer") {
		t.Errorf("no wait: %d %s", w.Code, w.Body)
	}
	for _, body := range []string{`{"input":{"text":"no message"}}`, `{"input":{"message":"x"},"wait":"soon"}`} {
		if w := f.request(http.MethodPost, "/api/v1/pipelines/Alpha/pipelines/bot.md/runs", body, bearer); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := f.request(http.MethodPost, "/api/v1/pipelines/Alpha/pipelines/bot.md/runs?wait=-1", `{"input":{"message":"x"}}`, bearer); w.Code != http.StatusBadRequest {
		t.Errorf("wait=-1: %d", w.Code)
	}
	// A run that stops at a wait.call comes back as it waits.
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/pipelines/Alpha/pipelines/ask.md/runs", `{"wait":5}`, nil); w.code != http.StatusOK || runOf(t, w.body).Status != jobs.Waiting {
		t.Errorf("a wait.call: %d %s", w.code, w.body)
	}
	// One that waits for the run it started is still at work: 202 once the
	// time passed.
	parent := "---\ntitle: Parent\ntype: pipeline\n---\n\n```pipeline\nnodes:\n  - {id: call, use: pipeline.run, with: {pipeline: pipelines/ask}}\n```\n"
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/notes", mustJSON(t, map[string]string{"path": "Alpha/pipelines/parent.md", "content": parent}), nil); w.code != http.StatusCreated {
		t.Fatalf("seed: %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/pipelines/Alpha/pipelines/parent.md/runs", `{"wait":1}`, nil); w.code != http.StatusAccepted || runOf(t, w.body).Status != jobs.Waiting {
		t.Errorf("a run that waits for its child: %d %s", w.code, w.body)
	}
}

// TestRouter_RunTokenRate: [pipelines] run_token_rate sets the starts of a
// run token in a minute.
func TestRouter_RunTokenRate(t *testing.T) {
	store, err := jobs.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s := mcpsrv.New(vault.New(t.TempDir()), nil, nil)
	e := pipeline.New(s.PipelineHost(), jobs.NewRunner(store, jobs.Options{}), pipeline.Config{RunTokenRate: 2}, nil, nil)
	r := NewRouter(&Deps{Pipelines: e})
	now := time.Now()
	if !r.runLimiter.Allow("token:a", now) || !r.runLimiter.Allow("token:a", now) || r.runLimiter.Allow("token:a", now) {
		t.Error("two starts a minute, not three")
	}
	if r := NewRouter(&Deps{}); !r.runLimiter.Allow("token:a", now) {
		t.Error("without pipelines")
	}
}

// chatPlugins answers chat_configure as a chat plugin would.
type chatPlugins struct{ configured int }

func (c *chatPlugins) CallTool(_ context.Context, _, tool string, args, _ map[string]any) (*mcp.CallToolResult, error) {
	if tool == chatbot.ToolConfigure {
		c.configured++
	}
	b, _ := json.Marshal(map[string]any{"embed_url": "https://chat.example.com/embed/" + fmt.Sprint(args["bot"]), "script_url": "https://chat.example.com/chat.js"})
	return mcp.NewToolResultStructured(json.RawMessage(b), string(b)), nil
}

func (c *chatPlugins) FilesURL(string) string { return "http://gosidian:8080" }

func (c *chatPlugins) State(name string) (plugins.State, bool) {
	return plugins.State{Name: name, Status: plugins.StatusOK}, true
}

type chatSecrets map[string]string

func (s chatSecrets) Get(n string) (string, error) {
	if v, ok := s[n]; ok {
		return v, nil
	}
	return "", errors.New("no such secret")
}
func (s chatSecrets) Set(n, v string) error { s[n] = v; return nil }
func (s chatSecrets) Delete(n string) error { delete(s, n); return nil }

// TestChatREST: the status of a bot's note, its configuration on demand,
// and a token for a user who reads its project.
func TestChatREST(t *testing.T) {
	f := newPipelineFixture(t)
	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/chat/status?note=Alpha/bots/bot.md", "", nil); w.code != http.StatusNotFound {
		t.Errorf("chat off: %d", w.code)
	}
	for path, content := range map[string]string{
		"Alpha/pipelines/bot.md": restChatPipeline,
		"Alpha/bots/bot.md":      "---\ntitle: Bot\ntype: chat\n---\n\n```chat\npipeline: pipelines/bot\nscopes: [{name: tutto}]\n```\n",
		"Alpha/bots/broken.md":   "---\ntitle: Broken\ntype: chat\n---\n\n```chat\ntitle: no pipeline\n```\n",
	} {
		if w := f.doAuthRecorder(http.MethodPost, "/api/v1/notes", mustJSON(t, map[string]string{"path": path, "content": content}), nil); w.code != http.StatusCreated {
			t.Fatalf("seed %s: %d %s", path, w.code, w.body)
		}
	}
	s := mcpsrv.New(vault.New(f.vaultRoot), f.idx, nil)
	s.SetProjects(f.projects)
	s.SetPipelinesSwitch(true)
	s.SetPipelines(f.engine)
	pl := &chatPlugins{}
	chat, err := chatbot.New(s.ChatHost(), pl, f.engine.Runner().Store(), chatSecrets{}, t.TempDir(), chatbot.Options{})
	if err != nil {
		t.Fatal(err)
	}
	f.router.deps.Chat = chat
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/chat/status?note=Alpha/bots/broken.md", "", nil)
	if w.code != http.StatusOK || !strings.Contains(w.body, "pipeline names") {
		t.Errorf("broken: %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/chat/token", `{"note":"Alpha/bots/bot.md"}`, nil); w.code != http.StatusConflict {
		t.Errorf("a token before the bot is configured: %d %s", w.code, w.body)
	}
	w = f.doAuthRecorder(http.MethodPost, "/api/v1/chat/configure", `{"note":"Alpha/bots/bot.md"}`, nil)
	if w.code != http.StatusOK || !strings.Contains(w.body, `"configured":true`) || pl.configured != 1 {
		t.Fatalf("configure: %d %s", w.code, w.body)
	}
	w = f.doAuthRecorder(http.MethodPost, "/api/v1/chat/token", `{"note":"Alpha/bots/bot.md"}`, nil)
	if w.code != http.StatusOK || !strings.Contains(w.body, `"embed_url":"https://chat.example.com/embed/bot"`) || strings.Count(w.body, ".") < 3 {
		t.Errorf("token: %d %s", w.code, w.body)
	}
	w = f.doAuthRecorder(http.MethodGet, "/api/v1/chat/status?note=Alpha/bots/bot.md", "", nil)
	if w.code != http.StatusOK || !strings.Contains(w.body, `"bot":"bot"`) || !strings.Contains(w.body, `"state":{`) || strings.Contains(w.body, "token_id\":\"\"") {
		t.Errorf("status: %d %s", w.code, w.body)
	}
	for _, p := range []string{"/api/v1/chat/status?note=Private/bots/x.md", "/api/v1/chat/status?note=Alpha/notes/not-a-pipe.md", "/api/v1/chat/status"} {
		if w := f.doAuthRecorder(http.MethodGet, p, "", nil); w.code < 400 {
			t.Errorf("%s: %d %s", p, w.code, w.body)
		}
	}
	// The run token gosidian made starts the bot's pipeline.
	tokens, _ := f.engine.Runner().Store().Tokens("Alpha/pipelines/bot.md")
	if len(tokens) != 1 || tokens[0].Name != "chat:chat/bot" {
		t.Errorf("tokens: %+v", tokens)
	}
}
