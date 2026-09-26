package main

import "testing"

func TestSplitAnswers(t *testing.T) {
	reply := "Here are the answers.\n\n### L2\nFourteen days.\nSources: a.md\n\n## **T3** — Redis\nADR-004.\nSources: b.md\n\n### X9\nnot asked\n### N4\nNo BUG-031 in the vault."
	parts := splitAnswers(reply, []string{"L2", "T3", "N4", "P1"})
	want := map[string]string{
		"L2": "Fourteen days.\nSources: a.md",
		"T3": "** — Redis\nADR-004.\nSources: b.md",
		"N4": "No BUG-031 in the vault.",
	}
	for id, w := range want {
		if parts[id] != w {
			t.Errorf("%s: got %q, want %q", id, parts[id], w)
		}
	}
	if _, ok := parts["P1"]; ok {
		t.Error("P1 has no header and must get no section")
	}
	if _, ok := parts["X9"]; ok {
		t.Error("X9 was not asked")
	}
}
