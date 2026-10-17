package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/gosidian/gosidian/internal/jobs"
)

// PipelineTags makes the fake a TagPolicy.
func (h *fakeHost) PipelineTags(project string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tagging[project]
}

// fakeCommitter records the commits asked.
type fakeCommitter struct {
	mu    sync.Mutex
	calls []string // "message|tag"
	err   error
}

func (c *fakeCommitter) CommitNow(_ context.Context, message, tag string) (CommitDone, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, message+"|"+tag)
	if c.err != nil {
		return CommitDone{}, c.err
	}
	return CommitDone{SHA: strings.Repeat("ab", 20), Short: "ababab", Committed: true, Tag: tag}, nil
}

func TestVaultCommit(t *testing.T) {
	e, host := newEngine(t, Config{})
	env := &Env{Project: "p", Engine: e}
	env.Run.Ref, env.Run.ID = "p/pipelines/revision.md", "r1"
	ctx := context.Background()

	// Git sync off.
	if _, err := vaultCommit(ctx, env, nil); !errors.Is(err, ErrNoGit) || !strings.Contains(err.Error(), "GOSIDIAN_GIT_ENABLED") {
		t.Fatalf("git off: %v", err)
	}

	c := &fakeCommitter{}
	e.SetCommitter(c)
	out, err := vaultCommit(ctx, env, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["commit"] != strings.Repeat("ab", 20) || m["short"] != "ababab" || m["committed"] != true || m["pushed"] != false || m["tag"] != nil {
		t.Errorf("output %v", m)
	}
	if c.calls[0] != "pipeline p/pipelines/revision.md: run r1|" {
		t.Errorf("default message: %q", c.calls[0])
	}

	// A tag on a project without pipeline_tags: refused, nothing asked.
	with := map[string]any{"message": "  revision V2  ", "tag": "doc/manual-A-en/V2"}
	if _, err := vaultCommit(ctx, env, with); err == nil || !strings.Contains(err.Error(), `{"pipeline_tags": true}`) || len(c.calls) != 1 {
		t.Fatalf("tag without the flag: %v, %d calls", err, len(c.calls))
	}
	host.tagging = map[string]bool{"p": true}
	out, err = vaultCommit(ctx, env, with)
	if err != nil || out.(map[string]any)["tag"] != "doc/manual-A-en/V2" || c.calls[1] != "revision V2|doc/manual-A-en/V2" {
		t.Fatalf("tag: %v %v %q", out, err, c.calls)
	}

	c.err = errors.New("tag doc/manual-A-en/V2 is on commit 0123 already")
	if _, err := vaultCommit(ctx, env, with); err == nil || err.Error() != c.err.Error() {
		t.Errorf("the committer's error: %v", err)
	}
}

// A run writes a revision and commits it with its tag, the expressions
// resolved; the node is known to the spec and has its schemas.
func TestEngine_VaultCommitRun(t *testing.T) {
	e, host := newEngine(t, Config{})
	c := &fakeCommitter{}
	e.SetCommitter(c)
	host.tagging = map[string]bool{"p": true}
	host.notes["p/pipelines/revision.md"] = pipelineNote(`
trigger: {on: external}
nodes:
  - id: write
    use: note.write
    with: {path: "manuals/{{ trigger.code }}", body: "Text", frontmatter: {revisione: "{{ trigger.revision }}"}}
  - id: commit
    use: vault.commit
    with:
      message: "{{ trigger.code }}: revision {{ trigger.revision }}"
      tag: "doc/{{ trigger.code }}/{{ trigger.revision }}"
`)
	run, _, err := e.Start(StartRequest{Path: "p/pipelines/revision.md", Trigger: TriggerExternal,
		Input: map[string]any{"code": "manual-A-en", "revision": "V2"}, Actor: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	if len(c.calls) != 1 || c.calls[0] != "manual-A-en: revision V2|doc/manual-A-en/V2" {
		t.Errorf("calls %q", c.calls)
	}
	var out map[string]any
	if err := json.Unmarshal(stepOf(t, e, run.ID, "commit").Output, &out); err != nil || out["tag"] != "doc/manual-A-en/V2" || out["committed"] != true {
		t.Errorf("output %s %v", stepOf(t, e, run.ID, "commit").Output, err)
	}
	if !strings.Contains(host.note("p/manuals/manual-A-en.md"), "revisione: V2") {
		t.Errorf("the note:\n%s", host.note("p/manuals/manual-A-en.md"))
	}
}
