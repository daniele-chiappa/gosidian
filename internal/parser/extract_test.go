package parser

import (
	"reflect"
	"strings"
	"testing"
)

func TestExtractFrontmatterRaw(t *testing.T) {
	body := []byte("---\ntitle: Hi\ntags: [a, b]\n---\n\nbody text\n")
	raw := ExtractFrontmatterRaw(body)
	if !strings.Contains(raw, "title: Hi") {
		t.Errorf("raw missing title: %q", raw)
	}
	if !strings.Contains(raw, "tags: [a, b]") {
		t.Errorf("raw missing tags: %q", raw)
	}
	if strings.Contains(raw, "---") {
		t.Errorf("raw must not include --- markers: %q", raw)
	}
	if strings.Contains(raw, "body text") {
		t.Errorf("raw must not include body: %q", raw)
	}
	// No frontmatter at all.
	if got := ExtractFrontmatterRaw([]byte("# Just a heading\n")); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestParseFrontmatterFields(t *testing.T) {
	raw := "title: The title\ndescription: one-liner\ntype: plan\nstatus: in-progress\nupdated: 2026-04-15\ntags: [foo, bar]\n"
	got := ParseFrontmatterFields(raw)

	if got["title"] != "The title" {
		t.Errorf("title = %v", got["title"])
	}
	if got["description"] != "one-liner" {
		t.Errorf("description = %v", got["description"])
	}
	if got["type"] != "plan" {
		t.Errorf("type = %v", got["type"])
	}
	if got["status"] != "in-progress" {
		t.Errorf("status = %v", got["status"])
	}
	if got["updated"] != "2026-04-15" {
		t.Errorf("updated = %v", got["updated"])
	}
	tags, ok := got["tags"].([]string)
	if !ok {
		t.Fatalf("tags is %T, want []string", got["tags"])
	}
	if !reflect.DeepEqual(tags, []string{"foo", "bar"}) {
		t.Errorf("tags = %v", tags)
	}

	// Empty input → empty map.
	if len(ParseFrontmatterFields("")) != 0 {
		t.Errorf("empty input should produce empty map")
	}

	// Quoted values should have quotes stripped.
	got = ParseFrontmatterFields(`title: "Quoted title"` + "\n")
	if got["title"] != "Quoted title" {
		t.Errorf("quoted title = %v", got["title"])
	}
}

func TestExtract_WikiLinks(t *testing.T) {
	body := []byte(`# Hello
This links to [[Other]] and [[folder/note|alias]].
Also [[   Spaced   ]].
`)
	links, _, _ := Extract(body)
	want := []WikiLinkRef{
		{Target: "Other"},
		{Target: "folder/note", Alias: "alias"},
		{Target: "Spaced"},
	}
	if !reflect.DeepEqual(links, want) {
		t.Errorf("links mismatch:\ngot  %+v\nwant %+v", links, want)
	}
}

func TestExtract_Tags(t *testing.T) {
	body := []byte(`# Notes
Hello #alpha and #beta/sub here.
Mid-word not#atag.
Line start:
#gamma
`)
	_, tags, _ := Extract(body)
	want := []string{"alpha", "beta/sub", "gamma"}
	if !reflect.DeepEqual(tags, want) {
		t.Errorf("tags mismatch:\ngot  %v\nwant %v", tags, want)
	}
}

func TestExtractHeadings(t *testing.T) {
	body := []byte("---\ntitle: x\n---\n# Top heading\n\nbody\n\n## Sub one\n### Deep\n\n```\n# fake heading inside code\n```\n\n## Sub due\n")
	hs := ExtractHeadings(body)
	if len(hs) != 4 {
		t.Fatalf("expected 4 headings, got %d: %+v", len(hs), hs)
	}
	want := []struct {
		level int
		text  string
		id    string
	}{
		{1, "Top heading", "top-heading"},
		{2, "Sub one", "sub-one"},
		{3, "Deep", "deep"},
		{2, "Sub due", "sub-due"},
	}
	for i, w := range want {
		if hs[i].Level != w.level || hs[i].Text != w.text || hs[i].ID != w.id {
			t.Errorf("h[%d] = %+v, want %+v", i, hs[i], w)
		}
	}
}

func TestExtractSection(t *testing.T) {
	body := []byte(`---
title: x
---
# Top

intro

## Auth & Permessi

content one
more

### Sub of auth
nested

## Other

other section

# Another top
final
`)
	cases := []struct {
		head string
		want []string // substrings expected to be present
		miss []string // substrings expected to be absent
	}{
		{"Auth & Permessi", []string{"## Auth & Permessi", "content one", "### Sub of auth", "nested"}, []string{"## Other", "Another top"}},
		{"Other", []string{"## Other", "other section"}, []string{"Auth & Permessi", "Another top"}},
		{"Top", []string{"# Top", "intro", "## Auth & Permessi", "## Other"}, []string{"Another top"}},
		{"missing", nil, nil},
	}
	for _, c := range cases {
		got := ExtractSection(body, c.head)
		if c.want == nil {
			if got != "" {
				t.Errorf("ExtractSection(%q) should be empty, got %q", c.head, got)
			}
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("ExtractSection(%q) missing %q in:\n%s", c.head, w, got)
			}
		}
		for _, m := range c.miss {
			if strings.Contains(got, m) {
				t.Errorf("ExtractSection(%q) should not include %q in:\n%s", c.head, m, got)
			}
		}
	}
}

// Bug #3: \| in a wikilink target (used inside markdown tables to escape the
// pipe so it doesn't terminate a cell) was being captured as a literal
// backslash, leaving the link unresolvable.
func TestExtract_WikiLinkEscapedPipe(t *testing.T) {
	body := []byte("| [[rc/memory/architecture\\|architecture.md]] | row |\n")
	links, _, _ := Extract(body)
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d: %+v", len(links), links)
	}
	if links[0].Target != "rc/memory/architecture" {
		t.Errorf("target = %q, want rc/memory/architecture", links[0].Target)
	}
	if links[0].Alias != "architecture.md" {
		t.Errorf("alias = %q, want architecture.md", links[0].Alias)
	}
}

// \| is the markdown-table escape for | so [[a\|b]] inside a cell behaves
// the same as [[a|b]] outside one: target=a, alias=b.
func TestExtract_WikiLinkEscapedPipeWithoutTable(t *testing.T) {
	body := []byte("Reference [[note\\|alias text]] only.")
	links, _, _ := Extract(body)
	if len(links) != 1 || links[0].Target != "note" || links[0].Alias != "alias text" {
		t.Errorf("unexpected: %+v", links)
	}
}

func TestExtract_FrontmatterTags_Inline(t *testing.T) {
	body := []byte("---\ntitle: x\ntags: [rc, agent, permissions]\n---\n# Body\nNo inline tags here.\n")
	_, tags, _ := Extract(body)
	want := []string{"rc", "agent", "permissions"}
	if !reflect.DeepEqual(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
}

func TestExtract_FrontmatterTags_BlockList(t *testing.T) {
	body := []byte("---\ntitle: x\ntags:\n  - rc\n  - \"backend\"\n  - 'auth'\n  - \"#docs\"\n---\nbody\n")
	_, tags, _ := Extract(body)
	want := []string{"rc", "backend", "auth", "docs"}
	if !reflect.DeepEqual(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
}

func TestExtract_FrontmatterTags_Merged(t *testing.T) {
	// Frontmatter says rc + auth, body has #fresh + a duplicate #rc.
	body := []byte("---\ntags: [rc, auth]\n---\nBody with #fresh and another #rc tag.\n")
	_, tags, _ := Extract(body)
	want := []string{"rc", "auth", "fresh"}
	if !reflect.DeepEqual(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
}

func TestExtract_FrontmatterTitle(t *testing.T) {
	body := []byte(`---
title: My Title
other: x
---
# Heading
body`)
	_, _, title := Extract(body)
	if title != "My Title" {
		t.Errorf("title = %q, want %q", title, "My Title")
	}
}

func TestExtract_IgnoresCodeBlocks(t *testing.T) {
	body := []byte("Text [[Real]]\n```\n[[Fake]] #faketag\n```\nAnd `[[inlinefake]]` outside.")
	links, tags, _ := Extract(body)
	for _, l := range links {
		if l.Target == "Fake" || l.Target == "inlinefake" {
			t.Errorf("code-block link leaked: %+v", l)
		}
	}
	for _, tag := range tags {
		if tag == "faketag" {
			t.Errorf("code-block tag leaked: %q", tag)
		}
	}
	if len(links) != 1 || links[0].Target != "Real" {
		t.Errorf("expected only [[Real]], got %+v", links)
	}
}
