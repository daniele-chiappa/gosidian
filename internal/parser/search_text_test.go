package parser

import "testing"

// A link reads as the words a reader sees: its alias, or the target's file
// name and heading; code stays as written (IMP-120).
func TestSearchText(t *testing.T) {
	for in, want := range map[string]string{
		"See [[gosidian/plans/20261007-x|the plan]].":   "See the plan.",
		"See [[gosidian/plans/20261007-x]].":            "See 20261007-x.",
		"See [[gosidian/docs/bugs#BUG-065]] now":        "See bugs BUG-065 now",
		"Same note: [[#Context]]":                       "Same note: Context",
		"Block: [[proj/note#^abc123]]":                  "Block: note",
		"Embed ![[proj/attachments/fig.png]] here":      "Embed fig.png here",
		"HTML [[proj/report.html]] and [[proj/n.md]]":   "HTML report and n",
		"Escaped [[proj/x\\|label]] in a table":         "Escaped label in a table",
		"Code `[[proj/plans/a]]` and [[proj/plans/b]].": "Code `[[proj/plans/a]]` and b.",
		"Nested [[proj/n#H1#H2]]":                       "Nested n H1 H2",
		"No link here.":                                 "No link here.",
	} {
		if got := SearchText(in); got != want {
			t.Errorf("SearchText(%q) = %q, want %q", in, got, want)
		}
	}
	fenced := "Before [[proj/plans/a]]\n```\n[[proj/plans/b]]\n```\nAfter [[proj/plans/c]]"
	want := "Before a\n```\n[[proj/plans/b]]\n```\nAfter c"
	if got := SearchText(fenced); got != want {
		t.Errorf("fenced:\n%q\nwant\n%q", got, want)
	}
}
