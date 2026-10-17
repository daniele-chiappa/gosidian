package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// The node vault.commit (IMP-201): a commit of the vault now, with a
// message and, on a project with pipeline_tags, a tag, so that each
// revision of a document is a commit of its own and a tag names it
// (doc/<code>/<revision>). The git sync makes it as it makes its
// debounced commits: the same author, the same files left out, and the
// commit pipelines started the same way.

// Committer makes a commit of the vault now: the git sync.
type Committer interface {
	CommitNow(ctx context.Context, message, tag string) (CommitDone, error)
}

// CommitDone is what a Committer did: the commit made, or HEAD when
// nothing was pending (Committed false), the tag put on it, the push.
type CommitDone struct {
	SHA       string
	Short     string
	Committed bool
	Tag       string
	Pushed    bool
	PushError string
}

// TagPolicy is what a host offers to say whether a project's pipelines
// may tag commits (pipeline_tags). Without it, none may.
type TagPolicy interface {
	PipelineTags(project string) bool
}

// SetCommitter gives vault.commit the git sync. Call it before the engine
// runs, and only while git sync is on: without it the node fails with
// ErrNoGit.
func (e *Engine) SetCommitter(c Committer) { e.committer = c }

// ErrNoGit is vault.commit's error on a server without git sync.
var ErrNoGit = errors.New("this server keeps no git history: set GOSIDIAN_GIT_ENABLED")

func vaultCommit(ctx context.Context, env *Env, with map[string]any) (any, error) {
	c := env.Engine.committer
	if c == nil {
		return nil, ErrNoGit
	}
	msg := strings.TrimSpace(str(with, "message"))
	if msg == "" {
		msg = fmt.Sprintf("pipeline %s: run %s", env.Run.Ref, env.Run.ID)
	}
	tag := strings.TrimSpace(str(with, "tag"))
	if tag != "" {
		if tp, ok := env.Engine.host.(TagPolicy); !ok || !tp.PipelineTags(env.Project) {
			return nil, fmt.Errorf("project %s does not let its pipelines tag commits: turn on its pipeline_tags flag "+
				"(Projects window, chip \"git tags\", or PUT /api/v1/projects/%s with {\"pipeline_tags\": true})", env.Project, env.Project)
		}
	}
	done, err := c.CommitNow(ctx, msg, tag)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"commit": done.SHA, "short": done.Short, "committed": done.Committed, "pushed": done.Pushed}
	if done.Tag != "" {
		out["tag"] = done.Tag
	}
	if done.PushError != "" {
		out["push_error"] = done.PushError
	}
	return out, nil
}
