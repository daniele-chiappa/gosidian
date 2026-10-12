package plugins

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/pipeline"
)

const (
	// maxFiles caps the files one call may give back.
	maxFiles = 200
	// maxFilesBytes caps their size together.
	maxFilesBytes = 100 << 20
	// maxListFiles caps the files one setting may name as a list.
	maxListFiles = 100

	// MetaLLMTokensLeft is the _meta field of a node's call with the LLM
	// tokens the run may still use, when [pipelines] max_llm_tokens caps
	// them.
	MetaLLMTokensLeft = "gosidian/llm_tokens_left"
)

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
	base := c.FilesURL
	if base == "" && spec.FileRefs {
		base = env.Engine.Config().PublicURL
	}
	for _, f := range spec.Files {
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
	var meta map[string]any
	if left, capped := env.LLMTokensLeft(); capped {
		meta = map[string]any{MetaLLMTokensLeft: left}
	}
	sess, err := n.r.connect(ctx, c)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	// The manifest is read again at every call: a plugin that changed it
	// waits for the owner's new accept.
	live, err := fetchManifest(ctx, sess, c.Name)
	if err != nil {
		return nil, err
	}
	if sha := live.SHA(); sha != acc.SHA {
		return nil, fmt.Errorf("plugin %s serves another manifest (version %s, %s) than the accepted one (version %s, %s): gosidian plugin show %s, then gosidian plugin accept %s <sha>",
			c.Name, live.Version, sha[:12], acc.Manifest.Version, acc.SHA[:12], c.Name, c.Name)
	}
	res, err := callToolMeta(ctx, sess, spec.Tool, args, meta)
	if err != nil {
		return nil, fmt.Errorf("plugin %s, %s: %w", c.Name, spec.Tool, err)
	}
	if res.IsError {
		return nil, fmt.Errorf("plugin %s, %s: %s", c.Name, spec.Tool, strings.TrimSpace(resultText(res)))
	}
	var out any
	if raw := resultJSON(res); len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			out = map[string]any{"text": string(raw)}
		}
	}
	files, skipped := storeFiles(env, res.Content)
	if len(files) == 0 && len(skipped) == 0 {
		return out, nil
	}
	out = linkFiles(out, files)
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

// fileList hands a plugin that takes files by reference the files a
// setting names as a list: vault paths, or entries with a path, as in.list
// and an in trigger give them ({{ trigger.files }}).
func (n node) fileList(env *pipeline.Env, spec NodeSpec, f string, list []any, base string) ([]any, error) {
	if !spec.FileRefs {
		return nil, fmt.Errorf("with.%s is a list of files: %s takes one file (only a plugin that takes files by reference gets several)", f, n.use)
	}
	if len(list) == 0 || len(list) > maxListFiles {
		return nil, fmt.Errorf("with.%s names %d files: from 1 to %d", f, len(list), maxListFiles)
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

// storeFiles stores the files of a tool result as attachments.
func storeFiles(env *pipeline.Env, content []mcp.Content) ([]any, []any) {
	files, skipped := []any{}, []any{}
	total, images := 0, 0
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
		if len(files) >= maxFiles {
			skip(fmt.Sprintf("more than %d files", maxFiles))
			continue
		}
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			skip("not base64")
			continue
		}
		if total += len(data); total > maxFilesBytes {
			skip(fmt.Sprintf("more than %d MiB of files", maxFilesBytes>>20))
			continue
		}
		rel, err := env.StoreAttachment(name, data)
		if err != nil {
			skip(err.Error())
			continue
		}
		_, isImage, _ := attach.ValidateExt(strings.ToLower(path.Ext(rel)))
		files = append(files, map[string]any{"name": name, "path": rel, "mime": mime, "markdown": attach.MarkdownRef(rel, name, isImage)})
	}
	return files, skipped
}

// linkFiles writes, in every string of a node's output, the markdown links
// to the files a plugin gave back by their names (](fig-1.png), also with
// a title, ](fig-1.png "caption")) as links to the attachments they were
// stored as.
func linkFiles(v any, files []any) any {
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
