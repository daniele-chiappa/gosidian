package views

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/index"
)

// embedIndex: a template note with a section holding a view of what links
// to `this`, two notes that embed it, a note that links to one of them.
func embedIndex(t *testing.T) (*index.Index, map[string]string) {
	t.Helper()
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	notes := map[string]string{
		"p/templates/blocks.md": "---\ntitle: Blocks\n---\n\n# Blocks\n\n## Links here\n\nWhat links to this note:\n\n```view\nfrom: p/notes\nwhere: [links contains this]\nas: list\n```\n\nNested: \n\n![[p/notes/b]]\n\n## Other\n\nOther text.\n",
		"p/notes/a.md":          "---\ntitle: A\n---\n\n# A\n\n![[p/templates/blocks#Links here]]\n",
		"p/notes/b.md":          "---\ntitle: B\n---\n\n# B\n\nSee [[p/notes/a]].\n",
		"p/notes/c.md":          "---\ntitle: C\n---\n\n# C\n\n![[p/templates/blocks#Nope]]\n\n![[p/notes/c]]\n\n![[p/attachments/x.png]]\n\n```md\n![[p/templates/blocks]]\n```\n\n![[p/none]]\n",
	}
	for p, body := range notes {
		if err := idx.Upsert(index.NoteDoc{Path: p, Title: strings.TrimSuffix(filepath.Base(p), ".md"), Body: body, ModTime: 1, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
	}
	return idx, notes
}

func embedContext(idx *index.Index, notes map[string]string, self string) Context {
	return Context{
		This:    ThisFields(self, nil),
		Resolve: idx.Resolve,
		Load: func(p string) ([]byte, bool) {
			b, ok := notes[p]
			return []byte(b), ok
		},
	}
}

// The embedded section comes with its view computed for the note that
// embeds it: a's section lists what links to a (b), not to the template.
func TestEmbeds_Agent(t *testing.T) {
	idx, notes := embedIndex(t)
	c := embedContext(idx, notes, "p/notes/a.md")
	out, hash := RenderNote([]byte(notes["p/notes/a.md"]), true, c, idx.Query)
	s := string(out)
	for _, want := range []string{
		"![[p/templates/blocks#Links here]]\n<!-- gosidian:embed — included from [[p/templates/blocks#Links here]]",
		"## Links here", "What links to this note:", "[[p/notes/b\\|B]]", "<!-- /gosidian:embed -->",
		"[[p/notes/b]]", // the nested embed is a link
	} {
		if !strings.Contains(s, want) {
			t.Errorf("agent form lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "![[p/notes/b]]") || strings.Contains(s, "## Other") {
		t.Errorf("one level and one section only:\n%s", s)
	}
	if hash == "" {
		t.Error("an embed changes the hash")
	}
	// Read back with the result copied in by mistake: not doubled.
	again, _ := RenderNote(out, true, c, idx.Query)
	if strings.Count(string(again), "gosidian:embed —") != 1 {
		t.Errorf("a copied embed is included again, not doubled:\n%s", again)
	}
	if n := CountEmbeds([]byte(notes["p/notes/a.md"]), c); n != 1 {
		t.Errorf("CountEmbeds = %d", n)
	}
}

// For the web UI the included text sits between two sibling markers, and
// the view it includes is a top-level placeholder with its data.
func TestEmbeds_Web(t *testing.T) {
	idx, notes := embedIndex(t)
	c := embedContext(idx, notes, "p/notes/a.md")
	out, data := RenderNoteData([]byte(notes["p/notes/a.md"]), c, idx.Query)
	s := string(out)
	for _, want := range []string{
		`<div class="gosidian-embed-start" data-embed="p/templates/blocks#Links here">[[p/templates/blocks#Links here|blocks › Links here]]</div>`,
		"\n\n## Links here", `<div class="gosidian-view" data-view="0">`, `<div class="gosidian-embed-end"></div>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("web form lacks %q:\n%s", want, s)
		}
	}
	if len(data) != 1 || data[0].Total != 1 || data[0].Rows[0].Path != "p/notes/b.md" {
		t.Errorf("data = %+v", data)
	}
}

// What is not included: a heading that is not there (a warning), the note
// itself, an image, a fence, a note that does not exist.
func TestEmbeds_NotIncluded(t *testing.T) {
	idx, notes := embedIndex(t)
	c := embedContext(idx, notes, "p/notes/c.md")
	out, _ := RenderNote([]byte(notes["p/notes/c.md"]), true, c, idx.Query)
	s := string(out)
	if !strings.Contains(s, `⚠️ embed: [[p/templates/blocks]] has no heading "Nope"`) {
		t.Errorf("missing heading:\n%s", s)
	}
	if strings.Count(s, "gosidian:embed —") != 1 {
		t.Errorf("only the template's embed is expanded:\n%s", s)
	}
	for _, kept := range []string{"![[p/notes/c]]", "![[p/attachments/x.png]]", "```md\n![[p/templates/blocks]]\n```", "![[p/none]]"} {
		if !strings.Contains(s, kept) {
			t.Errorf("%q should stay as written:\n%s", kept, s)
		}
	}
	if out, _ := ExpandEmbeds([]byte("![[p/templates/blocks]]"), true, Context{}); string(out) != "![[p/templates/blocks]]" {
		t.Errorf("without Load embeds stay links: %q", out)
	}
}
