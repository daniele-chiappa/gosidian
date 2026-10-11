package plugins

import (
	"encoding/json"
	"strings"
	"testing"
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
