package v1

import "testing"

// The hover excerpt drops the frontmatter as the indexer reads it, and
// nothing that only looks like a fence (IMP-160, S2-14).
func TestStripLeadingFrontmatter(t *testing.T) {
	cases := map[string]string{
		"---\ntitle: x\n---\n\nbody\n":        "body\n",
		"---\r\ntitle: x\r\n---\r\nbody\r\n":  "body\r\n",
		"---\ntitle: x\n----\nbody\n":         "---\ntitle: x\n----\nbody\n",
		"---\nintro\n--- not a fence\nmore\n": "---\nintro\n--- not a fence\nmore\n",
		"no frontmatter\n---\nrule\n":         "no frontmatter\n---\nrule\n",
		"":                                    "",
	}
	for in, want := range cases {
		if got := stripLeadingFrontmatter(in); got != want {
			t.Errorf("stripLeadingFrontmatter(%q) = %q, want %q", in, got, want)
		}
	}
}
