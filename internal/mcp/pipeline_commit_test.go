package mcp

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/config"
	"github.com/gosidian/gosidian/internal/gitsync"
	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/projects"
)

// syncCommitter is git sync as vault.commit's committer, as the server
// wires it (cmd/gosidian).
type syncCommitter struct{ s *gitsync.Sync }

func (c syncCommitter) CommitNow(ctx context.Context, message, tag string) (pipeline.CommitDone, error) {
	r, err := c.s.CommitNow(ctx, message, tag)
	return pipeline.CommitDone{SHA: r.SHA, Short: r.Short, Committed: r.Committed, Tag: r.Tag, Pushed: r.Pushed, PushError: r.PushError}, err
}

const revisionPipeline = "---\ntitle: Revision\ntype: pipeline\n---\n\n```pipeline\n" + `trigger: {on: external}
nodes:
  - id: write
    use: note.write
    with: {path: "manuals/{{ trigger.code }}", body: "Revision {{ trigger.revision }}", frontmatter: {revisione: "{{ trigger.revision }}"}}
  - id: commit
    use: vault.commit
    with: {message: "{{ trigger.code }} {{ trigger.revision }}", tag: "doc/{{ trigger.code }}/{{ trigger.revision }}"}
` + "```\n"

const watchPipeline = "---\ntitle: Watch\ntype: pipeline\n---\n\n```pipeline\n" + `trigger: {on: commit, folder: manuals}
nodes:
  - {id: seen, use: starlark, with: {code: "output = ctx"}}
` + "```\n"

// Each revision a pipeline writes is a commit of its own, with its tag,
// and the commit pipelines start on it as on any commit (IMP-201).
func TestPipelineVaultCommit_RevisionsAsHistory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	s, _, dir := newTestServer(t)
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pstore.Set("p", projects.Flags{UsePipelines: true, PipelineTags: true}); err != nil {
		t.Fatal(err)
	}
	s.SetProjects(pstore)
	s.SetPipelinesSwitch(true)
	ctx := context.Background()
	for path, content := range map[string]string{"p/pipelines/revision.md": revisionPipeline, "p/pipelines/watch.md": watchPipeline} {
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": path, "content": content})); err != nil || res.IsError {
			t.Fatalf("create %s: %v %+v", path, err, res)
		}
	}

	syncer := gitsync.New(dir, config.GitConfig{Enabled: true, Branch: "main", AuthorName: "Test Bot", AuthorEmail: "bot@example.com", Debounce: time.Hour})
	sctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	if err := syncer.Start(sctx); err != nil {
		t.Fatal(err)
	}
	store, err := jobs.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	runner := jobs.NewRunner(store, jobs.Options{Workers: 2, Poll: 10 * time.Millisecond, Lease: time.Minute})
	e := pipeline.New(s.PipelineHost(), runner, pipeline.Config{Location: time.UTC}, nil, nil)
	e.SetCommits(syncer.Commits)
	e.SetCommitter(syncCommitter{syncer})
	syncer.OnCommit(func(c gitsync.CommitChanges) {
		files := make([]pipeline.CommitFile, len(c.Files))
		for i, f := range c.Files {
			files[i] = pipeline.CommitFile{Path: f.Path, From: f.From, Change: f.Status}
		}
		e.Committed(pipeline.Commit{SHA: c.SHA, Date: c.Date, Files: files})
	})
	s.SetPipelines(e)
	rctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { runner.Start(rctx); close(stopped) }()
	t.Cleanup(func() { cancel(); <-stopped; store.Close() })

	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	var shas []string
	for _, rev := range []string{"V1", "V2", "V3"} {
		res, err := s.handlePipelineRun(ctx, call(map[string]any{"path": "p/pipelines/revision.md",
			"input": map[string]any{"code": "manual-A-en", "revision": rev}, "wait": 10}))
		if err != nil {
			t.Fatal(err)
		}
		if text := resultText(t, res); !strings.Contains(text, `"status":"done"`) {
			t.Fatalf("%s: %s", rev, text)
		}
		shas = append(shas, git("rev-parse", "HEAD"))
		if got := git("log", "-1", "--format=%s"); got != "manual-A-en "+rev {
			t.Errorf("%s: HEAD is %q", rev, got)
		}
		if got := git("show", "doc/manual-A-en/"+rev+":p/manuals/manual-A-en.md"); !strings.Contains(got, "revisione: "+rev) {
			t.Errorf("%s from its tag:\n%s", rev, got)
		}
	}
	if shas[0] == shas[1] || shas[1] == shas[2] {
		t.Errorf("the revisions share a commit: %v", shas)
	}

	// The watch pipeline started once per revision's commit.
	deadline := time.Now().Add(5 * time.Second)
	for {
		runs, err := store.Runs(jobs.RunFilter{})
		if err != nil {
			t.Fatal(err)
		}
		keys := map[string]bool{}
		for _, r := range runs {
			if r.Ref == "p/pipelines/watch.md" && r.Trigger == pipeline.TriggerCommit {
				keys[r.IdemKey] = true
			}
		}
		if len(keys) == 3 && keys["commit:"+shas[0]] && keys["commit:"+shas[1]] && keys["commit:"+shas[2]] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the commit runs: %v, the commits %v", keys, shas)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A project without pipeline_tags gets the reason, and nothing tagged.
	if err := pstore.Set("p", projects.Flags{UsePipelines: true}); err != nil {
		t.Fatal(err)
	}
	res, _ := s.handlePipelineRun(ctx, call(map[string]any{"path": "p/pipelines/revision.md",
		"input": map[string]any{"code": "manual-A-en", "revision": "V4"}, "wait": 10}))
	if text := resultText(t, res); !strings.Contains(text, `"status":"failed"`) || !strings.Contains(text, "pipeline_tags") {
		t.Errorf("without pipeline_tags: %s", text)
	}
	if tags := git("tag", "-l"); len(strings.Fields(tags)) != 3 {
		t.Errorf("tags:\n%s", tags)
	}
}
