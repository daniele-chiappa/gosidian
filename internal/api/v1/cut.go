package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/plugins"
)

// The cut view of the preview (IMP-170): a note cut as note.sections cuts
// it, each section and paragraph with its size, and, given a collection of
// an archive of vectors exposed in the project, whether the archive holds
// it as it is (same), holds another text of it (changed: to cut again), or
// does not hold it (missing). The note is never written.

// cutRequest asks /preview for the cut view instead of the preview.
type cutRequest struct {
	// Collection is <plugin>/<collection>; empty for the cut alone.
	Collection string               `json:"collection,omitempty"`
	MaxChars   int                  `json:"max_chars,omitempty"`
	Context    string               `json:"context,omitempty"`
	Words      *pipeline.MediaWords `json:"media_words,omitempty"`
	// InlineMaxSide is note.sections' inline_max_side: unset, its default.
	InlineMaxSide *int `json:"inline_max_side,omitempty"`
}

// cutRequestError is a cut request the reader fixes (400); any other error
// of the cut view is the server's.
type cutRequestError struct{ msg string }

func (e *cutRequestError) Error() string { return e.msg }

func badCut(format string, a ...any) error { return &cutRequestError{msg: fmt.Sprintf(format, a...)} }

// cutBlock is a block of the cut view: Head and HTML are its heading and
// its text rendered, as the preview renders a note (not sanitized).
type cutBlock struct {
	pipeline.CutBlock
	// State is same, changed or missing against the collection; empty
	// without one.
	State string `json:"state,omitempty"`
	Head  string `json:"head,omitempty"`
	HTML  string `json:"html"`
}

type cutResponse struct {
	Blocks     []cutBlock          `json:"blocks"`
	Sections   int                 `json:"sections"`
	Paragraphs int                 `json:"paragraphs"`
	Media      pipeline.MediaStats `json:"media"`
	MaxChars   int                 `json:"max_chars"`
	Collection string              `json:"collection,omitempty"`
	// Stored are the chunks of the note in the collection, Orphans those of
	// them whose base the note no longer has.
	Stored  int            `json:"stored"`
	Orphans int            `json:"orphans"`
	States  map[string]int `json:"states,omitempty"`
	// Error is why the collection could not be read: the cut is shown
	// without its states.
	Error string `json:"error,omitempty"`
}

// previewCut is the cut view of md, the text of the note at notePath.
func (r *Router) previewCut(ctx context.Context, p authz.Principal, caller, notePath, md string, c *cutRequest) (*cutResponse, error) {
	if !strings.HasSuffix(strings.ToLower(notePath), ".md") || !r.canSee(p, notePath) {
		return nil, badCut("the cut view is for a markdown note you may read")
	}
	project := projectOf(notePath)
	opt := pipeline.CutOptions{MaxChars: c.MaxChars, Context: c.Context, InlineMax: pipeline.DefaultInlineMaxSide}
	if c.Words != nil {
		opt.Words = *c.Words
	}
	if c.InlineMaxSide != nil {
		if *c.InlineMaxSide < 0 || *c.InlineMaxSide > 10000 {
			return nil, badCut("inline_max_side: 0 to 10000")
		}
		opt.InlineMax = *c.InlineMaxSide
	}
	// The rules are note.sections': a section up to a step's output.
	maxChars := 1 << 20
	if r.deps.Pipelines != nil {
		maxChars = r.deps.Pipelines.MaxOutputBytes()
	}
	switch {
	case opt.MaxChars == 0:
	case opt.MaxChars < 200 || opt.MaxChars > maxChars:
		return nil, badCut("max_chars: 200 to %d ([pipelines] max_output_bytes)", maxChars)
	}
	switch opt.Context {
	case "", "heading", "path", "none":
	default:
		return nil, badCut("context %q: heading, path or none", opt.Context)
	}
	raw := []byte(md)
	fm := parser.ParseFrontmatterFields(parser.FrontmatterRawForPath(notePath, raw))
	title, _ := fm["title"].(string)
	if title == "" {
		title = strings.TrimSuffix(path.Base(notePath), path.Ext(notePath))
		if r.deps.Index != nil {
			if n, err := r.deps.Index.Note(notePath); err == nil && n != nil && n.Title != "" {
				title = n.Title
			}
		}
	}
	blocks, st, err := pipeline.CutView(apiNotes{r: r, p: p}, project, notePath, title, parser.BodyAfterFrontmatter(raw), opt)
	if err != nil {
		return nil, err
	}
	out := &cutResponse{Blocks: make([]cutBlock, len(blocks)), Media: st, MaxChars: opt.MaxChars, Collection: c.Collection}
	if out.MaxChars == 0 {
		out.MaxChars = 6000
	}
	resolver := previewResolver{r: r, p: p, note: notePath}
	heads := map[string]string{}
	for i, b := range blocks {
		if b.Level == 1 {
			out.Sections++
		} else {
			out.Paragraphs++
		}
		html, err := r.deps.Renderer.Render([]byte(b.Show), resolver)
		if err != nil {
			return nil, fmt.Errorf("render: %w", err)
		}
		cb := cutBlock{CutBlock: b, HTML: html}
		if b.Level == 1 && b.Prefix != "" {
			h, ok := heads[b.Prefix]
			if !ok {
				if h, err = r.deps.Renderer.Render([]byte(b.Prefix), resolver); err != nil {
					return nil, fmt.Errorf("render: %w", err)
				}
				heads[b.Prefix] = h
			}
			cb.Head = h
		}
		out.Blocks[i] = cb
	}
	if c.Collection == "" {
		return out, nil
	}
	stored, err := r.storedChunks(ctx, project, caller, notePath, c.Collection)
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	out.States = map[string]int{}
	seen := map[string]bool{}
	for i := range out.Blocks {
		b := &out.Blocks[i]
		hashes, ok := stored[b.ID]
		switch {
		case !ok:
			b.State = "missing"
		case hashes[b.Hash]:
			b.State = "same"
		default:
			b.State = "changed"
		}
		out.States[b.State]++
		seen[b.ID] = true
	}
	for base, hashes := range stored {
		out.Stored += len(hashes)
		if !seen[base] {
			out.Orphans++
		}
	}
	return out, nil
}

// storedChunks reads the chunks of a note an archive holds, by base: the
// hashes of the texts stored under it, any version. It follows the pages
// of list, whose size the archive decides.
func (r *Router) storedChunks(ctx context.Context, project, caller, notePath, source string) (map[string]map[string]bool, error) {
	plugin, coll, ok := strings.Cut(source, "/")
	if !ok || plugin == "" || coll == "" {
		return nil, errors.New("collection is <plugin>/<collection>")
	}
	if r.deps.Plugins == nil {
		return nil, errors.New("the plugins are off on this server")
	}
	if !slices.Contains(r.deps.Plugins.ExposedIn(plugin), project) {
		return nil, fmt.Errorf("plugin %s is not exposed in project %s (gosidian plugin expose %s %s)", plugin, project, plugin, project)
	}
	if !r.readTools()[plugin]["list"] {
		return nil, fmt.Errorf("plugin %s has no list tool a reader may call", plugin)
	}
	r.auditPluginRead(ctx, caller, project, source)
	// The reader waits: at most [web] plugin_timeout, and a page at a
	// time up to [web] cut_max_chunks.
	ctx, cancel := context.WithTimeout(ctx, r.web.PluginTimeout)
	defer cancel()
	out := map[string]map[string]bool{}
	cursor, n := "", 0
	for {
		args := map[string]any{"collection": coll, "filter": map[string]any{"doc": notePath}, "fields": []any{"id", "meta.base", "meta.hash"}}
		if cursor != "" {
			args["cursor"] = cursor
		}
		res, err := r.deps.Plugins.CallTool(ctx, plugin, "list", args,
			map[string]any{plugins.MetaProject: project, plugins.MetaCaller: caller, plugins.MetaAccess: plugins.AccessRead})
		if err != nil {
			return nil, err
		}
		body := plugins.ResultJSON(res)
		if res.IsError {
			return nil, fmt.Errorf("plugin %s: %s", plugin, strings.TrimSpace(string(body)))
		}
		var list struct {
			Chunks []struct {
				ID   string `json:"id"`
				Meta struct {
					Base string `json:"base"`
					Hash string `json:"hash"`
				} `json:"meta"`
			} `json:"chunks"`
			Next string `json:"next"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, fmt.Errorf("plugin %s: the list is not what an archive gives: %w", plugin, err)
		}
		if n += len(list.Chunks); n > r.web.CutMaxChunks {
			return nil, fmt.Errorf("the note has more than %d chunks in %s ([web] cut_max_chunks)", r.web.CutMaxChunks, source)
		}
		for _, ch := range list.Chunks {
			base := ch.Meta.Base
			if base == "" {
				base, _, _ = strings.Cut(ch.ID, "@")
			}
			if out[base] == nil {
				out[base] = map[string]bool{}
			}
			out[base][ch.Meta.Hash] = true
		}
		// A page without chunks that still gives a next would never end.
		if list.Next == "" || len(list.Chunks) == 0 {
			return out, nil
		}
		cursor = list.Next
	}
}

// readTools lists, by plugin, the tools whose manifest asks only read
// access: the cut view calls an archive's list and collections for any
// reader of the project, so only when they are read tools.
func (r *Router) readTools() map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, t := range r.deps.Plugins.Tools() {
		if t.Spec.Access != plugins.AccessRead {
			continue
		}
		if out[t.Plugin] == nil {
			out[t.Plugin] = map[string]bool{}
		}
		out[t.Plugin][t.Spec.Name] = true
	}
	return out
}

// auditPluginRead records a plugin's tool the web UI called for a reader,
// to = <plugin>/<what>, as the graphs of the plugins do.
func (r *Router) auditPluginRead(ctx context.Context, caller, project, to string) {
	if r.deps.Audit == nil {
		return
	}
	e := audit.Entry{Source: audit.SourceHTTP, Actor: caller, Action: audit.ActionPluginCall, Path: project, To: to}
	if u := UserFromContext(ctx); u != nil {
		e.UserID = u.ID
	}
	_ = r.deps.Audit.Write(e)
}

// cutCollection is a collection the cut view can compare a note with.
type cutCollection struct {
	Source     string `json:"source"`
	Plugin     string `json:"plugin"`
	Collection string `json:"collection"`
	Model      string `json:"model,omitempty"`
	Count      int64  `json:"count"`
}

// handleCutCollections answers GET /api/v1/cut/collections?project=<p>:
// the collections of the archives exposed in the project (a plugin whose
// tools are list and collections), for the cut view.
func (r *Router) handleCutCollections(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	project := strings.TrimSpace(req.URL.Query().Get("project"))
	out := []cutCollection{}
	var problems []string
	if r.deps.Plugins != nil && project != "" && r.canSeeProject(req, project) {
		tools := r.readTools()
		caller := callerOf(req)
		for _, name := range r.deps.Plugins.Names() {
			if !tools[name]["list"] || !tools[name]["collections"] || !slices.Contains(r.deps.Plugins.ExposedIn(name), project) {
				continue
			}
			r.auditPluginRead(req.Context(), caller, project, name+"/collections")
			ctx, cancel := context.WithTimeout(req.Context(), r.web.PluginTimeout)
			res, err := r.deps.Plugins.CallTool(ctx, name, "collections", map[string]any{},
				map[string]any{plugins.MetaProject: project, plugins.MetaCaller: caller, plugins.MetaAccess: plugins.AccessRead})
			cancel()
			if err != nil || res.IsError {
				problems = append(problems, name+": unreachable")
				continue
			}
			var got struct {
				Collections []struct {
					Name  string `json:"name"`
					Model string `json:"model"`
					Count int64  `json:"count"`
				} `json:"collections"`
			}
			if json.Unmarshal(plugins.ResultJSON(res), &got) != nil {
				continue
			}
			for _, c := range got.Collections {
				out = append(out, cutCollection{Source: name + "/" + c.Name, Plugin: name, Collection: c.Name, Model: c.Model, Count: c.Count})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project, "collections": out, "problems": problems})
}

// callerOf names who asks a plugin from the web UI.
func callerOf(req *http.Request) string {
	if u := UserFromContext(req.Context()); u != nil {
		return "web:" + u.Username
	}
	return "web"
}

// apiNotes reads notes and folders for the cut view, as the reader may:
// the image notes of a note it may see (pipeline.NoteReader).
type apiNotes struct {
	r *Router
	p authz.Principal
}

func (n apiNotes) ReadNote(project, rel string) (pipeline.NoteData, error) {
	if !strings.HasPrefix(rel, project+"/") || !n.r.canSee(n.p, rel) {
		return pipeline.NoteData{}, pipeline.ErrNoNote
	}
	note, err := n.r.deps.Vault.Load(rel)
	if err != nil {
		return pipeline.NoteData{}, err
	}
	fm := parser.ParseFrontmatterFields(parser.FrontmatterRawForPath(note.Path, note.Content))
	return pipeline.NoteData{Path: note.Path, Title: note.Title, Frontmatter: fm, Body: parser.BodyAfterFrontmatter(note.Content), ETag: note.ETag(), Raw: note.Content}, nil
}

func (n apiNotes) NotesIn(project, folder string) ([]string, error) {
	folder = strings.TrimSuffix(folder, "/")
	if n.r.deps.Index == nil || !strings.HasPrefix(folder+"/", project+"/") {
		return nil, nil
	}
	rows, err := n.r.deps.Index.NotesByPrefix(folder)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, row := range rows {
		if path.Dir(row.Path) == folder && n.r.canSee(n.p, row.Path) {
			out = append(out, row.Path)
		}
	}
	return out, nil
}
