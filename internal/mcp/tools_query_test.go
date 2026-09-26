package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/projects"
)

type queryResult struct {
	Notes []struct {
		Path     string         `json:"path"`
		Title    string         `json:"title"`
		Modified string         `json:"modified"`
		Fields   map[string]any `json:"fields"`
	} `json:"notes"`
	Count     int  `json:"count"`
	Total     int  `json:"total"`
	Truncated bool `json:"truncated"`
}

func seedQueryNotes(t *testing.T, s *Server) {
	t.Helper()
	ctx := context.Background()
	for path, content := range map[string]string{
		"alpha/plans/a.md":  "---\ntitle: Plan A\ntype: plan\nstatus: draft\nimportance: 4\nupdated: 2026-09-01\nimplements_imp: [IMP-001]\ntags: [type:plan, status:draft]\n---\n\n# A\n",
		"alpha/plans/b.md":  "---\ntitle: Plan B\ntype: plan\nimportance: 2\nupdated: 2026-09-20\ntags: [type:plan, status:in-progress]\n---\n\n# B\n",
		"alpha/plans/c.md":  "---\ntitle: Plan C\ntype: plan\nupdated: 2026-08-15\ntags: [type:plan, status:draft]\n---\n\n# C\n",
		"beta/plans/d.md":   "---\ntitle: Plan D\ntype: plan\nstatus: draft\nimportance: 5\nupdated: 2026-09-25\ntags: [type:plan]\n---\n\n# D\n",
		"Hidden/plans/e.md": "---\ntitle: Plan E\ntype: plan\nstatus: draft\ntags: [type:plan]\n---\n\n# E\n",
	} {
		if res, _ := s.handleCreate(ctx, call(map[string]any{"path": path, "content": content})); res.IsError {
			t.Fatalf("seed %s: %s", path, expectError(t, res))
		}
	}
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pstore.Set("Hidden", projects.Flags{HiddenFromMCP: true}); err != nil {
		t.Fatal(err)
	}
	s.SetProjects(pstore)
}

func runQuery(t *testing.T, s *Server, ctx context.Context, args map[string]any) queryResult {
	t.Helper()
	res, _ := s.handleQuery(ctx, call(args))
	var out queryResult
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func where(conds ...map[string]any) []any {
	out := make([]any, 0, len(conds))
	for _, c := range conds {
		out = append(out, c)
	}
	return out
}

func TestMCP_Query(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedQueryNotes(t, s)
	ctx := context.Background()

	// Draft plans with importance >= 4, status from the field or the tag,
	// the hidden project left out, sorted by importance.
	out := runQuery(t, s, ctx, map[string]any{
		"where": where(
			map[string]any{"field": "type", "value": "plan"}, // op defaults to eq
			map[string]any{"field": "status", "op": "eq", "value": "draft"},
			map[string]any{"field": "importance", "op": "gte", "value": float64(4)},
		),
		"sort": "importance",
	})
	var paths []string
	for _, n := range out.Notes {
		paths = append(paths, n.Path)
	}
	if !reflect.DeepEqual(paths, []string{"beta/plans/d.md", "alpha/plans/a.md"}) || out.Total != 2 || out.Truncated {
		t.Fatalf("draft plans by importance: %v (total %d, truncated %v)", paths, out.Total, out.Truncated)
	}
	// Default fields are those of where + sort; a single scalar is a string.
	if !reflect.DeepEqual(out.Notes[0].Fields, map[string]any{"type": "plan", "status": "draft", "importance": "5"}) {
		t.Errorf("default fields: %v", out.Notes[0].Fields)
	}
	if out.Notes[0].Title != "Plan D" || !strings.HasPrefix(out.Notes[0].Modified, "20") {
		t.Errorf("note head: %+v", out.Notes[0])
	}

	// Project scope, in on the tag-only status, explicit fields: a list stays a list.
	out = runQuery(t, s, ctx, map[string]any{
		"project": "alpha",
		"where":   where(map[string]any{"field": "status", "op": "in", "value": []any{"in-progress", "draft"}}),
		"sort":    "path",
		"fields":  []any{"implements_imp", "status"},
		"limit":   float64(2),
	})
	if out.Count != 2 || out.Total != 3 || !out.Truncated || out.Notes[0].Path != "alpha/plans/a.md" {
		t.Fatalf("alpha by path, limit 2: %+v", out)
	}
	if !reflect.DeepEqual(out.Notes[0].Fields["implements_imp"], []any{"IMP-001"}) || out.Notes[1].Fields["status"] != "in-progress" {
		t.Errorf("fields: %v / %v", out.Notes[0].Fields, out.Notes[1].Fields)
	}

	// Dates: gte on a day.
	out = runQuery(t, s, ctx, map[string]any{
		"where": where(map[string]any{"field": "updated", "op": "gte", "value": "2026-09-20"}),
		"sort":  "updated", "order": "asc",
	})
	if len(out.Notes) != 2 || out.Notes[0].Path != "alpha/plans/b.md" || out.Notes[1].Path != "beta/plans/d.md" {
		t.Errorf("updated >= 2026-09-20 asc: %+v", out.Notes)
	}
}

func TestMCP_QueryScopeAndHidden(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedQueryNotes(t, s)
	plans := where(map[string]any{"field": "type", "op": "eq", "value": "plan"})

	scoped := &auth.Token{ID: "q", Name: "scoped", Project: "beta", Scopes: []string{auth.ScopeRead}}
	ctx := context.WithValue(context.Background(), tokenCtxKey, scoped)
	out := runQuery(t, s, ctx, map[string]any{"where": plans})
	if len(out.Notes) != 1 || out.Notes[0].Path != "beta/plans/d.md" || out.Total != 1 {
		t.Errorf("scoped token sees only its project: %+v", out)
	}
	out = runQuery(t, s, ctx, map[string]any{"where": plans, "project": "alpha"})
	if len(out.Notes) != 0 || out.Total != 0 {
		t.Errorf("a project outside the scope yields nothing: %+v", out)
	}

	res, _ := s.handleQuery(context.Background(), call(map[string]any{"where": plans, "project": "Hidden"}))
	if !res.IsError {
		t.Errorf("an explicit hidden project must be refused: %s", resultText(t, res))
	}
	out = runQuery(t, s, context.Background(), map[string]any{"where": plans})
	for _, n := range out.Notes {
		if strings.HasPrefix(n.Path, "Hidden/") {
			t.Errorf("hidden project leaked: %s", n.Path)
		}
	}
	if out.Total != 4 {
		t.Errorf("vault-wide total = %d, want 4", out.Total)
	}
}

func TestMCP_QueryBadInput(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedQueryNotes(t, s)
	ctx := context.Background()
	for name, args := range map[string]map[string]any{
		"no where":      {},
		"empty where":   {"where": []any{}},
		"not an object": {"where": []any{"status=draft"}},
		"unknown op":    {"where": where(map[string]any{"field": "status", "op": "like", "value": "d%"})},
		"object value":  {"where": where(map[string]any{"field": "status", "value": map[string]any{"x": 1}})},
		"bad order":     {"where": where(map[string]any{"field": "type", "value": "plan"}), "order": "sideways"},
		"missing value": {"where": where(map[string]any{"field": "status", "op": "eq"})},
		"too many conds": {"where": func() []any {
			w := make([]any, 17)
			for k := range w {
				w[k] = map[string]any{"field": "type", "op": "exists"}
			}
			return w
		}()},
	} {
		if res, _ := s.handleQuery(ctx, call(args)); !res.IsError {
			t.Errorf("%s: want an error, got %s", name, resultText(t, res))
		}
	}
}

func TestMCP_QueryInCoreProfile(t *testing.T) {
	if _, ok := coreToolSet["memory_query"]; !ok {
		t.Error("memory_query must be in the core profile")
	}
}
