package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/pipeline"
)

// --- a plugin ---

func pngOf(c color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for x := 0; x < 2; x++ {
		for y := 0; y < 2; y++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// fakePlugin is a converter: convert takes a file and gives back its
// markdown, one image as an embedded resource and one as image content.
// It wants the bearer t0k; version is its manifest's.
func fakePlugin(t *testing.T, version *atomic.Value) *httptest.Server {
	t.Helper()
	s := server.NewMCPServer("docling", "1.0.0", server.WithToolCapabilities(false))
	s.AddTool(mcp.NewTool(ManifestTool), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		b, _ := json.Marshal(map[string]any{
			"name": "docling", "version": version.Load(), "gosidian": 1,
			"nodes": []any{map[string]any{
				"name": "convert", "tool": "convert", "description": "A document to markdown", "files": []string{"file"},
				"input_schema": map[string]any{"type": "object"},
			}},
			"permissions": map[string]any{"files": true, "network": []string{}},
		})
		return mcp.NewToolResultText(string(b)), nil
	})
	s.AddTool(mcp.NewTool("convert", mcp.WithObject("file"), mcp.WithBoolean("ocr")), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		file, _ := args["file"].(map[string]any)
		data, err := base64.StdEncoding.DecodeString(fmt.Sprint(file["data"]))
		if err != nil || file["mime"] != "application/pdf" {
			return mcp.NewToolResultError(fmt.Sprintf("bad file %v", file["name"])), nil
		}
		res := mcp.NewToolResultStructured(map[string]any{
			"markdown": "# " + string(data) + "\n\n![](fig-1.png)\n", "name": file["name"], "ocr": args["ocr"],
		}, "converted")
		res.Content = append(res.Content,
			mcp.NewEmbeddedResource(mcp.BlobResourceContents{URI: "file:///fig-1.png", MIMEType: "image/png", Blob: base64.StdEncoding.EncodeToString(pngOf(color.White))}),
			mcp.NewImageContent(base64.StdEncoding.EncodeToString(pngOf(color.Black)), "image/png"),
			mcp.NewEmbeddedResource(mcp.BlobResourceContents{URI: "file:///run.sh", MIMEType: "text/x-sh", Blob: base64.StdEncoding.EncodeToString([]byte("#!/bin/sh"))}),
		)
		return res, nil
	})
	h := server.NewStreamableHTTPServer(s)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t0k" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

type secretsMap map[string]string

func (m secretsMap) Get(name string) (string, error) {
	if v, ok := m[name]; ok {
		return v, nil
	}
	return "", fmt.Errorf("no secret %q", name)
}

// --- a vault ---

type memHost struct {
	mu     sync.Mutex
	notes  map[string]string
	att    map[string][]byte
	actors []string
	types  pipeline.Types
}

func (h *memHost) Pipelines(project string) ([]pipeline.Pipeline, error) { return nil, nil }

func (h *memHost) Pipeline(p string) (pipeline.Pipeline, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.notes[p]
	if !ok {
		return pipeline.Pipeline{}, pipeline.ErrNoNote
	}
	return pipeline.FromNote(p, "p", p, []byte(c), h.types), nil
}

func (h *memHost) Enabled(string) bool { return true }

func (h *memHost) ReadNote(project, p string) (pipeline.NoteData, error) {
	return pipeline.NoteData{}, pipeline.ErrNoNote
}

func (h *memHost) WriteNote(project, p string, content []byte, _ pipeline.WriteMode, actor string) (pipeline.WriteResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.notes[p] = string(content)
	return pipeline.WriteResult{Path: p}, nil
}

func (h *memHost) ReadAttachment(project, p string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if b, ok := h.att[p]; ok {
		return b, nil
	}
	return nil, errors.New("no such attachment")
}

func (h *memHost) StatFile(project, p string) (pipeline.FileStat, error) {
	data, err := h.ReadAttachment(project, p)
	if err != nil {
		return pipeline.FileStat{}, err
	}
	sum := sha256.Sum256(data)
	return pipeline.FileStat{Path: p, Name: path.Base(p), Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}, nil
}

func (h *memHost) OpenFile(project, p string) (io.ReadSeekCloser, pipeline.FileStat, error) {
	st, err := h.StatFile(project, p)
	if err != nil {
		return nil, pipeline.FileStat{}, err
	}
	data, _ := h.ReadAttachment(project, p)
	return nopSeekCloser{bytes.NewReader(data)}, st, nil
}

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

func (h *memHost) StoreAttachment(project, name string, data []byte, actor string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ext := strings.ToLower(path.Ext(name))
	if ext != ".png" && ext != ".pdf" {
		return "", fmt.Errorf("extension %q not allowed", ext)
	}
	sum := sha256.Sum256(data)
	rel := project + "/attachments/" + hex.EncodeToString(sum[:8]) + ext
	h.att[rel] = data
	h.actors = append(h.actors, actor)
	return rel, nil
}

func (h *memHost) Changed(jobs.Run) {}

// note and stored read the host under its lock: the run's goroutines
// still write to it (a run's summary note) after the run has ended.
func (h *memHost) note(p string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.notes[p]
}

func (h *memHost) stored() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.actors...)
}

func newEngine(t *testing.T, reg *Registry) (*pipeline.Engine, *memHost) {
	t.Helper()
	return newEngineConfig(t, reg, pipeline.Config{})
}

func newEngineConfig(t *testing.T, reg *Registry, cfg pipeline.Config) (*pipeline.Engine, *memHost) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	runner := jobs.NewRunner(st, jobs.Options{Workers: 2, Poll: 10 * time.Millisecond, Lease: time.Minute})
	host := &memHost{notes: map[string]string{}, att: map[string][]byte{}}
	e := pipeline.New(host, runner, cfg, nil, nil)
	e.Types().Extend(reg)
	host.types = e.Types()
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { runner.Start(ctx); close(stopped) }()
	t.Cleanup(func() { cancel(); <-stopped; st.Close() })
	return e, host
}

func waitEnd(t *testing.T, e *pipeline.Engine, id string) jobs.Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		run, err := e.Runner().Store().Run(id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status.Terminal() {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run is still %s", run.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const convertPipeline = "---\ntype: pipeline\n---\n\n```pipeline\ntrigger: {on: manual}\nnodes:\n" +
	"  - id: convert\n    use: docling.convert\n    with: {file: \"{{ trigger.file }}\", ocr: true}\n" +
	"  - id: save\n    use: note.write\n    with: {path: out/doc.md, body: \"{{ convert.markdown }}\"}\n```\n"

// --- tests ---

func TestManifest_Check(t *testing.T) {
	good := `{"name":"docling","version":"1.0","gosidian":1,"nodes":[{"name":"convert","tool":"convert","files":["file"],"input_schema":{"type": "object"}}],"permissions":{"files":true}}`
	m, err := ParseManifest([]byte(good), "docling")
	if err != nil {
		t.Fatal(err)
	}
	spaced := strings.Replace(good, `{"type": "object"}`, "{ \"type\" :\n \"object\" }", 1)
	if m2, _ := ParseManifest([]byte(spaced), "docling"); m2 == nil || m2.SHA() != m.SHA() {
		t.Error("the spacing of a schema changes the sha")
	}
	if m3, _ := ParseManifest([]byte(strings.Replace(good, `"1.0"`, `"1.1"`, 1)), "docling"); m3.SHA() == m.SHA() {
		t.Error("a new version keeps the sha")
	}
	for name, bad := range map[string]string{
		"another name":            good,
		"format 2":                strings.Replace(good, `"gosidian":1`, `"gosidian":2`, 1),
		"files without the grant": strings.Replace(good, `"files":true`, `"files":false`, 1),
		"the manifest tool":       strings.Replace(good, `"tool":"convert"`, `"tool":"gosidian_manifest"`, 1),
		"a bad node name":         strings.Replace(good, `"name":"convert"`, `"name":"Convert"`, 1),
	} {
		want := "docling"
		if name == "another name" {
			want = "converter"
		}
		if _, err := ParseManifest([]byte(bad), want); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestNew_RefusesBadRegistrations(t *testing.T) {
	for _, c := range [][]Config{
		{{Name: "Docling", URL: "http://x/mcp"}},
		{{Name: "llm", URL: "http://x/mcp"}},
		{{Name: "docling", URL: "ftp://x/mcp"}},
		{{Name: "docling", URL: "http://x/mcp"}, {Name: "docling", URL: "http://y/mcp"}},
	} {
		if _, err := New(c, t.TempDir(), nil, "test", nil); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
}

// A plugin's nodes exist once its manifest is accepted; they run through
// the plugin with the file's content, and the files it gives back become
// attachments; a manifest that changed stops them until accepted again.
func TestRegistry_AcceptRunAndChange(t *testing.T) {
	var version atomic.Value
	version.Store("1.0")
	ts := fakePlugin(t, &version)
	reg, err := New([]Config{{Name: "docling", URL: ts.URL + "/mcp", TokenSecret: "docling-token"}}, t.TempDir(), secretsMap{"docling-token": "t0k"}, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reg.Check(ctx)
	st, _ := reg.State("docling")
	if st.Status != StatusUnaccepted || st.Version != "1.0" || len(st.SHA) != 64 {
		t.Fatalf("before the accept: %+v", st)
	}
	if reg.Known("docling.convert") {
		t.Error("a node of a manifest not accepted is known")
	}
	if why := reg.Why("docling.convert"); !strings.Contains(why, "not accepted") {
		t.Errorf("why = %q", why)
	}
	if _, err := reg.Accept(ctx, "docling", "deadbeef", "test"); err == nil {
		t.Error("accepted with another sha")
	}
	if _, err := reg.Accept(ctx, "docling", st.SHA[:12], "test"); err != nil {
		t.Fatal(err)
	}
	reg.Check(ctx)
	if st, _ = reg.State("docling"); st.Status != StatusOK || len(st.Nodes) != 1 || st.Nodes[0].Type != "docling.convert" {
		t.Fatalf("after the accept: %+v", st)
	}
	if why := reg.Why("docling.convertx"); !strings.Contains(why, "docling.convert") {
		t.Errorf("why for a missing node = %q", why)
	}
	// The editor's catalog: the accepted node, its schemas, the files it gives back.
	if ns := reg.NodeSchemas(); len(ns) != 1 || ns[0].Type != "docling.convert" || ns[0].Plugin != "docling" ||
		ns[0].Status != StatusOK || !strings.Contains(string(ns[0].Input), `"object"`) || !strings.Contains(string(ns[0].Output), `"files"`) {
		t.Errorf("node schemas: %+v", ns)
	}

	e, host := newEngine(t, reg)
	host.mu.Lock()
	host.notes["p/pipelines/convert.md"] = convertPipeline
	host.att["p/attachments/doc.pdf"] = []byte("%PDF-1.4 Report")
	host.mu.Unlock()
	run, _, err := e.Start(pipeline.StartRequest{Path: "p/pipelines/convert.md", Trigger: pipeline.TriggerManual, Input: map[string]any{"file": "p/attachments/doc.pdf"}})
	if err != nil {
		t.Fatal(err)
	}
	if run = waitEnd(t, e, run.ID); run.Status != jobs.Done {
		steps, _ := e.Runner().Store().Steps(run.ID)
		t.Fatalf("run %s: %s %+v", run.Status, run.Error, steps)
	}
	if got := host.note("p/out/doc.md"); !strings.Contains(got, "# %PDF-1.4 Report") {
		t.Errorf("the note = %q", got)
	}
	steps, _ := e.Runner().Store().Steps(run.ID)
	var out map[string]any
	for _, s := range steps {
		if s.Name == "convert" {
			_ = json.Unmarshal(s.Output, &out)
		}
	}
	files, _ := out["files"].([]any)
	skipped, _ := out["skipped"].([]any)
	if out["name"] != "doc.pdf" || out["ocr"] != true || len(files) != 2 || len(skipped) != 1 {
		t.Fatalf("output = %v", out)
	}
	f0 := files[0].(map[string]any)
	if f0["name"] != "fig-1.png" || !strings.HasPrefix(fmt.Sprint(f0["path"]), "p/attachments/") || !strings.Contains(fmt.Sprint(f0["markdown"]), "/vault-files/p/attachments/") {
		t.Errorf("first file = %v", f0)
	}
	if files[1].(map[string]any)["name"] != "image-1.png" {
		t.Errorf("second file = %v", files[1])
	}
	// The markdown links the image as gosidian links an attachment
	// (IMP-175), and so does the note made of it.
	want := "![](/vault-files/" + fmt.Sprint(f0["path"]) + ")"
	if md, _ := out["markdown"].(string); !strings.Contains(md, want) || strings.Contains(md, "](fig-1.png)") {
		t.Errorf("the markdown = %q, want %s", md, want)
	}
	if got := host.note("p/out/doc.md"); !strings.Contains(got, want) {
		t.Errorf("the note = %q", got)
	}
	if actors := host.stored(); len(actors) != 2 || actors[0] != "pipeline:p/pipelines/convert.md" {
		t.Errorf("stored as %v", actors)
	}

	version.Store("1.1")
	reg.Check(ctx)
	if st, _ = reg.State("docling"); st.Status != StatusChanged || !reg.Known("docling.convert") {
		t.Fatalf("after the change: %+v", st)
	}
	run, _, err = e.Start(pipeline.StartRequest{Path: "p/pipelines/convert.md", Trigger: pipeline.TriggerManual, Input: map[string]any{"file": "p/attachments/doc.pdf"}})
	if err != nil {
		t.Fatal(err)
	}
	run = waitEnd(t, e, run.ID)
	if run.Status != jobs.Failed {
		t.Fatalf("a changed manifest ran: %s", run.Status)
	}
	if conv, _ := e.Runner().Store().Steps(run.ID); !strings.Contains(fmt.Sprint(conv), "another manifest") {
		t.Errorf("steps = %+v", conv)
	}

	if err := reg.Forget("docling"); err != nil {
		t.Fatal(err)
	}
	if reg.Known("docling.convert") {
		t.Error("a forgotten plugin's node is known")
	}
}

func TestRegistry_Down(t *testing.T) {
	var version atomic.Value
	version.Store("1.0")
	ts := fakePlugin(t, &version)
	reg, err := New([]Config{
		{Name: "notoken", URL: ts.URL + "/mcp"},
		{Name: "gone", URL: "http://127.0.0.1:1/mcp"},
		{Name: "nokey", URL: ts.URL + "/mcp", TokenSecret: "x"},
	}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	reg.Check(context.Background())
	for _, s := range reg.States() {
		if s.Status != StatusDown || s.Error == "" {
			t.Errorf("%s: %+v", s.Name, s)
		}
	}
	if s, _ := reg.State("nokey"); !strings.Contains(s.Error, "secrets key") {
		t.Errorf("nokey: %q", s.Error)
	}
}

// refPlugin's node read takes its file by reference (file_refs): it fetches
// the URL whole, then its first three bytes with a range.
func refPlugin(t *testing.T) *httptest.Server {
	t.Helper()
	s := server.NewMCPServer("fetch", "1.0.0", server.WithToolCapabilities(false))
	s.AddTool(mcp.NewTool(ManifestTool), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		b, _ := json.Marshal(map[string]any{
			"name": "fetch", "version": "1.0", "gosidian": 1,
			"nodes":       []any{map[string]any{"name": "read", "tool": "read", "files": []string{"file"}, "file_refs": true}},
			"permissions": map[string]any{"files": true, "network": []string{"gosidian"}},
		})
		return mcp.NewToolResultText(string(b)), nil
	})
	s.AddTool(mcp.NewTool("read", mcp.WithObject("file")), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		file, _ := req.GetArguments()["file"].(map[string]any)
		url, _ := file["url"].(string)
		whole, err := http.Get(url)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		body, _ := io.ReadAll(whole.Body)
		whole.Body.Close()
		r, _ := http.NewRequest(http.MethodGet, url, nil)
		r.Header.Set("Range", "bytes=0-2")
		part, err := http.DefaultClient.Do(r)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		head, _ := io.ReadAll(part.Body)
		part.Body.Close()
		return mcp.NewToolResultStructured(map[string]any{
			"whole": string(body), "status": whole.StatusCode, "head": string(head), "range": part.StatusCode,
			"name": file["name"], "size": file["size"], "sha256": file["sha256"], "data": file["data"], "url": url,
		}, "read"), nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(ts.Close)
	return ts
}

func TestNode_FileRefs(t *testing.T) {
	ts := refPlugin(t)
	var e *pipeline.Engine
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, st, _, err := e.OpenTicketFile(strings.TrimPrefix(r.URL.Path, pipeline.FileTicketPath))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		http.ServeContent(w, r, st.Name, st.ModTime, f)
	}))
	t.Cleanup(files.Close)
	reg, err := New([]Config{{Name: "fetch", URL: ts.URL + "/mcp", FilesURL: files.URL}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reg.Check(ctx)
	st, _ := reg.State("fetch")
	if _, err := reg.Accept(ctx, "fetch", st.SHA, "test"); err != nil {
		t.Fatal(err)
	}
	e, host := newEngine(t, reg)
	host.mu.Lock()
	host.notes["p/pipelines/read.md"] = "---\ntype: pipeline\n---\n\n```pipeline\nnodes: [{id: r, use: fetch.read, with: {file: attachments/doc.pdf}}]\n```\n"
	host.att["p/attachments/doc.pdf"] = []byte("%PDF the whole file")
	host.mu.Unlock()
	run, _, err := e.Start(pipeline.StartRequest{Path: "p/pipelines/read.md", Trigger: pipeline.TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	if run = waitEnd(t, e, run.ID); run.Status != jobs.Done {
		steps, _ := e.Runner().Store().Steps(run.ID)
		t.Fatalf("run %s: %s %+v", run.Status, run.Error, steps)
	}
	steps, _ := e.Runner().Store().Steps(run.ID)
	var out map[string]any
	_ = json.Unmarshal(steps[0].Output, &out)
	sum := sha256.Sum256([]byte("%PDF the whole file"))
	if out["whole"] != "%PDF the whole file" || out["status"] != float64(200) || out["head"] != "%PD" || out["range"] != float64(206) ||
		out["name"] != "doc.pdf" || out["size"] != float64(19) || out["sha256"] != hex.EncodeToString(sum[:]) || out["data"] != nil {
		t.Fatalf("output = %v", out)
	}
	url, _ := out["url"].(string)
	if !strings.HasPrefix(url, files.URL+pipeline.FileTicketPath) {
		t.Errorf("url %q", url)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		res, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == http.StatusNotFound {
			break // the run ended: its tickets went with it
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ticket still works after the run: %d", res.StatusCode)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// listPlugin's node look takes a list of files by reference and says what
// it got: their names, the LLM tokens left in its call's _meta; it reports
// 500 tokens used.
func listPlugin(t *testing.T) *httptest.Server {
	t.Helper()
	s := server.NewMCPServer("agent", "1.0.0", server.WithToolCapabilities(false))
	s.AddTool(mcp.NewTool(ManifestTool), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		b, _ := json.Marshal(map[string]any{
			"name": "agent", "version": "1.0", "gosidian": 1,
			"nodes":       []any{map[string]any{"name": "look", "tool": "look", "files": []string{"docs"}, "file_refs": true}},
			"permissions": map[string]any{"files": true, "network": []string{"gosidian"}},
		})
		return mcp.NewToolResultText(string(b)), nil
	})
	s.AddTool(mcp.NewTool("look", mcp.WithArray("docs")), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		docs, _ := req.GetArguments()["docs"].([]any)
		names := []any{}
		for _, d := range docs {
			m, _ := d.(map[string]any)
			names = append(names, m["name"])
		}
		var left, project, run any
		if req.Params.Meta != nil {
			left = req.Params.Meta.AdditionalFields[MetaLLMTokensLeft]
			project, run = req.Params.Meta.AdditionalFields[MetaProject], req.Params.Meta.AdditionalFields[MetaRun]
		}
		return mcp.NewToolResultStructured(map[string]any{
			"names": names, "left": left, "project": project, "run": run, "usage": map[string]any{"total_tokens": 500},
		}, "looked"), nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(ts.Close)
	return ts
}

func TestNode_FileListAndTokensLeft(t *testing.T) {
	ts := listPlugin(t)
	reg, err := New([]Config{{Name: "agent", URL: ts.URL + "/mcp", FilesURL: "http://files.test"}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reg.Check(ctx)
	st, _ := reg.State("agent")
	if _, err := reg.Accept(ctx, "agent", st.SHA, "test"); err != nil {
		t.Fatal(err)
	}
	e, host := newEngineConfig(t, reg, pipeline.Config{MaxLLMTokens: 2000})
	host.mu.Lock()
	host.notes["p/pipelines/look.md"] = "---\ntype: pipeline\n---\n\n```pipeline\nnodes:\n" +
		"  - {id: a, use: agent.look, with: {docs: [attachments/a.pdf, {path: attachments/b.pdf}]}}\n" +
		"  - {id: b, use: agent.look, with: {docs: [attachments/b.pdf]}}\n```\n"
	host.att["p/attachments/a.pdf"] = []byte("%PDF a")
	host.att["p/attachments/b.pdf"] = []byte("%PDF b")
	host.mu.Unlock()
	run, _, err := e.Start(pipeline.StartRequest{Path: "p/pipelines/look.md", Trigger: pipeline.TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	if run = waitEnd(t, e, run.ID); run.Status != jobs.Done {
		steps, _ := e.Runner().Store().Steps(run.ID)
		t.Fatalf("run %s: %s %+v", run.Status, run.Error, steps)
	}
	steps, _ := e.Runner().Store().Steps(run.ID)
	outs := map[string]map[string]any{}
	for _, s := range steps {
		var out map[string]any
		_ = json.Unmarshal(s.Output, &out)
		outs[s.Name] = out
	}
	if fmt.Sprint(outs["a"]["names"]) != "[a.pdf b.pdf]" || outs["a"]["left"] != float64(2000) {
		t.Errorf("a = %v", outs["a"])
	}
	// The plugin knows the project and the run it works for (IMP-182).
	if outs["a"]["project"] != "p" || outs["a"]["run"] != run.ID {
		t.Errorf("a's _meta: project %v, run %v (want p, %s)", outs["a"]["project"], outs["a"]["run"], run.ID)
	}
	// b sees the 500 tokens a reported.
	if fmt.Sprint(outs["b"]["names"]) != "[b.pdf]" || outs["b"]["left"] != float64(1500) {
		t.Errorf("b = %v", outs["b"])
	}
}

func TestNode_FileListRefused(t *testing.T) {
	var version atomic.Value
	version.Store("1.0.0")
	ts := fakePlugin(t, &version)
	reg, err := New([]Config{{Name: "docling", URL: ts.URL + "/mcp", TokenSecret: "docling"}}, t.TempDir(), secretsMap{"docling": "t0k"}, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reg.Check(ctx)
	st, _ := reg.State("docling")
	if _, err := reg.Accept(ctx, "docling", st.SHA, "test"); err != nil {
		t.Fatal(err)
	}
	ls := listPlugin(t)
	reg2, err := New([]Config{{Name: "agent", URL: ls.URL + "/mcp", FilesURL: "http://files.test"}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	reg2.Check(ctx)
	st2, _ := reg2.State("agent")
	if _, err := reg2.Accept(ctx, "agent", st2.SHA, "test"); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		reg  *Registry
		body string
		want string
	}{
		"one file only": {reg, "{id: c, use: docling.convert, with: {file: [attachments/a.pdf]}}", "takes one file"},
		"empty list":    {reg2, "{id: c, use: agent.look, with: {docs: []}}", "from 1 to 100"},
		"no path":       {reg2, "{id: c, use: agent.look, with: {docs: [{name: x}]}}", "with.docs[0] names no file"},
	} {
		t.Run(name, func(t *testing.T) {
			e, host := newEngine(t, c.reg)
			host.mu.Lock()
			host.notes["p/pipelines/x.md"] = "---\ntype: pipeline\n---\n\n```pipeline\nnodes:\n  - " + c.body + "\n```\n"
			host.att["p/attachments/a.pdf"] = []byte("%PDF a")
			host.mu.Unlock()
			run, _, err := e.Start(pipeline.StartRequest{Path: "p/pipelines/x.md", Trigger: pipeline.TriggerManual})
			if err != nil {
				t.Fatal(err)
			}
			run = waitEnd(t, e, run.ID)
			steps, _ := e.Runner().Store().Steps(run.ID)
			if run.Status != jobs.Failed || len(steps) == 0 || !strings.Contains(steps[0].Error, c.want) {
				t.Fatalf("run %s %q, steps %+v; want %q", run.Status, run.Error, steps, c.want)
			}
		})
	}
}

func TestLinkFiles(t *testing.T) {
	files := []any{
		map[string]any{"name": "fig-1.png", "path": "p/attachments/aa.png"},
		map[string]any{"name": "fig-10.png", "path": "p/attachments/bb.png"},
	}
	out := linkFiles(map[string]any{
		"markdown": "![a](fig-1.png) ![b](fig-10.png) ![c](fig-1.png \"cap\") ![d](<fig-1.png>) [no](fig-1.pngx) fig-1.png",
		"list":     []any{"x ![](fig-10.png)", 3},
		"n":        2,
	}, files).(map[string]any)
	want := "![a](/vault-files/p/attachments/aa.png) ![b](/vault-files/p/attachments/bb.png) ![c](/vault-files/p/attachments/aa.png \"cap\") ![d](/vault-files/p/attachments/aa.png) [no](fig-1.pngx) fig-1.png"
	if out["markdown"] != want {
		t.Errorf("markdown\n got %q\nwant %q", out["markdown"], want)
	}
	if l := out["list"].([]any); l[0] != "x ![](/vault-files/p/attachments/bb.png)" || l[1] != 3 {
		t.Errorf("list %v", l)
	}
	if out["n"] != 2 {
		t.Errorf("n %v", out["n"])
	}
}
