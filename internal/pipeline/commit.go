package pipeline

import (
	"encoding/json"
	"path"
	"strings"
	"time"
)

// The commit trigger (IMP-170): a commit of the vault's git sync starts the
// pipelines whose folder it changed, one run per pipeline and commit, with
// the files it changed there. A pipeline that keeps something beside the
// notes (the chunks of an archive of vectors) follows their versions this
// way: the commit is the version. The files a run of the pipeline itself
// wrote last are left out, and those of the pipelines it starts with
// pipeline.run, so that a pipeline writing in its own folder does not
// start itself again; the runs notes, which every run's end rewrites,
// or two commit pipelines would start each other forever; and what the
// pipelines on run write (IMP-206), or a commit run that fails would start
// them again.

// Commit is a commit of the vault's git sync: its SHA, its date and the
// files it changed, as vault paths.
type Commit struct {
	SHA   string
	Date  time.Time
	Files []CommitFile
}

// CommitFile is a file a commit changed: Change is added, modified,
// deleted or renamed, From the path a renamed file had.
type CommitFile struct {
	Path   string `json:"path"`
	From   string `json:"from,omitempty"`
	Change string `json:"change"`
}

// Authors is what a host offers to tell who wrote a note last, as the
// audit names it: pipeline:<path> for a run of that pipeline. Without it,
// the commit trigger leaves out no file.
type Authors interface {
	LastWriter(path string) string
}

// SetCommits tells the engine whether git sync makes commits, for the
// notice of the commit pipelines when it does not. Call it before the
// engine runs.
func (e *Engine) SetCommits(on func() bool) { e.commits = on }

// commitNotice is why a commit pipeline does not start by itself.
const commitNotice = "it starts on a commit, and the server's git sync is off: it starts only by hand"

// Infos lists ps with their last run, and the notice of a commit pipeline
// while git sync makes no commits.
func (e *Engine) Infos(ps []Pipeline) []Info {
	out := Infos(ps, e.runner.Store())
	off := e.commits == nil || !e.commits()
	for i, p := range ps {
		if off && p.Spec != nil && p.Spec.Trigger.On == TriggerCommit {
			out[i].Notice = commitNotice
		}
	}
	return out
}

// Committed starts the commit pipelines of the projects that run
// pipelines whose folder c changed: each with the files under it (match
// narrows them by name), but those a run of the pipeline, of the ones it
// starts, of the ones its skip names or of a pipeline on run (and the ones
// it starts) wrote last. The
// commit is the run's idempotency key. When the files do not fit in the
// run's input, files_listed is false, and count says how many they are.
func (e *Engine) Committed(c Commit) {
	if len(c.Files) == 0 {
		return
	}
	ps, err := e.host.Pipelines("")
	if err != nil {
		e.log.Warn("pipeline: listing the pipelines for a commit", "commit", c.SHA, "err", err)
		return
	}
	authors, _ := e.host.(Authors)
	byPath := map[string]Pipeline{}
	runsNotes := map[string]bool{}
	for _, p := range ps {
		byPath[p.Path] = p
		runsNotes[RunsNote(p.Path)] = true
	}
	// What the pipelines on run write, and the ones they start, starts no
	// commit pipeline: a commit run that fails could start them again, and
	// they would write again (runtrigger.go).
	onRun := map[string]bool{}
	for _, p := range ps {
		if p.Err == nil && p.Spec != nil && p.Spec.Trigger.On == TriggerRun {
			for a := range chainOf(p, byPath) {
				onRun[a] = true
			}
		}
	}
	for _, p := range ps {
		if p.Err != nil || p.Spec == nil || p.Spec.Trigger.On != TriggerCommit || !e.host.Enabled(p.Project) {
			continue
		}
		tr := p.Spec.Trigger
		under := p.Project + "/"
		if tr.Folder != "" {
			under += tr.Folder + "/"
		}
		chain := chainOf(p, byPath)
		for _, name := range tr.Skip {
			if rel, err := projectRel(p.Project, name); err == nil {
				if !strings.HasSuffix(rel, ".md") {
					rel += ".md"
				}
				chain[Actor(rel)] = true
			}
		}
		files := []CommitFile{}
		for _, f := range c.Files {
			in := strings.HasPrefix(f.Path, under) || (f.From != "" && strings.HasPrefix(f.From, under))
			if !in || runsNotes[f.Path] || (tr.Match != "" && !globMatch(tr.Match, path.Base(f.Path))) {
				continue
			}
			if authors != nil {
				if w := authors.LastWriter(f.Path); chain[w] || onRun[w] {
					continue
				}
			}
			files = append(files, f)
		}
		if len(files) == 0 {
			continue
		}
		input := map[string]any{"commit": c.SHA, "date": c.Date.Unix(), "project": p.Project, "folder": tr.Folder,
			"count": len(files), "files": files, "files_listed": true}
		if b, err := json.Marshal(input); err != nil || len(b) > e.cfg.MaxInputBytes {
			delete(input, "files")
			input["files_listed"] = false
		}
		if _, _, err := e.Start(StartRequest{Path: p.Path, Trigger: TriggerCommit, Input: input, Actor: "git", IdemKey: "commit:" + c.SHA}); err != nil {
			e.log.Warn("pipeline: a commit run", "pipeline", p.Path, "commit", c.SHA, "err", err)
		}
	}
}

// chainOf is the actors of p and of the pipelines it starts with
// pipeline.run, transitively: their writes do not start p again. A name
// built by an expression is not followed: the chain is what the notes say.
func chainOf(p Pipeline, byPath map[string]Pipeline) map[string]bool {
	out := map[string]bool{}
	var walk func(q Pipeline)
	walk = func(q Pipeline) {
		if out[Actor(q.Path)] {
			return
		}
		out[Actor(q.Path)] = true
		if q.Spec == nil {
			return
		}
		for _, n := range q.Spec.Nodes {
			name, _ := n.With["pipeline"].(string)
			if n.Use != "pipeline.run" || name == "" || strings.Contains(name, "{{") {
				continue
			}
			rel, err := projectRel(q.Project, name)
			if err != nil {
				continue
			}
			if !strings.HasSuffix(rel, ".md") {
				rel += ".md"
			}
			if c, ok := byPath[rel]; ok {
				walk(c)
			}
		}
	}
	walk(p)
	return out
}

func globMatch(pattern, name string) bool {
	ok, _ := path.Match(pattern, name)
	return ok
}
