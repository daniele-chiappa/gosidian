package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// keep_last_n=0 means "archive everything": it must produce a file with the
// summary and no entries, not an index-out-of-range panic (BUG-040).
func TestCompact_KeepZeroArchivesEverything(t *testing.T) {
	s, _, dir := newTestServer(t)
	body := "# Log\n\nintro\n\n## 2026-01-01 — first\n\n- a\n\n## 2026-01-02 — second\n\n- b\n"
	if err := os.MkdirAll(filepath.Join(dir, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "proj/log.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := s.handleCompact(context.Background(), call(map[string]any{
		"path": "proj/log.md", "keep_last_n": 0, "archive_summary": "everything archived",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, res))
	}
	var out compactResult
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ArchivedEntries != 2 || out.KeptEntries != 0 {
		t.Fatalf("archived=%d kept=%d, want 2/0", out.ArchivedEntries, out.KeptEntries)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "proj/log.md"))
	if strings.Contains(string(got), "## 2026-01-0") {
		t.Fatalf("entries survived a keep=0 compaction:\n%s", got)
	}
	if !strings.Contains(string(got), "everything archived") {
		t.Fatalf("summary missing:\n%s", got)
	}
}
