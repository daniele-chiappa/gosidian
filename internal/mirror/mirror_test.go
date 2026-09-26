package mirror

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/mcp"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/vault"
)

// fixture runs the real MCP HTTP handler on a vault where proj allows
// local mirrors and closed does not.
type fixture struct {
	v     *vault.Vault
	url   string
	token string
	dir   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	v := vault.New(t.TempDir())
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	tokens, err := auth.Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := tokens.Create("mirror", nil, []string{auth.ScopeRead}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pstore.Set("proj", projects.Flags{AllowLocalMirror: true}); err != nil {
		t.Fatal(err)
	}
	s := mcp.New(v, idx, tokens)
	s.SetProjects(pstore)
	srv := httptest.NewServer(s.Handler("/mcp"))
	t.Cleanup(srv.Close)
	return &fixture{v: v, url: srv.URL + "/mcp/sse", token: plain, dir: filepath.Join(t.TempDir(), "mirror")}
}

func (f *fixture) save(t *testing.T, path, body string, mtime time.Time) {
	t.Helper()
	if err := f.v.Save(path, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(filepath.Join(f.v.Root, path), mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *fixture) sync(t *testing.T) Result {
	t.Helper()
	res, err := Sync(context.Background(), Options{BaseURL: f.url, Token: f.token, Project: "proj", Dir: f.dir})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	return res
}

func TestSync_CopiesUpdatesAndDeletes(t *testing.T) {
	f := newFixture(t)
	day := func(d int) time.Time { return time.Date(2026, 9, d, 12, 0, 0, 0, time.UTC) }
	f.save(t, "proj/hot.md", "---\ntitle: Hot\ntags: [proj, type:hot]\n---\n\n# Hot\n", day(20))
	f.save(t, "proj/plans/old.md", "---\ntitle: Old plan\n---\n\nold\n", day(1))
	f.save(t, "proj/plans/new.md", "---\ntitle: New plan\n---\n\nnew\n", day(25))
	f.save(t, "closed/x.md", "# not mirrored", time.Time{})

	res := f.sync(t)
	if res.Notes != 3 || res.Added != 3 || res.Updated != 0 || res.Deleted != 0 {
		t.Fatalf("first sync = %+v, want 3 added", res)
	}
	hot := filepath.Join(f.dir, "proj", "hot.md")
	info, err := os.Stat(hot)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Errorf("mirrored file is writable: %v", info.Mode())
	}
	if !info.ModTime().Equal(day(20)) {
		t.Errorf("mtime = %v, want the server's %v", info.ModTime(), day(20))
	}
	if _, err := os.Stat(filepath.Join(f.dir, "closed")); !errors.Is(err, os.ErrNotExist) {
		t.Error("another project leaked into the mirror")
	}

	idx, err := os.ReadFile(filepath.Join(f.dir, "proj", "_index.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(idx)
	if !(strings.Index(text, "New plan") < strings.Index(text, "Hot") && strings.Index(text, "Hot") < strings.Index(text, "Old plan")) {
		t.Errorf("_index.md not sorted from the most recent:\n%s", text)
	}
	if !strings.Contains(text, "`proj/hot.md` · proj, type:hot") {
		t.Errorf("_index.md misses path or tags:\n%s", text)
	}
	readme, err := os.ReadFile(filepath.Join(f.dir, "MIRROR.md"))
	if err != nil || !strings.Contains(string(readme), "| proj |") || strings.Contains(string(readme), f.token) {
		t.Errorf("MIRROR.md = %q (%v): want the project row and never the token", readme, err)
	}

	// A second sync with nothing changed downloads nothing.
	if res := f.sync(t); res.Added+res.Updated+res.Deleted != 0 {
		t.Errorf("idle sync = %+v, want no change", res)
	}

	// Server-side edit, delete and create.
	f.save(t, "proj/hot.md", "---\ntitle: Hot\n---\n\n# Hot, edited\n", day(26))
	if err := f.v.Delete("proj/plans/old.md"); err != nil {
		t.Fatal(err)
	}
	if err := f.v.Delete("proj/plans/new.md"); err != nil {
		t.Fatal(err)
	}
	f.save(t, "proj/docs/added.md", "# Added\n", time.Time{})
	res = f.sync(t)
	if res.Added != 1 || res.Updated != 1 || res.Deleted != 2 {
		t.Fatalf("second sync = %+v, want 1 added, 1 updated, 2 deleted", res)
	}
	if body, _ := os.ReadFile(hot); !strings.Contains(string(body), "edited") {
		t.Errorf("hot.md not refreshed: %s", body)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "proj", "plans")); !errors.Is(err, os.ErrNotExist) {
		t.Error("emptied folder left behind")
	}

	st, err := ReadState(f.dir, "proj")
	if err != nil || len(st.ETags) != 2 || st.Source == "" || strings.HasSuffix(st.Source, "/sse") {
		t.Errorf("state = %+v (%v), want 2 notes and the base URL without /sse", st, err)
	}
}

func TestSync_Refusals(t *testing.T) {
	f := newFixture(t)
	f.save(t, "closed/x.md", "# x", time.Time{})
	_, err := Sync(context.Background(), Options{BaseURL: f.url, Token: f.token, Project: "closed", Dir: f.dir})
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "allow_local_mirror") {
		t.Errorf("flag off: err = %v, want the server's 403 explanation", err)
	}
	if _, err := Sync(context.Background(), Options{BaseURL: f.url, Token: "wrong", Project: "proj", Dir: f.dir}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("bad token: err = %v, want 401", err)
	}
	if _, err := Sync(context.Background(), Options{BaseURL: f.url, Token: f.token, Project: "../x", Dir: f.dir}); err == nil {
		t.Error("an invalid project name must be refused before any request")
	}

	// A running sync holds the lock; a stale lock does not block.
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lp := lockPath(f.dir, "proj")
	if err := os.WriteFile(lp, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(context.Background(), Options{BaseURL: f.url, Token: f.token, Project: "proj", Dir: f.dir}); !errors.Is(err, ErrLocked) {
		t.Errorf("held lock: err = %v, want ErrLocked", err)
	}
	old := time.Now().Add(-2 * staleLock)
	if err := os.Chtimes(lp, old, old); err != nil {
		t.Fatal(err)
	}
	f.save(t, "proj/a.md", "# a", time.Time{})
	if _, err := Sync(context.Background(), Options{BaseURL: f.url, Token: f.token, Project: "proj", Dir: f.dir}); err != nil {
		t.Errorf("stale lock should be taken over: %v", err)
	}
}

func TestPurge(t *testing.T) {
	f := newFixture(t)
	f.save(t, "proj/a.md", "# a", time.Time{})
	f.sync(t)
	if err := Purge(f.dir, "other"); err == nil {
		t.Error("purging a project that is not mirrored must fail")
	}
	if err := Purge(f.dir, "proj"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "proj")); !errors.Is(err, os.ErrNotExist) {
		t.Error("project folder still there")
	}
	if _, err := os.Stat(filepath.Join(f.dir, "MIRROR.md")); !errors.Is(err, os.ErrNotExist) {
		t.Error("MIRROR.md should go with the last project")
	}
	notMirror := t.TempDir()
	if err := Purge(notMirror, ""); err == nil {
		t.Error("purging a folder without MIRROR.md must fail")
	}
}
