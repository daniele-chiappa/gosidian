package plugins

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/pipeline"
)

const (
	// MetaLLMTokensLeft is the _meta field of a node's call with the LLM
	// tokens the run may still use, when [pipelines] max_llm_tokens caps
	// them.
	MetaLLMTokensLeft = "gosidian/llm_tokens_left"
	// MetaProject is the _meta field with the project a call acts for: a
	// node's pipeline's, or the one an agent named calling a tool. Only
	// gosidian holds the plugin's token, so a plugin may trust it to keep
	// the data of its projects apart (IMP-182).
	MetaProject = "gosidian/project"
	// MetaRun is the run a node's call belongs to.
	MetaRun = "gosidian/run"
	// MetaCaller is the id of the token that called a tool, MetaAccess the
	// tool's access (read or write).
	MetaCaller = "gosidian/caller"
	MetaAccess = "gosidian/access"
	// MetaMCPURL and MetaMCPToken are gosidian's MCP endpoint and a token
	// for it, read or write as the node's setting mcp asks, for the run's
	// project and the call alone (IMP-193): the plugin hands them to its
	// agent. The token is revoked when the call returns.
	MetaMCPURL   = "gosidian/mcp_url"
	MetaMCPToken = "gosidian/mcp_token"
)

// runTokenSlack keeps a call's MCP token valid a little past the call's
// deadline, for the agent's last answer on its way back.
const runTokenSlack = 30 * time.Second

// node runs a plugin's node: the settings that name files carry their
// content, the tool's result is the output, and the files it gives back
// (embedded resources, images) become attachments of the project, listed
// in the output's files. A markdown link of the output to one of those
// files by its name (![](fig-1.png)) is written as gosidian links an
// attachment (![](/vault-files/<path>)), so a note made of the output
// shows them (IMP-175).
type node struct {
	r   *Registry
	use string
}

func (n node) Run(ctx context.Context, env *pipeline.Env, with map[string]any) (any, error) {
	c, acc, spec, ok := n.r.accepted(n.use)
	if !ok {
		return nil, fmt.Errorf("node type %s: %s", n.use, n.r.Why(n.use))
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	args := make(map[string]any, len(with))
	for k, v := range with {
		args[k] = v
	}
	// The role of an agent (IMP-195): the body of an agent note, its system
	// prompt, before the node's own system; gosidian reads it, the plugin
	// never sees the setting.
	if spec.Contract == ContractAgentRun {
		if ro, _ := args["role"].(string); strings.TrimSpace(ro) != "" {
			prompt, err := env.AgentPrompt(ro)
			if err != nil {
				return nil, fmt.Errorf("with.role: %w", err)
			}
			if sys, _ := args["system"].(string); strings.TrimSpace(sys) != "" {
				prompt += "\n\n" + sys
			}
			args["system"] = prompt
		}
		delete(args, "role")
	}
	base := c.FilesURL
	if base == "" && spec.FileRefs {
		base = env.Engine.Config().PublicURL
	}
	optional := optionalFiles(spec)
	for _, f := range spec.Files {
		// A file field the node's schema does not require may be left out,
		// or be empty: the plugin then gets none.
		if optional[f] {
			if v, ok := args[f]; !ok || v == nil || v == "" {
				delete(args, f)
				continue
			} else if list, ok := v.([]any); ok && len(list) == 0 {
				delete(args, f)
				continue
			}
		}
		if list, ok := args[f].([]any); ok {
			refs, err := n.fileList(env, spec, f, list, base)
			if err != nil {
				return nil, err
			}
			args[f] = refs
			continue
		}
		p, _ := args[f].(string)
		if strings.TrimSpace(p) == "" {
			return nil, fmt.Errorf("with.%s names the file for %s", f, n.use)
		}
		if spec.FileRefs {
			ref, err := env.FileRef(p, base)
			if err != nil {
				return nil, err
			}
			args[f] = ref
			continue
		}
		rel, data, err := env.ReadAttachment(p)
		if err != nil {
			return nil, err
		}
		args[f] = map[string]any{"name": path.Base(rel), "mime": mimeOf(rel), "data": base64.StdEncoding.EncodeToString(data)}
	}
	// What the run may still spend on language models, for a plugin that
	// calls them (an agent): the plugin decides, gosidian counts the usage
	// its output reports.
	meta := map[string]any{MetaProject: env.Project, MetaRun: env.Run.ID}
	if left, capped := env.LLMTokensLeft(); capped {
		meta[MetaLLMTokensLeft] = left
	}
	if spec.MCP {
		token, revoke, err := n.runToken(ctx, env, c, acc, with)
		if err != nil {
			return nil, err
		}
		if token != "" {
			defer revoke()
			meta[MetaMCPURL], meta[MetaMCPToken] = c.MCPURL, token
		}
	}
	sess, err := n.r.session(ctx, c, acc)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	res, err := callToolMeta(ctx, sess, spec.Tool, args, meta)
	if err != nil {
		return nil, fmt.Errorf("plugin %s, %s: %w", c.Name, spec.Tool, err)
	}
	if res.IsError {
		err := fmt.Errorf("plugin %s, %s: %s", c.Name, spec.Tool, strings.TrimSpace(resultText(res)))
		// A step that fails after spending (an agent stopped at its
		// limits) says what it spent in the error's structured content:
		// the step and its task keep it (IMP-195).
		if spent := errorOutput(res); spent != nil {
			return nil, pipeline.FailedWith{Output: spent, Err: err}
		}
		return nil, err
	}
	var out any
	if raw := ResultJSON(res); len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			out = map[string]any{"text": string(raw)}
		}
	}
	files, skipped := n.r.StoreFiles(res.Content, env.StoreAttachment)
	if len(files) == 0 && len(skipped) == 0 {
		return out, nil
	}
	out = LinkFiles(out, files)
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
	return m, nil
}

// runToken mints the MCP token of a call whose setting mcp asks for
// gosidian's MCP: read or write, within what the accepted manifest grants,
// for the run's project, lasting the call; revoke drops it. No setting, or
// none: no token. A run of a team's task writes only in its role's
// folders, and gets no write token when the role writes nowhere (IMP-195).
func (n node) runToken(ctx context.Context, env *pipeline.Env, c Config, acc Accepted, with map[string]any) (token string, revoke func(), err error) {
	level, _ := with["mcp"].(string)
	switch level {
	case "", "none":
		return "", nil, nil
	case AccessRead, AccessWrite:
	default:
		return "", nil, fmt.Errorf("with.mcp is none, read or write, not %q", level)
	}
	if !slices.Contains(acc.Manifest.Permissions.MCP, level) {
		return "", nil, fmt.Errorf("with.mcp %s: plugin %s was accepted without it in permissions.mcp", level, c.Name)
	}
	if n.r.tokens == nil || c.MCPURL == "" {
		return "", nil, fmt.Errorf("with.mcp %s: gosidian's MCP is not open to plugin %s ([pipelines] mcp_url)", level, c.Name)
	}
	write := level == AccessWrite
	var paths []string
	if write && n.r.scopes != nil {
		roleDirs, scoped, err := n.r.scopes.RunWritePaths(env.Run.ID)
		switch {
		case err != nil:
			return "", nil, fmt.Errorf("with.mcp write: %w", err)
		case scoped && len(roleDirs) == 0:
			return "", nil, errors.New("with.mcp write: the run's role writes nowhere in the vault (writes in the team)")
		case scoped:
			paths = roleDirs
		}
	}
	ttl := env.Engine.Config().FileTicketTTL
	if dl, ok := ctx.Deadline(); ok {
		ttl = time.Until(dl)
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	name := "run-" + env.Run.ID
	if env.Node != nil {
		name += "-" + env.Node.ID
	}
	id, secret, err := n.r.tokens.MintRun(name, env.Project, write, ttl+runTokenSlack, env.Run.ID, paths)
	if err != nil {
		return "", nil, fmt.Errorf("with.mcp %s: %w", level, err)
	}
	return secret, func() {
		if err := n.r.tokens.Revoke(id); err != nil {
			n.r.log.Warn("plugins: a call's MCP token not revoked, it expires on its own", "plugin", c.Name, "token", id, "err", err)
		}
	}, nil
}

// fileList hands a plugin that takes files by reference the files a
// setting names as a list: vault paths, or entries with a path, as in.list
// and an in trigger give them ({{ trigger.files }}).
func (n node) fileList(env *pipeline.Env, spec NodeSpec, f string, list []any, base string) ([]any, error) {
	if !spec.FileRefs {
		return nil, fmt.Errorf("with.%s is a list of files: %s takes one file (only a plugin that takes files by reference gets several)", f, n.use)
	}
	if max := n.r.Limits().Files; len(list) == 0 || len(list) > max {
		return nil, fmt.Errorf("with.%s names %d files: from 1 to %d ([pipelines] max_plugin_files)", f, len(list), max)
	}
	refs := make([]any, 0, len(list))
	for i, item := range list {
		p, _ := item.(string)
		if m, ok := item.(map[string]any); ok {
			p, _ = m["path"].(string)
		}
		if strings.TrimSpace(p) == "" {
			return nil, fmt.Errorf("with.%s[%d] names no file", f, i)
		}
		ref, err := env.FileRef(p, base)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// StoreFiles stores the files of a tool result with store (a run's
// attachments, or those of an agent's call): the stored ones as
// {name, path, mime, markdown}, the refused ones as {name, error}. A
// text file of a type attachments do not take (scheda.md, page.html)
// is not stored: it comes back as {name, mime, text}, within the
// Limits' TextBytes.
func (reg *Registry) StoreFiles(content []mcp.Content, store func(name string, data []byte) (string, error)) ([]any, []any) {
	lim := reg.Limits()
	files, skipped := []any{}, []any{}
	var total int64
	images, text := 0, 0
	for _, c := range content {
		var name, mime, b64 string
		if r, ok := mcp.AsEmbeddedResource(c); ok {
			blob, ok := mcp.AsBlobResourceContents(r.Resource)
			if !ok {
				continue
			}
			name, mime, b64 = resourceName(blob.URI), blob.MIMEType, blob.Blob
		} else if img, ok := mcp.AsImageContent(c); ok {
			images++
			name, mime, b64 = fmt.Sprintf("image-%d%s", images, extOf(img.MIMEType)), img.MIMEType, img.Data
		} else {
			continue
		}
		skip := func(why string) { skipped = append(skipped, map[string]any{"name": name, "error": why}) }
		if len(files) >= lim.FilesOut {
			skip(fmt.Sprintf("more than %d files ([pipelines] max_plugin_files_out)", lim.FilesOut))
			continue
		}
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			skip("not base64")
			continue
		}
		if total += int64(len(data)); total > lim.FilesBytes {
			skip(fmt.Sprintf("more than %d bytes of files ([pipelines] max_plugin_files_bytes)", lim.FilesBytes))
			continue
		}
		if _, _, err := attach.ValidateExt(path.Ext(name)); err != nil && isText(data) {
			if text+len(data) > lim.TextBytes {
				skip(fmt.Sprintf("more than %d bytes of text files ([pipelines] max_output_bytes)", lim.TextBytes))
				continue
			}
			text += len(data)
			if mime == "" {
				mime = "text/plain"
			}
			files = append(files, map[string]any{"name": name, "mime": mime, "text": string(data)})
			continue
		}
		rel, err := store(name, data)
		if err != nil {
			skip(err.Error())
			continue
		}
		_, isImage, _ := attach.ValidateExt(strings.ToLower(path.Ext(rel)))
		files = append(files, map[string]any{"name": name, "path": rel, "mime": mime, "markdown": attach.MarkdownRef(rel, name, isImage)})
	}
	return files, skipped
}

// isText reports whether data reads as text: UTF-8, without a NUL.
func isText(data []byte) bool {
	return utf8.Valid(data) && bytes.IndexByte(data, 0) < 0
}

// LinkFiles writes, in every string of a node's output, the markdown links
// to the files a plugin gave back by their names (](fig-1.png), also with
// a title, ](fig-1.png "caption")) as links to the attachments they were
// stored as.
func LinkFiles(v any, files []any) any {
	links := map[string]string{}
	for _, f := range files {
		m, _ := f.(map[string]any)
		name, _ := m["name"].(string)
		p, _ := m["path"].(string)
		if name != "" && p != "" {
			links[name] = attach.FileURL(p)
		}
	}
	if len(links) == 0 {
		return v
	}
	var walk func(any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case string:
			if !strings.Contains(t, "](") {
				return t
			}
			for name, url := range links {
				t = strings.ReplaceAll(t, "]("+name+")", "]("+url+")")
				t = strings.ReplaceAll(t, "]("+name+" ", "]("+url+" ")
				t = strings.ReplaceAll(t, "](<"+name+">)", "]("+url+")")
			}
			return t
		case map[string]any:
			for k, x := range t {
				t[k] = walk(x)
			}
			return t
		case []any:
			for i, x := range t {
				t[i] = walk(x)
			}
			return t
		}
		return v
	}
	return walk(v)
}

// resourceName is the file name of a resource's URI (file:///fig-1.png).
func resourceName(uri string) string {
	p := uri
	if u, err := url.Parse(uri); err == nil && u.Path != "" {
		p = u.Path
	}
	name := path.Base(strings.ReplaceAll(p, "\\", "/"))
	if name == "." || name == "/" {
		return "file"
	}
	return name
}

func mimeOf(rel string) string {
	if info, ok := attach.AllowedExt[strings.ToLower(path.Ext(rel))]; ok {
		return info.MIME
	}
	return "application/octet-stream"
}

func extOf(mime string) string {
	for ext, info := range attach.AllowedExt {
		if info.MIME == mime && ext != ".jpeg" {
			return ext
		}
	}
	return ""
}

// optionalFiles are the file fields of a node its input schema declares
// and does not require. A node without a schema has none: each of its file
// fields names a file.
func optionalFiles(spec NodeSpec) map[string]bool {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if len(spec.Input) == 0 || json.Unmarshal(spec.Input, &schema) != nil {
		return nil
	}
	out := map[string]bool{}
	for _, f := range spec.Files {
		if _, ok := schema.Properties[f]; ok {
			out[f] = true
		}
	}
	for _, r := range schema.Required {
		delete(out, r)
	}
	return out
}
