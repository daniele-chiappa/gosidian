package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/plugins"
)

// The plugins' tools (IMP-182, ADR-038): the tools an accepted manifest
// lists are tools of this server too, as <plugin>_<tool>. A token sees one
// only when the owner exposed the plugin in a project the token reaches
// (gosidian plugin expose), a write tool only when it may write there; a
// call goes to the plugin with the project in its _meta, and the result
// comes back as the plugin gave it.

const (
	// defaultPluginToolTimeout caps a call of a plugin's tool, within the
	// plugin's own timeout when it has a shorter one, unless [mcp]
	// plugin_tool_timeout says otherwise: an agent waits for it.
	defaultPluginToolTimeout = 2 * time.Minute
	// defaultMaxPluginResult caps the text and the structured content of
	// a plugin tool's result, unless [mcp] max_plugin_result says more.
	defaultMaxPluginResult = 1 << 20
)

// pluginTool is a plugin's tool registered on this server.
type pluginTool struct {
	plugin string
	tool   string
	access string
	// files are the arguments that name files of the project, handed to
	// the plugin by reference (IMP-184).
	files []string
}

// syncPluginTools registers the tools of the accepted manifests, and drops
// those no accepted manifest lists any more. The set rarely changes and the
// registry reads plugins.json again only when the file changed, so most
// calls cost a stat and a comparison.
func (s *Server) syncPluginTools() {
	if s.plugins == nil {
		return
	}
	tools := s.plugins.Tools()
	keys := make([]string, 0, len(tools))
	for _, t := range tools {
		keys = append(keys, t.Name+"@"+t.SHA)
	}
	key := strings.Join(keys, ",")
	s.pluginToolsMu.Lock()
	defer s.pluginToolsMu.Unlock()
	if key == s.pluginToolsKey && s.pluginTools != nil {
		return
	}
	want := make(map[string]pluginTool, len(tools))
	var add []server.ServerTool
	for _, t := range tools {
		if _, mine := s.pluginTools[t.Name]; !mine && s.impl.GetTool(t.Name) != nil {
			// The reserved plugin names keep this from happening.
			slog.Warn("plugins: a tool of the plugin has the name of one of the server's", "plugin", t.Plugin, "tool", t.Name)
			continue
		}
		want[t.Name] = pluginTool{plugin: t.Plugin, tool: t.Spec.Name, access: t.Spec.Access, files: t.Spec.Files}
		add = append(add, server.ServerTool{Tool: pluginToolDef(t), Handler: s.handlePluginTool(t.Name)})
	}
	var gone []string
	for name := range s.pluginTools {
		if _, ok := want[name]; !ok {
			gone = append(gone, name)
		}
	}
	if len(gone) > 0 {
		s.impl.DeleteTools(gone...)
	}
	if len(add) > 0 {
		s.impl.AddTools(add...)
	}
	s.pluginTools, s.pluginToolsKey = want, key
}

// pluginToolOf is the plugin's tool name names, if it is one.
func (s *Server) pluginToolOf(name string) (pluginTool, bool) {
	s.pluginToolsMu.Lock()
	defer s.pluginToolsMu.Unlock()
	t, ok := s.pluginTools[name]
	return t, ok
}

// pluginToolVisible says whether tok may call t: the plugin is exposed in a
// project the token reaches and MCP does not hide, and for a write tool
// the token may write there. Without a token (open mode) an exposure is
// enough.
func (s *Server) pluginToolVisible(tok *auth.Token, t pluginTool) bool {
	if s.plugins == nil {
		return false
	}
	for _, p := range s.plugins.ExposedIn(t.plugin) {
		if s.projectHidden(p) {
			continue
		}
		if tok == nil || s.pluginToolAllowed(tok, t, p) {
			return true
		}
	}
	return false
}

// pluginToolAllowed says whether tok may call t for project.
func (s *Server) pluginToolAllowed(tok *auth.Token, t pluginTool, project string) bool {
	if t.access == plugins.AccessWrite {
		return tok.AllowsWrite(project + "/")
	}
	return tok.HasScope(auth.ScopeRead) && tok.AllowsProject(project)
}

// pluginToolDef is the tool an agent sees: the plugin's description and
// schema, with project added.
func pluginToolDef(t plugins.PluginTool) mcp.Tool {
	desc := strings.TrimSpace(t.Spec.Description)
	if desc != "" && !strings.HasSuffix(desc, ".") {
		desc += "."
	}
	needs := "read"
	if t.Spec.Access == plugins.AccessWrite {
		needs = "write"
	}
	desc = fmt.Sprintf("[plugin %s] %s Runs in the plugin %s, offered in the projects its owner exposed it in; needs %s access to the project.", t.Plugin, desc, t.Plugin, needs)
	tool := mcp.NewToolWithRawSchema(t.Name, strings.Join(strings.Fields(desc), " "), withProjectArg(t.Spec.Input))
	tool.Annotations.ReadOnlyHint = mcp.ToBoolPtr(t.Spec.Access == plugins.AccessRead)
	tool.Annotations.OpenWorldHint = mcp.ToBoolPtr(true)
	return tool
}

// withProjectArg adds project to a tool's input schema; the manifest's
// check refused a schema that declares it, or one of another type.
func withProjectArg(raw json.RawMessage) json.RawMessage {
	schema := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &schema)
	}
	schema["type"] = "object"
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}
	props["project"] = map[string]any{"type": "string",
		"description": "The project the call acts for, one the plugin is exposed in. " + scopedProjectNote}
	schema["properties"] = props
	b, err := json.Marshal(schema)
	if err != nil {
		return json.RawMessage(`{"type":"object","properties":{"project":{"type":"string"}}}`)
	}
	return b
}

// handlePluginTool forwards a call of the plugin's tool name.
func (s *Server) handlePluginTool(name string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, ok := s.pluginToolOf(name)
		if !ok || s.plugins == nil {
			return mcp.NewToolResultErrorf("the plugin tool %s is gone: its manifest is no longer accepted", name), nil
		}
		tok, errRes := s.authorizeRead(ctx)
		if errRes != nil {
			return errRes, nil
		}
		project, err := s.resolveProject(tok, req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if !slices.Contains(s.plugins.ExposedIn(t.plugin), project) {
			return mcp.NewToolResultErrorf("plugin %s is not exposed in project %q (its owner exposes it with gosidian plugin expose %s %s)", t.plugin, project, t.plugin, project), nil
		}
		if !s.pluginToolAllowed(tok, t, project) {
			return mcp.NewToolResultErrorf("%s needs %s access to project %q, which this token lacks", name, t.access, project), nil
		}
		args := make(map[string]any, len(req.GetArguments()))
		for k, v := range req.GetArguments() {
			if k != "project" {
				args[k] = v
			}
		}
		raw, _ := json.Marshal(args)
		// A write counts against the token's rate of writes; the size cap
		// is the notes', not the plugin's data.
		if t.access == plugins.AccessWrite {
			if res := s.checkWriteLimits(ctx, tok, 0); res != nil {
				return res, nil
			}
		}
		if len(t.files) > 0 {
			call, err := s.handFiles(t, project, args)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer s.pipelines.DropCall(call)
		}
		meta := map[string]any{plugins.MetaProject: project, plugins.MetaCaller: tok.ID, plugins.MetaAccess: t.access}
		cctx, cancel := context.WithTimeout(ctx, s.pluginToolTimeout)
		defer cancel()
		res, err := s.plugins.CallTool(cctx, t.plugin, t.tool, args, meta)
		s.auditWrite(ctx, audit.ActionPluginCall, project, name, int64(len(raw)))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(t.files) > 0 && t.access == plugins.AccessWrite && !res.IsError {
			res = s.storeToolFiles(ctx, project, res)
		}
		if n := resultSize(res); int64(n) > s.maxPluginResult {
			return mcp.NewToolResultErrorf("plugin %s gave %s a result of %d bytes, more than %d ([mcp] max_plugin_result): ask for less", t.plugin, name, n, s.maxPluginResult), nil
		}
		return res, nil
	}
}

// handFiles puts, in place of each argument of t that names files of the
// project (a vault path, a list of them, or entries with a path), where the
// plugin fetches each one: tickets bound to the call, which the caller
// ends with DropCall. The files are read with the access the call was
// checked for.
func (s *Server) handFiles(t pluginTool, project string, args map[string]any) (string, error) {
	if s.pipelines == nil {
		return "", fmt.Errorf("%s takes files, which gosidian hands a plugin by reference: the pipelines are off on this server", plugins.ToolName(t.plugin, t.tool))
	}
	var b [12]byte
	_, _ = rand.Read(b[:])
	call := "call:" + hex.EncodeToString(b[:])
	base := s.plugins.FilesURL(t.plugin)
	if base == "" {
		base = s.pipelines.Config().PublicURL
	}
	ref := func(v any) (map[string]any, error) {
		p, _ := v.(string)
		if m, ok := v.(map[string]any); ok {
			p, _ = m["path"].(string)
		}
		if strings.TrimSpace(p) == "" {
			return nil, fmt.Errorf("a file is named by its vault path")
		}
		r, err := s.pipelines.CallFileRef(project, call, p, base)
		if errors.Is(err, fs.ErrNotExist) {
			// The agent learns the file is not there, not where the
			// server keeps its vault.
			return nil, fmt.Errorf("no file %q in project %q (an attachment or a file of its IN folder)", p, project)
		}
		return r, err
	}
	for _, f := range t.files {
		v, ok := args[f]
		if !ok || v == nil {
			continue // the plugin's schema says whether it is required
		}
		list, isList := v.([]any)
		if !isList {
			r, err := ref(v)
			if err != nil {
				s.pipelines.DropCall(call)
				return "", fmt.Errorf("%s: %w", f, err)
			}
			args[f] = r
			continue
		}
		if max := s.plugins.Limits().Files; len(list) == 0 || len(list) > max {
			s.pipelines.DropCall(call)
			return "", fmt.Errorf("%s names %d files: from 1 to %d ([pipelines] max_plugin_files)", f, len(list), max)
		}
		refs := make([]any, len(list))
		for i, item := range list {
			r, err := ref(item)
			if err != nil {
				s.pipelines.DropCall(call)
				return "", fmt.Errorf("%s[%d]: %w", f, i, err)
			}
			refs[i] = r
		}
		args[f] = refs
	}
	return call, nil
}

// storeToolFiles stores the files a write tool gave back as attachments of
// the project, written as the caller, and gives the agent its result with
// them as files ({name, path, mime, markdown}) in place of their bytes.
func (s *Server) storeToolFiles(ctx context.Context, project string, res *mcp.CallToolResult) *mcp.CallToolResult {
	files, skipped := s.plugins.StoreFiles(res.Content, func(name string, data []byte) (string, error) {
		r, err := attach.Store(s.vault, data, name, project)
		if err != nil {
			return "", err
		}
		s.auditWrite(ctx, audit.ActionUploadAttachment, r.Path, "", int64(len(data)))
		return r.Path, nil
	})
	if len(files) == 0 && len(skipped) == 0 {
		return res
	}
	var out any
	if raw := plugins.ResultJSON(res); len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			out = map[string]any{"text": string(raw)}
		}
	}
	out = plugins.LinkFiles(out, files)
	m, isMap := out.(map[string]any)
	if !isMap {
		m = map[string]any{}
		if out != nil {
			m["result"] = out
		}
	}
	m["files"] = files
	if len(skipped) > 0 {
		m["skipped"] = skipped
	}
	b, err := json.Marshal(m)
	if err != nil {
		return mcp.NewToolResultErrorf("the result of the plugin: %v", err)
	}
	return mcp.NewToolResultStructured(json.RawMessage(b), string(b))
}

// resultSize is the bytes of a result's content and structured content.
func resultSize(res *mcp.CallToolResult) int {
	n := len(res.RawStructuredContent)
	if n == 0 && res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			n = len(b)
		}
	}
	if b, err := json.Marshal(res.Content); err == nil {
		n += len(b)
	}
	return n
}

// pluginToolNames lists the names of a plugin's tools, sorted.
func pluginToolNames(m plugins.Manifest) []string {
	out := make([]string, 0, len(m.Tools))
	for _, t := range m.Tools {
		out = append(out, plugins.ToolName(m.Name, t.Name))
	}
	sort.Strings(out)
	return out
}
