package mcp

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/infolder"
	"github.com/gosidian/gosidian/internal/plugins"
	"github.com/gosidian/gosidian/internal/plugins/sourcetest"
)

// A file of a source's folder reaches the nodes as a file of the disk
// does (IMP-172 phase 2): described as in.db holds it, read from its
// source.
func TestPipelineHost_SourceFile(t *testing.T) {
	s, v, _ := newTestServer(t)
	v.SetINProjects(func(p string) bool { return p == "p" })
	if err := os.MkdirAll(filepath.Join(v.Root, "p", "IN"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.pdf"), []byte("%PDF-remote"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &sourcetest.Source{Plugin: "store", Name: "docs", Dir: dir}
	ts := httptest.NewServer(src.Handler())
	defer ts.Close()
	src.SetBase(ts.URL)
	reg, err := plugins.New([]plugins.Config{{Name: "store", URL: ts.URL + "/mcp"}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, err := reg.Fetch(ctx, "store")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Accept(ctx, "store", m.SHA(), "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Bind(infolder.Binding{Project: "p", Plugin: "store", Source: "docs", Folder: "nc"}, "test"); err != nil {
		t.Fatal(err)
	}
	svc, err := infolder.Open(filepath.Join(t.TempDir(), "in.db"), v, func() []string { return []string{"p"} },
		infolder.Options{Every: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	svc.SetSources(reg)
	s.SetIN(svc)
	go svc.Run(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := svc.Store().File("p", "p/IN/nc/a.pdf"); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	h := s.PipelineHost()
	st, err := h.StatFile("p", "p/IN/nc/a.pdf")
	if err != nil || st.Size != 11 || st.Name != "a.pdf" || st.SHA256 != "" {
		t.Fatalf("stat %+v %v", st, err)
	}
	if src.Opens.Load() != 0 {
		t.Error("the stat read the file from its source")
	}
	f, st, err := h.OpenFile("p", "p/IN/nc/a.pdf")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil || string(data) != "%PDF-remote" || st.Size != 11 {
		t.Errorf("open %q %v %+v", data, err, st)
	}
	if b, err := h.ReadAttachment("p", "p/IN/nc/a.pdf"); err != nil || string(b) != "%PDF-remote" {
		t.Errorf("read %q %v", b, err)
	}
	if _, _, err := h.OpenFile("q", "p/IN/nc/a.pdf"); err == nil {
		t.Error("another project's file opened")
	}
}
