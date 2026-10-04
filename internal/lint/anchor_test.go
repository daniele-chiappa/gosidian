package lint

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// A link to a heading that is not there is reported; one to a heading by
// its text, its ID alone, its anchor id or a block reference is not.
func TestBrokenAnchor(t *testing.T) {
	l, v, idx := newTestLinter(t)
	seed(t, v, idx, "p/target.md", "---\ntitle: T\ntags: [p]\n---\n# T\n\n## ADR-010 — Decide\n\n## Plain heading\n\n## IMP-1 (a)\n\n## IMP-1 (b)\n")
	seed(t, v, idx, "p/src.md", "---\ntitle: S\ntags: [p]\n---\n# S\n\n## Local\n\n"+
		"[[p/target#ADR-010]] [[p/target#plain heading]] [[p/target#adr-010-decide]] [[p/target#^block]] [[#Local]]\n"+
		"[[p/target#Gone]] [[p/target#IMP-1]] [[#Missing]] [[p/nowhere#X]]\n")
	issues, err := l.Run(context.Background(), "p", []string{"broken-anchor"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, is := range issues {
		got = append(got, is.Message)
	}
	want := []string{
		`wikilink "p/target#Gone": p/target.md has no heading "Gone"`,
		`wikilink "p/target#IMP-1": p/target.md has no heading "IMP-1"`, // two headings start so
		`wikilink "#Missing": p/src.md has no heading "Missing"`,
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("issues:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A frontmatter that is not valid YAML is reported with the note's line.
func TestFrontmatterInvalidYAML(t *testing.T) {
	l, v, idx := newTestLinter(t)
	seed(t, v, idx, "p/ok.md", "---\ntitle: \"Plan: one\"\ntags: [p]\n---\n# ok\n")
	seed(t, v, idx, "p/bad.md", "---\ntitle: bad\ndescription: Plan: one\ntags: [p]\n---\n# bad\n")
	seed(t, v, idx, "p/none.md", "# no frontmatter\n")
	issues, err := l.Run(context.Background(), "p", []string{"frontmatter-invalid-yaml"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].File != "p/bad.md" || !strings.Contains(issues[0].Message, "line 3 of the note") {
		t.Errorf("issues = %+v", issues)
	}
}
