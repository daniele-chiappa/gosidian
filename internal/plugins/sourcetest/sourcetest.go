// Package sourcetest is a plugin with one source for the tests of the
// sources of the IN folders (IMP-172 phase 2): an MCP server whose source
// is a folder of the disk, its files served at /files/ with ranges. The Go
// tests run it in-process; scripts/e2e-source wraps it for the browser
// tests. It is no part of the server's image.
package sourcetest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Source is the plugin.
type Source struct {
	// Plugin and Name name the plugin and its one source.
	Plugin, Name string
	// Dir is the source's folder.
	Dir string
	// Base is the plugin's address, for the URLs of its files: set it
	// with SetBase once it serves.
	Base string
	// Changes, Move and SHA say what the source can do: give its changes
	// from a cursor, move its files (SetMove once it serves), give their
	// SHA-256.
	Changes, Move, SHA bool
	// Hosts are its manifest's source_hosts.
	Hosts []string
	// Token, when set, is the bearer it wants on /mcp.
	Token string

	// Opens and Gets count the files opened and the GETs of /files/.
	Opens, Gets atomic.Int64
	// Fail, when set, makes every tool answer it as an error.
	Fail atomic.Value

	mu     sync.Mutex
	tokens map[string]string             // file tokens → path
	snaps  map[string]map[string]fileRow // cursors → what the folder held
}

type fileRow struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	ETag     string    `json:"etag"`
	SHA256   string    `json:"sha256,omitempty"`
	Deleted  bool      `json:"deleted,omitempty"`
}

// Handler serves the plugin: MCP at /mcp, the files at /files/.
func (s *Source) Handler() http.Handler {
	ms := server.NewMCPServer(s.Plugin, "1.0.0", server.WithToolCapabilities(false))
	ms.AddTool(mcp.NewTool("gosidian_manifest"), s.manifest)
	ms.AddTool(mcp.NewTool("gosidian_source_list", mcp.WithString("source"), mcp.WithString("cursor"), mcp.WithNumber("limit")), s.list)
	ms.AddTool(mcp.NewTool("gosidian_source_changes", mcp.WithString("source"), mcp.WithString("cursor")), s.changes)
	ms.AddTool(mcp.NewTool("gosidian_source_open", mcp.WithString("source"), mcp.WithString("path")), s.open)
	ms.AddTool(mcp.NewTool("gosidian_source_move", mcp.WithString("source"), mcp.WithString("from"), mcp.WithString("to")), s.move)
	mcpH := server.NewStreamableHTTPServer(ms)
	mux := http.NewServeMux()
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Token != "" && r.Header.Get("Authorization") != "Bearer "+s.Token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mcpH.ServeHTTP(w, r)
	}))
	mux.HandleFunc("/files/", s.serveFile)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })
	return mux
}

// SetBase changes the address of its files' URLs while it serves.
func (s *Source) SetBase(base string) {
	s.mu.Lock()
	s.Base = base
	s.mu.Unlock()
}

// SetMove changes whether it moves files while it serves: another
// manifest.
func (s *Source) SetMove(move bool) {
	s.mu.Lock()
	s.Move = move
	s.mu.Unlock()
}

func (s *Source) conf() (base string, move bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Base, s.Move
}

func (s *Source) failed() *mcp.CallToolResult {
	if v, _ := s.Fail.Load().(string); v != "" {
		return mcp.NewToolResultError(v)
	}
	return nil
}

func structured(v any) *mcp.CallToolResult {
	b, _ := json.Marshal(v)
	return mcp.NewToolResultText(string(b))
}

func (s *Source) manifest(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	hosts := s.Hosts
	if hosts == nil {
		hosts = []string{}
	}
	_, move := s.conf()
	return structured(map[string]any{
		"name": s.Plugin, "version": "1.0.0", "gosidian": 1, "nodes": []any{},
		"sources": []any{map[string]any{"name": s.Name, "kind": "folder", "description": "A folder of the disk, for the tests",
			"changes": s.Changes, "move": move}},
		"permissions": map[string]any{"files": false, "network": []string{}, "source_hosts": hosts},
	}), nil
}

func (s *Source) check(req mcp.CallToolRequest) *mcp.CallToolResult {
	if r := s.failed(); r != nil {
		return r
	}
	if req.GetString("source", "") != s.Name {
		return mcp.NewToolResultError(fmt.Sprintf("no source %q", req.GetString("source", "")))
	}
	return nil
}

// rows reads the folder: its files, hidden ones too (a marker is one).
func (s *Source) rows() (map[string]fileRow, error) {
	out := map[string]fileRow{}
	err := filepath.WalkDir(s.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(s.Dir, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		r := fileRow{Path: filepath.ToSlash(rel), Size: info.Size(), Modified: info.ModTime().UTC(),
			ETag: fmt.Sprintf("%x-%x", info.Size(), info.ModTime().UnixNano())}
		if s.SHA {
			if b, err := os.ReadFile(p); err == nil {
				sum := sha256.Sum256(b)
				r.SHA256 = hex.EncodeToString(sum[:])
			}
		}
		out[r.Path] = r
		return nil
	})
	return out, err
}

func (s *Source) list(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if r := s.check(req); r != nil {
		return r, nil
	}
	rows, err := s.rows()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	paths := make([]string, 0, len(rows))
	for p := range rows {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	cursor := req.GetString("cursor", "")
	limit := int(req.GetFloat("limit", 100))
	if limit <= 0 {
		limit = 100
	}
	files := []fileRow{}
	next := ""
	for _, p := range paths {
		if p <= cursor {
			continue
		}
		if len(files) == limit {
			next = files[len(files)-1].Path
			break
		}
		files = append(files, rows[p])
	}
	return structured(map[string]any{"files": files, "next": next}), nil
}

func newToken() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// changes diffs the folder against what it held at the cursor.
func (s *Source) changes(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if r := s.check(req); r != nil {
		return r, nil
	}
	if !s.Changes {
		return mcp.NewToolResultError("this source gives no changes"), nil
	}
	now, err := s.rows()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	cursor := req.GetString("cursor", "")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snaps == nil {
		s.snaps = map[string]map[string]fileRow{}
	}
	next := newToken()
	s.snaps[next] = now
	if cursor == "" {
		return structured(map[string]any{"changes": []any{}, "cursor": next}), nil
	}
	before, ok := s.snaps[cursor]
	if !ok {
		return structured(map[string]any{"changes": []any{}, "cursor": next, "reset": true}), nil
	}
	changes := []fileRow{}
	for p, r := range now {
		if b, ok := before[p]; !ok || b.ETag != r.ETag {
			changes = append(changes, r)
		}
	}
	for p, b := range before {
		if _, ok := now[p]; !ok {
			changes = append(changes, fileRow{Path: p, Deleted: true, ETag: b.ETag})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return structured(map[string]any{"changes": changes, "cursor": next}), nil
}

// inside is the file at the source's path p, or an error.
func (s *Source) inside(p string) (string, error) {
	if p == "" || path.IsAbs(p) || path.Clean(p) != p || strings.HasPrefix(p, "../") || p == ".." {
		return "", fmt.Errorf("path %q", p)
	}
	return filepath.Join(s.Dir, filepath.FromSlash(p)), nil
}

func (s *Source) open(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if r := s.check(req); r != nil {
		return r, nil
	}
	p := req.GetString("path", "")
	abs, err := s.inside(p)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return mcp.NewToolResultError("no file " + p), nil
	}
	s.Opens.Add(1)
	tok := newToken()
	s.mu.Lock()
	if s.tokens == nil {
		s.tokens = map[string]string{}
	}
	s.tokens[tok] = p
	s.mu.Unlock()
	base, _ := s.conf()
	return structured(map[string]any{"url": strings.TrimRight(base, "/") + "/files/" + tok, "size": info.Size(),
		"etag": fmt.Sprintf("%x-%x", info.Size(), info.ModTime().UnixNano())}), nil
}

// Expire forgets the URLs given so far, as a source whose links expire.
func (s *Source) Expire() {
	s.mu.Lock()
	s.tokens = nil
	s.mu.Unlock()
}

func (s *Source) serveFile(w http.ResponseWriter, r *http.Request) {
	s.Gets.Add(1)
	s.mu.Lock()
	p, ok := s.tokens[strings.TrimPrefix(r.URL.Path, "/files/")]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "expired", http.StatusForbidden)
		return
	}
	abs, err := s.inside(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, _ := f.Stat()
	http.ServeContent(w, r, path.Base(p), info.ModTime(), f)
}

func (s *Source) move(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if r := s.check(req); r != nil {
		return r, nil
	}
	if _, move := s.conf(); !move {
		return mcp.NewToolResultError("this source does not move files"), nil
	}
	from, err := s.inside(req.GetString("from", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	to, err := s.inside(req.GetString("to", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if _, err := os.Stat(to); err == nil {
		return mcp.NewToolResultError("there already"), nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := os.Rename(from, to); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	info, err := os.Stat(to)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return structured(map[string]any{"path": req.GetString("to", ""), "size": info.Size(),
		"etag": fmt.Sprintf("%x-%x", info.Size(), info.ModTime().UnixNano())}), nil
}
