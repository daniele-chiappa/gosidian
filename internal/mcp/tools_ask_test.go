package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func decodeAsk(t *testing.T, body string) askResult {
	t.Helper()
	var r askResult
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	return r
}

func TestMCP_Ask_CreatesFileWithFirstOQ(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	// Bootstrap a minimal project presence so NotesByPrefix has something,
	// but the ask tool doesn't require it. Still, create one note so the
	// vault isn't wholly empty.
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "proj/README.md", "content": "---\ntitle: r\ntags: [proj]\n---\n\n# r\n"}))

	res, _ := s.handleAsk(ctx, call(map[string]any{
		"project":  "proj",
		"question": "Should we switch to modernc.org/sqlite from mattn/go-sqlite3?",
	}))
	body := resultText(t, res)
	r := decodeAsk(t, body)

	if r.OQID != "OQ-001" || r.Index != 1 {
		t.Fatalf("expected OQ-001 index 1, got %+v", r)
	}
	if r.Path != "proj/docs/open-questions.md" {
		t.Fatalf("unexpected path: %s", r.Path)
	}
	if r.ETag == "" {
		t.Fatalf("expected etag in response, got empty")
	}

	// Confirm the file was created with canonical structure.
	get, _ := s.handleGet(ctx, call(map[string]any{"path": "proj/docs/open-questions.md"}))
	text := resultText(t, get)
	for _, want := range []string{"# Open questions", "## Aperte", "### OQ-001 — Should we switch"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in created file\n---\n%s", want, text)
		}
	}
}

func TestMCP_Ask_AutoIncrementsAcrossCalls(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "proj/README.md", "content": "---\ntitle: r\ntags: [proj]\n---\n\n# r\n"}))

	for i := 1; i <= 3; i++ {
		res, _ := s.handleAsk(ctx, call(map[string]any{
			"project":  "proj",
			"question": "question number " + []string{"", "one", "two", "three"}[i],
		}))
		r := decodeAsk(t, resultText(t, res))
		if want := i; r.Index != want {
			t.Errorf("call %d: expected index %d, got %d", i, want, r.Index)
		}
	}

	get, _ := s.handleGet(ctx, call(map[string]any{"path": "proj/docs/open-questions.md"}))
	body := resultText(t, get)
	for _, id := range []string{"OQ-001", "OQ-002", "OQ-003"} {
		if !strings.Contains(body, id) {
			t.Errorf("expected %s in file, got:\n%s", id, body)
		}
	}
}

func TestMCP_Ask_UrgencyRecorded(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "proj/README.md", "content": "---\ntitle: r\ntags: [proj]\n---\n\n# r\n"}))

	_, _ = s.handleAsk(ctx, call(map[string]any{
		"project":  "proj",
		"question": "Pick rebranded domain?",
		"urgency":  "high",
		"context":  "needed before the public announcement",
	}))

	get, _ := s.handleGet(ctx, call(map[string]any{"path": "proj/docs/open-questions.md"}))
	body := resultText(t, get)
	if !strings.Contains(body, "**Urgency**: high") {
		t.Errorf("urgency not recorded correctly:\n%s", body)
	}
	if !strings.Contains(body, "needed before the public announcement") {
		t.Errorf("context not recorded correctly:\n%s", body)
	}
}

func TestMCP_Ask_InsertsBeforeRisolteSection(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "proj/README.md", "content": "---\ntitle: r\ntags: [proj]\n---\n\n# r\n"}))

	// Seed a pre-existing open-questions.md with both sections + one prior OQ.
	seed := "---\ntitle: open-questions\ntags: [proj, type:doc]\n---\n\n# Open questions\n\n## Aperte\n\n### OQ-007 — preexisting\n\n- **Date**: 2026-01-01\n\n## Risolte\n\nSee qa.md.\n"
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "proj/docs/open-questions.md", "content": seed}))

	res, _ := s.handleAsk(ctx, call(map[string]any{
		"project":  "proj",
		"question": "another new question",
	}))
	r := decodeAsk(t, resultText(t, res))
	if r.Index != 8 {
		t.Fatalf("expected next index after 007 to be 8, got %d", r.Index)
	}

	get, _ := s.handleGet(ctx, call(map[string]any{"path": "proj/docs/open-questions.md"}))
	body := resultText(t, get)
	// OQ-008 must appear before the Risolte heading in the file.
	oqIdx := strings.Index(body, "OQ-008")
	risolteIdx := strings.Index(body, "## Risolte")
	if oqIdx < 0 || risolteIdx < 0 || oqIdx > risolteIdx {
		t.Errorf("expected OQ-008 before ## Risolte, got positions %d/%d in:\n%s", oqIdx, risolteIdx, body)
	}
}

func TestMCP_Ask_RejectsEmptyQuestion(t *testing.T) {
	s, _, _ := newTestServer(t)
	res, _ := s.handleAsk(context.Background(), call(map[string]any{"project": "proj", "question": "  "}))
	if msg := expectError(t, res); msg == "" {
		t.Error("expected error for empty question")
	}
}

func TestMCP_Ask_RejectsUnknownUrgency(t *testing.T) {
	s, _, _ := newTestServer(t)
	res, _ := s.handleAsk(context.Background(), call(map[string]any{
		"project": "proj", "question": "q?", "urgency": "critical",
	}))
	if msg := expectError(t, res); msg == "" {
		t.Error("expected error for unknown urgency")
	}
}
