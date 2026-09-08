package v1

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/authz"
)

var (
	// `![[target]]` / `![[target|alias]]` Obsidian image embed.
	mdEmbedRe = regexp.MustCompile(`!\[\[([^\]]+)\]\]`)
	// `![alt](/vault-files/...)` standard markdown image at a vault URL.
	mdVaultImgRe = regexp.MustCompile(`(!\[[^\]]*\]\()(/vault-files/[^)\s]+)(\))`)
	// `<img ... src="/vault-files/...">` in an HTML note.
	htmlVaultImgRe = regexp.MustCompile(`(<img\b[^>]*?\bsrc\s*=\s*)(["'])(/vault-files/[^"']+)(["'])`)
)

// inlineImages rewrites a note's image references into data: URIs so a
// downloaded file is self-contained, WITHOUT touching the stored note (which
// keeps the lightweight reference for MCP reads and storage — token savings).
// Markdown `![[X]]` and `![](/vault-files/Y)` become `![](data:...)`; HTML
// `<img src="/vault-files/Z">` becomes `src="data:..."`. Unresolvable refs are
// left untouched.
func (r *Router) inlineImages(content, format string, p authz.Principal) string {
	if r.deps.Vault == nil {
		return content
	}
	if format == "html" {
		return htmlVaultImgRe.ReplaceAllStringFunc(content, func(m string) string {
			sub := htmlVaultImgRe.FindStringSubmatch(m)
			if d := r.vaultURLToDataURI(sub[3], p); d != "" {
				return sub[1] + sub[2] + d + sub[4]
			}
			return m
		})
	}
	// Markdown: resolve ![[embed]] first (needs vault/index resolution), then
	// plain ![](/vault-files/...) URLs.
	res := previewResolver{r: r, p: p}
	content = mdEmbedRe.ReplaceAllStringFunc(content, func(m string) string {
		sub := mdEmbedRe.FindStringSubmatch(m)
		target, alias := splitEmbed(sub[1])
		if u := res.ResolveImage(target); u != "" {
			if d := r.vaultURLToDataURI(u, p); d != "" {
				return "![" + alias + "](" + d + ")"
			}
		}
		return m
	})
	content = mdVaultImgRe.ReplaceAllStringFunc(content, func(m string) string {
		sub := mdVaultImgRe.FindStringSubmatch(m)
		if d := r.vaultURLToDataURI(sub[2], p); d != "" {
			return sub[1] + d + sub[3]
		}
		return m
	})
	return content
}

// vaultURLToDataURI reads the image behind a /vault-files/<rel> URL and returns
// its data: URI, or "" on any failure or when the caller may not read it.
//
// This helper embeds file bytes into a response, so it MUST NOT be more
// permissive than the endpoint that serves the same /vault-files/ URLs
// (server.handleVaultFile). It applies, in order: the same attachments/
// confinement (so notes and the machine-owned .gosidian/ credential store are
// never reachable), an image-only extension gate (only real images are ever
// inlined — a .json/.pdf attachment is legitimate but must not be embedded
// here), and the shared per-note authz gate every other read funnels through
// (so an embed cannot cross a project boundary). Dropping the principal on this
// path was CVE/GHSA-45w4-74p9-cj5j: a member could inline .gosidian/auth.json.
func (r *Router) vaultURLToDataURI(url string, p authz.Principal) string {
	rel := strings.TrimPrefix(url, "/vault-files/")
	clean, err := r.deps.Vault.Rel(rel)
	if err != nil {
		return ""
	}
	// Confine to attachments/ subpaths, exactly like server.handleVaultFile.
	// This keeps notes and .gosidian/ (auth.json, gitsync.json, tokens.json)
	// out of reach regardless of extension or principal.
	if !strings.Contains("/"+clean, "/attachments/") {
		return ""
	}
	// Only ever inline real images. attach.DataURI would otherwise fall back to
	// application/octet-stream for any allowlisted-but-non-image attachment.
	if info, ok := attach.AllowedExt[strings.ToLower(filepath.Ext(clean))]; !ok || !info.IsImage {
		return ""
	}
	// Reading a file is a read of its project: apply the same gate every other
	// read handler uses, so an embed cannot cross a project boundary.
	if !r.canSee(p, clean) {
		return ""
	}
	abs, err := r.deps.Vault.Abs(clean)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	return attach.DataURI(data, filepath.Ext(clean))
}

func splitEmbed(inner string) (target, alias string) {
	parts := strings.SplitN(inner, "|", 2)
	target = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		alias = strings.TrimSpace(parts[1])
	}
	return target, alias
}
