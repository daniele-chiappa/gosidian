package parser

import (
	"strings"
	"testing"
)

func TestRenderer_Callout(t *testing.T) {
	r := NewRenderer()
	resolver := ResolverFunc(func(string) string { return "" })

	input := "Prima riga.\n\n> [!warning] Attenzione\n> body line 1\n> body **bold**\n\nDopo.\n"
	out, err := r.Render([]byte(input), resolver)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `class="callout callout-warning"`) {
		t.Errorf("missing callout wrapper: %s", out)
	}
	if !strings.Contains(out, "Attenzione") {
		t.Errorf("missing title: %s", out)
	}
	if !strings.Contains(out, "body line 1") {
		t.Errorf("missing body line: %s", out)
	}
	if !strings.Contains(out, "<strong>bold</strong>") {
		t.Errorf("markdown inside callout not rendered: %s", out)
	}
}

func TestRenderer_CalloutUnknownType(t *testing.T) {
	r := NewRenderer()
	resolver := ResolverFunc(func(string) string { return "" })
	out, err := r.Render([]byte("> [!foobar]\n> text\n"), resolver)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `callout-foobar`) {
		t.Errorf("unknown type should still emit class: %s", out)
	}
}

func TestRenderer_WikiLinkResolved(t *testing.T) {
	r := NewRenderer()
	resolver := ResolverFunc(func(target string) string {
		if target == "Other" {
			return "folder/other.md"
		}
		return ""
	})
	out, err := r.Render([]byte("See [[Other]] here."), resolver)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="/notes/folder/other.md"`) {
		t.Errorf("expected resolved href, got: %s", out)
	}
	if !strings.Contains(out, `class="wikilink"`) {
		t.Errorf("expected wikilink class, got: %s", out)
	}
}

func TestRenderer_WikiLinkUnresolved(t *testing.T) {
	r := NewRenderer()
	resolver := ResolverFunc(func(string) string { return "" })
	out, err := r.Render([]byte("Missing: [[Ghost]]"), resolver)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "unresolved") {
		t.Errorf("expected unresolved class, got: %s", out)
	}
	if !strings.Contains(out, `/notes/new?title=Ghost`) {
		t.Errorf("expected new-note href, got: %s", out)
	}
}

func TestRenderer_WikiLinkAlias(t *testing.T) {
	r := NewRenderer()
	resolver := ResolverFunc(func(string) string { return "other.md" })
	out, err := r.Render([]byte("[[Other|my alias]]"), resolver)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, ">my alias<") {
		t.Errorf("expected alias text, got: %s", out)
	}
}

func TestRenderer_Tag(t *testing.T) {
	r := NewRenderer()
	out, err := r.Render([]byte("Hello #foo world"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="/tags/foo"`) {
		t.Errorf("expected tag link, got: %s", out)
	}
}

// A hex color renders as text, not as a link to a tag (IMP-145).
func TestRenderer_ColorIsNotTag(t *testing.T) {
	out, err := NewRenderer().Render([]byte("Brand #AC1F24 and #brand"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `/tags/AC1F24`) || !strings.Contains(out, "Brand #AC1F24 and") || !strings.Contains(out, `href="/tags/brand"`) {
		t.Errorf("render: %s", out)
	}
}

func TestRenderer_BlockReference(t *testing.T) {
	r := NewRenderer()
	resolver := ResolverFunc(func(target string) string {
		if target == "Other" {
			return "folder/other.md"
		}
		return ""
	})
	out, err := r.Render([]byte("Jump to [[Other#Sub Heading]] please."), resolver)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="/notes/folder/other.md#sub-heading"`) {
		t.Errorf("expected resolved href with anchor, got: %s", out)
	}
}

func TestRenderer_AnchorOnlyReference(t *testing.T) {
	r := NewRenderer()
	out, err := r.Render([]byte("See [[#Local Section]] above."), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="#local-section"`) {
		t.Errorf("expected local anchor, got: %s", out)
	}
}

func TestRenderer_SyntaxHighlighting(t *testing.T) {
	r := NewRenderer()
	src := "```go\nfunc main() { println(\"hi\") }\n```\n"
	out, err := r.Render([]byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	// chroma marks the tokens with classes the SPA colours from the
	// preset's tokens; an inline colour would ignore the theme (IMP-154).
	if !strings.Contains(out, `class="chroma"`) || !strings.Contains(out, `<span class="kd">func</span>`) {
		t.Errorf("expected chroma classes in output, got: %s", out)
	}
	if strings.Contains(out, "style=") {
		t.Errorf("highlighted code carries inline colours: %s", out)
	}
}

func TestRenderer_CodeBlockPreserved(t *testing.T) {
	r := NewRenderer()
	in := "```\n[[NotALink]] #nottag\n```\n"
	out, err := r.Render([]byte(in), ResolverFunc(func(string) string { return "" }))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "wikilink") {
		t.Errorf("wiki-link parsed inside code block: %s", out)
	}
	if strings.Contains(out, `href="/tags/nottag"`) {
		t.Errorf("tag parsed inside code block: %s", out)
	}
	if !strings.Contains(out, "[[NotALink]]") {
		t.Errorf("literal wiki-link text missing: %s", out)
	}
}

// A link to a heading carries the heading as written, so the web UI finds it
// as memory_get_section does: by an ID at its start too (IMP-140).
func TestRenderer_WikiLinkHeading(t *testing.T) {
	r := NewRenderer()
	resolver := ResolverFunc(func(target string) string {
		if target == "Other" {
			return "folder/other.md"
		}
		return ""
	})
	out, err := r.Render([]byte("See [[Other#ADR-010]] and [[#Local part|here]]."), resolver)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`href="/notes/folder/other.md#adr-010" data-heading="ADR-010" data-preview-path="folder/other.md"`,
		`href="#local-part" data-heading="Local part">here</a>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in: %s", want, out)
		}
	}
}
