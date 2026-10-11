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
)

// node runs a plugin's node: the settings that name files carry their
// content, the tool's result is the output, and the files it gives back
// (embedded resources, images) become attachments of the project, listed
// in the output's files.
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
	for _, f := range spec.Files {
		p, _ := args[f].(string)
		if strings.TrimSpace(p) == "" {
			return nil, fmt.Errorf("with.%s names the file for %s", f, n.use)
		}
		if spec.FileRefs {
			base := c.FilesURL
			if base == "" {
				base = env.Engine.Config().PublicURL
			}
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
	res, err := callTool(ctx, sess, spec.Tool, args)
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
