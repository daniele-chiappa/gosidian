package pipeline

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// LastWriter makes the fake an Authors.
func (h *fakeHost) LastWriter(p string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.writers[p]
}

const commitPipeline = `
trigger: {on: commit, folder: docs, match: "*.md"}
nodes:
  - id: show
    use: starlark
    with: {code: "output = ctx"}
`

func TestEngine_CommitTrigger(t *testing.T) {
	e, host := newEngine(t, Config{MaxInputBytes: 4096})
	host.notes["p/pipelines/cut.md"] = pipelineNote(commitPipeline)
	host.notes["p/pipelines/other.md"] = pipelineNote("trigger: {on: manual}\nnodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}")
	host.notes["q/pipelines/cut.md"] = pipelineNote(commitPipeline) // q runs no pipelines
	host.writers = map[string]string{"p/docs/written.md": Actor("p/pipelines/cut.md"), "p/docs/a.md": "admin"}
	date := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	c := Commit{SHA: "c0ffee12c0ffee12c0ffee12c0ffee12c0ffee12", Date: date, Files: []CommitFile{
		{Path: "p/docs/a.md", Change: "modified"},
		{Path: "p/docs/sub/b.md", Change: "added"},
		{Path: "p/docs/new name.md", From: "p/other/old.md", Change: "renamed"},
		{Path: "p/elsewhere/x.md", From: "p/docs/x.md", Change: "renamed"},
		{Path: "p/docs/gone.md", Change: "deleted"},
		{Path: "p/docs/pic.png", Change: "added"},
		{Path: "p/docs/written.md", Change: "modified"},
		{Path: "p/documents/a.md", Change: "added"},
		{Path: "q/docs/a.md", Change: "added"},
	}}
	e.Committed(c)
	runs, err := e.Runner().Store().Runs(jobs.RunFilter{})
	if err != nil || len(runs) != 1 || runs[0].Ref != "p/pipelines/cut.md" || runs[0].Trigger != TriggerCommit || runs[0].Actor != "git" || runs[0].IdemKey != "commit:"+c.SHA {
		t.Fatalf("runs %+v %v", runs, err)
	}
	var in struct {
		Commit      string       `json:"commit"`
		Date        int64        `json:"date"`
		Project     string       `json:"project"`
		Folder      string       `json:"folder"`
		Count       int          `json:"count"`
		FilesListed bool         `json:"files_listed"`
		Files       []CommitFile `json:"files"`
	}
	if err := json.Unmarshal(runs[0].Input, &in); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range in.Files {
		paths = append(paths, f.Path+":"+f.Change)
	}
	want := "p/docs/a.md:modified p/docs/sub/b.md:added p/docs/new name.md:renamed p/elsewhere/x.md:renamed p/docs/gone.md:deleted"
	if in.Commit != c.SHA || in.Date != date.Unix() || in.Project != "p" || in.Folder != "docs" || in.Count != 5 || !in.FilesListed || strings.Join(paths, " ") != want {
		t.Errorf("input %+v\nfiles %s", in, strings.Join(paths, " "))
	}
	run := waitStatus(t, e, runs[0].ID, jobs.Done)
	if out := string(stepOf(t, e, run.ID, "show").Output); !strings.Contains(out, `"trigger":"commit"`) {
		t.Errorf("the run's ctx: %s", out)
	}

	// The same commit again starts nothing; a commit of the pipeline's own
	// writes only, or outside its folder, starts nothing.
	e.Committed(c)
	e.Committed(Commit{SHA: "b", Date: date, Files: []CommitFile{{Path: "p/docs/written.md", Change: "modified"}, {Path: "p/other/a.md", Change: "added"}}})
	if runs, _ := e.Runner().Store().Runs(jobs.RunFilter{}); len(runs) != 1 {
		t.Errorf("%d runs after the commits that start none", len(runs))
	}

	// Too many files for the input: the run starts without them.
	var many []CommitFile
	for i := range 200 {
		many = append(many, CommitFile{Path: "p/docs/" + strings.Repeat("n", 20) + string(rune('a'+i%26)) + ".md", Change: "added"})
	}
	e.Committed(Commit{SHA: "d", Date: date, Files: many})
	runs, _ = e.Runner().Store().Runs(jobs.RunFilter{IdemPrefix: "commit:d"})
	if len(runs) != 1 || !strings.Contains(string(runs[0].Input), `"files_listed":false`) || !strings.Contains(string(runs[0].Input), `"count":200`) || strings.Contains(string(runs[0].Input), `"files":`) {
		t.Errorf("a commit of many files: %+v", runs)
	}
}

func TestEngine_CommitNotice(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/pipelines/cut.md"] = pipelineNote(commitPipeline)
	host.notes["p/pipelines/other.md"] = pipelineNote("trigger: {on: manual}\nnodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}")
	ps, _ := host.Pipelines("p")
	notices := func() map[string]string {
		out := map[string]string{}
		for _, i := range e.Infos(ps) {
			out[i.Path] = i.Notice
		}
		return out
	}
	if n := notices(); n["p/pipelines/cut.md"] != commitNotice || n["p/pipelines/other.md"] != "" {
		t.Errorf("git sync off: %v", n)
	}
	e.SetCommits(func() bool { return true })
	if n := notices(); n["p/pipelines/cut.md"] != "" {
		t.Errorf("git sync on: %v", n)
	}
}

func TestParse_CommitTrigger(t *testing.T) {
	s, err := Parse("trigger: {on: commit, folder: /docs/, match: \"*.md\"}\nnodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}", nil)
	if err != nil || s.Trigger.On != TriggerCommit || s.Trigger.Folder != "docs" || s.Trigger.Match != "*.md" {
		t.Fatalf("%+v %v", s, err)
	}
	s, err = Parse("trigger: {on: commit, skip: [pipelines/import, ' x.md ']}\nnodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}", nil)
	if err != nil || strings.Join(s.Trigger.Skip, ",") != "pipelines/import,x.md" {
		t.Fatalf("skip %+v %v", s.Trigger, err)
	}
	for _, bad := range []string{"trigger: {on: commit, match: \"[\"}", "trigger: {on: commit, folder: ../x}", "trigger: {on: commit, take: false}",
		"trigger: {on: manual, skip: [a]}", "trigger: {on: commit, skip: [\"\"]}", "trigger: {on: commit, skip: [../q/a]}", "trigger: {on: commit, skip: [\"{{ x }}\"]}"} {
		if _, err := Parse(bad+"\nnodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}", nil); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}

// Two commit pipelines of the whole project do not start each other through
// their runs notes, and a pipeline started by a commit pipeline does not
// start it again by writing in its folder.
func TestEngine_CommitTriggerStartsNoLoop(t *testing.T) {
	e, host := newEngine(t, Config{})
	whole := "trigger: {on: commit}\nnodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}\n"
	host.notes["p/pipelines/a.md"] = pipelineNote(whole)
	host.notes["p/pipelines/b.md"] = pipelineNote(`
trigger: {on: commit, folder: docs}
nodes:
  - {id: call, use: pipeline.run, with: {pipeline: pipelines/child, wait: false}}
`)
	host.notes["p/pipelines/child.md"] = pipelineNote("nodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}\n")
	host.writers = map[string]string{
		"p/pipelines/a-runs.md": Actor("p/pipelines/a.md"),
		"p/pipelines/b-runs.md": Actor("p/pipelines/b.md"),
		"p/docs/by-child.md":    Actor("p/pipelines/child.md"),
	}
	date := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	e.Committed(Commit{SHA: "c1", Date: date, Files: []CommitFile{
		{Path: "p/pipelines/a-runs.md", Change: "modified"},
		{Path: "p/pipelines/b-runs.md", Change: "modified"},
		{Path: "p/pipelines/child-runs.md", Change: "added"},
	}})
	if runs, _ := e.Runner().Store().Runs(jobs.RunFilter{}); len(runs) != 0 {
		t.Fatalf("a runs note started %+v", runs)
	}
	e.Committed(Commit{SHA: "c2", Date: date, Files: []CommitFile{{Path: "p/docs/by-child.md", Change: "modified"}}})
	runs, _ := e.Runner().Store().Runs(jobs.RunFilter{})
	if len(runs) != 1 || runs[0].Ref != "p/pipelines/a.md" {
		t.Fatalf("what the child wrote: runs %+v, want a's only", runs)
	}
}

// skip leaves out what the pipelines it names wrote, and only those: what
// a pipeline they start wrote still starts it (IMP-201: the import cuts its
// own notes, the OCR's second round leaves its notes to the commit).
func TestEngine_CommitTriggerSkip(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/pipelines/cut.md"] = pipelineNote(`
trigger: {on: commit, folder: docs, skip: [pipelines/import]}
nodes:
  - {id: a, use: starlark, with: {input: "{{ trigger.files }}", code: "output = [f['path'] for f in input]"}}
`)
	host.notes["p/pipelines/import.md"] = pipelineNote(`
nodes:
  - {id: convert, use: pipeline.run, with: {pipeline: pipelines/convert}}
`)
	host.notes["p/pipelines/convert.md"] = pipelineNote("nodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}\n")
	host.writers = map[string]string{
		"p/docs/imported.md":  Actor("p/pipelines/import.md"),
		"p/docs/converted.md": Actor("p/pipelines/convert.md"),
		"p/docs/by-hand.md":   "admin",
	}
	e.Committed(Commit{SHA: "c1", Date: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC), Files: []CommitFile{
		{Path: "p/docs/imported.md", Change: "added"},
		{Path: "p/docs/converted.md", Change: "modified"},
		{Path: "p/docs/by-hand.md", Change: "modified"},
	}})
	runs, _ := e.Runner().Store().Runs(jobs.RunFilter{Ref: "p/pipelines/cut.md"})
	if len(runs) != 1 {
		t.Fatalf("runs %+v", runs)
	}
	waitStatus(t, e, runs[0].ID, jobs.Done)
	if out := string(stepOf(t, e, runs[0].ID, "a").Output); out != `["p/docs/converted.md","p/docs/by-hand.md"]` {
		t.Fatalf("files %s", out)
	}
	e.Committed(Commit{SHA: "c2", Date: time.Date(2026, 10, 18, 12, 1, 0, 0, time.UTC), Files: []CommitFile{{Path: "p/docs/imported.md", Change: "modified"}}})
	if runs, _ := e.Runner().Store().Runs(jobs.RunFilter{Ref: "p/pipelines/cut.md"}); len(runs) != 1 {
		t.Fatalf("a commit of the skipped pipeline's notes alone started %d runs", len(runs)-1)
	}
}
