// Package mcp — memory_query tool (IMP-099): notes selected by their
// frontmatter, like a Dataview filter, from the note_fields index table.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/index"
	"github.com/mark3labs/mcp-go/mcp"
)

func (s *Server) registerQueryTool() {
	s.impl.AddTool(mcp.NewTool("memory_query",
		mcp.WithDescription("Find notes by their frontmatter, like a Dataview filter: every condition in `where` must hold. "+
			"A field is a frontmatter key (a list matches element by element); a namespaced tag counts as a field when the note has no field of that name "+
			"(tag status:done → status = done), and `tags` is the tag list. "+
			"Operators: eq, ne, in, exists, lt, lte, gt, gte, contains. ISO dates (YYYY-MM-DD) and numbers compare as such, text ignores case; "+
			"ne and exists:false also match notes without the field. "+
			"Returns path, title, modification time and the fields listed in `fields` (default: those used in where and sort), "+
			"newest modification first unless `sort` names a field or path, title, modified. "+
			"Use it for any question about status, type, dates, importance or other frontmatter instead of memory_notes_by_tag + memory_batch_get. "+
			"Example — draft plans with importance ≥ 3 updated since September: where "+
			`[{"field":"type","op":"eq","value":"plan"},{"field":"status","op":"eq","value":"draft"},{"field":"importance","op":"gte","value":3},{"field":"updated","op":"gte","value":"2026-09-01"}].`),
		mcp.WithString("project", mcp.Description("Project (top-level folder) to query; empty = every project the token can read. Scoped tokens are forced to their projects.")),
		mcp.WithArray("where", mcp.Required(), mcp.Description(fmt.Sprintf("Conditions, all required (max %d). Each: {field, op (default eq), value} — value is a string or number, a list for in, true/false for exists.", index.MaxQueryConds)),
			mcp.Items(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"field": map[string]any{"type": "string", "description": "Frontmatter key, a namespaced tag prefix (status, type, topic…) or tags."},
					"op":    map[string]any{"type": "string", "enum": []string{"eq", "ne", "in", "exists", "lt", "lte", "gt", "gte", "contains"}},
					"value": map[string]any{"description": "String, number, list (for in) or boolean (for exists)."},
				},
				"required": []string{"field"},
			})),
		mcp.WithString("sort", mcp.Description("Field to sort by, or path, title, modified (default: modified, newest first). Notes without the field come last.")),
		mcp.WithString("order", mcp.Description("desc (default) or asc; path and title default to asc.")),
		mcp.WithArray("fields", mcp.Description("Frontmatter fields to return for each note (default: the fields used in where and sort)."), mcp.WithStringItems()),
		mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Max notes (default %d, max %d). `total` tells how many matched.", index.QueryDefaultLimit, index.QueryMaxLimit))),
	), s.handleQuery)
}

type queryNote struct {
	Path     string         `json:"path"`
	Title    string         `json:"title"`
	Modified string         `json:"modified"`
	Fields   map[string]any `json:"fields,omitempty"`
}

func (s *Server) handleQuery(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	args := req.GetArguments()
	where, err := parseWhere(args["where"])
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	sort := strings.TrimSpace(req.GetString("sort", ""))
	desc, err := index.SortDesc(sort, req.GetString("order", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := index.ClampQueryLimit(req.GetInt("limit", index.QueryDefaultLimit))
	fields := req.GetStringSlice("fields", nil)
	if len(fields) == 0 {
		fields = index.DefaultQueryFields(where, sort)
	}

	project := strings.TrimSpace(req.GetString("project", ""))
	var requested []string
	if project != "" {
		if res := s.rejectIfHidden(project); res != nil {
			return res, nil
		}
		requested = []string{project}
	}
	filter := buildProjectsFilter(requested, tok.ProjectList())
	opts := index.QueryOptions{Exclude: s.hiddenProjects(), Where: where, Sort: sort, Desc: desc, Limit: limit, Fields: fields}
	if filter.active {
		opts.Projects = append([]string{}, filter.allowed...) // non-nil: empty matches nothing
	}
	hits, total, err := s.index.Query(opts)
	if errors.Is(err, index.ErrBadQuery) {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err != nil {
		return mcp.NewToolResultErrorFromErr("query failed", err), nil
	}

	// The checks below repeat the query's scope as defence in depth.
	out := make([]queryNote, 0, len(hits))
	for _, h := range hits {
		if !tok.AllowsPath(h.Path) || !filter.matches(h.Path) || s.pathInHiddenProject(h.Path) {
			total--
			continue
		}
		out = append(out, queryNote{Path: h.Path, Title: h.Title,
			Modified: time.Unix(h.ModTime, 0).UTC().Format(time.RFC3339), Fields: h.FieldValues()})
	}
	return mcp.NewToolResultJSON(map[string]any{
		"notes":     out,
		"count":     len(out),
		"total":     total,
		"truncated": total > len(out),
	})
}

// parseWhere reads the where argument: a list of {field, op, value}.
func parseWhere(raw any) ([]index.FieldCond, error) {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, errors.New("where must be a non-empty list of {field, op, value}")
	}
	if len(list) > index.MaxQueryConds {
		return nil, fmt.Errorf("where accepts at most %d conditions", index.MaxQueryConds)
	}
	out := make([]index.FieldCond, 0, len(list))
	for k, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("where[%d] must be an object {field, op, value}", k)
		}
		field, _ := m["field"].(string)
		op, _ := m["op"].(string)
		values, err := index.CondValues(m["value"])
		if err != nil {
			return nil, fmt.Errorf("where[%d].value: %w", k, err)
		}
		out = append(out, index.FieldCond{Field: field, Op: op, Values: values})
	}
	return out, nil
}
