package plugins

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/infolder"
	"github.com/gosidian/gosidian/internal/plugins/sourcetest"
	"github.com/gosidian/gosidian/internal/vault"
)

func TestManifest_Sources(t *testing.T) {
	ok := `{"name":"storage","version":"0.1.0","gosidian":1,"nodes":[],
		"sources":[{"name":"fatture","kind":"webdav","changes":true}],
		"permissions":{"files":false,"network":["nc.lan"],"source_hosts":["nc.lan","10.0.0.5:8443"]}}`
	m, err := ParseManifest([]byte(ok), "storage")
	if err != nil {
		t.Fatal(err)
	}
	if src, found := m.Source("fatture"); !found || !src.Changes || src.Move || src.Kind != "webdav" {
		t.Errorf("source %+v %v", src, found)
	}
	// A manifest without sources reads as before: its sha does not change.
	old := `{"name":"docling","version":"0.2.0","gosidian":1,"nodes":[{"name":"convert","tool":"convert"}],"permissions":{"files":true,"network":[]}}`
	m, err = ParseManifest([]byte(old), "docling")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(m)
	if strings.Contains(string(b), "sources") || strings.Contains(string(b), "source_hosts") {
		t.Errorf("a manifest without sources marshals them: %s", b)
	}
	for name, bad := range map[string]string{
		"none":       `{"name":"x","version":"1","gosidian":1,"nodes":[],"permissions":{}}`,
		"name":       `{"name":"x","version":"1","gosidian":1,"sources":[{"name":"Fatture"}],"permissions":{}}`,
		"twice":      `{"name":"x","version":"1","gosidian":1,"sources":[{"name":"a"},{"name":"a"}],"permissions":{}}`,
		"scheme":     `{"name":"x","version":"1","gosidian":1,"sources":[{"name":"a"}],"permissions":{"source_hosts":["https://nc.lan"]}}`,
		"path":       `{"name":"x","version":"1","gosidian":1,"sources":[{"name":"a"}],"permissions":{"source_hosts":["nc.lan/x"]}}`,
		"userinfo":   `{"name":"x","version":"1","gosidian":1,"sources":[{"name":"a"}],"permissions":{"source_hosts":["u@nc.lan"]}}`,
		"node tool":  `{"name":"x","version":"1","gosidian":1,"nodes":[{"name":"n","tool":"gosidian_source_list"}],"permissions":{}}`,
		"empty host": `{"name":"x","version":"1","gosidian":1,"sources":[{"name":"a"}],"permissions":{"source_hosts":[""]}}`,
	} {
		if _, err := ParseManifest([]byte(bad), "x"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestHostAllowed(t *testing.T) {
	plugin, _ := url.Parse("http://storage:8000/mcp")
	hosts := []string{"nc.lan", "files.example.com:8443"}
	for raw, want := range map[string]bool{
		"http://storage:8000/files/x":           true,
		"http://storage:9000/files/x":           false,
		"http://STORAGE:8000/x":                 true,
		"https://nc.lan/remote.php/x":           true,
		"http://nc.lan:8080/x":                  true,
		"https://files.example.com:8443/x":      true,
		"https://files.example.com/x":           false,
		"https://evil.example.com/x":            false,
		"https://nc.lan.evil.example.com/x":     false,
		"http://169.254.169.254/latest/meta-da": false,
	} {
		u, _ := url.Parse(raw)
		if got := HostAllowed(u, plugin, hosts); got != want {
			t.Errorf("%s: %v", raw, got)
		}
	}
}

// sourcePlugin runs the test source on a folder.
func sourcePlugin(t *testing.T, dir string, changes, move bool) (*sourcetest.Source, *httptest.Server) {
	t.Helper()
	src := &sourcetest.Source{Plugin: "store", Name: "docs", Dir: dir, Changes: changes, Move: move}
	ts := httptest.NewServer(src.Handler())
	t.Cleanup(ts.Close)
	src.SetBase(ts.URL)
	return src, ts
}

func acceptedRegistry(t *testing.T, pluginURL string) *Registry {
	t.Helper()
	reg, err := New([]Config{{Name: "store", URL: pluginURL}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := reg.Fetch(context.Background(), "store")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Accept(context.Background(), "store", m.SHA(), "test"); err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestBind(t *testing.T) {
	_, ts := sourcePlugin(t, t.TempDir(), false, false)
	reg := acceptedRegistry(t, ts.URL+"/mcp")
	b, err := reg.Bind(infolder.Binding{Project: "p", Plugin: "store", Source: "docs"}, "cli")
	if err != nil || b.Folder != "docs" || b.By != "cli" {
		t.Fatalf("bind %+v %v", b, err)
	}
	for name, bad := range map[string]infolder.Binding{
		"no plugin":   {Project: "p", Plugin: "nope", Source: "docs"},
		"no source":   {Project: "p", Plugin: "store", Source: "nope"},
		"folder":      {Project: "p", Plugin: "store", Source: "docs", Folder: ".x"},
		"same folder": {Project: "p", Plugin: "store", Source: "docs", Folder: "docs"},
		"same source": {Project: "p", Plugin: "store", Source: "docs", Folder: "other"},
	} {
		if _, err := reg.Bind(bad, "cli"); err == nil {
			t.Errorf("%s: bound", name)
		}
	}
	if _, err := reg.Bind(infolder.Binding{Project: "q", Plugin: "store", Source: "docs", Folder: "nc", Write: true}, "cli"); err != nil {
		t.Fatal(err)
	}
	// Accepting again keeps the bindings; a rename follows.
	if _, err := reg.Accept(context.Background(), "store", reg.mustSHA(t), "test"); err != nil {
		t.Fatal(err)
	}
	if err := reg.ProjectRenamed("q", "r"); err != nil {
		t.Fatal(err)
	}
	list := reg.Bindings()
	if len(list) != 2 || list[1].Project != "r" || !list[1].Write || list[1].Folder != "nc" {
		t.Errorf("bindings %+v", list)
	}
	if _, err := reg.Bind(infolder.Binding{Project: "gone", Plugin: "store", Source: "docs"}, "cli"); err != nil {
		t.Fatal(err)
	}
	if err := reg.ProjectRemoved("gone"); err != nil {
		t.Fatal(err)
	}
	for _, b := range reg.Bindings() {
		if b.Project == "gone" {
			t.Errorf("a binding of a project deleted stays: %+v", b)
		}
	}
	if _, err := reg.Unbind("p", "docs"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Unbind("p", "docs"); err == nil {
		t.Error("unbound twice")
	}
	if list := reg.Bindings(); len(list) != 1 || list[0].Project != "r" {
		t.Errorf("after unbind %+v", list)
	}
	// What the CLI wrote, the server reads.
	again, err := New([]Config{{Name: "store", URL: ts.URL + "/mcp"}}, filepath.Dir(reg.store.path), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if list := again.Bindings(); len(list) != 1 || list[0].Folder != "nc" {
		t.Errorf("read again %+v", list)
	}
}

func (r *Registry) mustSHA(t *testing.T) string {
	t.Helper()
	m, err := r.Fetch(context.Background(), "store")
	if err != nil {
		t.Fatal(err)
	}
	return m.SHA()
}

func TestSession(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.txt", "alpha")
	write(t, dir, "2024/b.pdf", "beta")
	src, ts := sourcePlugin(t, dir, true, true)
	reg := acceptedRegistry(t, ts.URL+"/mcp")
	ctx := context.Background()
	b := infolder.Binding{Project: "p", Folder: "docs", Plugin: "store", Source: "docs"}
	sess, err := reg.Session(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if info := sess.Info(); !info.Changes || !info.Move || info.Kind != "folder" {
		t.Errorf("info %+v", info)
	}
	page, err := sess.List(ctx, "", 1)
	if err != nil || len(page.Files) != 1 || page.Files[0].Path != "2024/b.pdf" || page.Next != "2024/b.pdf" || page.Files[0].ETag == "" {
		t.Fatalf("list %+v %v", page, err)
	}
	u, err := sess.Open(ctx, "a.txt")
	if err != nil || u.Size != 5 || !strings.HasPrefix(u.URL, ts.URL+"/files/") {
		t.Fatalf("open %+v %v", u, err)
	}
	// A URL to a host the manifest does not name is refused.
	src.SetBase("http://elsewhere.example.com")
	if _, err := sess.Open(ctx, "a.txt"); err == nil || !strings.Contains(err.Error(), "source_hosts") {
		t.Errorf("another host: %v", err)
	}
	src.SetBase(ts.URL)
	ch, err := sess.Changes(ctx, "")
	if err != nil || ch.Cursor == "" {
		t.Fatalf("changes %+v %v", ch, err)
	}
	write(t, dir, "c.txt", "gamma")
	ch, err = sess.Changes(ctx, ch.Cursor)
	if err != nil || len(ch.Changes) != 1 || ch.Changes[0].Path != "c.txt" {
		t.Errorf("changes %+v %v", ch, err)
	}
	if f, err := sess.Move(ctx, "c.txt", "done/c.txt"); err != nil || f.Path != "done/c.txt" {
		t.Errorf("move %+v %v", f, err)
	}
	// A plugin that serves another manifest: no session until accepted.
	src.SetMove(false)
	if _, err := reg.Session(ctx, b); err == nil || !strings.Contains(err.Error(), "another manifest") {
		t.Errorf("changed manifest: %v", err)
	}
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSources_INFolder is the whole way: a source bound to a project, its
// files in the IN folder's state, read through the URLs the plugin gives.
func TestSources_INFolder(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "2024/a.pdf", "%PDF-a")
	write(t, dir, "2024/b.pdf", "%PDF-b")
	write(t, dir, "2024/.done", "")
	src, ts := sourcePlugin(t, dir, true, false)
	reg := acceptedRegistry(t, ts.URL+"/mcp")
	if _, err := reg.Bind(infolder.Binding{Project: "p", Plugin: "store", Source: "docs", Folder: "nc"}, "cli"); err != nil {
		t.Fatal(err)
	}
	v := vault.New(t.TempDir())
	v.SetINProjects(func(p string) bool { return p == "p" })
	if err := os.MkdirAll(filepath.Join(v.Root, "p", "IN"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc, err := infolder.Open(filepath.Join(t.TempDir(), "in.db"), v, func() []string { return []string{"p"} },
		infolder.Options{Every: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	svc.SetSources(reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)
	var page infolder.Page
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		page, err = svc.Store().List("p", infolder.Query{Folder: "nc"})
		if err == nil && len(page.Files) == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(page.Files) != 2 || page.Files[0].Source != "store/docs" || page.Files[0].Dir != "nc/2024" {
		t.Fatalf("files %+v %v", page.Files, err)
	}
	f, e, err := svc.Open(ctx, "p/IN/nc/2024/b.pdf")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil || string(data) != "%PDF-b" || e.Size != 6 {
		t.Errorf("read %q %v %+v", data, err, e)
	}
	if src.Opens.Load() == 0 || src.Gets.Load() == 0 {
		t.Errorf("opens %d gets %d", src.Opens.Load(), src.Gets.Load())
	}
	states, err := svc.SourceStates("p")
	if err != nil || len(states) != 1 || states[0].Status != "ok" || states[0].Plugin != "store" || !states[0].Changes {
		t.Errorf("states %+v %v", states, err)
	}
}
