package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

var italian = MediaWords{Icon: "icona", Image: "immagine"}

func TestInlineMedia(t *testing.T) {
	media := map[string]mediaText{
		"/vault-files/p/att/icon.png":  {label: "Binocolo: ricerca", description: "Apre la ricerca.", side: 16},
		"/vault-files/p/att/shot.png":  {label: "Finestra principale", description: "La finestra mostra la griglia.", side: 900},
		"/vault-files/p/att/wide.png":  {label: "Barra", description: "Una barra di comandi.", side: 700},
		"/vault-files/p/att/nolab.png": {description: "Solo una descrizione.", side: 20},
	}
	body := strings.Join([]string{
		"Premere il bottone ![](/vault-files/p/att/icon.png) per cercare.",
		"![Immagine che contiene testo, schermata Descrizione generata automaticamente](/vault-files/p/att/shot.png)",
		"Dopo la schermata.",
		"La barra ![x](/vault-files/p/att/wide.png) in alto, e ![Immagine che contiene testo Descrizione generata automaticamente](/vault-files/p/att/none.png) qui.",
		"",
		"![Schema](/vault-files/p/att/other.png)",
		"",
		"In `![](/vault-files/p/att/icon.png)` il codice resta, ![](/vault-files/p/att/nolab.png) no.",
		"> ![](/vault-files/p/att/shot.png)",
		"- ![](/vault-files/p/att/icon.png)",
		"```",
		"![](/vault-files/p/att/icon.png)",
		"```",
	}, "\n")
	got, st := inlineMedia(body, media, italian, 128)
	want := strings.Join([]string{
		"Premere il bottone [icona: Binocolo: ricerca] per cercare.",
		"",
		"[immagine: Finestra principale] La finestra mostra la griglia.",
		"",
		"Dopo la schermata.",
		"La barra [immagine: Barra (Una barra di comandi.)] in alto, e [icona] qui.",
		"",
		"[immagine: Schema]",
		"",
		"In `![](/vault-files/p/att/icon.png)` il codice resta, [icona: Solo una descrizione.] no.",
		"> [immagine: Finestra principale] La finestra mostra la griglia.",
		"- [immagine: Binocolo: ricerca] Apre la ricerca.",
		"```",
		"![](/vault-files/p/att/icon.png)",
		"```",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if st != (MediaStats{Images: 8, Icons: 3, Described: 6, Missing: 2, WordAlt: 1}) {
		t.Errorf("stats %+v", st)
	}
	// inline_max_side 0: any image in a sentence is an icon.
	got, _ = inlineMedia("La barra ![](/vault-files/p/att/wide.png) in alto.", media, DefaultMediaWords, 0)
	if got != "La barra [icon: Barra] in alto." {
		t.Errorf("max side 0: %q", got)
	}
}

const cutDoc = `# Manuale

Introduzione al manuale.

## Avvio

Primo paragrafo dell'avvio.

Secondo paragrafo, con un elenco:
- uno
- due

### Requisiti

` + "```sh\n# not a heading\n\nmake run\n```" + `

## Vuota

### Nota

Una nota.

### Nota

Un'altra nota con lo stesso titolo.
`

func TestCutNote(t *testing.T) {
	pieces := cutNote("p/docs/manuale.md", "Manuale", cutDoc, cutOptions{maxChars: 6000, paragraphs: true, context: "heading"})
	type row struct {
		level           int
		section, parent string
		text            string
	}
	var got []row
	byBase := map[string]piece{}
	for _, p := range pieces {
		got = append(got, row{p.level, p.section, p.parent, p.text})
		if _, dup := byBase[p.base]; dup {
			t.Errorf("the base %s twice", p.base)
		}
		byBase[p.base] = p
	}
	wantTexts := []row{
		{1, "", "", "# Manuale\n\nIntroduzione al manuale."},
		{2, "", "x", "# Manuale\n\nIntroduzione al manuale."},
		{1, "Avvio", "", "## Avvio\n\nPrimo paragrafo dell'avvio.\n\nSecondo paragrafo, con un elenco:\n- uno\n- due"},
		{2, "Avvio", "x", "## Avvio\n\nPrimo paragrafo dell'avvio."},
		{2, "Avvio", "x", "## Avvio\n\nSecondo paragrafo, con un elenco:\n- uno\n- due"},
		{1, "Avvio > Requisiti", "", "### Requisiti\n\n```sh\n# not a heading\n\nmake run\n```"},
		{2, "Avvio > Requisiti", "x", "### Requisiti\n\n```sh\n# not a heading\n\nmake run\n```"},
		{1, "Vuota > Nota", "", "### Nota\n\nUna nota."},
		{2, "Vuota > Nota", "x", "### Nota\n\nUna nota."},
		{1, "Vuota > Nota", "", "### Nota\n\nUn'altra nota con lo stesso titolo."},
		{2, "Vuota > Nota", "x", "### Nota\n\nUn'altra nota con lo stesso titolo."},
	}
	if len(got) != len(wantTexts) {
		t.Fatalf("%d pieces, want %d: %+v", len(got), len(wantTexts), got)
	}
	for i, w := range wantTexts {
		g := got[i]
		if g.level != w.level || g.section != w.section || g.text != w.text || (w.parent == "x") != (g.parent != "") {
			t.Errorf("piece %d: %+v, want %+v", i, g, w)
		}
	}
	// A paragraph's parent is its section; the title holds the path.
	if p := pieces[4]; p.parent != pieces[2].base || p.title != "Manuale > Avvio" || !strings.HasPrefix(p.base, pieces[2].base+".p") {
		t.Errorf("paragraph %+v", p)
	}

	// The same note with a paragraph changed: the bases stay, the text of
	// that section only changes.
	changed := cutNote("p/docs/manuale.md", "Manuale", strings.Replace(cutDoc, "Una nota.", "Una nota cambiata.", 1), cutOptions{maxChars: 6000, paragraphs: true, context: "heading"})
	for i := range pieces {
		if changed[i].base != pieces[i].base {
			t.Errorf("piece %d: base %s, was %s", i, changed[i].base, pieces[i].base)
		}
		if same := changed[i].text == pieces[i].text; same == (i == 7 || i == 8) {
			t.Errorf("piece %d: changed %v", i, !same)
		}
	}
	if other := cutNote("p/docs/altro.md", "Manuale", cutDoc, cutOptions{maxChars: 6000, context: "none"}); other[0].base == pieces[0].base || other[0].text != "Introduzione al manuale." {
		t.Errorf("another note: %+v", other[0])
	}
	if p := cutNote("p/docs/manuale.md", "Manuale", cutDoc, cutOptions{maxChars: 6000, context: "path"}); p[1].text != "Manuale > Avvio\n\nPrimo paragrafo dell'avvio.\n\nSecondo paragrafo, con un elenco:\n- uno\n- due" || p[0].text != "Manuale\n\nIntroduzione al manuale." {
		t.Errorf("context path: %q, %q", p[0].text, p[1].text)
	}
}

// A section longer than maxChars is cut in parts at its paragraphs, each
// with its own paragraphs; a paragraph longer than that is a part alone.
func TestCutNote_LongSection(t *testing.T) {
	para := func(c string, n int) string { return strings.Repeat(c, n) }
	body := "## Lunga\n\n" + para("a", 120) + "\n\n" + para("b", 120) + "\n\n" + para("c", 300) + "\n\n" + para("d", 50) + "\n"
	pieces := cutNote("p/l.md", "L", body, cutOptions{maxChars: 250, paragraphs: true, context: "none"})
	var sections []piece
	for _, p := range pieces {
		if p.level == levelSection {
			sections = append(sections, p)
		}
	}
	if len(sections) != 3 || !strings.HasSuffix(sections[0].base, ".k1") || !strings.HasSuffix(sections[2].base, ".k3") {
		t.Fatalf("parts %+v", sections)
	}
	if sections[0].text != para("a", 120)+"\n\n"+para("b", 120) || sections[1].text != para("c", 300) || sections[2].text != para("d", 50) {
		t.Errorf("parts' texts %q", []string{sections[0].text, sections[1].text, sections[2].text})
	}
	if pieces[1].parent != sections[0].base || pieces[2].parent != sections[0].base || pieces[4].parent != sections[1].base {
		t.Errorf("parents %+v", pieces)
	}
}

const sectionsArticle = `---
title: Gestione stampanti
---

# Gestione stampanti

## La schermata

Premere ![](/vault-files/p/attachments/icon.png) per cercare.

![Immagine che contiene testo Descrizione generata automaticamente](/vault-files/p/attachments/shot.png)

Fine.
`

func imageNote(article, media, label, body string, side int) string {
	return "---\ntype: image\nmedia: " + media + "\narticle: \"[[" + strings.TrimSuffix(article, ".md") + "]]\"\nlabel: " + label +
		"\nwidth: " + itoa(side) + "\nheight: " + itoa(side) + "\n---\n\n" + body + "\n"
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func sectionsFixture(t *testing.T) *Env {
	t.Helper()
	e, host := newEngine(t, Config{})
	host.notes["p/docs/stampanti.md"] = sectionsArticle
	host.notes["p/docs/stampanti_img/001.md"] = imageNote("p/docs/stampanti.md", "p/attachments/icon.png", "Binocolo: ricerca", "Apre la ricerca.", 16)
	host.notes["p/docs/stampanti_img/002.md"] = imageNote("p/docs/stampanti.md", "p/attachments/shot.png", "Elenco delle stampanti", "Una griglia con le stampanti.", 900)
	// Of another article, in the same folder: left out.
	host.notes["p/docs/stampanti_img/003.md"] = imageNote("p/docs/altro.md", "p/attachments/icon.png", "Altro", "Altro.", 16)
	return &Env{Project: "p", Engine: e}
}

func TestNoteSections(t *testing.T) {
	env := sectionsFixture(t)
	ctx := context.Background()
	run := func(with map[string]any) map[string]any {
		t.Helper()
		out, err := noteSections(ctx, env, with)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(out)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m
	}
	out := run(map[string]any{"path": "docs/stampanti", "version": "c0ffee12", "media_words": map[string]any{"icon": "icona", "image": "immagine"},
		"meta": map[string]any{"status": "attivo", "dal": 100}})
	if out["doc"] != "p/docs/stampanti.md" || out["title"] != "Gestione stampanti" || out["sections"] != 1.0 || out["paragraphs"] != 3.0 || out["count"] != 4.0 {
		t.Fatalf("output %v", out)
	}
	if m := out["media"].(map[string]any); m["images"] != 2.0 || m["described"] != 2.0 || m["icons"] != 1.0 || m["word_alt"] != 0.0 {
		t.Errorf("media %v", m)
	}
	chunks := out["chunks"].([]any)
	sec := chunks[0].(map[string]any)
	wantText := "## La schermata\n\nPremere [icona: Binocolo: ricerca] per cercare.\n\n[immagine: Elenco delle stampanti] Una griglia con le stampanti.\n\nFine."
	meta := sec["meta"].(map[string]any)
	if sec["text"] != wantText || sec["level"] != 1.0 || sec["title"] != "Gestione stampanti > La schermata" || sec["doc"] != "p/docs/stampanti.md" ||
		!strings.HasSuffix(sec["id"].(string), "@c0ffee12") || meta["status"] != "attivo" || meta["dal"] != 100.0 || meta["section"] != "La schermata" ||
		meta["position"] != 1.0 || meta["base"].(string)+"@c0ffee12" != sec["id"] || len(meta["hash"].(string)) != 16 {
		t.Errorf("section %v", sec)
	}
	para := chunks[2].(map[string]any)
	if para["parent"] != sec["id"] || para["level"] != 2.0 || para["text"] != "## La schermata\n\n[immagine: Elenco delle stampanti] Una griglia con le stampanti." {
		t.Errorf("paragraph %v", para)
	}

	// Without the text: what tells which changed, small.
	out = run(map[string]any{"path": "docs/stampanti.md", "version": "c0ffee12", "text": false, "meta": map[string]any{"status": "attivo"},
		"media_words": map[string]any{"icon": "icona", "image": "immagine"}})
	lean := out["chunks"].([]any)[0].(map[string]any)
	if lean["text"] != nil || lean["doc"] != nil || lean["meta"].(map[string]any)["status"] != nil || lean["meta"].(map[string]any)["hash"] != meta["hash"] {
		t.Errorf("without text %v", lean)
	}
	// only: the chunks of some bases, with their text.
	out = run(map[string]any{"path": "docs/stampanti.md", "only": []any{para["meta"].(map[string]any)["base"]}})
	if out["count"] != 1.0 || out["sections"] != 1.0 || out["chunks"].([]any)[0].(map[string]any)["id"] != para["meta"].(map[string]any)["base"] {
		t.Errorf("only %v", out)
	}
	// Sections only; the images kept as links.
	out = run(map[string]any{"path": "docs/stampanti.md", "levels": []any{"section"}, "media": "keep"})
	if out["count"] != 1.0 || !strings.Contains(out["chunks"].([]any)[0].(map[string]any)["text"].(string), "![](/vault-files/p/attachments/icon.png)") {
		t.Errorf("sections, media keep %v", out)
	}
	// Paragraphs only: the sections are still cut, as their parents and
	// paths, and counted, but not given.
	out = run(map[string]any{"path": "docs/stampanti.md", "levels": []any{"paragraph"}})
	for _, c := range out["chunks"].([]any) {
		ch := c.(map[string]any)
		if ch["level"] != 2.0 || ch["parent"] == nil || ch["meta"].(map[string]any)["section"] == nil {
			t.Errorf("paragraphs only: %v", ch)
		}
	}
	if out["count"] != out["paragraphs"] || out["sections"] != 1.0 {
		t.Errorf("paragraphs only: %d chunks, %v paragraphs, %v sections", len(out["chunks"].([]any)), out["paragraphs"], out["sections"])
	}
	for name, with := range map[string]map[string]any{
		"levels":    {"path": "docs/stampanti.md", "levels": []any{"page"}},
		"context":   {"path": "docs/stampanti.md", "context": "all"},
		"max_chars": {"path": "docs/stampanti.md", "max_chars": 10},
		"version":   {"path": "docs/stampanti.md", "version": "a b"},
		"media":     {"path": "docs/stampanti.md", "media": "drop"},
		"words":     {"path": "docs/stampanti.md", "media_words": map[string]any{"icon": "[x]"}},
		"outside":   {"path": "../q/x.md"},
		"missing":   {"path": "docs/nessuna.md"},
	} {
		if _, err := noteSections(ctx, env, with); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestNoteRead_MediaInline(t *testing.T) {
	env := sectionsFixture(t)
	out, err := noteRead(context.Background(), env, map[string]any{"path": "docs/stampanti.md", "media": "inline"})
	if err != nil {
		t.Fatal(err)
	}
	n := out.(NoteData)
	if !strings.Contains(n.Body, "Premere [icon: Binocolo: ricerca] per cercare.") || !strings.Contains(n.Body, "[image: Elenco delle stampanti] Una griglia") ||
		n.Media == nil || n.Media.Images != 2 || !strings.Contains(string(n.Raw), "![](/vault-files/p/attachments/icon.png)") {
		t.Errorf("note %+v", n)
	}
	out, _ = noteRead(context.Background(), env, map[string]any{"path": "docs/stampanti.md"})
	if n := out.(NoteData); n.Media != nil || !strings.Contains(n.Body, "![](/vault-files/p/attachments/icon.png)") {
		t.Errorf("without media %+v", n)
	}
	if _, err := noteRead(context.Background(), env, map[string]any{"path": "docs/stampanti.md", "media": "all"}); err == nil {
		t.Error("media all: no error")
	}
}
