package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Only "not found" may bootstrap open-questions.md from the template; any
// other read error must abort instead of overwriting the file (BUG-044).
func TestAsk_NonNotFoundLoadErrorAborts(t *testing.T) {
	s, _, dir := newTestServer(t)
	// A directory where the note should be: Load fails with something that
	// is not ErrNotExist.
	if err := os.MkdirAll(filepath.Join(dir, "p/docs/open-questions.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, _ := s.handleAsk(context.Background(), call(map[string]any{"project": "p", "question": "q?"}))
	msg := expectError(t, res)
	if !strings.Contains(msg, "load failed") {
		t.Fatalf("expected the load error to surface, got: %s", msg)
	}
}
