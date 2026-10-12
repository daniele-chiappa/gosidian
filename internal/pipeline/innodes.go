package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/gosidian/gosidian/internal/infolder"
	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/vault"
)

// The IN folders (IMP-172): the trigger in, and the nodes in.list and
// in.move.

// SetIN gives the engine the state of the IN folders: the in.* nodes read
// and move through it, and the files a run takes are marked with it. Call
// it before the engine runs; without it the in.* nodes fail and say why.
func (e *Engine) SetIN(svc *infolder.Service) { e.in = svc }

// errNoIN is what an in.* node says on a server that keeps no IN folders.
var errNoIN = errors.New("this server keeps no IN folders")

// inRelative is a folder of an IN folder as a node or a trigger names it,
// relative to the IN folder: "IN/a", "a/" and "a" are all "a".
func inRelative(p string) string {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == vault.INFolder {
		return ""
	}
	return strings.TrimPrefix(p, vault.INFolder+"/")
}

// inPath is a file or a folder of the run's IN folder as a node names it
// (relative to the IN folder, to the project, or the vault path) as its
// vault path.
func (env *Env) inPath(p string) (string, error) {
	p = strings.Trim(strings.TrimSpace(p), "/")
	root := vault.INRoot(env.Project)
	switch {
	case p == "":
		return "", errors.New("an empty path")
	case p == root || strings.HasPrefix(p, root+"/"):
	case p == vault.INFolder || strings.HasPrefix(p, vault.INFolder+"/"):
		p = env.Project + "/" + p
	default:
		p = root + "/" + p
	}
	if clean := path.Clean(p); clean != p || strings.Contains(p, "..") {
		return "", fmt.Errorf("path %q: it must be a plain path inside the IN folder", p)
	}
	return p, nil
}

// TakesIN says whether an in trigger takes a file: in its folder or
// below, of one of its kinds (any without), its name matching its glob.
func (t Trigger) TakesIN(f infolder.File) bool {
	if t.On != TriggerIN {
		return false
	}
	if t.Folder != "" && f.Dir != t.Folder && !strings.HasPrefix(f.Dir, t.Folder+"/") {
		return false
	}
	if len(t.Kinds) > 0 && !slices.Contains(t.Kinds, f.Ext) {
		return false
	}
	if t.Match != "" {
		if ok, _ := path.Match(t.Match, f.Name); !ok {
			return false
		}
	}
	return true
}

// inFileInput is what a run's trigger holds about a file of an IN folder.
func inFileInput(f infolder.File) map[string]any {
	return map[string]any{
		"file": f.Path, "name": f.Name, "stem": f.Stem, "ext": f.Ext, "dir": f.Dir,
		"size": f.Size, "sha256": f.SHA256, "etag": f.ETag, "source": f.Source, "project": f.Project,
	}
}

// INBatch starts the runs of the in pipelines of a batch's project
// (infolder.Sink): one for the batch, with the files it takes, for a
// pipeline with batch: true; one per file it takes otherwise. Each file a
// run takes is marked working with it. A run already started (the same
// batch, the same file with the same content) is not started again.
func (e *Engine) INBatch(b infolder.Batch, files []infolder.File) {
	if !e.host.Enabled(b.Project) {
		return
	}
	ps, err := e.host.Pipelines(b.Project)
	if err != nil {
		e.log.Warn("pipeline: listing the pipelines for an IN batch", "project", b.Project, "err", err)
		return
	}
	for _, p := range ps {
		if p.Err != nil || p.Spec.Trigger.On != TriggerIN {
			continue
		}
		var taken []infolder.File
		for _, f := range files {
			if p.Spec.Trigger.TakesIN(f) {
				taken = append(taken, f)
			}
		}
		if len(taken) == 0 {
			continue
		}
		if p.Spec.Trigger.Batch {
			e.startIN(p, e.batchInput(b, taken), "inbatch:"+b.ID, taken)
			continue
		}
		for _, f := range taken {
			e.startIN(p, inFileInput(f), "in:"+f.Path+":"+f.Version(), []infolder.File{f})
		}
	}
}

// batchInput is a batch run's trigger: the batch, and its files when they
// fit in a run's input; otherwise files_listed is false and in.list with
// batch: {{ trigger.batch }} reads them.
func (e *Engine) batchInput(b infolder.Batch, files []infolder.File) map[string]any {
	var bytes int64
	list := make([]any, len(files))
	for i, f := range files {
		list[i] = inFileInput(f)
		bytes += f.Size
	}
	in := map[string]any{"project": b.Project, "batch": b.ID, "dir": b.Dir, "count": len(files), "bytes": bytes, "files": list, "files_listed": true}
	if raw, err := json.Marshal(in); err == nil && len(raw) > e.cfg.MaxInputBytes {
		in["files"] = []any{}
		in["files_listed"] = false
	}
	return in
}

func (e *Engine) startIN(p Pipeline, input map[string]any, key string, files []infolder.File) {
	run, existing, err := e.Start(StartRequest{Path: p.Path, Trigger: TriggerIN, Input: input, Actor: "server", IdemKey: key})
	if err != nil {
		e.log.Warn("pipeline: an IN run", "pipeline", p.Path, "key", key, "err", err)
		return
	}
	if existing || e.in == nil {
		return
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	if err := e.in.SetStatus(p.Project, paths, infolder.StatusWorking, run.ID); err != nil {
		e.log.Warn("pipeline: marking the files of a run", "run", run.ID, "err", err)
	}
}

// --- in.list, in.move ---

func strList(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s := strings.TrimSpace(render(x)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		var out []string
		for _, s := range strings.Split(t, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func inList(_ context.Context, env *Env, with map[string]any) (any, error) {
	svc := env.Engine.in
	if svc == nil {
		return nil, errNoIN
	}
	q := infolder.Query{
		Folder: inRelative(str(with, "folder")),
		Match:  strings.TrimSpace(str(with, "match")),
		Kinds:  strList(with["kinds"]),
		Status: strings.TrimSpace(str(with, "status")),
		Batch:  strings.TrimSpace(str(with, "batch")),
		Limit:  int(number(with, "limit", infolder.DefaultLimit)),
		Cursor: strings.TrimSpace(str(with, "cursor")),
	}
	if r, ok := with["recursive"].(bool); ok {
		q.Shallow = !r
	}
	if q.Status != "" && !infolder.ValidStatus(q.Status) {
		return nil, fmt.Errorf("with.status %q: new, working, done, error or ignored", q.Status)
	}
	page, err := svc.Store().List(env.Project, q)
	if err != nil {
		return nil, err
	}
	if page.Files == nil {
		page.Files = []infolder.File{}
	}
	return map[string]any{"files": page.Files, "count": len(page.Files), "next": page.Next}, nil
}

func inMove(_ context.Context, env *Env, with map[string]any) (any, error) {
	svc := env.Engine.in
	if svc == nil {
		return nil, errNoIN
	}
	f, err := required(with, "file")
	if err != nil {
		return nil, err
	}
	from, err := env.inPath(f)
	if err != nil {
		return nil, err
	}
	status := strings.TrimSpace(str(with, "status"))
	if status != "" && !infolder.ValidStatus(status) {
		return nil, fmt.Errorf("with.status %q: new, working, done, error or ignored", status)
	}
	to := strings.TrimSpace(str(with, "to"))
	if to == "" && status == "" {
		return nil, errors.New("with.to or with.status is required")
	}
	at := from
	if to != "" {
		target, err := env.inPath(to)
		if err != nil {
			return nil, err
		}
		// Into a folder: one written with a trailing slash, or one there.
		if strings.HasSuffix(to, "/") {
			target += "/" + path.Base(from)
		} else if st, err := svc.Stat(env.Project, target); err == nil && st.Dir {
			target += "/" + path.Base(from)
		}
		e, err := svc.Move(env.Project, from, target)
		if err != nil {
			return nil, err
		}
		at = e.Path
	}
	if status != "" {
		if err := svc.SetStatus(env.Project, []string{at}, status, env.Run.ID); err != nil {
			return nil, err
		}
	}
	return map[string]any{"path": at, "from": from, "status": status}, nil
}

// INSkipped is a file chosen by hand that a pipeline does not take:
// Reason is folder, kinds or match, Value what the trigger says, Why both
// in words.
type INSkipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Value  string `json:"value"`
	Why    string `json:"why"`
}

// StartINFiles starts a pipeline of the project on files of its IN folder
// chosen by hand (the IN window), the way its trigger would: an in
// pipeline that runs per file gets one run per file, with a file's
// trigger; one that runs per batch, one run with the files, shaped as a
// batch's (batch empty). Either takes only the files its trigger takes
// (folder, kinds, match): the others are skipped, and said. Any other
// pipeline gets one run with all the files, shaped as a batch's. Started
// by hand, a run is a new one even for files a run took before. The files
// are marked working with their run.
func (e *Engine) StartINFiles(pipelinePath string, files []infolder.File, actor string) ([]jobs.Run, []INSkipped, error) {
	p, err := e.host.Pipeline(pipelinePath)
	if err != nil {
		return nil, nil, err
	}
	if p.Err != nil {
		return nil, nil, p.Err
	}
	for _, f := range files {
		if f.Project != p.Project {
			return nil, nil, fmt.Errorf("%s is not in the pipeline's project %q", f.Path, p.Project)
		}
	}
	tr := p.Spec.Trigger
	var skipped []INSkipped
	if tr.On == TriggerIN {
		var taken []infolder.File
		for _, f := range files {
			if tr.TakesIN(f) {
				taken = append(taken, f)
			} else {
				skipped = append(skipped, tr.skip(f))
			}
		}
		files = taken
	}
	if len(files) == 0 {
		return nil, skipped, nil
	}
	start := func(input map[string]any, of []infolder.File) (jobs.Run, error) {
		run, _, err := e.Start(StartRequest{Path: pipelinePath, Trigger: TriggerManual, Input: input, Actor: actor})
		if err != nil {
			return run, err
		}
		if e.in != nil {
			paths := make([]string, len(of))
			for i, f := range of {
				paths[i] = f.Path
			}
			if err := e.in.SetStatus(p.Project, paths, infolder.StatusWorking, run.ID); err != nil {
				e.log.Warn("pipeline: marking the files of a run", "run", run.ID, "err", err)
			}
		}
		return run, nil
	}
	if tr.On == TriggerIN && !tr.Batch {
		runs := make([]jobs.Run, 0, len(files))
		for _, f := range files {
			run, err := start(inFileInput(f), []infolder.File{f})
			if err != nil {
				return runs, skipped, err
			}
			runs = append(runs, run)
		}
		return runs, skipped, nil
	}
	run, err := start(e.batchInput(infolder.Batch{Project: p.Project}, files), files)
	if err != nil {
		return nil, skipped, err
	}
	return []jobs.Run{run}, skipped, nil
}

// skip says why an in trigger does not take a file.
func (t Trigger) skip(f infolder.File) INSkipped {
	s := INSkipped{Path: f.Path}
	switch {
	case t.Folder != "" && f.Dir != t.Folder && !strings.HasPrefix(f.Dir, t.Folder+"/"):
		s.Reason, s.Value, s.Why = "folder", t.Folder, "not in the folder "+t.Folder
	case len(t.Kinds) > 0 && !slices.Contains(t.Kinds, f.Ext):
		s.Reason, s.Value, s.Why = "kinds", strings.Join(t.Kinds, ", "), "not of the kinds "+strings.Join(t.Kinds, ", ")
	default:
		s.Reason, s.Value, s.Why = "match", t.Match, "its name does not match "+t.Match
	}
	return s
}
