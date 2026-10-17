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
	"github.com/gosidian/gosidian/internal/marks"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/plugins"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/vault"
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

// SetMarks gives note.mark the store of the marks (IMP-188).
func (s *Server) SetMarks(m *marks.Store) { s.marks = m }

// PutMarks keeps a set of marks on a note of the project
// (pipeline.Marker).
func (h pipelineHost) PutMarks(project string, set marks.Set) error {
	if err := inProject(project, set.Path); err != nil {
		return err
	}
	if h.s.marks == nil {
		return errors.New("this server keeps no marks")
	}
	return h.s.marks.Put(set)
}

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

// The optional faces of the host the nodes look for.
var (
	_ pipeline.Folders    = pipelineHost{}
	_ pipeline.NoteLister = pipelineHost{}
	_ pipeline.Authors    = pipelineHost{}
	_ pipeline.Marker     = pipelineHost{}
	_ pipeline.TagPolicy  = pipelineHost{}
)

// PipelineTags says whether the project's pipelines may tag commits
// (pipeline_tags, IMP-201).
func (h pipelineHost) PipelineTags(project string) bool {
	return h.s.projects != nil && h.s.projects.PipelineTags(project)
}

// LastWriter is who wrote a note last, as the index keeps it from the
// audit: pipeline:<path> for a run of that pipeline (pipeline.Authors).
func (h pipelineHost) LastWriter(rel string) string {
	a, ok, err := h.s.index.AuthorOf(rel)
	if err != nil || !ok {
		return ""
	}
	return a.ModifiedBy
}

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
	return pipeline.NoteData{Path: note.Path, Title: title, Frontmatter: fm, Body: parser.BodyAfterFrontmatter(note.Content), ETag: note.ETag(), Raw: note.Content}, nil
}

// NotesIn lists the notes directly in a folder of the project, from the
// index (pipeline.Folders).
func (h pipelineHost) NotesIn(project, folder string) ([]string, error) {
	folder = strings.TrimSuffix(folder, "/")
	if err := inProject(project, folder+"/"); err != nil {
		return nil, err
	}
	rows, err := h.s.index.NotesByPrefix(folder)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range rows {
		if path.Dir(r.Path) == folder {
			out = append(out, r.Path)
		}
	}
	return out, nil
}

// ListNotes gives note.list a page of the notes of a folder of the project,
// from the index, with memory_query's conditions and sort
// (pipeline.NoteLister, IMP-186).
func (h pipelineHost) ListNotes(project string, q pipeline.NoteQuery) ([]pipeline.NoteRow, int, error) {
	folder := strings.Trim(q.Folder, "/")
	if folder != project {
		if err := inProject(project, folder+"/"); err != nil {
			return nil, 0, err
		}
	}
	if q.Recursive {
		folder += "/**"
	}
	var where []index.FieldCond
	if !isEmptyList(q.Where) {
		w, err := parseWhere(q.Where)
		if err != nil {
			return nil, 0, fmt.Errorf("with.where: %w", err)
		}
		where = w
	}
	keys, err := index.ParseSort(q.Sort, "")
	if err != nil {
		return nil, 0, fmt.Errorf("with.sort: %w", err)
	}
	opts := index.QueryOptions{Projects: []string{project}, Folders: []string{folder}, Where: where,
		Limit: q.Limit, Offset: q.Offset, Fields: q.Fields}
	opts.SetSortKeys(keys)
	hits, total, err := h.s.index.Query(opts)
	if err != nil {
		return nil, 0, err
	}
	rows := make([]pipeline.NoteRow, 0, len(hits))
	for _, hit := range hits {
		rows = append(rows, pipeline.NoteRow{Path: hit.Path, Title: hit.Title, Fields: hit.FieldValues()})
	}
	return rows, total, nil
}

// DeleteNote deletes a note of the project as the pipeline: into the trash
// when the server keeps one, as memory_delete does (pipeline.Folders).
func (h pipelineHost) DeleteNote(project, rel, actor string) error {
	if err := inProject(project, rel); err != nil {
		return err
	}
	if !h.s.vault.IsNoteFile(rel) {
		return fmt.Errorf("%s is not a note", rel)
	}
	return h.s.discardNote(rel, audit.SourcePipeline, actor)
}

// discardNote moves a note to the trash (deletes it, without one), as the
// server writing for source and actor.
func (s *Server) discardNote(rel string, source audit.Source, actor string) error {
	unlock := s.vault.LockPath(rel)
	defer unlock()
	if s.trash != nil {
		if _, err := s.vault.Load(rel); err != nil {
			return err
		}
		if _, err := s.trash.DiscardNote(rel); err != nil {
			return err
		}
	} else if err := s.vault.Delete(rel); err != nil {
		return err
	}
	if err := s.index.Delete(rel); err != nil {
		return err
	}
	if s.audit != nil {
		_ = s.audit.Write(audit.Entry{Source: source, Actor: actor, Action: audit.ActionDelete, Path: rel})
	}
	s.publishNoteChange("delete", rel, "", true)
	return nil
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

// UpdateNote writes a note from its content as it is, under the note's
// lock (pipeline.NoteUpdater): note.write's append and frontmatter_merge.
func (h pipelineHost) UpdateNote(project, rel string, build func(existing []byte) ([]byte, error), actor string) (pipeline.WriteResult, error) {
	if err := inProject(project, rel); err != nil {
		return pipeline.WriteResult{}, err
	}
	out, werr := h.s.writeNote(context.Background(), nil, noteWrite{
		rel: rel, mode: writeUpsert,
		content: func(existing *vault.Note) ([]byte, error) {
			if existing == nil {
				return build(nil)
			}
			return build(existing.Content)
		},
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
		if h.s.in != nil {
			return h.s.in.Read(context.Background(), rel, maxAttachmentRead)
		}
		return h.s.vault.INRead(rel, maxAttachmentRead)
	}
	return h.s.vault.ReadAttachment(rel, maxAttachmentRead)
}

// OpenFile opens an attachment of the project, or a file of its IN folder
// (IMP-172), a source's too, for a plugin that fetches it by reference.
func (h pipelineHost) OpenFile(project, rel string) (io.ReadSeekCloser, pipeline.FileStat, error) {
	if err := inProject(project, rel); err != nil {
		return nil, pipeline.FileStat{}, err
	}
	if h.s.vault.InArea(rel) {
		var f io.ReadSeekCloser
		var e vault.INEntry
		var err error
		if h.s.in != nil {
			f, e, err = h.s.in.Open(context.Background(), rel)
		} else {
			f, e, err = h.s.vault.INOpen(rel)
		}
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
// IN folder's state holds, while the file did not change, else read. A
// source's file is described as in.db holds it, its SHA-256 the one its
// source gave, or none: it is not read for it.
func (h pipelineHost) StatFile(project, rel string) (pipeline.FileStat, error) {
	if err := inProject(project, rel); err != nil {
		return pipeline.FileStat{}, err
	}
	if h.s.in != nil && h.s.vault.InArea(rel) {
		if row, err := h.s.in.Store().File(project, rel); err == nil && row.Remote() {
			return pipeline.FileStat{Path: row.Path, Name: row.Name, Size: row.Size, ModTime: row.Modified, SHA256: row.SHA256}, nil
		}
	}
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
