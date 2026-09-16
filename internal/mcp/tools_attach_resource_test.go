package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// When the filename is derived from source_path/bridge_filename the response
// must still report it (IMP-082).
func TestUploadResource_ReportsDerivedFilename(t *testing.T) {
	s, _, _ := newTestServer(t)
	root := t.TempDir()
	s.SetAllowedUploadRoots([]string{root})
	src := filepath.Join(root, "report.pdf")
	if err := os.WriteFile(src, []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n1 0 obj\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := s.handleUploadResource(context.Background(), call(map[string]any{"project": "p", "source_path": src}))
	var out map[string]any
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	if out["original_filename"] != "report.pdf" {
		t.Fatalf("original_filename = %v, want report.pdf (%v)", out["original_filename"], out)
	}
	if h, _ := out["hash"].(string); h == "" {
		t.Fatalf("hash empty: %v", out)
	}
}
