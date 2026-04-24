// Package mcp — memory_project_scaffold tool (v1.6, extended in v1.8).
//
// Scaffolds a project by copying a template from
// <vault>/.gosidian/templates/<name>/ into the new project directory.
// Idempotent: files that already exist in the target project are
// skipped, never overwritten. Templates live in the vault so users
// can edit / add variants without rebuilding the binary; the binary
// seeds three presets (karpathy-wiki / minimal / team) at first boot
// via scaffold.SeedTemplates (see cmd/gosidian/main.go).
package mcp

import (
	"context"
	"embed"
	"fmt"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/scaffold"
	"github.com/mark3labs/mcp-go/mcp"
)

//go:embed all:assets_templates/*
var embeddedTemplates embed.FS

// EmbeddedTemplatesFS exposes the embedded template tree for seeding
// from main.go.
func EmbeddedTemplatesFS() embed.FS { return embeddedTemplates }

// EmbeddedTemplatesRoot is the path inside EmbeddedTemplatesFS that
// holds the template directories.
const EmbeddedTemplatesRoot = "assets_templates"

const defaultTemplate = "karpathy-wiki"

// registerScaffoldTool adds memory_project_scaffold (extended in v1.8
// with `template` and `variables` parameters).
func (s *Server) registerScaffoldTool() {
	s.impl.AddTool(mcp.NewTool("memory_project_scaffold",
		mcp.WithDescription("Populate a project with a bootstrap template. Templates live under <vault>/.gosidian/templates/<name>/ and can be inspected with memory_list_bootstrap_templates. Idempotent — existing files are skipped, never overwritten. Default template is \"karpathy-wiki\" (full Karpathy-Wiki-Stack layout); pass template=\"minimal\" for a lightweight scaffold or template=\"team\" for the team-oriented preset with pre-populated agents/."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project (top-level folder) to scaffold. The folder does not need to exist yet. Scoped tokens are forced to their project.")),
		mcp.WithString("template", mcp.Description("Template name (directory under .gosidian/templates/). Defaults to \"karpathy-wiki\". Use memory_list_bootstrap_templates to discover what's installed.")),
		mcp.WithObject("variables", mcp.Description("Override map for template placeholders. Only variables declared in the template's _template.toml as `required` (without `auto` or `default`) need to be supplied; PROJECT and TODAY are filled automatically.")),
	), s.handleProjectScaffold)
}

type scaffoldResult struct {
	Project  string   `json:"project"`
	Template string   `json:"template"`
	Created  []string `json:"created"`
	Skipped  []string `json:"skipped"`
}

func (s *Server) handleProjectScaffold(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeWrite(ctx, "")
	if errRes != nil {
		return errRes, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !tok.AllowsPath(project) {
		return mcp.NewToolResultErrorf("project %q is outside the token's scope", project), nil
	}
	tmplName := strings.TrimSpace(req.GetString("template", defaultTemplate))
	if tmplName == "" {
		tmplName = defaultTemplate
	}

	tmpl, err := scaffold.LoadTemplate(s.vault.Root, tmplName)
	if err != nil {
		return mcp.NewToolResultErrorf("template %q not found in vault; run memory_list_bootstrap_templates to list what's available", tmplName), nil
	}

	// Resolve variables: start from defaults + auto, overlay caller input.
	vars, err := resolveVariables(tmpl.Meta, project, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	res := scaffoldResult{Project: project, Template: tmpl.Name}
	for _, rel := range tmpl.Files {
		data, err := tmpl.ReadFile(rel)
		if err != nil {
			return mcp.NewToolResultErrorf("read template file %s: %v", rel, err), nil
		}
		body := applyVars(string(data), vars)
		vaultPath := project + "/" + rel
		if _, loadErr := s.vault.Load(vaultPath); loadErr == nil {
			res.Skipped = append(res.Skipped, vaultPath)
			continue
		}
		if errRes := s.checkWriteLimits(tok, len(body)); errRes != nil {
			return errRes, nil
		}
		if err := s.writeAndIndex(vaultPath, []byte(body)); err != nil {
			return mcp.NewToolResultErrorFromErr(fmt.Sprintf("write %s", vaultPath), err), nil
		}
		s.auditWrite(ctx, audit.ActionCreate, vaultPath, "", int64(len(body)))
		res.Created = append(res.Created, vaultPath)
	}
	if res.Created == nil {
		res.Created = []string{}
	}
	if res.Skipped == nil {
		res.Skipped = []string{}
	}
	return mcp.NewToolResultJSON(res)
}

// resolveVariables assembles the substitution map from the template
// definition + caller-supplied overrides.
//
// Rules:
//  1. Each `Variable` in the template meta gets a value (or an error).
//  2. PROJECT is always taken from the tool argument.
//  3. Variables with `auto: date` are filled with today's UTC date.
//  4. Variables with a `default` fall back to that when no override is
//     supplied.
//  5. Variables marked `required: true` without default/auto must be
//     present in the caller's `variables` map — otherwise error.
//  6. Unknown caller keys are ignored silently (forward compatible).
func resolveVariables(meta scaffold.Meta, project string, req mcp.CallToolRequest) (map[string]string, error) {
	vars := map[string]string{
		"PROJECT": project,
		"TODAY":   time.Now().UTC().Format("2006-01-02"),
	}
	overrides := extractStringMap(req.GetArguments()["variables"])

	for _, v := range meta.Variables {
		if v.Name == "" {
			continue
		}
		// Auto fill (wins over any caller override — the caller
		// shouldn't need to pass PROJECT/TODAY, and if they do it's
		// respected for PROJECT only, since auto == "" for PROJECT).
		switch v.Auto {
		case "date":
			vars[v.Name] = time.Now().UTC().Format("2006-01-02")
			continue
		case "project":
			vars[v.Name] = project
			continue
		}
		// Caller override.
		if val, ok := overrides[v.Name]; ok && val != "" {
			vars[v.Name] = val
			continue
		}
		// PROJECT/TODAY already seeded above.
		if _, seeded := vars[v.Name]; seeded {
			continue
		}
		// Default.
		if v.Default != "" {
			vars[v.Name] = v.Default
			continue
		}
		// Required without fallback → error.
		if v.Required {
			return nil, fmt.Errorf("template variable %q is required; pass it via the `variables` argument", v.Name)
		}
		// Otherwise leave unset — the placeholder will stay literal,
		// which is fine for optional substitutions.
	}
	return vars, nil
}

// applyVars substitutes every {{NAME}} occurrence with vars[NAME]. Keys
// not in `vars` are left in place so missing substitutions are visible
// in the scaffolded output.
func applyVars(body string, vars map[string]string) string {
	for k, v := range vars {
		body = strings.ReplaceAll(body, "{{"+k+"}}", v)
	}
	return body
}

// extractStringMap coerces an arbitrary JSON-decoded value into
// map[string]string, dropping non-string values. Returns an empty map
// when input is nil or not a map. Used to parse the `variables`
// object argument of memory_project_scaffold in a mcp-go version
// that doesn't ship a GetStringMap helper.
func extractStringMap(v any) map[string]string {
	out := map[string]string{}
	if v == nil {
		return out
	}
	m, ok := v.(map[string]any)
	if !ok {
		return out
	}
	for k, val := range m {
		if s, ok := val.(string); ok {
			out[k] = s
		}
	}
	return out
}
