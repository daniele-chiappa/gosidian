package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/infolder"
	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/vault"
)

type inFixture struct {
	e    *Engine
	host *fakeHost
	v    *vault.Vault
	svc  *infolder.Service
	now  time.Time
}

func newINFixture(t *testing.T) *inFixture {
	t.Helper()
	e, host := newEngine(t, Config{})
	v := vault.New(t.TempDir())
	v.SetINProjects(func(p string) bool { return p == "p" })
	if err := os.MkdirAll(filepath.Join(v.Root, "p", "IN"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &inFixture{e: e, host: host, v: v, now: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	svc, err := infolder.Open(filepath.Join(t.TempDir(), "in.db"), v, func() []string { return []string{"p"} },
		infolder.Options{Now: func() time.Time { return f.now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })
	e.SetIN(svc)
	svc.SetSink(e)
	f.svc = svc
	return f
}

func (f *inFixture) write(t *testing.T, rel, content string) {
	t.Helper()
	full := filepath.Join(f.v.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// arrive scans, waits out the quiet time and scans again: the batches close.
func (f *inFixture) arrive(t *testing.T) {
	t.Helper()
	if err := f.svc.Scan("p"); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(61 * time.Second)
	if err := f.svc.Scan("p"); err != nil {
		t.Fatal(err)
	}
}

func (f *inFixture) runs(t *testing.T, ref string) []jobs.Run {
	t.Helper()
	runs, err := f.e.Runner().Store().Runs(jobs.RunFilter{Ref: ref})
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

func (f *inFixture) status(t *testing.T, p string) infolder.File {
	t.Helper()
	file, err := f.svc.Store().File("p", p)
	if err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	return file
}

// One run per file the trigger takes (its folder, its kinds), once per
// content; the file is working, then done when the run ends well.
func TestIN_TriggerPerFile(t *testing.T) {
	f := newINFixture(t)
	f.host.notes["p/pipelines/each.md"] = pipelineNote(`
trigger: {on: in, folder: IN/fatture/, kinds: [pdf]}
nodes: [{id: a, use: starlark, with: {input: "{{ trigger }}", code: "output = input['name'] + '|' + input['dir'] + '|' + input['file']"}}]
`)
	f.write(t, "p/IN/fatture/2024/a.pdf", "a")
	f.write(t, "p/IN/fatture/b.xml", "b")
	f.write(t, "p/IN/z.pdf", "z")
	f.arrive(t)
	runs := f.runs(t, "p/pipelines/each.md")
	if len(runs) != 1 {
		t.Fatalf("%d runs, want 1", len(runs))
	}
	if got := f.status(t, "p/IN/fatture/2024/a.pdf"); got.Run != runs[0].ID {
		t.Errorf("the file was not taken by the run: %+v", got)
	}
	waitStatus(t, f.e, runs[0].ID, jobs.Done)
	if out := string(stepOf(t, f.e, runs[0].ID, "a").Output); out != `"a.pdf|fatture/2024|p/IN/fatture/2024/a.pdf"` {
		t.Errorf("trigger %s", out)
	}
	eventually(t, func() bool { return f.status(t, "p/IN/fatture/2024/a.pdf").Status == infolder.StatusDone }, "the file done")
	if got := f.status(t, "p/IN/z.pdf"); got.Status != infolder.StatusNew {
		t.Errorf("a file the trigger does not take: %+v", got)
	}

	f.arrive(t)
	if n := len(f.runs(t, "p/pipelines/each.md")); n != 1 {
		t.Fatalf("%d runs after a quiet scan", n)
	}
	f.write(t, "p/IN/fatture/2024/a.pdf", "a, changed")
	f.arrive(t)
	if n := len(f.runs(t, "p/pipelines/each.md")); n != 2 {
		t.Fatalf("%d runs after a change, want 2", n)
	}
}

// One run per batch, with its files; in.list reads the batch, in.move takes
// each file to done/ and sets its status, which the end of the run keeps.
func TestIN_TriggerBatchAndNodes(t *testing.T) {
	f := newINFixture(t)
	f.host.notes["p/pipelines/lot.md"] = pipelineNote(`
trigger: {on: in, batch: true, match: "*.pdf"}
nodes:
  - {id: n, use: starlark, with: {input: "{{ trigger }}", code: "output = str(input['count']) + ' ' + input['dir'] + ' ' + str(len(input['files']))"}}
  - {id: l, use: in.list, with: {batch: "{{ trigger.batch }}", kinds: [pdf]}}
  - {id: m, use: in.move, each: "{{ l.files }}", with: {file: "{{ item.path }}", to: "done/", status: done}}
`)
	f.write(t, "p/IN/lotto/a.pdf", "a")
	f.write(t, "p/IN/lotto/sub/b.pdf", "b")
	f.write(t, "p/IN/lotto/c.txt", "c")
	f.arrive(t)
	runs := f.runs(t, "p/pipelines/lot.md")
	if len(runs) != 1 {
		t.Fatalf("%d runs, want 1", len(runs))
	}
	waitStatus(t, f.e, runs[0].ID, jobs.Done)
	if out := string(stepOf(t, f.e, runs[0].ID, "n").Output); out != `"2 lotto 2"` {
		t.Errorf("trigger %s", out)
	}
	for _, p := range []string{"p/IN/done/a.pdf", "p/IN/done/b.pdf"} {
		if _, err := os.Stat(filepath.Join(f.v.Root, filepath.FromSlash(p))); err != nil {
			t.Errorf("%s not moved: %v", p, err)
		}
		eventually(t, func() bool { return f.status(t, p).Status == infolder.StatusDone }, p+" done")
	}
	if got := f.status(t, "p/IN/lotto/c.txt"); got.Status != infolder.StatusNew || got.Run != "" {
		t.Errorf("a file the trigger does not take: %+v", got)
	}
	f.arrive(t)
	if n := len(f.runs(t, "p/pipelines/lot.md")); n != 1 {
		t.Errorf("the moves started %d runs", n)
	}
}

func TestIN_NodesWithoutIN(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/pipelines/l.md"] = pipelineNote(`nodes: [{id: l, use: in.list}]`)
	run, _, err := e.Start(StartRequest{Path: "p/pipelines/l.md", Trigger: TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitStatus(t, e, run.ID, jobs.Failed); !strings.Contains(got.Error, "no IN folders") {
		t.Errorf("error %q", got.Error)
	}
}

func TestTrigger_IN(t *testing.T) {
	s, err := Parse("trigger: {on: in, folder: /IN/a/b/, kinds: [.PDF], match: '*_T?.pdf'}\nnodes: [{id: a, use: starlark, with: {code: 'output = 1'}}]", nil)
	if err != nil {
		t.Fatal(err)
	}
	tr := s.Trigger
	if tr.Folder != "a/b" || tr.Kinds[0] != "pdf" || tr.Batch {
		t.Fatalf("trigger %+v", tr)
	}
	for _, c := range []struct {
		f    infolder.File
		want bool
	}{
		{infolder.File{Dir: "a/b", Name: "x_T1.pdf", Ext: "pdf"}, true},
		{infolder.File{Dir: "a/b/c", Name: "x_T2.pdf", Ext: "pdf"}, true},
		{infolder.File{Dir: "a/bc", Name: "x_T2.pdf", Ext: "pdf"}, false},
		{infolder.File{Dir: "a/b", Name: "x_T12.pdf", Ext: "pdf"}, false},
		{infolder.File{Dir: "a/b", Name: "x_T1.xml", Ext: "xml"}, false},
	} {
		if got := tr.TakesIN(c.f); got != c.want {
			t.Errorf("TakesIN(%+v) = %v", c.f, got)
		}
	}
	if _, err := Parse("trigger: {on: in, match: '['}\nnodes: [{id: a, use: starlark, with: {code: 'output = 1'}}]", nil); err == nil {
		t.Error("a bad glob")
	}
	env := &Env{Project: "p"}
	for in, want := range map[string]string{"a/b.pdf": "p/IN/a/b.pdf", "IN/a.pdf": "p/IN/a.pdf", "p/IN/a.pdf": "p/IN/a.pdf", "/done/": "p/IN/done"} {
		if got, err := env.inPath(in); err != nil || got != want {
			t.Errorf("inPath(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := env.inPath("a/../../x"); err == nil {
		t.Error("inPath with ..")
	}
}

// Started by hand on files chosen, a pipeline runs as its trigger would: an
// in pipeline per file once per file it takes, the others skipped and said;
// a batch one, or a manual one, once with the files.
func TestStartINFiles(t *testing.T) {
	f := newINFixture(t)
	f.host.notes["p/pipelines/each.md"] = pipelineNote(`
trigger: {on: in, kinds: [pdf]}
nodes: [{id: a, use: starlark, with: {input: "{{ trigger }}", code: "output = input['name']"}}]
`)
	f.host.notes["p/pipelines/lot.md"] = pipelineNote(`
trigger: {on: in, batch: true}
nodes: [{id: a, use: starlark, with: {input: "{{ trigger }}", code: "output = input['count']"}}]
`)
	f.host.notes["p/pipelines/hand.md"] = pipelineNote(`
nodes: [{id: a, use: starlark, with: {input: "{{ trigger }}", code: "output = len(input['files'])"}}]
`)
	f.write(t, "p/IN/x/a.pdf", "a")
	f.write(t, "p/IN/x/b.pdf", "b")
	f.write(t, "p/IN/x/c.xml", "c")
	if err := f.svc.Scan("p"); err != nil {
		t.Fatal(err)
	}
	var chosen []infolder.File
	for _, p := range []string{"p/IN/x/a.pdf", "p/IN/x/b.pdf", "p/IN/x/c.xml"} {
		chosen = append(chosen, f.status(t, p))
	}

	runs, skipped, err := f.e.StartINFiles("p/pipelines/each.md", chosen, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || len(skipped) != 1 || skipped[0].Path != "p/IN/x/c.xml" || !strings.Contains(skipped[0].Why, "pdf") {
		t.Fatalf("per file: %d runs, skipped %+v", len(runs), skipped)
	}
	got := map[string]bool{}
	for _, r := range runs {
		waitStatus(t, f.e, r.ID, jobs.Done)
		got[string(stepOf(t, f.e, r.ID, "a").Output)] = true
		if r.Trigger != TriggerManual || r.Actor != "alice" {
			t.Errorf("run %+v", r)
		}
	}
	if !got[`"a.pdf"`] || !got[`"b.pdf"`] {
		t.Errorf("outputs %v", got)
	}
	eventually(t, func() bool { return f.status(t, "p/IN/x/a.pdf").Status == infolder.StatusDone }, "a.pdf done")
	if s := f.status(t, "p/IN/x/c.xml"); s.Status != infolder.StatusNew || s.Run != "" {
		t.Errorf("a skipped file was taken: %+v", s)
	}
	// By hand again: a new run, not the one before.
	again, _, err := f.e.StartINFiles("p/pipelines/each.md", chosen[:1], "alice")
	if err != nil || len(again) != 1 || again[0].ID == runs[0].ID || again[0].ID == runs[1].ID {
		t.Fatalf("again: %+v, %v", again, err)
	}

	for path, want := range map[string]string{"p/pipelines/lot.md": "3", "p/pipelines/hand.md": "3"} {
		runs, skipped, err := f.e.StartINFiles(path, chosen, "alice")
		if err != nil || len(runs) != 1 || len(skipped) != 0 {
			t.Fatalf("%s: %+v %+v %v", path, runs, skipped, err)
		}
		waitStatus(t, f.e, runs[0].ID, jobs.Done)
		if out := string(stepOf(t, f.e, runs[0].ID, "a").Output); out != want {
			t.Errorf("%s: output %s, want %s", path, out, want)
		}
	}
}
