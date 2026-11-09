package mcp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/gitsync"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/mark3labs/mcp-go/mcp"
)

// The versions of a note (IMP-201): its history, the versions that have a
// name, and the one readers must see. The note as it is stays the draft,
// where every write goes, and memory_get reads it, says that a valid version
// differs, and reads that one on request. A single tool, not to weigh on the
// schema of every session. A note can also choose the version who calls over
// MCP reads (IMP-228): then the tools that read it give that one, and say so.

// versionsLimit is how many commits memory_versions lists by default.
const versionsLimit = 20

// versionDraft asks for the note as it is, against what it chooses.
const versionDraft = "draft"

// SetGit wires the git sync, for the versions of a note; nil without one.
func (s *Server) SetGit(g *gitsync.Sync) { s.git = g }

// registerVersionsTool adds memory_versions (IMP-201).
func (s *Server) registerVersionsTool() {
	s.impl.AddTool(mcp.NewTool("memory_versions",
		mcp.WithDescription("The versions of a note: its history and the versions that have a name. A note's valid version, when it has one, is the version readers must see, while the note as it is stays the draft: every write goes to the draft, and memory_get reads it unless you pass version:\"valid\". A note may also choose what MCP callers read: then the read tools give that version and say so, and version:\"draft\" reads the draft. action list (the default) gives the tags, both choices and the last commits. The project's admin alone, with a token of their account or for the whole vault: action tag names a version (label such as V3; sha a commit of the list, or none for the note as it is), action valid makes a tag the valid version, action clear removes it, action mcp sets what MCP callers read (tag: a tag, \"valid\" to follow the valid version, \"draft\" for the note as it is). A tag never moves. The keys valid_version and mcp_version change only this way: a write leaves them as they were."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path of the note.")),
		mcp.WithString("action", mcp.Description("list (default), tag, valid, clear or mcp.")),
		mcp.WithString("label", mcp.Description("tag: what people call the version, a word such as V3.")),
		mcp.WithString("sha", mcp.Description("tag: the version, a commit of the note's history; none for the note as it is.")),
		mcp.WithString("message", mcp.Description("tag: what the tag says.")),
		mcp.WithString("tag", mcp.Description("valid, mcp: a tag of the note, whole or by its label.")),
		mcp.WithString("if_match", mcp.Description("valid, clear, mcp: the etag of a previous memory_get.")),
		mcp.WithNumber("limit", mcp.Description("list: how many commits, 20 by default.")),
	), s.handleVersions)
}

// noteValid says a note's valid version. Same says the note is that version
// now; Missing that the tag gives no text you may read.
type noteValid struct {
	Tag     string `json:"tag"`
	Label   string `json:"label"`
	SHA     string `json:"sha,omitempty"`
	Date    string `json:"date,omitempty"`
	Same    bool   `json:"same"`
	Missing bool   `json:"missing,omitempty"`
}

// canRead says whether tok reads the note at rel.
func (s *Server) canRead(tok *auth.Token, rel string) bool {
	return tok.AllowsPath(rel) && !s.pathInHiddenProject(rel)
}

// validOf reads the valid version content names: nil for a note without
// one. Who reads the note reads it, wherever it sat: who chose it could read
// it (chooseValid), and a project renamed since is out of everyone's sight.
func (s *Server) validOf(rel string, content []byte) *noteValid {
	v := s.git.Valid(rel, content, versionMaxBytes)
	if v == nil {
		return nil
	}
	out := &noteValid{Tag: v.Tag, Label: v.Label, Missing: v.Missing}
	if !v.Missing {
		out.SHA, out.Date, out.Same = v.SHA, v.Date.UTC().Format(time.RFC3339), v.Same
	}
	return out
}

// versionMaxBytes is the largest version a tool reads, as the REST API.
const versionMaxBytes = 1 << 20

// validHint says what a read of a note with a valid version got, or "" when
// nothing needs saying: the draft, or a valid version that cannot be read.
func validHint(v *noteValid, read string) string {
	switch {
	case v == nil || read != "" || v.Same:
		return ""
	case v.Missing:
		return "the valid version " + v.Label + " cannot be read: this is the note as it is"
	}
	return "this is the draft: readers must see the valid version " + v.Label + ", which differs. Pass version:\"valid\" to read that"
}

// chosenHint says what a read of a version the note gave got, or "" for
// none.
func chosenHint(label string) string {
	if label == "" {
		return ""
	}
	return "this is " + label + ", the version this note gives MCP callers: it has a newer draft, where writes go, which memory_get reads with version:\"draft\""
}

// withChosen adds to the answer of a read which version the note gave.
func withChosen(out map[string]any, label string) map[string]any {
	if label != "" {
		out["version"] = label
		hint, _ := out["hint"].(string)
		out["hint"] = joinHints(hint, chosenHint(label))
	}
	return out
}

// chosenForMCP reads the version note gives who calls over MCP (IMP-228): nil
// for none, for one that cannot be read, and for one that is the note as it
// is, whose etag then still writes.
func (s *Server) chosenForMCP(note *vault.Note) *gitsync.Valid {
	chosen := s.git.ForMCP(note.Path, note.Content, versionMaxBytes)
	if chosen == nil || chosen.Missing || chosen.Same {
		return nil
	}
	return chosen
}

// noteVersion reads the version want names, "valid" or a tag of the note's
// history: nil for a valid version that cannot be read, or none, as valid
// says, and for versionDraft. With no want it is what the note gives who
// calls over MCP, named by the string: nil for nothing.
func (s *Server) noteVersion(tok *auth.Token, note *vault.Note, valid *noteValid, want string) (*gitsync.Version, string, *mcp.CallToolResult) {
	switch want {
	case versionDraft:
		return nil, "", nil
	case "":
		if chosen := s.chosenForMCP(note); chosen != nil {
			return &chosen.Version, chosen.Label, nil
		}
		return nil, "", nil
	}
	if s.git == nil {
		return nil, "", mcp.NewToolResultError("version: git sync is off")
	}
	if want == "valid" {
		if valid == nil || valid.Missing {
			return nil, "", nil
		}
		chosen := s.git.Valid(note.Path, note.Content, versionMaxBytes)
		if chosen == nil || chosen.Missing {
			return nil, "", nil
		}
		return &chosen.Version, "", nil
	}
	v, err := s.git.TagVersion(note.Path, want, versionMaxBytes)
	// A tag by name is a part of the history, with its rule (validOf).
	if err != nil || v.Content == nil || !s.canRead(tok, v.Path) {
		return nil, "", mcp.NewToolResultErrorf("version %q: not a version of %q you can read: memory_versions lists them", want, note.Path)
	}
	return &v, "", nil
}

// noteAtVersion gives note as the version want names, or as it is, and what
// noteVersion says of it. The etag that comes with a version is not the
// note's, so that no write takes what was read here over the draft.
func (s *Server) noteAtVersion(tok *auth.Token, note *vault.Note, valid *noteValid, want string) (*vault.Note, string, *mcp.CallToolResult) {
	v, chosen, errRes := s.noteVersion(tok, note, valid, want)
	if errRes != nil || v == nil {
		return note, "", errRes
	}
	at := *note
	at.Content, at.Size, at.ModTime = v.Content, v.Size, v.Date
	return &at, chosen, nil
}

// forMCP gives note as who calls over MCP reads it, and the version the note
// gave, "" for none.
func (s *Server) forMCP(note *vault.Note) (*vault.Note, string) {
	at, chosen, _ := s.noteAtVersion(nil, note, nil, "")
	return at, chosen
}

// adminsProject says whether tok may do what is the project admin's: the
// account behind it decides, and a token of no account must be for the
// whole vault.
func (s *Server) adminsProject(tok *auth.Token, project string) bool {
	if tok.OwnerUserID != "" && s.principalResolver != nil && s.projects != nil {
		p, ok := s.principalResolver(tok.OwnerUserID)
		return ok && p.CanAdminProject(project, s.projects.AccessConfig())
	}
	return tok.IsAdmin()
}

func (s *Server) handleVersions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	reader, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	rel, err := s.vault.Rel(s.notePathArg(reader, raw))
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err), nil
	}
	if !s.canRead(reader, rel) {
		return mcp.NewToolResultErrorf("note %q not found", rel), nil
	}
	if s.git == nil {
		return mcp.NewToolResultError("the versions of a note need git sync, and it is off"), nil
	}
	note, err := s.vault.Load(rel)
	if err != nil {
		return readNoteError(rel, err), nil
	}
	action := req.GetString("action", "list")
	switch action {
	case "list":
		return s.listVersions(reader, note, req.GetInt("limit", versionsLimit))
	case "tag", "valid", "clear", "mcp":
	default:
		return mcp.NewToolResultErrorf("action %q: list, tag, valid, clear or mcp", action), nil
	}
	tok, errRes := s.authorizeWrite(ctx, rel)
	if errRes != nil {
		return errRes, nil
	}
	project, _, _ := strings.Cut(rel, "/")
	if !s.adminsProject(tok, project) {
		return mcp.NewToolResultErrorf("the versions of %q are for the admin of project %q: this token does not act for one", rel, project), nil
	}
	switch action {
	case "tag":
		return s.tagVersion(ctx, tok, rel, req)
	case "mcp":
		return s.chooseForMCP(ctx, tok, note, req)
	}
	return s.chooseValid(ctx, tok, note, action, req)
}

// listVersions answers action list.
func (s *Server) listVersions(tok *auth.Token, note *vault.Note, limit int) (*mcp.CallToolResult, error) {
	tags, err := s.git.NoteTags(note.Path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("tags", err), nil
	}
	commits, err := s.git.History(note.Path, limit)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("history", err), nil
	}
	type tagInfo struct {
		Tag     string `json:"tag"`
		Label   string `json:"label"`
		SHA     string `json:"sha"`
		By      string `json:"by,omitempty"`
		Date    string `json:"date"`
		Message string `json:"message,omitempty"`
		Path    string `json:"path"`
	}
	type commitInfo struct {
		SHA     string `json:"sha"`
		Author  string `json:"author"`
		Date    string `json:"date"`
		Subject string `json:"subject"`
	}
	outTags := make([]tagInfo, 0, len(tags))
	for _, t := range tags {
		if s.canRead(tok, t.Path) {
			outTags = append(outTags, tagInfo{t.Name, t.Label, t.SHA, t.By, t.Date.UTC().Format(time.RFC3339), t.Message, t.Path})
		}
	}
	outCommits := make([]commitInfo, 0, len(commits))
	for _, c := range commits {
		if !s.canRead(tok, c.Path) {
			continue
		}
		subject := c.Subject
		if c.From != "" && !s.canRead(tok, c.From) {
			subject = "rename (from another project) -> " + c.Path
		}
		outCommits = append(outCommits, commitInfo{c.SHA, c.Author, c.Date.UTC().Format(time.RFC3339), subject})
	}
	project, _, _ := strings.Cut(note.Path, "/")
	return mcp.NewToolResultJSON(map[string]any{
		"path":      note.Path,
		"tags":      outTags,
		"valid":     s.validOf(note.Path, note.Content),
		"mcp":       vault.MCPVersion(note.Path, note.Content),
		"commits":   outCommits,
		"can_admin": tok.HasScope(auth.ScopeWrite) && s.adminsProject(tok, project),
	})
}

// tagVersion answers action tag.
func (s *Server) tagVersion(ctx context.Context, tok *auth.Token, rel string, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if errRes := s.checkWriteLimits(ctx, tok, 0); errRes != nil {
		return errRes, nil
	}
	label := strings.TrimSpace(req.GetString("label", ""))
	done, err := s.git.TagNote(ctx, rel, strings.TrimSpace(req.GetString("sha", "")), label, req.GetString("message", ""), tok.Name)
	if errors.Is(err, gitsync.ErrNoVersion) {
		return mcp.NewToolResultErrorf("no such version of %q: memory_versions lists the commits", rel), nil
	}
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	s.auditWrite(ctx, audit.ActionNoteTag, rel, done.Label, 0)
	return mcp.NewToolResultJSON(map[string]any{
		"path": rel, "tag": done.Name, "label": done.Label, "sha": done.SHA,
		"pushed": done.Pushed, "push_error": done.PushError,
	})
}

// chooseValid answers actions valid and clear: the write of the note, which
// the git sync takes at once, so the history says who chose what.
func (s *Server) chooseValid(ctx context.Context, tok *auth.Token, note *vault.Note, action string, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rel := note.Path
	var tag gitsync.NoteTag
	if action == "valid" {
		found, errRes := s.versionTag(tok, rel, req)
		if errRes != nil {
			return errRes, nil
		}
		tag = found
	}
	done, werr := s.writeNote(ctx, tok, noteWrite{
		rel: rel, mode: writeReplace, ifMatch: req.GetString("if_match", ""), action: audit.ActionNoteValid, to: tag.Label,
		validVersion: &tag.Name, content: func(n *vault.Note) ([]byte, error) { return n.Content, nil },
	})
	if werr != nil {
		return werr.result(), nil
	}
	out := map[string]any{"path": rel, "etag": done.ETag, "valid": nil}
	if fresh, err := s.vault.Load(rel); err == nil {
		out["valid"] = s.validOf(rel, fresh.Content)
	}
	return mcp.NewToolResultJSON(s.afterChoice(ctx, rel, tag.Name, out))
}

// chooseForMCP answers action mcp: what the note gives who calls over MCP.
func (s *Server) chooseForMCP(ctx context.Context, tok *auth.Token, note *vault.Note, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rel := note.Path
	var tag gitsync.NoteTag
	switch strings.TrimSpace(req.GetString("tag", "")) {
	case versionDraft:
	case vault.FollowValid:
		tag = gitsync.NoteTag{Name: vault.FollowValid, Label: vault.FollowValid}
	default:
		found, errRes := s.versionTag(tok, rel, req)
		if errRes != nil {
			return errRes, nil
		}
		tag = found
	}
	done, werr := s.writeNote(ctx, tok, noteWrite{
		rel: rel, mode: writeReplace, ifMatch: req.GetString("if_match", ""), action: audit.ActionNoteMCPVersion, to: tag.Label,
		mcpVersion: &tag.Name, content: func(n *vault.Note) ([]byte, error) { return n.Content, nil },
	})
	if werr != nil {
		return werr.result(), nil
	}
	return mcp.NewToolResultJSON(s.afterChoice(ctx, rel, tag.Name, map[string]any{"path": rel, "etag": done.ETag, "mcp": tag.Name}))
}

// versionTag finds the tag the request names among the note's.
func (s *Server) versionTag(tok *auth.Token, rel string, req mcp.CallToolRequest) (gitsync.NoteTag, *mcp.CallToolResult) {
	tags, err := s.git.NoteTags(rel)
	if err != nil {
		return gitsync.NoteTag{}, mcp.NewToolResultErrorFromErr("tags", err)
	}
	found, ok := gitsync.FindTag(tags, strings.TrimSpace(req.GetString("tag", "")))
	if !ok || !s.canRead(tok, found.Path) {
		return gitsync.NoteTag{}, mcp.NewToolResultErrorf("no such tag on the versions of %q: memory_versions lists them", rel)
	}
	return found, nil
}

// afterChoice brings back what the version tag names and the vault has lost,
// and commits the choice at once: the history says who chose what.
func (s *Server) afterChoice(ctx context.Context, rel, tag string, out map[string]any) map[string]any {
	if tag != "" && tag != vault.FollowValid {
		v, err := s.git.TagVersion(rel, tag, versionMaxBytes)
		var restored []string
		if err == nil {
			restored, err = s.git.RestoreFiles(v, s.vault, allowedExtSet())
		}
		if len(restored) > 0 {
			out["restored"] = restored
		}
		for _, file := range restored {
			s.auditWrite(ctx, audit.ActionUploadAttachment, file, "", 0)
		}
		if err != nil {
			out["restore_error"] = err.Error()
		}
	}
	if err := s.git.CommitWrites(); err != nil {
		out["commit_error"] = err.Error()
	}
	return out
}
