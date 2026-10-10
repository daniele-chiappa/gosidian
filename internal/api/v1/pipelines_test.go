package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
