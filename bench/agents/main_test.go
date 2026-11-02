package main

import (
	"reflect"
	"testing"
)

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

func TestScoreReject(t *testing.T) {
	q := question{Accept: []string{`IMP-0*36\b`, `IMP-0*125\b`}, Reject: []string{`IMP-0*15\b`}}
	if ok, missing := score(q, "IMP-036 and IMP-125."); !ok || missing != nil {
		t.Errorf("both wanted entries: got %v %v", ok, missing)
	}
	ok, missing := score(q, "IMP-036 and IMP-125; IMP-015 is in progress, so not listed.")
	if !ok || !reflect.DeepEqual(missing, []string{`check: IMP-0*15\b`}) || !toCheck(missing) {
		t.Errorf("a near miss named: got %v %v, want correct and flagged", ok, missing)
	}
	if ok, missing := score(q, "IMP-015."); ok || toCheck(missing) && len(missing) != 3 {
		t.Errorf("the wanted entries missing: got %v %v", ok, missing)
	}
}
