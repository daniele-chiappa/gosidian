package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadEnvFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "gosidian.env")
	body := "# comment\n\nGOSIDIAN_URL=https://host/mcp\nexport GOSIDIAN_PROJECT=\"work\"\nGOSIDIAN_TOKEN='abc' # inline\nBROKEN\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := readEnvFile(p)
	for k, want := range map[string]string{
		"GOSIDIAN_URL":     "https://host/mcp",
		"GOSIDIAN_PROJECT": "work",
		"GOSIDIAN_TOKEN":   "abc",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	if len(got) != 3 {
		t.Errorf("parsed %v, want 3 keys", got)
	}
	if m := readEnvFile(filepath.Join(t.TempDir(), "missing")); len(m) != 0 {
		t.Errorf("missing file = %v, want empty", m)
	}
}
