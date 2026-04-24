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
	// chroma emits <span> wrappers with inline style for the dark theme.
	if !strings.Contains(out, "<span") || !strings.Contains(out, "style=") {
		t.Errorf("expected highlighted spans in output, got: %s", out)
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
