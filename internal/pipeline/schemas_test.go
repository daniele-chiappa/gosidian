package pipeline

import (
	"encoding/json"
	"testing"
)

// Every base node type has its schemas, and they are JSON objects.
func TestBaseSchemas_EveryBaseType(t *testing.T) {
	have := map[string]bool{}
	for _, s := range BaseSchemas() {
		have[s.Type] = true
		for name, raw := range map[string]json.RawMessage{"input": s.Input, "output": s.Output} {
			var v map[string]any
			if err := json.Unmarshal(raw, &v); err != nil {
				t.Errorf("%s %s schema: %v", s.Type, name, err)
			}
		}
	}
	for _, n := range NewRegistry().Names() {
		if !have[n] {
			t.Errorf("no schema for %s", n)
		}
	}
	if len(have) != len(NewRegistry().Names()) {
		t.Errorf("%d schemas for %d types", len(have), len(NewRegistry().Names()))
	}
}
