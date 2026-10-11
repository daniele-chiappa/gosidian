package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/infolder"
	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/plugins"
	"github.com/gosidian/gosidian/internal/server/events"
)

// The pipelines (IMP-167, ADR-035) act through the server: PipelineHost
// reads and writes as the pipeline, an identity limited to the pipeline's
// project, through writeNote (ADR-034) with the audit source pipeline.

// SetPipelines gives the server the pipeline engine; nil while
// [pipelines] is off.
func (s *Server) SetPipelines(e *pipeline.Engine) { s.pipelines = e }

// Pipelines is the pipeline engine, nil when off.
func (s *Server) Pipelines() *pipeline.Engine { return s.pipelines }

// SetPlugins gives the server the plugin registry (IMP-168); nil while
// [pipelines] is off.
func (s *Server) SetPlugins(r *plugins.Registry) { s.plugins = r }

// Plugins is the plugin registry, nil when the pipelines are off.
func (s *Server) Plugins() *plugins.Registry { return s.plugins }

// SetIN gives the server the state of the IN folders (IMP-172).
func (s *Server) SetIN(svc *infolder.Service) { s.in = svc }

// SetPipelinesSwitch tells the server whether [pipelines] is on, before
// the engine exists (the host is built first).
func (s *Server) SetPipelinesSwitch(on bool) { s.pipelinesOn = on }

// PipelineHost is the server as the pipelines' host.
func (s *Server) PipelineHost() pipeline.Host { return pipelineHost{s} }

type pipelineHost struct{ s *Server }

// maxAttachmentRead caps an attachment a node reads (an image for a model).
const maxAttachmentRead = 20 << 20

func (h pipelineHost) types() pipeline.Types {
	if h.s.pipelines != nil {
		return h.s.pipelines.Types()
	}
	return pipeline.NewRegistry()
}

func (h pipelineHost) Enabled(project string) bool {
	return h.s.pipelinesOn && h.s.projects != nil && h.s.projects.UsesPipelines(project)
}

func (h pipelineHost) fromNote(rel string, content []byte) pipeline.Pipeline {
	proj, _, _ := strings.Cut(rel, "/")
	fm := parser.ParseFrontmatterFields(parser.FrontmatterRawForPath(rel, content))
	title, _ := fm["title"].(string)
	if title == "" {
		title = strings.TrimSuffix(path.Base(rel), ".md")
	}
	if t, _ := fm["type"].(string); t != pipeline.NoteType {
		return pipeline.Pipeline{Path: rel, Project: proj, Title: title, Err: fmt.Errorf("%s is not a pipeline note (type: %s)", rel, pipeline.NoteType)}
	}
	p := pipeline.FromNote(rel, proj, title, content, h.types())
	if p.Err == nil && !h.Enabled(proj) {
		p.Err = pipeline.ErrDisabled
	}
	return p
}

func (h pipelineHost) Pipelines(project string) ([]pipeline.Pipeline, error) {
	opts := index.QueryOptions{
		Where: []index.FieldCond{{Field: "type", Op: index.OpEq, Values: []string{pipeline.NoteType}}},
		Sort:  "path",
		Limit: index.QueryMaxLimit,
	}
	if project != "" {
		opts.Projects = []string{project}
	}
	hits, _, err := h.s.index.Query(opts)
	if err != nil {
		return nil, err
	}
	out := make([]pipeline.Pipeline, 0, len(hits))
	for _, hit := range hits {
		note, err := h.s.vault.Load(hit.Path)
		if err != nil {
			continue
		}
		out = append(out, h.fromNote(hit.Path, note.Content))
	}
	return out, nil
}

func (h pipelineHost) Pipeline(rel string) (pipeline.Pipeline, error) {
	note, err := h.s.vault.Load(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return pipeline.Pipeline{}, fmt.Errorf("%w: %s", pipeline.ErrNoNote, rel)
	}
	if err != nil {
		return pipeline.Pipeline{}, err
	}
	return h.fromNote(note.Path, note.Content), nil
}

func inProject(project, rel string) error {
	if !strings.HasPrefix(rel, project+"/") {
		return fmt.Errorf("%s is outside the pipeline's project %q", rel, project)
	}
	return nil
}

func (h pipelineHost) ReadNote(project, rel string) (pipeline.NoteData, error) {
	if err := inProject(project, rel); err != nil {
		return pipeline.NoteData{}, err
	}
	note, err := h.s.vault.Load(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return pipeline.NoteData{}, fmt.Errorf("%w: %s", pipeline.ErrNoNote, rel)
	}
	if err != nil {
		return pipeline.NoteData{}, err
	}
	fm := parser.ParseFrontmatterFields(parser.FrontmatterRawForPath(note.Path, note.Content))
	title, _ := fm["title"].(string)
	if title == "" {
		title = note.Title
	}
	return pipeline.NoteData{Path: note.Path, Title: title, Frontmatter: fm, Body: parser.BodyAfterFrontmatter(note.Content), ETag: note.ETag()}, nil
}

func (h pipelineHost) WriteNote(project, rel string, content []byte, mode pipeline.WriteMode, actor string) (pipeline.WriteResult, error) {
	if err := inProject(project, rel); err != nil {
		return pipeline.WriteResult{}, err
	}
	m := writeUpsert
	switch mode {
	case pipeline.WriteCreate:
		m = writeCreate
	case pipeline.WriteReplace:
		m = writeReplace
	}
	out, werr := h.s.writeNote(context.Background(), nil, noteWrite{
		rel: rel, mode: m, content: fixedContent(content),
		automation: true, source: audit.SourcePipeline, actor: actor,
	})
	if werr != nil {
		return pipeline.WriteResult{}, errors.New(werr.msg)
	}
	return pipeline.WriteResult{Path: out.Path, ETag: out.ETag, Created: out.Created}, nil
}

func (h pipelineHost) ReadAttachment(project, rel string) ([]byte, error) {
	if err := inProject(project, rel); err != nil {
		return nil, err
	}
	if h.s.vault.InArea(rel) {
		return h.s.vault.INRead(rel, maxAttachmentRead)
	}
	return h.s.vault.ReadAttachment(rel, maxAttachmentRead)
}

// OpenFile opens an attachment of the project, or a file of its IN folder
// (IMP-172), for a plugin that fetches it by reference.
func (h pipelineHost) OpenFile(project, rel string) (io.ReadSeekCloser, pipeline.FileStat, error) {
	if err := inProject(project, rel); err != nil {
		return nil, pipeline.FileStat{}, err
	}
	if h.s.vault.InArea(rel) {
		f, e, err := h.s.vault.INOpen(rel)
		if err != nil {
			return nil, pipeline.FileStat{}, err
		}
		return f, pipeline.FileStat{Path: e.Path, Name: path.Base(e.Path), Size: e.Size, ModTime: e.ModTime}, nil
	}
	f, info, clean, err := h.s.vault.OpenAttachment(rel)
	if err != nil {
		return nil, pipeline.FileStat{}, err
	}
	return f, pipeline.FileStat{Path: clean, Name: path.Base(clean), Size: info.Size(), ModTime: info.ModTime()}, nil
}

// StatFile describes a file OpenFile opens, with its SHA-256: the one the
// IN folder's state holds, while the file did not change, else read.
func (h pipelineHost) StatFile(project, rel string) (pipeline.FileStat, error) {
	f, st, err := h.OpenFile(project, rel)
	if err != nil {
		return pipeline.FileStat{}, err
	}
	defer f.Close()
	if h.s.in != nil && h.s.vault.InArea(st.Path) {
		if row, err := h.s.in.Store().File(project, st.Path); err == nil && row.SHA256 != "" &&
			row.Size == st.Size && row.Modified.Equal(st.ModTime.UTC()) {
			st.SHA256 = row.SHA256
			return st, nil
		}
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return pipeline.FileStat{}, err
	}
	st.SHA256 = hex.EncodeToString(sum.Sum(nil))
	return st, nil
}

// StoreAttachment stores a file a node made (a plugin's images) as an
// attachment of the project, audited as the pipeline. It does not call
// arrived: a run's files start no runs.
func (h pipelineHost) StoreAttachment(project, name string, data []byte, actor string) (string, error) {
	res, err := attach.Store(h.s.vault, data, name, project)
	if err != nil {
		return "", err
	}
	if h.s.audit != nil {
		_ = h.s.audit.Write(audit.Entry{Source: audit.SourcePipeline, Actor: actor, Action: audit.ActionUploadAttachment, Path: res.Path, Size: int64(len(data))})
	}
	return res.Path, nil
}

// arrived starts the ingest pipelines of the attachment's project for a
// document just stored (IMP-167): name is its original file name, note the
// note made with it ("" for none). The first store of a file starts the
// runs: the attachment's path is their idempotency key.
func (s *Server) arrived(ctx context.Context, att, name, note string) {
	if s.pipelines == nil {
		return
	}
	actor := ""
	if tok := s.tokenFromContext(ctx); tok != nil {
		actor = tok.Name
	}
	if name == "" {
		name = path.Base(att)
	}
	s.pipelines.OnAttachment(auth.ProjectOf(att), att, path.Base(name), note, actor)
}

// uploadName is an upload's original file name: filename, else the base of
// the file it came from.
func uploadName(filename, sourcePath, bridgeFilename string) string {
	for _, n := range []string{filename, sourcePath, bridgeFilename} {
		if n = strings.TrimSpace(n); n != "" {
			return path.Base(strings.ReplaceAll(n, "\\", "/"))
		}
	}
	return ""
}

func (h pipelineHost) Changed(run jobs.Run) {
	if h.s.events == nil {
		return
	}
	h.s.events.Publish(events.TopicJob, map[string]any{
		"action": "run", "run": run.ID, "pipeline": run.Ref, "path": run.Ref, "project": run.Project, "status": run.Status,
	})
}
