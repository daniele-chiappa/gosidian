package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/infolder"
	"github.com/gosidian/gosidian/internal/projects"
)

type inUploadFixture struct {
	s   *Server
	in  *infolder.Service
	h   http.Handler
	c   context.Context
	tok *auth.Token
}

func newINUploadFixture(t *testing.T) *inUploadFixture {
	t.Helper()
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pstore.Set("p", projects.Flags{UseIN: true}); err != nil {
		t.Fatal(err)
	}
	s.SetProjects(pstore)
	s.vault.SetINProjects(pstore.UsesIN)
	for _, d := range []string{"p/IN", "q"} {
		if err := os.MkdirAll(filepath.Join(s.vault.Root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svc, err := infolder.Open(filepath.Join(t.TempDir(), "in.db"), s.vault, pstore.INNames, infolder.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })
	s.SetIN(svc)
	tok, _ := ctx.Value(tokenCtxKey).(*auth.Token)
	return &inUploadFixture{s: s, in: svc, h: s.Handler("/mcp"), c: ctx, tok: tok}
}

func (f *inUploadFixture) ctx() context.Context { return f.c }

// open calls the tool's open and returns its answer.
func (f *inUploadFixture) open(t *testing.T, files ...map[string]any) map[string]any {
	t.Helper()
	list := make([]any, len(files))
	for i, x := range files {
		list[i] = x
	}
	res, _ := f.s.handleINUpload(f.ctx(), call(map[string]any{"action": "open", "project": "p", "dir": "lot", "files": list}))
	var out map[string]any
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	// A call outside the HTTP transport has no base path: the handler's.
	if e, _ := out["endpoint"].(string); strings.HasPrefix(e, "/in-upload/") {
		out["endpoint"] = "/mcp" + e
	}
	return out
}

func (f *inUploadFixture) do(method, path, ticket, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if ticket != "" {
		req.Header.Set("Authorization", "Bearer "+ticket)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

// An agent opens an upload with the tool, sends the pieces with the
// ticket, completes it over HTTP, and reads the result (IMP-172 phase 5b).
func TestINUpload_ToolAndPieces(t *testing.T) {
	f := newINUploadFixture(t)
	out := f.open(t, map[string]any{"path": "a.bin", "size": 6})
	ticket, _ := out["ticket"].(string)
	endpoint, _ := out["endpoint"].(string)
	if len(ticket) != 64 || !strings.HasPrefix(endpoint, "/mcp/in-upload/") {
		t.Fatalf("open: %v", out)
	}
	if rec := f.do(http.MethodPut, endpoint+"/files/0?offset=0", "wrong", "abc"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong ticket: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodPut, endpoint+"/files/0?offset=0", "", "abc"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no ticket: %d", rec.Code)
	}
	if rec := f.do(http.MethodPut, endpoint+"/files/0?offset=0", ticket, "abc"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"received":3`) {
		t.Fatalf("a piece: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodPut, endpoint+"/files/0?offset=5", ticket, "zz"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"received":3`) {
		t.Fatalf("the wrong offset: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodPut, endpoint+"/files/0?offset=3", ticket, "def"); rec.Code != http.StatusOK {
		t.Fatalf("the last piece: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodPost, endpoint+"/complete", ticket, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("complete: %d %s", rec.Code, rec.Body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := f.do(http.MethodGet, endpoint+"?files=false", ticket, "")
		if strings.Contains(rec.Body.String(), `"state":"done"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not done: %s", rec.Body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if data, err := os.ReadFile(filepath.Join(f.s.vault.Root, "p", "IN", "lot", "a.bin")); err != nil || string(data) != "abcdef" {
		t.Fatalf("a.bin: %q %v", data, err)
	}
	id := strings.TrimPrefix(endpoint, "/mcp/in-upload/")
	res, _ := f.s.handleINUpload(f.ctx(), call(map[string]any{"action": "status", "project": "p", "id": id, "files_out": true}))
	if txt := resultText(t, res); !strings.Contains(txt, `"result":"new"`) || !strings.Contains(txt, "p/IN/lot/a.bin") {
		t.Fatalf("status: %s", txt)
	}
	// The upload is the token's account's, or the token's.
	if list, _ := f.in.Uploads("p", "token:"+f.tok.ID); len(list) != 1 || list[0].Channel != "mcp" {
		t.Fatalf("the token's uploads: %+v", list)
	}
}

// The same files opened again are the same upload, with a new ticket; the
// old one stops. A revoked token stops its ticket.
func TestINUpload_TicketLife(t *testing.T) {
	f := newINUploadFixture(t)
	first := f.open(t, map[string]any{"path": "a.bin", "size": 4})
	endpoint := first["endpoint"].(string)
	if rec := f.do(http.MethodPut, endpoint+"/files/0?offset=0", first["ticket"].(string), "ab"); rec.Code != http.StatusOK {
		t.Fatalf("a piece: %d %s", rec.Code, rec.Body)
	}
	again := f.open(t, map[string]any{"path": "a.bin", "size": 4})
	if again["endpoint"] != endpoint || again["ticket"] == first["ticket"] {
		t.Fatalf("the same upload, a new ticket: %v", again)
	}
	if !strings.Contains(mustJSON(t, again["upload"]), `"received":2`) {
		t.Fatalf("what arrived: %v", again["upload"])
	}
	if rec := f.do(http.MethodPut, endpoint+"/files/0?offset=2", first["ticket"].(string), "cd"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the old ticket: %d", rec.Code)
	}
	if err := f.s.tokens.Revoke(f.tok.ID); err != nil {
		t.Fatal(err)
	}
	if rec := f.do(http.MethodPut, endpoint+"/files/0?offset=2", again["ticket"].(string), "cd"); rec.Code != http.StatusForbidden {
		t.Fatalf("a revoked token's ticket: %d %s", rec.Code, rec.Body)
	}
}

// The tool refuses a project without an IN folder, and the caps of the
// agents' way in are theirs.
func TestINUpload_Refusals(t *testing.T) {
	f := newINUploadFixture(t)
	res, _ := f.s.handleINUpload(f.ctx(), call(map[string]any{"action": "open", "project": "q", "files": []any{map[string]any{"path": "a", "size": 1}}}))
	if msg := expectError(t, res); !strings.Contains(msg, "no IN folder") {
		t.Fatalf("no IN folder: %s", msg)
	}
	f.in.SetUploadLimits(infolder.UploadLimits{Channels: map[string]infolder.ChannelLimits{"mcp": {MaxFile: 3}, "web": {MaxFile: 100}}})
	res, _ = f.s.handleINUpload(f.ctx(), call(map[string]any{"action": "open", "project": "p", "files": []any{map[string]any{"path": "a", "size": 10}}}))
	if msg := expectError(t, res); !strings.Contains(msg, "[in.mcp]") {
		t.Fatalf("the mcp cap: %s", msg)
	}
	res, _ = f.s.handleINUpload(f.ctx(), call(map[string]any{"action": "open", "project": "p", "files": []any{}}))
	if msg := expectError(t, res); !strings.Contains(msg, "at least one") {
		t.Fatalf("no files: %s", msg)
	}
	res, _ = f.s.handleINUpload(f.ctx(), call(map[string]any{"action": "complete", "project": "p"}))
	if msg := expectError(t, res); !strings.Contains(msg, "id") {
		t.Fatalf("complete without id: %s", msg)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// inUploadServer is a server without a token store (the implicit admin)
// whose project p has its IN folder, for the write conformance suite.
func inUploadServer(t *testing.T) *Server {
	t.Helper()
	s, v, _ := newTestServer(t)
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pstore.Set("p", projects.Flags{UseIN: true}); err != nil {
		t.Fatal(err)
	}
	s.SetProjects(pstore)
	v.SetINProjects(pstore.UsesIN)
	if err := os.MkdirAll(filepath.Join(v.Root, "p", "IN"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc, err := infolder.Open(filepath.Join(t.TempDir(), "in.db"), v, pstore.INNames, infolder.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })
	s.SetIN(svc)
	return s
}

// countingReader counts what the handler read of a request body.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	k, err := c.r.Read(p)
	c.n += int64(k)
	return k, err
}

// A piece refused (a gap, a wrong ticket) is read whole before the
// answer: with a body left unread, Go closes the connection, and the
// client, still sending, sees a reset instead of the 409 or the 401.
// A piece sent again from before the bytes received is written past them.
func TestINUpload_RefusedPieceRead(t *testing.T) {
	f := newINUploadFixture(t)
	out := f.open(t, map[string]any{"path": "a.bin", "size": 8 << 20})
	endpoint, ticket := out["endpoint"].(string), out["ticket"].(string)
	piece := func(offset int, auth string, size int) (*httptest.ResponseRecorder, *countingReader) {
		body := &countingReader{r: strings.NewReader(strings.Repeat("x", size))}
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("%s/files/0?offset=%d", endpoint, offset), body)
		req.Header.Set("Authorization", "Bearer "+auth)
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, req)
		return rec, body
	}
	for name, c := range map[string]struct {
		offset int
		auth   string
		code   int
	}{"a gap": {100, ticket, http.StatusConflict}, "a wrong ticket": {0, "nope", http.StatusUnauthorized}} {
		rec, body := piece(c.offset, c.auth, 2<<20)
		if rec.Code != c.code || body.n != 2<<20 {
			t.Errorf("%s: %d, %d of %d bytes read", name, rec.Code, body.n, 2<<20)
		}
	}
	if rec, _ := piece(0, ticket, 3<<20); rec.Code != http.StatusOK {
		t.Fatalf("a piece: %d %s", rec.Code, rec.Body)
	}
	if rec, _ := piece(1<<20, ticket, 3<<20); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), fmt.Sprintf(`"received":%d`, 4<<20)) {
		t.Fatalf("a piece sent again from before the bytes received: %d %s", rec.Code, rec.Body)
	}
}
