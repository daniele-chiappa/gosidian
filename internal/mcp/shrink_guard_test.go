package mcp

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
)

// bigNote is a note of about 2 KB, over the guard's default 1 KiB.
func bigNote() string {
	return "---\ntitle: Hot\n---\n\n# Hot\n\n" + strings.Repeat("a line of the note\n", 104)
}

// seedNote writes rel on disk, outside the MCP write path (and its limiter).
func seedNote(t *testing.T, root, rel, body string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func noteOnDisk(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A placeholder written over a note is refused and the note stays as it was;
// allow_shrink lets the same call through (IMP-147).
func TestShrinkGuard_Update(t *testing.T) {
	s, _, root := newTestServer(t)
	body := bigNote()
	seedNote(t, root, "p/hot.md", body)

	res, _ := s.handleUpdate(context.Background(), call(map[string]any{"path": "p/hot.md", "content": "placeholder"}))
	msg := expectError(t, res)
	for _, want := range []string{fmt.Sprintf("from %d to 11 bytes (less than 1%% of it)", len(body)), "allow_shrink: true", "Nothing was written", "memory_edit"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q lacks %q", msg, want)
		}
	}
	if got := noteOnDisk(t, root, "p/hot.md"); got != body {
		t.Fatalf("the refused rewrite changed the note: %q", got)
	}

	res, _ = s.handleUpdate(context.Background(), call(map[string]any{"path": "p/hot.md", "content": "placeholder", "allow_shrink": true}))
	resultText(t, res)
	if got := noteOnDisk(t, root, "p/hot.md"); got != "placeholder" {
		t.Errorf("allow_shrink did not write: %q", got)
	}
}

// What the guard lets through: a note under the minimum size, a shrink to
// 15%, a growth, and anything with the guard off.
func TestShrinkGuard_Passes(t *testing.T) {
	s, _, root := newTestServer(t)
	update := func(rel, content string) {
		t.Helper()
		res, _ := s.handleUpdate(context.Background(), call(map[string]any{"path": rel, "content": content}))
		resultText(t, res)
	}
	seedNote(t, root, "p/small.md", strings.Repeat("x", 1000))
	update("p/small.md", "x")

	seedNote(t, root, "p/split.md", bigNote())
	update("p/split.md", strings.Repeat("y", 300)) // 15%

	update("p/split.md", bigNote()+bigNote())

	s.SetShrinkGuard(0, 1024)
	update("p/split.md", "z")
}

// The thresholds come from SetShrinkGuard: a higher percent refuses what the
// default lets through, a lower minimum looks at smaller notes.
func TestShrinkGuard_Thresholds(t *testing.T) {
	s, _, root := newTestServer(t)
	s.SetShrinkGuard(50, 100)
	seedNote(t, root, "p/n.md", strings.Repeat("x", 400))
	res, _ := s.handleUpdate(context.Background(), call(map[string]any{"path": "p/n.md", "content": strings.Repeat("y", 150)}))
	if msg := expectError(t, res); !strings.Contains(msg, "(37% of it), under the 50%") || !strings.Contains(msg, "100 bytes or more") {
		t.Errorf("refusal = %q", msg)
	}
	res, _ = s.handleUpdate(context.Background(), call(map[string]any{"path": "p/n.md", "content": strings.Repeat("y", 200)}))
	resultText(t, res)
}

// A refused rewrite does not take a place in the write limiter.
func TestShrinkGuard_NotCountedByLimiter(t *testing.T) {
	s, _, root := newTestServer(t)
	s.SetWriteLimits(1, 0)
	seedNote(t, root, "p/hot.md", bigNote())
	for range 3 {
		res, _ := s.handleUpdate(context.Background(), call(map[string]any{"path": "p/hot.md", "content": "stub"}))
		if msg := expectError(t, res); !strings.Contains(msg, "allow_shrink") {
			t.Fatalf("refusal = %q", msg)
		}
	}
	res, _ := s.handleUpdate(context.Background(), call(map[string]any{"path": "p/hot.md", "content": bigNote() + "more\n"}))
	resultText(t, res)
	// The limiter is on: the next write is refused by it.
	res, _ = s.handleUpdate(context.Background(), call(map[string]any{"path": "p/hot.md", "content": bigNote()}))
	if msg := expectError(t, res); !strings.Contains(msg, "rate limit") {
		t.Errorf("second write = %q, want the limiter", msg)
	}
}

// memory_edit is outside the guard: the agent names what it takes away.
func TestShrinkGuard_EditNotGuarded(t *testing.T) {
	s, _, root := newTestServer(t)
	body := bigNote()
	seedNote(t, root, "p/hot.md", body)
	lines := strings.Repeat("a line of the note\n", 104)
	res, _ := s.handleEdit(context.Background(), call(map[string]any{"path": "p/hot.md", "old_string": lines, "new_string": ""}))
	resultText(t, res)
	if got := noteOnDisk(t, root, "p/hot.md"); len(got) >= 100 {
		t.Errorf("the edit did not apply: %d bytes left", len(got))
	}
}

// memory_ingest with overwrite goes through the same guard, directly and
// through an upload ticket, where allow_shrink is given at mint time.
func TestShrinkGuard_Ingest(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	root := s.vault.Root
	seedNote(t, root, "proj/report.md", bigNote())

	res, _ := s.handleIngest(ctx, call(map[string]any{
		"project": "proj", "data": b64("# stub\n"), "filename": "report.md", "note_path": "proj/report.md", "overwrite": true,
	}))
	if msg := expectError(t, res); !strings.Contains(msg, "allow_shrink") {
		t.Fatalf("ingest refusal = %q", msg)
	}
	if got := noteOnDisk(t, root, "proj/report.md"); got != bigNote() {
		t.Fatalf("the refused ingest changed the note: %q", got)
	}

	h := s.Handler("")
	out := ingestOut(t, s, ctx, map[string]any{
		"project": "proj", "transfer": "http", "note_path": "proj/report.md", "overwrite": true,
	})
	endpoint, _ := out["endpoint"].(string)
	code, body := postTicket(t, h, endpoint, "report.md", []byte("# stub\n"))
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "allow_shrink") {
		t.Fatalf("ticket without allow_shrink: %d %s", code, body)
	}

	out = ingestOut(t, s, ctx, map[string]any{
		"project": "proj", "transfer": "http", "note_path": "proj/report.md", "overwrite": true, "allow_shrink": true,
	})
	endpoint, _ = out["endpoint"].(string)
	if code, body := postTicket(t, h, endpoint, "report.md", []byte("# stub\n")); code != http.StatusOK {
		t.Fatalf("ticket with allow_shrink: %d %s", code, body)
	}
	if got := noteOnDisk(t, root, "proj/report.md"); got != "# stub\n" {
		t.Errorf("ticket with allow_shrink did not write: %q", got)
	}
}
