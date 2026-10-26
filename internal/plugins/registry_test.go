package plugins

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestWithFiles(t *testing.T) {
	got := string(withFiles(json.RawMessage(`{"type":"object","properties":{"markdown":{"type":"string"}}}`)))
	if !strings.Contains(got, `"markdown"`) || !strings.Contains(got, `"files"`) {
		t.Errorf("object output: %s", got)
	}
	if got := string(withFiles(nil)); !strings.Contains(got, `"files"`) {
		t.Errorf("no output schema: %s", got)
	}
	if got := string(withFiles(json.RawMessage(`{"type":"string"}`))); got != `{"type":"string"}` {
		t.Errorf("a text output stays as it is: %s", got)
	}
}

func TestErrorOutput(t *testing.T) {
	spent := mcp.NewToolResultError("Claude Code stopped at max_tokens")
	spent.StructuredContent = map[string]any{"error": "stopped", "cost": 0.5, "usage": map[string]any{"total_tokens": 3076168}}
	if got := errorOutput(spent); got == nil || got["cost"] != 0.5 {
		t.Fatalf("an error with what it spent: %v", got)
	}
	raw := mcp.NewToolResultError("stopped")
	raw.RawStructuredContent = json.RawMessage(`{"usage":{"total_tokens":12}}`)
	if got := errorOutput(raw); got == nil || got["usage"] == nil {
		t.Fatalf("raw structured content: %v", got)
	}
	if got := errorOutput(mcp.NewToolResultError("bad file")); got != nil {
		t.Fatalf("a plain error: %v", got)
	}
	list := mcp.NewToolResultError("stopped")
	list.RawStructuredContent = json.RawMessage(`[1,2]`)
	if got := errorOutput(list); got != nil {
		t.Fatalf("not an object: %v", got)
	}
}
