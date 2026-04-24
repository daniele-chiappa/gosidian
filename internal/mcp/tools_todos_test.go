package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// seedTodosVault populates a vault with a mix of plan/doc notes containing
// checkboxes in various shapes. Returns the project name for convenience.
func seedTodosVault(t *testing.T, s *Server) string {
	t.Helper()
	ctx := context.Background()
	notes := []struct{ path, content string }{
		{
			"proj/plans/a.md",
			"---\ntitle: plan-a\ntype: plan\nstatus: in-progress\ntags: [type:plan, status:in-progress]\n---\n\n# Plan A\n\n## Goals\n\n- [ ] first open task\n- [x] first done task\n\n## Steps\n\n- [ ] step one\n- [ ] step two\n- [X] step three done\n",
		},
		{
			"proj/plans/b.md",
			"---\ntitle: plan-b\ntype: plan\nstatus: done\ntags: [type:plan, status:done]\n---\n\n# Plan B\n\n- [x] everything closed\n",
		},
		{
			"proj/plans/c.md",
			"---\ntitle: plan-c\ntype: plan\nstatus: draft\ntags: [type:plan, status:draft]\n---\n\n# Plan C\n\n- [ ] draft item\n",
		},
		{
			"proj/docs/tasks.md",
			"---\ntitle: tasks\ntags: [type:doc]\n---\n\n# Tasks\n\n## Pending\n\n- [ ] doc task no-plan\n",
		},
		{
			"proj/memory/weird.md",
			"---\ntitle: weird\ntags: [type:memory]\n---\n\n# Weird\n\n```markdown\n- [ ] this is inside a fence, must be ignored\n```\n\n* [ ] asterisk bullet must be ignored\n- [] missing space must be ignored\n- [-] non-standard marker must be ignored\n- [ ] real checkbox after the noise\n",
		},
	}
	for _, n := range notes {
		res, err := s.handleCreate(ctx, call(map[string]any{"path": n.path, "content": n.content}))
		if err != nil || (res != nil && res.IsError) {
			t.Fatalf("seed %q: err=%v res=%+v", n.path, err, res)
		}
	}
	return "proj"
}

type todosResponse struct {
	Todos    []todoEntry `json:"todos"`
	Count    int         `json:"count"`
	Project  string      `json:"project"`
	Limit    int         `json:"limit"`
	Filtered bool        `json:"filtered"`
}

func decodeTodos(t *testing.T, body string) todosResponse {
	t.Helper()
	var r todosResponse
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("json unmarshal failed: %v body=%s", err, body)
	}
	return r
}

func TestMCP_Todos_Unfiltered(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedTodosVault(t, s)

	res, _ := s.handleTodos(context.Background(), call(map[string]any{"project": "proj"}))
	body := resultText(t, res)
	r := decodeTodos(t, body)

	// plans/a.md → 5 todos, plans/b.md → 1, plans/c.md → 1, docs/tasks.md → 1, memory/weird.md → 1 (just the real one)
	if want := 9; r.Count != want {
		t.Fatalf("expected %d todos, got %d: %+v", want, r.Count, r.Todos)
	}

	// Spot check: plan-a has a "step three done" with capital X — must be
	// recognised as checked.
	var stepThree *todoEntry
	for i := range r.Todos {
		if r.Todos[i].Text == "step three done" {
			stepThree = &r.Todos[i]
			break
		}
	}
	if stepThree == nil {
		t.Fatal("expected to find 'step three done' todo")
	}
	if !stepThree.Checked {
		t.Errorf("expected 'step three done' checked=true, got %+v", stepThree)
	}
	if stepThree.ParentHeading != "Steps" {
		t.Errorf("expected parent_heading=Steps, got %q", stepThree.ParentHeading)
	}
	if stepThree.PlanStatus != "in-progress" {
		t.Errorf("expected plan_status=in-progress, got %q", stepThree.PlanStatus)
	}
}

func TestMCP_Todos_OnlyOpen(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedTodosVault(t, s)

	res, _ := s.handleTodos(context.Background(), call(map[string]any{"project": "proj", "only_open": true}))
	body := resultText(t, res)
	r := decodeTodos(t, body)

	// Unfiltered has 9; checked ones are: plans/a.md "first done task",
	// plans/a.md "step three done", plans/b.md "everything closed" → 3
	// checked → 6 open expected.
	if want := 6; r.Count != want {
		t.Fatalf("expected %d open todos, got %d: %+v", want, r.Count, r.Todos)
	}
	for _, td := range r.Todos {
		if td.Checked {
			t.Errorf("only_open=true returned a checked todo: %+v", td)
		}
	}
}

func TestMCP_Todos_PlanStatusFilter(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedTodosVault(t, s)

	res, _ := s.handleTodos(context.Background(), call(map[string]any{"project": "proj", "plan_status": "in-progress"}))
	body := resultText(t, res)
	r := decodeTodos(t, body)

	// Only plans/a.md is in-progress → 5 todos, all with plan_status=in-progress.
	if want := 5; r.Count != want {
		t.Fatalf("expected %d todos from in-progress plans, got %d: %+v", want, r.Count, r.Todos)
	}
	for _, td := range r.Todos {
		if td.PlanStatus != "in-progress" {
			t.Errorf("expected plan_status=in-progress, got %q in %+v", td.PlanStatus, td)
		}
	}
}

func TestMCP_Todos_PathPrefix(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedTodosVault(t, s)

	res, _ := s.handleTodos(context.Background(), call(map[string]any{"project": "proj", "path_prefix": "proj/plans"}))
	body := resultText(t, res)
	r := decodeTodos(t, body)

	// plans/ subset → a (5) + b (1) + c (1) = 7
	if want := 7; r.Count != want {
		t.Fatalf("expected %d todos under plans/, got %d: %+v", want, r.Count, r.Todos)
	}
}

func TestMCP_Todos_PathPrefixRejectsOutsideScope(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedTodosVault(t, s)
	res, _ := s.handleTodos(context.Background(), call(map[string]any{"project": "proj", "path_prefix": "other/plans"}))
	if msg := expectError(t, res); msg == "" {
		t.Error("expected error when path_prefix is outside project scope")
	}
}

func TestMCP_Todos_RejectsUnknownPlanStatus(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedTodosVault(t, s)
	res, _ := s.handleTodos(context.Background(), call(map[string]any{"project": "proj", "plan_status": "nonsense"}))
	if msg := expectError(t, res); msg == "" {
		t.Error("expected error for unknown plan_status")
	}
}

func TestMCP_Todos_RejectsWithoutProject(t *testing.T) {
	s, _, _ := newTestServer(t)
	res, _ := s.handleTodos(context.Background(), call(map[string]any{}))
	if msg := expectError(t, res); msg == "" {
		t.Error("expected error when project is missing")
	}
}

func TestMCP_Todos_IgnoresFenceAndVariants(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	// This note has 4 invalid patterns and only 1 valid checkbox.
	_, _ = s.handleCreate(ctx, call(map[string]any{
		"path":    "proj/x.md",
		"content": "---\ntitle: x\ntags: []\n---\n\n# X\n\n```\n- [ ] fence\n```\n\n* [ ] asterisk\n- [] nospace\n- [-] non-standard\n- [ ] the only real one\n",
	}))

	res, _ := s.handleTodos(context.Background(), call(map[string]any{"project": "proj"}))
	body := resultText(t, res)
	r := decodeTodos(t, body)
	if r.Count != 1 {
		t.Fatalf("expected exactly 1 valid checkbox, got %d: %+v", r.Count, r.Todos)
	}
	if r.Todos[0].Text != "the only real one" {
		t.Errorf("expected 'the only real one', got %q", r.Todos[0].Text)
	}
}
