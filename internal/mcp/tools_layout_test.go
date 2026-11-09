package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/fonts"
	"github.com/gosidian/gosidian/internal/fonts/fontstest"
	"github.com/gosidian/gosidian/internal/layout"
	"github.com/gosidian/gosidian/internal/plugins"
	"github.com/gosidian/gosidian/internal/uploadquota"
)

// fakeEngine is the plugin's side of the layout tools: it keeps what it is
// handed and notes the calls in order.
type fakeEngine struct {
	mu     sync.Mutex
	calls  []string
	parts  map[string][]byte
	saved  []byte
	gone   bool // every session is over
	active map[string]bool
}

func (e *fakeEngine) note(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, s)
}

func (e *fakeEngine) log() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return strings.Join(e.calls, " ")
}

func (e *fakeEngine) plugin(t *testing.T) *httptest.Server {
	t.Helper()
	names := []string{"engine_open", "engine_put", "engine_new", "engine_commands", "engine_run", "engine_render", "engine_save", "engine_get", "engine_close"}
	tools := make([]any, 0, len(names))
	for _, n := range names {
		tools = append(tools, map[string]any{"name": n, "access": "read", "internal": true})
	}
	s := server.NewMCPServer("layout", "1.0.0", server.WithToolCapabilities(false))
	s.AddTool(mcplib.NewTool(plugins.ManifestTool), func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		b, _ := json.Marshal(map[string]any{"name": "layout", "version": "1.0", "gosidian": 1, "tools": tools,
			"apps": []any{map[string]any{"name": "layout", "title": "Layout", "url": "https://l.example.com/app/",
				"note_type": "layout", "bridge": "layout/2", "engine": "e 1"}},
			"permissions": map[string]any{"files": false, "network": []string{}}})
		return mcplib.NewToolResultText(string(b)), nil
	})
	for _, n := range names {
		s.AddTool(mcplib.NewTool(n), func(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			return e.answer(n, req), nil
		})
	}
	ts := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(ts.Close)
	return ts
}

func (e *fakeEngine) answer(tool string, req mcplib.CallToolRequest) *mcplib.CallToolResult {
	id := req.GetString("session", "")
	e.mu.Lock()
	gone := e.gone || (tool != "engine_open" && !e.active[id])
	e.mu.Unlock()
	if gone {
		return mcplib.NewToolResultError(sessionGone)
	}
	out := func(v any) *mcplib.CallToolResult {
		b, _ := json.Marshal(v)
		return mcplib.NewToolResultText(string(b))
	}
	switch tool {
	case "engine_open":
		e.mu.Lock()
		e.active[id] = true
		e.mu.Unlock()
		e.note("open")
		return out(map[string]any{"session": id})
	case "engine_put":
		what, name := req.GetString("what", ""), req.GetString("name", "")
		data, _ := base64.StdEncoding.DecodeString(req.GetString("data", ""))
		e.mu.Lock()
		key := what + "/" + name
		if req.GetInt("offset", -1) != len(e.parts[key]) {
			e.mu.Unlock()
			return mcplib.NewToolResultErrorf("offset %d", req.GetInt("offset", -1))
		}
		e.parts[key] = append(e.parts[key], data...)
		e.mu.Unlock()
		if !req.GetBool("last", false) {
			e.note("piece")
			return out(map[string]any{})
		}
		e.note(what)
		if what == "file" {
			return out(map[string]any{"path": "/session/" + name})
		}
		return out(map[string]any{"ok": true})
	case "engine_new":
		e.note(fmt.Sprintf("new:%v", req.GetArguments()["params"]))
		return out(map[string]any{"ok": true})
	case "engine_commands":
		e.note("commands:" + req.GetString("filter", ""))
		return out([]any{"frame.create"})
	case "engine_run":
		b, _ := json.Marshal(req.GetArguments()["commands"])
		e.note("run:" + string(b))
		if strings.Contains(string(b), `"bad"`) {
			return mcplib.NewToolResultError(`{"failed":0}`)
		}
		return out(map[string]any{"ran": true})
	case "engine_render":
		e.note(fmt.Sprintf("render:%d", req.GetInt("page", -1)))
		return &mcplib.CallToolResult{Content: []mcplib.Content{mcplib.NewImageContent("cG5n", "image/png"), mcplib.NewTextContent("{}")}}
	case "engine_save":
		e.note("save")
		return out(map[string]any{"bytes": len(e.saved)})
	case "engine_get":
		off, n := req.GetInt("offset", 0), req.GetInt("length", 0)
		end := min(off+n, len(e.saved))
		return out(map[string]any{"data": base64.StdEncoding.EncodeToString(e.saved[off:end]), "eof": end == len(e.saved)})
	case "engine_close":
		e.mu.Lock()
		delete(e.active, id)
		e.mu.Unlock()
		e.note("close")
		return out(map[string]any{"closed": true})
	}
	return mcplib.NewToolResultError("no tool")
}

// testPNG is a real image: attachments check their bytes.
func testPNG(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(onePxPNG)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var layoutTestLimits = layout.Limits{MaxBytes: 64 << 20, MaxDocument: 16 << 20, MaxEntries: 100}

// testDesign is a package with a document and no assets.
func testDesign(t *testing.T, doc string) []byte {
	t.Helper()
	b, err := layout.Pack(&layout.Package{Document: []byte(doc), Assets: map[uint64]layout.File{}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// layoutServer is a server with the engine's plugin accepted, exposed in p.
func layoutServer(t *testing.T) (*Server, *fakeEngine) {
	t.Helper()
	s, _, _ := newTestServer(t)
	e := &fakeEngine{parts: map[string][]byte{}, active: map[string]bool{}, saved: testDesign(t, `{"version":1,"assets":{}}`)}
	ts := e.plugin(t)
	reg, err := plugins.New([]plugins.Config{{Name: "layout", URL: ts.URL + "/mcp"}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reg.Check(ctx)
	st, _ := reg.State("layout")
	if _, err := reg.Accept(ctx, "layout", st.SHA, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Expose("layout", "p", "test"); err != nil {
		t.Fatal(err)
	}
	s.SetPlugins(reg)
	s.SetLayout(layoutTestLimits, fonts.New(t.TempDir()))
	return s, e
}

func layoutConformance(t *testing.T) *Server {
	s, _ := layoutServer(t)
	return s
}

func layoutNoteSetup(t *testing.T, s *Server, ctx context.Context) string {
	t.Helper()
	if res, _ := s.handleCreate(ctx, call(map[string]any{"path": "p/libro.md", "content": "---\ntype: layout\n---\nUn blocco\n"})); res.IsError {
		t.Fatal(expectError(t, res))
	}
	return "p/libro.md"
}

func layoutSessionSetup(t *testing.T, s *Server, ctx context.Context) string {
	t.Helper()
	layoutNoteSetup(t, s, ctx)
	return openLayout(t, s, ctx, "p/libro.md")
}

func layoutSaveCall(ctx context.Context, s *Server, session string) (*mcplib.CallToolResult, error) {
	return s.handleLayoutSave(ctx, call(map[string]any{"session": session}))
}

func openLayout(t *testing.T, s *Server, ctx context.Context, note string) string {
	t.Helper()
	res, err := s.handleLayoutOpen(ctx, call(map[string]any{"note": note}))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	return out.Session
}

func wantRefused(t *testing.T, res *mcplib.CallToolResult, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if got := expectError(t, res); !strings.Contains(got, want) {
		t.Fatalf("want a refusal with %q: %s", want, got)
	}
}

func TestLayoutTools_OnlyWhereTheEngineIsExposed(t *testing.T) {
	s, _, _ := newTestServer(t)
	if has(listToolNames(t, s, context.Background()), "layout_open") {
		t.Fatal("offered without a plugin")
	}
	s, _ = layoutServer(t)
	reader := &auth.Token{ID: "r1", Name: "r", Project: "p", Scopes: []string{auth.ScopeRead}}
	other := &auth.Token{ID: "o1", Name: "o", Project: "q", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}}
	core := &auth.Token{ID: "c1", Name: "c", Project: "p", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}, ToolProfile: auth.ToolProfileCore}
	if names := listToolNames(t, s, ctxWithToken(reader)); !has(names, "layout_open") || !has(names, "layout_run") {
		t.Fatal("not offered to a reader of the project")
	}
	if !has(listToolNames(t, s, ctxWithToken(core)), "layout_save") {
		t.Fatal("not offered to a worker of the project")
	}
	if has(listToolNames(t, s, ctxWithToken(other)), "layout_open") {
		t.Fatal("offered in a project the plugin is not exposed in")
	}
	for name := range listToolNames(t, s, ctxWithToken(reader)) {
		if strings.HasPrefix(name, "layout_engine") {
			t.Fatalf("%s is gosidian's own", name)
		}
	}
}

func TestLayoutOpen_FontsBeforeTheDocumentInPieces(t *testing.T) {
	s, e := layoutServer(t)
	ctx := context.Background()
	if _, _, err := s.fonts.Add(fontstest.Build(fontstest.TrueType, "Prova", "Regular", 0)); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", engineChunk+1000)
	design := testDesign(t, `{"version":1,"assets":{},"big":"`+big+`"}`)
	writeLayoutDesign(t, s, design)
	res, err := s.handleLayoutOpen(ctx, call(map[string]any{"note": "p/libro.md"}))
	if err != nil || res.IsError {
		t.Fatalf("%v %+v", err, res)
	}
	if !strings.Contains(resultText(t, res), `"fonts":["Prova Regular"]`) {
		t.Fatalf("the agent is not told its fonts: %s", resultText(t, res))
	}
	log := e.log()
	if !strings.HasPrefix(log, "open font") || !strings.HasSuffix(log, "package") {
		t.Fatalf("order: %s", log)
	}
	got := e.parts["package/design"]
	u, err := layout.Unpack(got, layoutTestLimits)
	if err != nil || !strings.Contains(string(u.Document), big[:100]) {
		t.Fatalf("the package came apart: %v", err)
	}
}

// writeLayoutDesign stores a design as the note's, as a save does.
func writeLayoutDesign(t *testing.T, s *Server, design []byte) {
	t.Helper()
	layoutNoteSetup(t, s, context.Background())
	writeDesignOver(t, s, design)
}

func TestLayoutOpen_ANoteWithoutDesignGetsANewDocument(t *testing.T) {
	s, e := layoutServer(t)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	res, err := s.handleLayoutOpen(ctx, call(map[string]any{"note": "p/libro.md", "page": map[string]any{"width": 421}}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, resultText(t, res))
	}
	if !strings.Contains(resultText(t, res), `"new":true`) || !strings.Contains(resultText(t, res), `"etag":"none"`) {
		t.Fatal(resultText(t, res))
	}
	if !strings.Contains(e.log(), "new:map[width:421]") {
		t.Fatal(e.log())
	}
}

func TestLayoutOpen_TheSameNoteGivesTheSameSession(t *testing.T) {
	s, e := layoutServer(t)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	first := openLayout(t, s, ctx, "p/libro.md")
	if again := openLayout(t, s, ctx, "p/libro.md"); again != first {
		t.Fatalf("%s then %s", first, again)
	}
	if strings.Count(e.log(), "open") != 1 {
		t.Fatal(e.log())
	}
}

func TestLayoutOpen_Refuses(t *testing.T) {
	s, _ := layoutServer(t)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	res, err := s.handleLayoutOpen(ctx, call(map[string]any{"note": "p/none.md"}))
	wantRefused(t, res, err, "none.md")
	res, err = s.handleLayoutOpen(ctx, call(map[string]any{"note": "p/libro.html"}))
	wantRefused(t, res, err, "markdown")
	if res, _ := s.handleCreate(ctx, call(map[string]any{"path": "q/n.md", "content": "x\n"})); res.IsError {
		t.Fatal(expectError(t, res))
	}
	res, err = s.handleLayoutOpen(ctx, call(map[string]any{"note": "q/n.md"}))
	wantRefused(t, res, err, "not exposed")
	other := ctxWithToken(&auth.Token{ID: "o1", Name: "o", Project: "q", Scopes: []string{auth.ScopeRead}})
	res, err = s.handleLayoutOpen(other, call(map[string]any{"note": "p/libro.md"}))
	wantRefused(t, res, err, "not found")
}

func TestLayoutOpen_AFailureEndsTheSession(t *testing.T) {
	s, e := layoutServer(t)
	ctx := context.Background()
	if _, _, err := s.fonts.Add(fontstest.Build(fontstest.TrueType, "Prova", "Regular", 0)); err != nil {
		t.Fatal(err)
	}
	layoutNoteSetup(t, s, ctx)
	list, _ := s.fonts.List()
	e.parts["font/"+list[0].ID] = []byte("left over") // the offset does not match
	res, err := s.handleLayoutOpen(ctx, call(map[string]any{"note": "p/libro.md"}))
	wantRefused(t, res, err, "offset")
	if !strings.HasSuffix(e.log(), "close") || len(s.layoutSessions) != 0 {
		t.Fatalf("%s %d", e.log(), len(s.layoutSessions))
	}
}

func TestLayoutSession_BelongsToItsToken(t *testing.T) {
	s, _ := layoutServer(t)
	admin := context.Background()
	layoutNoteSetup(t, s, admin)
	one := ctxWithToken(&auth.Token{ID: "t1", Name: "a", Project: "p", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}})
	two := ctxWithToken(&auth.Token{ID: "t2", Name: "b", Project: "p", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}})
	session := openLayout(t, s, one, "p/libro.md")
	for _, h := range []func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error){s.handleLayoutSave, s.handleLayoutRender, s.handleLayoutCommands, s.handleLayoutClose} {
		res, err := h(two, call(map[string]any{"session": session}))
		wantRefused(t, res, err, "no session")
	}
	res, err := s.handleLayoutRun(two, call(map[string]any{"session": session, "commands": []any{map[string]any{"command": "x"}}}))
	wantRefused(t, res, err, "no session")
}

func TestLayoutRun_CopiesAttachmentsIntoTheSession(t *testing.T) {
	s, e := layoutServer(t)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	session := openLayout(t, s, ctx, "p/libro.md")
	img, err := s.layoutStore(ctx, "p", testPNG(t), "shot.png")
	if err != nil {
		t.Fatal(err)
	}
	place := func(p string) map[string]any {
		return map[string]any{"command": "piece.placeImage", "params": map[string]any{"n": 1, "hash": "h", "path": p}}
	}
	res, err := s.handleLayoutRun(ctx, call(map[string]any{"session": session, "commands": []any{place(img), place(img), place("$0.path")}}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, resultText(t, res))
	}
	log := e.log()
	if strings.Count(log, " file") != 1 {
		t.Fatalf("one copy: %s", log)
	}
	if strings.Contains(log, img) || !strings.Contains(log, `"path":"/session/`) || !strings.Contains(log, `"path":"$0.path"`) {
		t.Fatalf("paths: %s", log)
	}
}

func TestLayoutRun_Refuses(t *testing.T) {
	s, _ := layoutServer(t)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	session := openLayout(t, s, ctx, "p/libro.md")
	res, err := s.handleLayoutRun(ctx, call(map[string]any{"session": session}))
	wantRefused(t, res, err, "commands")
	res, err = s.handleLayoutRun(ctx, call(map[string]any{"session": session, "commands": []any{map[string]any{"x": 1}}}))
	wantRefused(t, res, err, "commands[0]")
	res, err = s.handleLayoutRun(ctx, call(map[string]any{"session": session, "commands": []any{
		map[string]any{"command": "file.place", "params": map[string]any{"path": "p/libro.md"}}}}))
	wantRefused(t, res, err, "no attachment")
	// The engine's own refusal comes back, and the session stays.
	res, err = s.handleLayoutRun(ctx, call(map[string]any{"session": session, "commands": []any{map[string]any{"command": "bad"}}}))
	wantRefused(t, res, err, `"failed":0`)
	res, err = s.handleLayoutRender(ctx, call(map[string]any{"session": session}))
	if err != nil || res.IsError {
		t.Fatalf("%v", err)
	}
}

func TestLayoutRun_KeepsAFileOfAnotherProjectOut(t *testing.T) {
	s, _ := layoutServer(t)
	admin := context.Background()
	layoutNoteSetup(t, s, admin)
	img, err := s.layoutStore(admin, "q", testPNG(t), "shot.png")
	if err != nil {
		t.Fatal(err)
	}
	tok := ctxWithToken(&auth.Token{ID: "t1", Name: "a", Project: "p", Scopes: []string{auth.ScopeRead}})
	session := openLayout(t, s, tok, "p/libro.md")
	res, err := s.handleLayoutRun(tok, call(map[string]any{"session": session, "commands": []any{
		map[string]any{"command": "file.place", "params": map[string]any{"path": img}}}}))
	wantRefused(t, res, err, "no attachment")
}

func TestLayoutRender_GivesTheImage(t *testing.T) {
	s, e := layoutServer(t)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	session := openLayout(t, s, ctx, "p/libro.md")
	res, err := s.handleLayoutRender(ctx, call(map[string]any{"session": session, "page": 2}))
	if err != nil || res.IsError {
		t.Fatalf("%v", err)
	}
	if img, ok := res.Content[0].(mcplib.ImageContent); !ok || img.MIMEType != "image/png" {
		t.Fatalf("%#v", res.Content)
	}
	if !strings.Contains(e.log(), "render:2") {
		t.Fatal(e.log())
	}
	s.maxPluginResult = 4
	res, err = s.handleLayoutRender(ctx, call(map[string]any{"session": session}))
	wantRefused(t, res, err, "max_plugin_result")
}

func TestLayoutCommands(t *testing.T) {
	s, e := layoutServer(t)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	session := openLayout(t, s, ctx, "p/libro.md")
	res, err := s.handleLayoutCommands(ctx, call(map[string]any{"session": session, "filter": "frame"}))
	if err != nil || res.IsError || !strings.Contains(resultText(t, res), "frame.create") || !strings.Contains(e.log(), "commands:frame") {
		t.Fatalf("%v %s", err, e.log())
	}
}

func TestLayoutSave_StoresTheDesignAndKeepsTheSession(t *testing.T) {
	s, e := layoutServer(t)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	session := openLayout(t, s, ctx, "p/libro.md")
	res, err := s.handleLayoutSave(ctx, call(map[string]any{"session": session}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, resultText(t, res))
	}
	var saved layout.Saved
	if err := json.Unmarshal([]byte(resultText(t, res)), &saved); err != nil {
		t.Fatal(err)
	}
	note, err := s.vault.Load("p/libro.md")
	if err != nil {
		t.Fatal(err)
	}
	if layout.DesignFile(note.Content) != saved.File || saved.File == "" || !strings.Contains(string(note.Content), "Un blocco") {
		t.Fatalf("%+v\n%s", saved, note.Content)
	}
	// The session goes on from the version it saved, and a package no
	// different is the same file.
	if res, err = s.handleLayoutSave(ctx, call(map[string]any{"session": session})); err != nil || res.IsError {
		t.Fatalf("%v %s", err, resultText(t, res))
	}
	if strings.Count(e.log(), "save") != 2 {
		t.Fatal(e.log())
	}
}

func TestLayoutSave_RefusesADesignSavedSince(t *testing.T) {
	s, _ := layoutServer(t)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	session := openLayout(t, s, ctx, "p/libro.md")
	// Another save, from the window.
	other := writeDesignOver(t, s, testDesign(t, `{"version":1,"assets":{},"other":true}`))
	res, err := s.handleLayoutSave(ctx, call(map[string]any{"session": session}))
	wantRefused(t, res, err, "saved since it was opened")
	note, _ := s.vault.Load("p/libro.md")
	if layout.DesignFile(note.Content) != other {
		t.Fatalf("the other version was lost: %s", note.Content)
	}
}

func writeDesignOver(t *testing.T, s *Server, design []byte) string {
	t.Helper()
	taken, problem := layout.Take(design, layoutTestLimits, func([]byte, string) error { return nil })
	if problem != nil {
		t.Fatal(problem)
	}
	note, err := s.vault.Load("p/libro.md")
	if err != nil {
		t.Fatal(err)
	}
	next, saved, problem := taken.Store("p/libro.md", note.Content, func(data []byte, name string) (string, error) {
		return s.layoutStore(context.Background(), "p", data, name)
	})
	if problem != nil {
		t.Fatal(problem)
	}
	if err := s.writeAndIndex("p/libro.md", next); err != nil {
		t.Fatal(err)
	}
	return saved.File
}

func TestLayoutSave_NeedsWriteOnTheNote(t *testing.T) {
	s, _ := layoutServer(t)
	admin := context.Background()
	layoutNoteSetup(t, s, admin)
	reader := ctxWithToken(&auth.Token{ID: "r1", Name: "r", Project: "p", Scopes: []string{auth.ScopeRead}})
	session := openLayout(t, s, reader, "p/libro.md")
	res, err := s.handleLayoutRender(reader, call(map[string]any{"session": session}))
	if err != nil || res.IsError {
		t.Fatalf("a reader sees the design: %v", err)
	}
	res, err = s.handleLayoutSave(reader, call(map[string]any{"session": session}))
	wantRefused(t, res, err, "write")
	res, err = s.handleLayoutAddPiece(reader, call(map[string]any{"note": "p/libro.md", "text": "x"}))
	wantRefused(t, res, err, "write")
}

func TestLayoutSave_NeedsTheProjectsAttachments(t *testing.T) {
	s, _ := layoutServer(t)
	layoutNoteSetup(t, s, context.Background())
	// A token that writes the note alone cannot leave the design's files.
	tok := ctxWithToken(&auth.Token{ID: "w1", Name: "w", Project: "p", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}, WritePaths: []string{"p/libro.md"}})
	session := openLayout(t, s, tok, "p/libro.md")
	res, err := s.handleLayoutSave(tok, call(map[string]any{"session": session}))
	wantRefused(t, res, err, "writes only in")
}

func TestLayoutSave_CountsAgainstTheUploadQuota(t *testing.T) {
	s, _ := layoutServer(t)
	layoutNoteSetup(t, s, context.Background())
	s.SetUploadQuota(uploadquota.New(10, time.Hour))
	tok := ctxWithToken(&auth.Token{ID: "w1", Name: "w", Project: "p", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}})
	session := openLayout(t, s, tok, "p/libro.md")
	before, _ := s.vault.Load("p/libro.md")
	res, err := s.handleLayoutSave(tok, call(map[string]any{"session": session}))
	if err != nil || !res.IsError {
		t.Fatalf("over the quota: %v %+v", err, res)
	}
	after, _ := s.vault.Load("p/libro.md")
	if string(before.Content) != string(after.Content) {
		t.Fatal("the note changed")
	}
}

func TestLayoutSession_FollowsTheRightsOfItsNote(t *testing.T) {
	s, e := layoutServer(t)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	session := openLayout(t, s, ctx, "p/libro.md")
	if err := s.plugins.Unexpose("layout", "p"); err != nil {
		t.Fatal(err)
	}
	res, err := s.handleLayoutRender(ctx, call(map[string]any{"session": session}))
	wantRefused(t, res, err, "not exposed")
	res, err = s.handleLayoutSave(ctx, call(map[string]any{"session": session}))
	wantRefused(t, res, err, "not exposed")
	// It can still be closed: nothing else would end it but the idle time.
	if res, err = s.handleLayoutClose(ctx, call(map[string]any{"session": session})); err != nil || res.IsError {
		t.Fatalf("%v %+v", err, res)
	}
	if !strings.HasSuffix(e.log(), "close") {
		t.Fatal(e.log())
	}
}

func TestLayoutSave_RefusesAPackageThatDoesNotRead(t *testing.T) {
	s, e := layoutServer(t)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	session := openLayout(t, s, ctx, "p/libro.md")
	e.saved = []byte("no zip")
	before, _ := s.vault.Load("p/libro.md")
	res, err := s.handleLayoutSave(ctx, call(map[string]any{"session": session}))
	wantRefused(t, res, err, "zip")
	after, _ := s.vault.Load("p/libro.md")
	if string(before.Content) != string(after.Content) {
		t.Fatal("the note changed")
	}
}

func TestLayoutSave_RefusesADesignOverTheLimit(t *testing.T) {
	s, e := layoutServer(t)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	session := openLayout(t, s, ctx, "p/libro.md")
	s.layoutLimits.MaxBytes = int64(len(e.saved)) - 1
	res, err := s.handleLayoutSave(ctx, call(map[string]any{"session": session}))
	wantRefused(t, res, err, "layout_max_bytes")
}

func TestLayoutSession_GoneInThePluginIsForgotten(t *testing.T) {
	s, e := layoutServer(t)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	session := openLayout(t, s, ctx, "p/libro.md")
	e.mu.Lock()
	e.gone = true
	e.mu.Unlock()
	res, err := s.handleLayoutRender(ctx, call(map[string]any{"session": session}))
	wantRefused(t, res, err, "layout_open opens the saved design again")
	res, err = s.handleLayoutRender(ctx, call(map[string]any{"session": session}))
	wantRefused(t, res, err, "no session")
}

func TestLayoutClose(t *testing.T) {
	s, e := layoutServer(t)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	session := openLayout(t, s, ctx, "p/libro.md")
	res, err := s.handleLayoutClose(ctx, call(map[string]any{"session": session}))
	if err != nil || res.IsError || !strings.HasSuffix(e.log(), "close") {
		t.Fatalf("%v %s", err, e.log())
	}
	res, err = s.handleLayoutClose(ctx, call(map[string]any{"session": session}))
	wantRefused(t, res, err, "no session")
}

func TestLayoutPieces_AddAndNumber(t *testing.T) {
	s, _ := layoutServer(t)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	res, err := s.handleLayoutNumberPieces(ctx, call(map[string]any{"note": "p/libro.md"}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, resultText(t, res))
	}
	var body struct {
		ETag   string         `json:"etag"`
		Pieces []layout.Piece `json:"pieces"`
		Added  *layout.Piece  `json:"added"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Pieces) != 1 || body.Pieces[0].N != 1 {
		t.Fatalf("%+v", body.Pieces)
	}
	res, err = s.handleLayoutAddPiece(ctx, call(map[string]any{"note": "p/libro.md", "after": 1, "text": "Secondo", "if_match": body.ETag}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, resultText(t, res))
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &body); err != nil {
		t.Fatal(err)
	}
	if body.Added == nil || body.Added.N != 2 || body.Added.Hash == "" {
		t.Fatalf("%+v", body.Added)
	}
	img, err := s.layoutStore(ctx, "p", testPNG(t), "shot.png")
	if err != nil {
		t.Fatal(err)
	}
	res, err = s.handleLayoutAddPiece(ctx, call(map[string]any{"note": "p/libro.md", "image": img, "alt": "Una [foto]"}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, resultText(t, res))
	}
	res, err = s.handleLayoutPieces(ctx, call(map[string]any{"note": "p/libro.md"}))
	if err != nil || res.IsError {
		t.Fatalf("%v", err)
	}
	body.Pieces = nil
	if err := json.Unmarshal([]byte(resultText(t, res)), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Pieces) != 3 || body.Pieces[2].Image == nil || body.Pieces[2].Image.Path != img {
		t.Fatalf("%+v", body.Pieces)
	}
}

func TestLayoutAddPiece_Refuses(t *testing.T) {
	s, _ := layoutServer(t)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	res, err := s.handleLayoutAddPiece(ctx, call(map[string]any{"note": "p/libro.md"}))
	wantRefused(t, res, err, "text or image")
	res, err = s.handleLayoutAddPiece(ctx, call(map[string]any{"note": "p/libro.md", "text": "x", "image": "p/attachments/a.png"}))
	wantRefused(t, res, err, "text or image")
	res, err = s.handleLayoutAddPiece(ctx, call(map[string]any{"note": "p/libro.md", "image": "p/attachments/none.png"}))
	wantRefused(t, res, err, "no attachment")
	res, err = s.handleLayoutAddPiece(ctx, call(map[string]any{"note": "p/libro.md", "text": "x", "if_match": "old"}))
	wantRefused(t, res, err, "etag")
}

func TestLayoutAttachment_StaysInAttachments(t *testing.T) {
	s, _, dir := newTestServer(t)
	if err := os.MkdirAll(filepath.Join(dir, "p"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p", "x.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.layoutAttachment(auth.AdminToken(), "p/x.txt"); got != "" {
		t.Fatalf("%q", got)
	}
}

// A version of the design opens to read and render, and has no save; its
// files come from git when the GC took them. Asking for another version
// while a session is open says so (IMP-201).
func TestLayoutOpen_AVersion(t *testing.T) {
	s, e := layoutServer(t)
	startGit(t, s)
	ctx := context.Background()
	layoutNoteSetup(t, s, ctx)
	first := writeDesignOver(t, s, testDesign(t, `{"version":1,"assets":{},"which":"first"}`))
	if _, err := s.git.TagNote(ctx, "p/libro.md", "", "V1", "", "t"); err != nil {
		t.Fatal(err)
	}
	res, err := s.handleVersions(ctx, call(map[string]any{"path": "p/libro.md", "action": "valid", "tag": "V1"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, res)
	second := writeDesignOver(t, s, testDesign(t, `{"version":1,"assets":{},"which":"second"}`))
	if first == second {
		t.Fatal("one file for two designs")
	}
	if err := os.Remove(filepath.Join(s.vault.Root, filepath.FromSlash(first))); err != nil {
		t.Fatal(err)
	}

	res, err = s.handleLayoutOpen(ctx, call(map[string]any{"note": "p/libro.md", "version": "valid"}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, resultText(t, res))
	}
	var opened struct {
		Session  string `json:"session"`
		ETag     string `json:"etag"`
		ReadOnly bool   `json:"read_only"`
	}
	decodeResult(t, res, &opened)
	if opened.ETag != layout.ETag(first) || !opened.ReadOnly {
		t.Fatalf("opened %+v, first %s", opened, first)
	}

	res, err = s.handleLayoutSave(ctx, call(map[string]any{"session": opened.Session}))
	wantRefused(t, res, err, "reads a version")
	if strings.Contains(e.log(), "save") {
		t.Errorf("the engine saved: %s", e.log())
	}
	note, _ := s.vault.Load("p/libro.md")
	if layout.DesignFile(note.Content) != second {
		t.Errorf("the note: %s", note.Content)
	}

	res, err = s.handleLayoutOpen(ctx, call(map[string]any{"note": "p/libro.md"}))
	wantRefused(t, res, err, "at another version")
	res, err = s.handleLayoutOpen(ctx, call(map[string]any{"note": "p/libro.md", "version": "note/none/V1"}))
	wantRefused(t, res, err, "not a version")
	res, err = s.handleLayoutOpen(ctx, call(map[string]any{"note": "p/libro.md", "version": "valid"}))
	if err != nil || !strings.Contains(resultText(t, res), "already_open") {
		t.Fatalf("again: %v", err)
	}
}
