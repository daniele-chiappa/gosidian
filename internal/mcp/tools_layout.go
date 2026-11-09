package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/fonts"
	"github.com/gosidian/gosidian/internal/layout"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/plugins"
	"github.com/gosidian/gosidian/internal/vault"
)

// The page layouts of the notes, for an agent (IMP-226): a note's pieces, and
// its design in a session. The engine runs in the plugin of the layout app
// (engine_*, internal tools), one process a session; these tools bring it the
// design with the token's rights and take it back, so the plugin keeps no
// files and no credentials. A session is not saved at each call: a save
// leaves a new document among the attachments, megabytes for a booklet, so
// there is one save, against the etag taken at the opening. The tools show
// where the owner exposed the plugin, like a plugin's own.

const (
	layoutNoteType = "layout"
	// engineChunk is the most a call carries of a package or a font.
	engineChunk = 4 << 20
	// sessionGone starts the engine's refusal of a session that is no more.
	sessionGone = "session gone"
	// layoutSessionLife is how long an unused session is remembered here:
	// the plugin ends it much sooner, this only keeps the table small.
	layoutSessionLife = 24 * time.Hour
)

// layoutTools are the tools shown where the engine's plugin is exposed.
var layoutTools = map[string]bool{
	"layout_pieces": true, "layout_add_piece": true, "layout_number_pieces": true, "layout_open": true,
	"layout_run": true, "layout_render": true, "layout_commands": true, "layout_save": true, "layout_close": true,
}

// layoutFileCommands are the commands whose path names a file of the vault:
// the session has a copy, the path of the copy goes to the engine.
var layoutFileCommands = map[string]bool{"piece.placeImage": true, "file.place": true}

// layoutSession is a design open in the plugin, for one token. mu keeps one
// call at a time on it: etag and files change with them.
type layoutSession struct {
	id, note, project, plugin, token string
	// version is the commit of the version open, for reading: it has no
	// save. "" is the note's design as it is.
	version string

	used time.Time // under Server.layoutMu

	mu    sync.Mutex
	etag  string
	files map[string]string // vault path → the copy's, in the session
}

// SetLayout gives the limits of a design and the installation's fonts.
func (s *Server) SetLayout(lim layout.Limits, f *fonts.Store) {
	s.layoutLimits, s.fonts = lim, f
}

// layoutPlugin is the plugin with the engine, if any.
func (s *Server) layoutPlugin() (string, bool) {
	if s.plugins == nil {
		return "", false
	}
	return s.plugins.AppTool(layoutNoteType, "engine_open")
}

// layoutVisible says whether tok is offered the layout tools: the engine's
// plugin is exposed in a project it reaches. A write takes more, which each
// tool checks on its note.
func (s *Server) layoutVisible(tok *auth.Token) bool {
	plugin, ok := s.layoutPlugin()
	if !ok {
		return false
	}
	for _, p := range s.plugins.ExposedIn(plugin) {
		if !s.projectHidden(p) && (tok == nil || (tok.HasScope(auth.ScopeRead) && tok.AllowsProject(p))) {
			return true
		}
	}
	return false
}

func (s *Server) registerLayoutTools() {
	note := mcp.WithString("note", mcp.Required(), mcp.Description("Vault-relative path of the markdown note, e.g. 'proj/booklet.md'."))
	session := mcp.WithString("session", mcp.Required(), mcp.Description("The session layout_open gave."))
	s.impl.AddTool(mcp.NewTool("layout_pieces",
		mcp.WithDescription("The pieces of a page-layout note, with number n, hash, kind, text and an image's path. Text with no numbers yet comes back as one piece, n 0: layout_number_pieces splits it. Place a piece in the design with piece.placeText or piece.placeImage (layout_run), giving n, hash and its text as markdown: that links the frame to the note."),
		note,
	), s.handleLayoutPieces)
	s.impl.AddTool(mcp.NewTool("layout_add_piece",
		mcp.WithDescription("Add a piece to a page-layout note: a text or an image, after a piece or at the end, so the note grows with the design. Answers with the piece, its n and hash, and the note's new etag. An open session sees it: the note's text is not the design."),
		note,
		mcp.WithNumber("after", mcp.Description("The piece it follows; omitted, it goes at the end.")),
		mcp.WithString("text", mcp.Description("Markdown of the piece. Give this or image.")),
		mcp.WithString("image", mcp.Description("Vault path of an attachment. Give this or text.")),
		mcp.WithString("alt", mcp.Description("The image's alternative text.")),
		mcp.WithString("if_match", mcp.Description("The note's etag: the write fails if it changed.")),
	), s.handleLayoutAddPiece)
	s.impl.AddTool(mcp.NewTool("layout_number_pieces",
		mcp.WithDescription("Number the blocks of a page-layout note, so that each is a piece that can be placed: every block without a number, or with n those of one piece, each its own."),
		note,
		mcp.WithNumber("n", mcp.Description("Split this piece into one piece per block; omitted, number all that have none.")),
	), s.handleLayoutNumberPieces)
	s.impl.AddTool(mcp.NewTool("layout_open",
		mcp.WithDescription("Open a note's design in a session: a note without one gets a new document. It answers with the installation's fonts, which the design may use beside the engine's own. Work with layout_run and layout_render, then layout_save; nothing reaches the vault before that. A session unused for a while ends, with what it had not saved. Asking again for a note whose session this token has open gives that session."),
		note,
		mcp.WithString("version", mcp.Description("\"valid\" or a tag of memory_versions: the design as that version holds it, to read and render; it has no layout_save. \"draft\": as it is, when the note gives you a version of its own.")),
		mcp.WithObject("page", mcp.Description("For a new document: width, height (points), or preset ('A4', 'A5', 'Letter'); pages, columns, gutter, facingPages, title, units; margins, a number or {top, bottom, inside, outside}; bleed, a number or [top, bottom, inside, outside].")),
	), s.handleLayoutOpen)
	s.impl.AddTool(mcp.NewTool("layout_run",
		mcp.WithDescription("Run commands on the session's design, in order, stopping at the first error: what ran before it stays, and edit.undo takes it back. layout_commands lists them. A later command takes a value of an earlier one's result as \"$N.path\" (0-based), e.g. \"$0.story\". In piece.placeImage and file.place, path is the vault path of an attachment. table.* commands name a table by story alone, or with table when the story has several. A rectangle or band is frame.create with content unassigned, then object.fill; a colour is swatch.create first."),
		session,
		mcp.WithArray("commands", mcp.Required(), mcp.Description("[{command, params}]."), mcp.Items(map[string]any{"type": "object",
			"required": []string{"command"}, "properties": map[string]any{"command": map[string]any{"type": "string"}, "params": map[string]any{"type": "object"}}})),
	), s.handleLayoutRun)
	s.impl.AddTool(mcp.NewTool("layout_render",
		mcp.WithDescription("One page of the session's design as a PNG, as it prints. Look at it after a change: what a command answers does not show what the page looks like."),
		session,
		mcp.WithNumber("page", mcp.Description("Page index, 0-based.")),
		mcp.WithNumber("scale", mcp.Description("Pixels per point; 1 (the default) is 72 ppi. A result over [mcp] max_plugin_result is refused: lower it.")),
	), s.handleLayoutRender)
	s.impl.AddTool(mcp.NewTool("layout_commands",
		mcp.WithDescription("The engine's commands with their parameters. Filter them: the whole list is long."),
		session,
		mcp.WithString("filter", mcp.Description("Text in the id, label or menu, e.g. 'frame', 'style', 'piece'.")),
		mcp.WithBoolean("enabled_only", mcp.Description("Only what can run now.")),
	), s.handleLayoutCommands)
	s.impl.AddTool(mcp.NewTool("layout_save",
		mcp.WithDescription("Save the session's design into the note. It fails if the design was saved since it was opened: open it again and redo the work. It leaves a new document among the attachments, so save at the end and not after each step. The session stays open."),
		session,
	), s.handleLayoutSave)
	s.impl.AddTool(mcp.NewTool("layout_close",
		mcp.WithDescription("End a session and what it had not saved."),
		session,
	), s.handleLayoutClose)
}

// layoutNote is the note a layout tool names, read access checked: its
// vault path and its project, where the engine's plugin must be exposed.
func (s *Server) layoutNote(tok *auth.Token, req mcp.CallToolRequest) (rel, plugin string, errRes *mcp.CallToolResult) {
	raw, err := req.RequireString("note")
	if err != nil {
		return "", "", mcp.NewToolResultError(err.Error())
	}
	rel, err = s.vault.Rel(s.notePathArg(tok, raw))
	if err != nil {
		return "", "", mcp.NewToolResultErrorFromErr("invalid path", err)
	}
	if !tok.AllowsPath(rel) || s.pathInHiddenProject(rel) {
		return "", "", mcp.NewToolResultErrorf("note %q not found", rel)
	}
	if path.Ext(rel) != ".md" {
		return "", "", mcp.NewToolResultErrorf("%q: a layout is a markdown note", rel)
	}
	plugin, ok := s.layoutPlugin()
	if !ok {
		return "", "", mcp.NewToolResultError("no plugin offers the page-layout engine")
	}
	project := auth.ProjectOf(rel)
	for _, p := range s.plugins.ExposedIn(plugin) {
		if p == project {
			return rel, plugin, nil
		}
	}
	return "", "", mcp.NewToolResultErrorf("plugin %s is not exposed in project %q (its owner exposes it with gosidian plugin expose %s %s)", plugin, project, plugin, project)
}

func (s *Server) handleLayoutPieces(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	rel, _, errRes := s.layoutNote(tok, req)
	if errRes != nil {
		return errRes, nil
	}
	note, err := s.vault.Load(rel)
	if err != nil {
		return readNoteError(rel, err), nil
	}
	note, chosen := s.forMCP(note)
	return mcp.NewToolResultJSON(withChosen(map[string]any{"path": rel, "etag": note.ETag(), "pieces": s.layoutPieces(tok, rel, note.Content)}, chosen))
}

// layoutPieces are a note's pieces, each image with the attachment it names
// when tok may read it.
func (s *Server) layoutPieces(tok *auth.Token, rel string, content []byte) []layout.Piece {
	pieces := layout.Pieces(content)
	if pieces == nil {
		pieces = []layout.Piece{}
	}
	var described map[string]string
	for i := range pieces {
		img := pieces[i].Image
		if img == nil {
			continue
		}
		if described == nil {
			described, _ = pipeline.MediaDescriptions(pipelineHost{s: s}, auth.ProjectOf(rel), rel)
		}
		img.Path = s.layoutImage(tok, rel, img.Src)
		img.Described = described[img.Path]
	}
	return pieces
}

// layoutImage is the attachment an image of the note at rel names, "" when
// it names none tok may read.
func (s *Server) layoutImage(tok *auth.Token, rel, src string) string {
	found := layout.ImageTarget(rel, src, func(name string) string {
		f, _ := s.vault.ResolveAttachment(name, auth.ProjectOf(rel), func(p string) bool { return tok.AllowsProject(p) && !s.projectHidden(p) })
		return f
	})
	if found == "" {
		return ""
	}
	return s.layoutAttachment(tok, found)
}

// layoutAttachment is p as a clean path, when it is an attachment tok may
// read; "" otherwise.
func (s *Server) layoutAttachment(tok *auth.Token, p string) string {
	clean, err := s.vault.Rel(p)
	if err != nil || !tok.AllowsPath(clean) || s.pathInHiddenProject(clean) || !strings.Contains("/"+clean, "/attachments/") || !s.vault.Exists(clean) {
		return ""
	}
	return clean
}

func (s *Server) handleLayoutAddPiece(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	reader, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	rel, _, errRes := s.layoutNote(reader, req)
	if errRes != nil {
		return errRes, nil
	}
	tok, errRes := s.authorizeWrite(ctx, rel)
	if errRes != nil {
		return errRes, nil
	}
	text, image := req.GetString("text", ""), req.GetString("image", "")
	if (strings.TrimSpace(text) == "") == (image == "") {
		return mcp.NewToolResultError("give text or image"), nil
	}
	if image != "" {
		clean := s.layoutAttachment(tok, image)
		if clean == "" {
			return mcp.NewToolResultErrorf("image %q: no attachment this token may read", image), nil
		}
		alt := strings.NewReplacer("[", "", "]", "", "\n", " ").Replace(req.GetString("alt", ""))
		text = "![" + alt + "](" + attach.FileURL(clean) + ")"
	}
	var n int
	var content []byte
	out, werr := s.writeNote(ctx, tok, noteWrite{rel: rel, mode: writeReplace, ifMatch: req.GetString("if_match", ""),
		content: func(existing *vault.Note) ([]byte, error) {
			content, n = layout.Add(existing.Content, req.GetInt("after", 0), text)
			return content, nil
		}})
	if werr != nil {
		return werr.result(), nil
	}
	var added *layout.Piece
	for _, p := range s.layoutPieces(tok, rel, content) {
		if p.N == n {
			added = &p
		}
	}
	return mcp.NewToolResultJSON(map[string]any{"path": rel, "etag": out.ETag, "added": added})
}

func (s *Server) handleLayoutNumberPieces(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	reader, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	rel, _, errRes := s.layoutNote(reader, req)
	if errRes != nil {
		return errRes, nil
	}
	tok, errRes := s.authorizeWrite(ctx, rel)
	if errRes != nil {
		return errRes, nil
	}
	var content []byte
	out, werr := s.writeNote(ctx, tok, noteWrite{rel: rel, mode: writeReplace,
		content: func(existing *vault.Note) ([]byte, error) {
			content = layout.Number(existing.Content, req.GetInt("n", 0))
			return content, nil
		}})
	if werr != nil {
		return werr.result(), nil
	}
	return mcp.NewToolResultJSON(map[string]any{"path": rel, "etag": out.ETag, "pieces": s.layoutPieces(tok, rel, content)})
}

func (s *Server) handleLayoutOpen(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	rel, plugin, errRes := s.layoutNote(tok, req)
	if errRes != nil {
		return errRes, nil
	}
	note, err := s.vault.Load(rel)
	if err != nil {
		return readNoteError(rel, err), nil
	}
	at, _, errRes := s.noteVersion(tok, note, s.validOf(rel, note.Content), req.GetString("version", ""))
	if errRes != nil {
		return errRes, nil
	}
	sha := ""
	if at != nil {
		sha = at.SHA
	}
	if ls := s.layoutSessionOn(tok, rel); ls != nil {
		ls.mu.Lock()
		defer ls.mu.Unlock()
		if ls.version != sha {
			return mcp.NewToolResultErrorf("session %s is open on %q at another version: layout_close it first", ls.id, rel), nil
		}
		return mcp.NewToolResultJSON(map[string]any{"session": ls.id, "note": rel, "etag": ls.etag, "already_open": true})
	}
	content := note.Content
	if at != nil {
		content = at.Content
	}
	opened, err := layout.Open(content, s.layoutLimits, func(link string, max int64) ([]byte, string, bool) {
		clean, err := s.vault.Rel(link)
		if err != nil {
			return nil, "", false
		}
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(clean), "."))
		readable := tok.AllowsPath(clean) && !s.pathInHiddenProject(clean)
		if readable {
			if data, err := s.vault.ReadAttachment(clean, max); err == nil {
				return data, ext, true
			}
		}
		// The version's own files, where it sat, or what the token reads.
		if at == nil || (!readable && auth.ProjectOf(clean) != auth.ProjectOf(at.Path)) {
			return nil, "", false
		}
		data, err := s.git.FileAt(at.SHA, clean, max)
		if err != nil {
			return nil, "", false
		}
		return data, ext, true
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var b [12]byte
	_, _ = rand.Read(b[:])
	ls := &layoutSession{id: hex.EncodeToString(b[:]), note: rel, project: auth.ProjectOf(rel), plugin: plugin, token: tok.ID,
		version: sha, etag: layout.ETag(opened.File), files: map[string]string{}, used: time.Now()}
	if res, err := s.engine(ctx, tok, ls, "engine_open", nil); err != nil || res.IsError {
		return layoutFailure(res, err), nil
	}
	sent, res, err := s.layoutFill(ctx, tok, ls, opened, req)
	if err != nil || res.IsError {
		_, _ = s.engine(context.WithoutCancel(ctx), tok, ls, "engine_close", nil)
		return layoutFailure(res, err), nil
	}
	s.layoutRemember(ls)
	out := map[string]any{"session": ls.id, "note": rel, "etag": ls.etag, "new": opened.Package == nil, "fonts": sent}
	if at != nil {
		out["read_only"] = true
	}
	if len(opened.Missing) > 0 {
		out["missing_assets"] = opened.Missing
	}
	return mcp.NewToolResultJSON(out)
}

// layoutFill gives a new session the fonts and then the design: the fonts
// first, or the document would open with others in their place. It answers
// with the fonts that went, and the engine's result of the document.
func (s *Server) layoutFill(ctx context.Context, tok *auth.Token, ls *layoutSession, opened *layout.Opened, req mcp.CallToolRequest) ([]string, *mcp.CallToolResult, error) {
	sent := []string{}
	if s.fonts != nil {
		list, err := s.fonts.List()
		if err != nil {
			return nil, nil, err
		}
		for _, f := range list {
			data, _, err := s.fonts.Read(f.ID)
			if err != nil {
				return nil, nil, err
			}
			res, err := s.enginePut(ctx, tok, ls, "font", f.ID, data)
			if err != nil || res.IsError {
				return nil, res, err
			}
			sent = append(sent, f.Family+" "+f.Style)
		}
	}
	var res *mcp.CallToolResult
	var err error
	if opened.Package != nil {
		res, err = s.enginePut(ctx, tok, ls, "package", "design", opened.Package)
	} else {
		page, _ := req.GetArguments()["page"].(map[string]any)
		res, err = s.engine(ctx, tok, ls, "engine_new", map[string]any{"params": page})
	}
	return sent, res, err
}

func layoutFailure(res *mcp.CallToolResult, err error) *mcp.CallToolResult {
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	return res
}

// layoutSessionOn is the session tok has on a note, if any.
func (s *Server) layoutSessionOn(tok *auth.Token, rel string) *layoutSession {
	s.layoutMu.Lock()
	defer s.layoutMu.Unlock()
	for _, ls := range s.layoutSessions {
		if ls.token == tok.ID && ls.note == rel {
			return ls
		}
	}
	return nil
}

// layoutRemember keeps a session, and forgets those long unused.
func (s *Server) layoutRemember(ls *layoutSession) {
	s.layoutMu.Lock()
	defer s.layoutMu.Unlock()
	if s.layoutSessions == nil {
		s.layoutSessions = map[string]*layoutSession{}
	}
	for id, old := range s.layoutSessions {
		if time.Since(old.used) > layoutSessionLife {
			delete(s.layoutSessions, id)
		}
	}
	s.layoutSessions[ls.id] = ls
}

func (s *Server) layoutForget(ls *layoutSession) {
	s.layoutMu.Lock()
	defer s.layoutMu.Unlock()
	delete(s.layoutSessions, ls.id)
}

// layoutSessionOf is the session a tool names, locked: layoutSessionNamed,
// with the rights of its opening still standing. The caller unlocks it.
func (s *Server) layoutSessionOf(ctx context.Context, req mcp.CallToolRequest) (*auth.Token, *layoutSession, *mcp.CallToolResult) {
	tok, ls, errRes := s.layoutSessionNamed(ctx, req)
	if errRes != nil {
		return nil, nil, errRes
	}
	switch {
	case !tok.AllowsPath(ls.note) || s.pathInHiddenProject(ls.note):
		ls.mu.Unlock()
		return nil, nil, mcp.NewToolResultErrorf("note %q not found", ls.note)
	case !slices.Contains(s.plugins.ExposedIn(ls.plugin), ls.project):
		ls.mu.Unlock()
		return nil, nil, mcp.NewToolResultErrorf("plugin %s is not exposed in project %q any more: layout_close ends this session", ls.plugin, ls.project)
	}
	return tok, ls, nil
}

// layoutSessionNamed is the session a tool names, locked, for its token
// alone: another token's is as unknown as one that never was.
func (s *Server) layoutSessionNamed(ctx context.Context, req mcp.CallToolRequest) (*auth.Token, *layoutSession, *mcp.CallToolResult) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return nil, nil, errRes
	}
	id, err := req.RequireString("session")
	if err != nil {
		return nil, nil, mcp.NewToolResultError(err.Error())
	}
	s.layoutMu.Lock()
	ls, ok := s.layoutSessions[id]
	ok = ok && ls.token == tok.ID
	if ok {
		ls.used = time.Now()
	}
	s.layoutMu.Unlock()
	if !ok {
		return nil, nil, mcp.NewToolResultErrorf("no session %q: layout_open gives one", id)
	}
	ls.mu.Lock()
	return tok, ls, nil
}

// engine calls a tool of the engine for the session, within the plugin's
// calls at once.
func (s *Server) engine(ctx context.Context, tok *auth.Token, ls *layoutSession, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	if args == nil {
		args = map[string]any{}
	}
	args["session"] = ls.id
	cctx, cancel := context.WithTimeout(ctx, s.pluginToolTimeout)
	defer cancel()
	if gate := s.pluginGate(); gate != nil {
		res := s.plugins.PluginResources(ls.plugin)
		if err := gate.Take(cctx, res); err != nil {
			return nil, fmt.Errorf("plugin %s is busy: its calls at once (max_concurrent) are all under way; try again in a while", ls.plugin)
		}
		defer gate.Release(res)
	}
	meta := map[string]any{plugins.MetaProject: ls.project, plugins.MetaCaller: tok.ID, plugins.MetaAccess: plugins.AccessRead}
	return s.plugins.CallTool(cctx, ls.plugin, tool, args, meta)
}

// enginePut hands the session a package, a font or a file, in pieces: the
// last answers for the whole.
func (s *Server) enginePut(ctx context.Context, tok *auth.Token, ls *layoutSession, what, name string, data []byte) (*mcp.CallToolResult, error) {
	for off := 0; ; off += engineChunk {
		end := min(off+engineChunk, len(data))
		res, err := s.engine(ctx, tok, ls, "engine_put", map[string]any{"what": what, "name": name, "offset": off,
			"data": base64.StdEncoding.EncodeToString(data[off:end]), "last": end == len(data)})
		if err != nil || res.IsError || end == len(data) {
			return res, err
		}
	}
}

// layoutAnswer passes the engine's result on: a session the plugin no longer
// has is forgotten, and a result over the cap refused.
func (s *Server) layoutAnswer(ls *layoutSession, res *mcp.CallToolResult, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if res.IsError && strings.HasPrefix(engineText(res), sessionGone) {
		s.layoutForget(ls)
		return mcp.NewToolResultError("the session ended, unused for a while or with the engine: layout_open opens the saved design again, and what was not saved is lost"), nil
	}
	if n := resultSize(res); int64(n) > s.maxPluginResult {
		return mcp.NewToolResultErrorf("the engine gave a result of %d bytes, more than %d ([mcp] max_plugin_result): ask for less", n, s.maxPluginResult), nil
	}
	return res, nil
}

// engineText is the text of a result.
func engineText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

func (s *Server) handleLayoutRun(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, ls, errRes := s.layoutSessionOf(ctx, req)
	if errRes != nil {
		return errRes, nil
	}
	defer ls.mu.Unlock()
	commands, _ := req.GetArguments()["commands"].([]any)
	if len(commands) == 0 {
		return mcp.NewToolResultError("commands: a list of {command, params}"), nil
	}
	for i, c := range commands {
		cmd, _ := c.(map[string]any)
		name, _ := cmd["command"].(string)
		if name == "" {
			return mcp.NewToolResultErrorf("commands[%d]: {command, params}", i), nil
		}
		params, _ := cmd["params"].(map[string]any)
		file, _ := params["path"].(string)
		if !layoutFileCommands[name] || file == "" || strings.HasPrefix(file, "$") {
			continue
		}
		local, err := s.layoutFile(ctx, tok, ls, file)
		if err != nil {
			return mcp.NewToolResultErrorf("commands[%d]: %v", i, err), nil
		}
		params["path"] = local
	}
	res, err := s.engine(ctx, tok, ls, "engine_run", map[string]any{"commands": commands})
	s.auditWrite(ctx, audit.ActionPluginCall, ls.project, "layout_run", int64(len(commands)))
	return s.layoutAnswer(ls, res, err)
}

// layoutFile copies an attachment into the session once, and gives the path
// of the copy.
func (s *Server) layoutFile(ctx context.Context, tok *auth.Token, ls *layoutSession, p string) (string, error) {
	clean := s.layoutAttachment(tok, p)
	if clean == "" {
		return "", fmt.Errorf("path %q: no attachment this token may read", p)
	}
	if local, ok := ls.files[clean]; ok {
		return local, nil
	}
	data, err := s.vault.ReadAttachment(clean, attach.MaxBytes())
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(clean))
	res, err := s.enginePut(ctx, tok, ls, "file", hex.EncodeToString(sum[:8])+strings.ToLower(path.Ext(clean)), data)
	if err != nil {
		return "", err
	}
	if res.IsError {
		return "", errors.New(engineText(res))
	}
	var out struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(engineText(res)), &out); err != nil || out.Path == "" {
		return "", fmt.Errorf("the engine gave no path for %s", clean)
	}
	ls.files[clean] = out.Path
	return out.Path, nil
}

func (s *Server) handleLayoutRender(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, ls, errRes := s.layoutSessionOf(ctx, req)
	if errRes != nil {
		return errRes, nil
	}
	defer ls.mu.Unlock()
	args := map[string]any{"page": req.GetInt("page", 0)}
	if v := req.GetFloat("scale", 0); v > 0 {
		args["scale"] = v
	}
	res, err := s.engine(ctx, tok, ls, "engine_render", args)
	return s.layoutAnswer(ls, res, err)
}

func (s *Server) handleLayoutCommands(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, ls, errRes := s.layoutSessionOf(ctx, req)
	if errRes != nil {
		return errRes, nil
	}
	defer ls.mu.Unlock()
	res, err := s.engine(ctx, tok, ls, "engine_commands", map[string]any{"filter": req.GetString("filter", ""), "enabledOnly": req.GetBool("enabled_only", false)})
	return s.layoutAnswer(ls, res, err)
}

func (s *Server) handleLayoutSave(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	reader, ls, errRes := s.layoutSessionOf(ctx, req)
	if errRes != nil {
		return errRes, nil
	}
	defer ls.mu.Unlock()
	if ls.version != "" {
		return mcp.NewToolResultError("this session reads a version of the design, which does not change: a save goes to the note as it is, from a session without version"), nil
	}
	tok, errRes := s.authorizeWrite(ctx, ls.note)
	if errRes != nil {
		return errRes, nil
	}
	// The design's files are attachments of the project, as an upload is.
	if _, errRes := s.authorizeWrite(ctx, attach.RelPath(ls.project, "layout.json")); errRes != nil {
		return errRes, nil
	}
	if res := s.checkWriteLimits(ctx, reader, 0); res != nil {
		return res, nil
	}
	raw, errRes, err := s.engineSaved(ctx, tok, ls)
	if errRes != nil || err != nil {
		return s.layoutAnswer(ls, errRes, err)
	}
	taken, problem := layout.Take(raw, s.layoutLimits, attach.Check)
	if problem != nil {
		return mcp.NewToolResultError(problem.Msg), nil
	}
	refund, refusal := s.reserveUpload(tok, int64(len(raw)))
	if refusal != nil {
		return mcp.NewToolResultError(refusal.Error()), nil
	}
	var saved *layout.Saved
	if _, werr := s.writeNote(ctx, tok, noteWrite{rel: ls.note, mode: writeReplace,
		content: func(existing *vault.Note) ([]byte, error) {
			if current := layout.ETag(layout.DesignFile(existing.Content)); current != ls.etag {
				return nil, &writeError{kind: writePrecondition, status: http.StatusPreconditionFailed,
					msg: fmt.Sprintf("the design was saved since it was opened (it was %s, it is %s): layout_open opens that version, this session's work stays unsaved", ls.etag, current)}
			}
			next, out, problem := taken.Store(ls.note, existing.Content, func(data []byte, name string) (string, error) {
				return s.layoutStore(ctx, ls.project, data, name)
			})
			if problem != nil {
				return nil, contentError("%s", problem.Msg)
			}
			saved = out
			return next, nil
		}}); werr != nil {
		refund()
		return werr.result(), nil
	}
	ls.etag = saved.ETag
	return mcp.NewToolResultJSON(saved)
}

// engineSaved is the session's document as a package. A result is the
// engine's refusal.
func (s *Server) engineSaved(ctx context.Context, tok *auth.Token, ls *layoutSession) ([]byte, *mcp.CallToolResult, error) {
	res, err := s.engine(ctx, tok, ls, "engine_save", nil)
	if err != nil || res.IsError {
		return nil, res, err
	}
	var size struct {
		Bytes int64 `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(engineText(res)), &size); err != nil {
		return nil, nil, fmt.Errorf("the engine's answer: %w", err)
	}
	if size.Bytes > s.layoutLimits.MaxBytes {
		return nil, mcp.NewToolResultErrorf("the design is %d bytes, more than %d ([web] layout_max_bytes)", size.Bytes, s.layoutLimits.MaxBytes), nil
	}
	raw := make([]byte, 0, size.Bytes)
	for {
		res, err := s.engine(ctx, tok, ls, "engine_get", map[string]any{"offset": len(raw), "length": engineChunk})
		if err != nil || res.IsError {
			return nil, res, err
		}
		var part struct {
			Data string `json:"data"`
			EOF  bool   `json:"eof"`
		}
		if err := json.Unmarshal([]byte(engineText(res)), &part); err != nil {
			return nil, nil, fmt.Errorf("the engine's answer: %w", err)
		}
		b, err := base64.StdEncoding.DecodeString(part.Data)
		if err != nil {
			return nil, nil, fmt.Errorf("the engine's answer: %w", err)
		}
		raw = append(raw, b...)
		if part.EOF {
			return raw, nil, nil
		}
		if int64(len(raw)) > s.layoutLimits.MaxBytes {
			return nil, mcp.NewToolResultErrorf("the design is more than %d bytes ([web] layout_max_bytes)", s.layoutLimits.MaxBytes), nil
		}
	}
}

// layoutStore stores a file of a design as an attachment of the project.
func (s *Server) layoutStore(ctx context.Context, project string, data []byte, name string) (string, error) {
	held := attach.Hold(s.vault, s.vault)
	defer held.Release()
	r, err := attach.Store(held, data, name, project)
	if err != nil {
		return "", err
	}
	s.auditWrite(ctx, audit.ActionUploadAttachment, r.Path, "", int64(len(data)))
	return r.Path, nil
}

func (s *Server) handleLayoutClose(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, ls, errRes := s.layoutSessionNamed(ctx, req)
	if errRes != nil {
		return errRes, nil
	}
	defer ls.mu.Unlock()
	res, err := s.engine(ctx, tok, ls, "engine_close", nil)
	s.layoutForget(ls)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if res.IsError {
		return res, nil
	}
	return mcp.NewToolResultJSON(map[string]any{"closed": ls.id})
}
