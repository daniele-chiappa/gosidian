package mcp

import "testing"

func TestAnchorSlug(t *testing.T) {
	cases := map[string]string{
		"plancia/agents/frontend-engineer.md": "frontend-engineer",
		"x.md":                                "x",
		"a/b/c.md":                            "c",
		"p/agents/foo.html":                   "foo",
		"p/agents/Bar.MD":                     "Bar",
	}
	for in, want := range cases {
		if got := anchorSlug(in); got != want {
			t.Errorf("anchorSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnchorInputFromNote(t *testing.T) {
	content := []byte("---\n" +
		"title: X\n" +
		"type: agent\n" +
		"description: routing hint\n" +
		"harness:\n" +
		"  tools: [Read, mcp__gosidian__memory_get]\n" +
		"  model: sonnet\n" +
		"tags: [type:agent]\n" +
		"---\n\nbody\n")
	in := anchorInputFromNote("p/agents/x.md", content)
	if in.Slug != "x" || in.Name != "x" {
		t.Errorf("slug/name = %q/%q", in.Slug, in.Name)
	}
	if in.Description != "routing hint" {
		t.Errorf("description = %q", in.Description)
	}
	if in.Model != "sonnet" {
		t.Errorf("model = %q", in.Model)
	}
	if len(in.Tools) != 2 {
		t.Errorf("tools = %v", in.Tools)
	}
	if !in.Materialize {
		t.Error("materialize should default to true")
	}
}

func TestAnchorInputFromNote_ToolsAll(t *testing.T) {
	note := func(tools string) []byte {
		return []byte("---\ntitle: X\ntype: agent\ntags: [type:agent]\nharness:\n  tools: " + tools + "\n---\n\nbody\n")
	}
	cases := []struct {
		name      string
		tools     string
		wantAll   bool
		wantCount int
	}{
		{"bare all", "all", true, 0},
		{"quoted all", "\"all\"", true, 0},
		{"case-insensitive", "ALL", true, 0},
		{"explicit list", "[Read, Edit]", false, 2},
		{"unknown scalar ignored", "everything", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := anchorInputFromNote("p/agents/x.md", note(tc.tools))
			if in.ToolsAll != tc.wantAll {
				t.Errorf("ToolsAll = %v, want %v", in.ToolsAll, tc.wantAll)
			}
			if len(in.Tools) != tc.wantCount {
				t.Errorf("tools = %v, want %d entries", in.Tools, tc.wantCount)
			}
		})
	}
}

func TestAnchorInputFromNote_MaterializeOptOut(t *testing.T) {
	note := func(harness string) []byte {
		return []byte("---\ntitle: X\ntype: agent\ntags: [type:agent]\n" + harness + "---\n\nbody\n")
	}
	cases := []struct {
		name    string
		harness string
		want    bool
	}{
		{"absent", "", true},
		{"no harness materialize", "harness:\n  model: sonnet\n", true},
		{"false", "harness:\n  materialize: false\n", false},
		{"false quoted", "harness:\n  materialize: \"false\"\n", false},
		{"no", "harness:\n  materialize: no\n", false},
		{"true", "harness:\n  materialize: true\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := anchorInputFromNote("p/agents/x.md", note(tc.harness)).Materialize; got != tc.want {
				t.Errorf("materialize = %v, want %v", got, tc.want)
			}
		})
	}
}
