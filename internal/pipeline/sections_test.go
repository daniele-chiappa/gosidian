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

// icon_descriptions (IMP-200): after an icon's label, its description's
// first sentence, the first time the icon is in the note or every time; an
// icon without a label has its description already, an image as before.
func TestInlineMedia_IconDescriptions(t *testing.T) {
	media := map[string]mediaText{
		"/vault-files/p/att/lens.png":  {label: "Ricerca", description: "Una lente d'ingrandimento nera. Apre la ricerca.", side: 40},
		"/vault-files/p/att/nolab.png": {description: "Solo una descrizione.", side: 20},
		"/vault-files/p/att/shot.png":  {label: "Finestra", description: "La griglia. Con i dati.", side: 900},
	}
	body := "Il simbolo ![](/vault-files/p/att/lens.png) e ![](/vault-files/p/att/nolab.png).\n\nDi nuovo ![](/vault-files/p/att/lens.png) qui.\n\n![](/vault-files/p/att/shot.png)"
	lens := "[icona: Ricerca (Una lente d'ingrandimento nera.)]"
	for icons, want := range map[string][2]string{"": {"[icona: Ricerca]", "[icona: Ricerca]"}, "first": {lens, "[icona: Ricerca]"}, "all": {lens, lens}} {
		got, _, _ := inlineMediaOpt(body, media, italian, 128, icons, false, false)
		w := "Il simbolo " + want[0] + " e [icona: Solo una descrizione.].\n\nDi nuovo " + want[1] + " qui.\n\n[immagine: Finestra] La griglia. Con i dati."
		if got != w {
			t.Errorf("%q:\n%s\nwant:\n%s", icons, got, w)
		}
	}
	for in, want := range map[string]string{
		"Misura 1.5 mm. Poi altro.":         "Misura 1.5 mm.",
		"Senza punto":                       "Senza punto",
		strings.Repeat("parola ", 30) + ".": strings.TrimSuffix(strings.Repeat("parola ", 22), " ") + "…",
	} {
		if got := firstSentence(in, iconShortChars); got != want {
			t.Errorf("first sentence of %q: %q, want %q", in, got, want)
		}
	}
}

func TestNumberedMedia(t *testing.T) {
	media := map[string]mediaText{
		"/vault-files/p/att/icon.png": {label: "Binocolo", side: 16, path: "p/att/icon.png", note: "p/d_img/001.md"},
		"/vault-files/p/att/shot.png": {label: "Finestra", description: "La griglia.", side: 900, path: "p/att/shot.png", note: "p/d_img/002.md"},
		"/vault-files/p/att/wide.png": {label: "Barra", description: "Comandi.", side: 700, path: "p/att/wide.png", note: "p/d_img/003.md"},
	}
	body := strings.Join([]string{
		"Premere ![](/vault-files/p/att/icon.png) per cercare.",
		"![](/vault-files/p/att/shot.png)",
		"La barra ![](/vault-files/p/att/wide.png) in alto.",
		"![Schema](/vault-files/p/att/other.png)",
		"![](https://example.com/x.png)",
		"![](/vault-files/p/att/shot.png)",
	}, "\n")
	got, st, refs := inlineMediaOpt(body, media, italian, 128, "", false, true)
	want := strings.Join([]string{
		"Premere [icona: Binocolo] per cercare.",
		"",
		"[immagine 1: Finestra] La griglia.",
		"",
		"La barra [immagine 2: Barra (Comandi.)] in alto.",
		"",
		"[immagine 3: Schema]",
		"",
		"[immagine 4]",
		"",
		"[immagine 5: Finestra] La griglia.",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	// The icons have no number; an image twice has two; one from outside
	// has no path.
	wantRefs := []MediaRef{{N: 1, Path: "p/att/shot.png", Note: "p/d_img/002.md", Label: "Finestra"},
		{N: 2, Path: "p/att/wide.png", Note: "p/d_img/003.md", Label: "Barra"}, {N: 3, Path: "p/att/other.png"}, {N: 4},
		{N: 5, Path: "p/att/shot.png", Note: "p/d_img/002.md", Label: "Finestra"}}
	if b, w := mustJSON(refs), mustJSON(wantRefs); b != w {
		t.Errorf("refs %s, want %s", b, w)
	}
	if st.Images != 6 || st.Icons != 1 {
		t.Errorf("stats %+v", st)
	}
	// A chunk's images: those whose text it holds, not [immagine 10] for 1.
	refs = append(refs, MediaRef{N: 10})
	if got := chunkMedia("Testo [immagine 10] e [immagine 2: Barra] qui.", "immagine", refs); mustJSON(got) != mustJSON([]MediaRef{refs[1], refs[5]}) {
		t.Errorf("chunk media %s", mustJSON(got))
	}
	if got := chunkMedia("Niente.", "immagine", refs); got == nil || len(got) != 0 {
		t.Errorf("no media: %v", got)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

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

// media_paragraphs: join (IMP-200): an image on a line of its own goes
// with the paragraph before it, or the images opening a section with the
// paragraph after them, up to maxChars; past that it starts a paragraph,
// which the images after it join.
func TestJoinMedia(t *testing.T) {
	img := "[immagine: Pompa] Una pompa vista di lato."
	long := "[immagine: Tavola] " + strings.Repeat("parola ", 60) // 439 characters
	text := strings.Repeat("Testo lungo. ", 8)                    // 104
	cases := []struct {
		name string
		in   []string
		max  int
		want []string
	}{
		{"after its text", []string{"Testo.", img, "Altro testo."}, 6000, []string{"Testo.\n\n" + img, "Altro testo."}},
		{"opening the section", []string{img, "[immagine 2]", "Testo.", "Altro."}, 6000, []string{img + "\n\n[immagine 2]\n\nTesto.", "Altro."}},
		{"several after a text", []string{"Testo.", img, "[immagine 3: Filtro] Il filtro."}, 6000, []string{"Testo.\n\n" + img + "\n\n[immagine 3: Filtro] Il filtro."}},
		{"past max_chars", []string{text, long, img, "Altro."}, 500, []string{text, long + "\n\n" + img, "Altro."}},
		{"images only", []string{img, img}, 6000, []string{img + "\n\n" + img}},
		{"no images", []string{"Uno.", "Due."}, 6000, []string{"Uno.", "Due."}},
	}
	for _, c := range cases {
		if got := joinMedia(c.in, "immagine", c.max); strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	for block, want := range map[string]bool{
		img:                            true,
		"[immagine]":                   true,
		"[immagine 12: Leva] La leva.": true,
		`<img class="gosidian-cut-thumb" src="/vault-files/p/attachments/x.png" alt="">[immagine: X] Y`: true,
		`<span class="gosidian-cut-missing">[immagine]</span>`:                                          true,
		"[immagine: X] Y\nuna seconda riga":                                                             false,
		"Premere [immagine: X] per avviare.":                                                            false,
		"[immagini: X] Y":                                                                               false,
		"[icona: Binocolo]":                                                                             false,
		"[immagine 3a: X]":                                                                              false,
	} {
		if got := mediaParagraph(block, "immagine"); got != want {
			t.Errorf("mediaParagraph(%q) = %v", block, got)
		}
	}
}

// With media_paragraphs: join a long section keeps its parts: the images
// join their text within a part, never across, so the sections and parts
// of a cut with the rule are those of a cut without it.
func TestCutNote_JoinKeepsTheParts(t *testing.T) {
	text := strings.Repeat("Testo del passo. ", 20) // 340 characters
	img := "[immagine: Passo] " + strings.Repeat("descrizione ", 20)
	body := "# Manuale\n\n## Montaggio\n\n" + strings.Join([]string{text, img, text, img, text, img, text}, "\n\n")
	apart := cutNote("p/m.md", "Manuale", body, cutOptions{maxChars: 800, paragraphs: true, context: "heading"})
	joined := cutNote("p/m.md", "Manuale", body, cutOptions{maxChars: 800, paragraphs: true, context: "heading", joinMedia: "immagine"})
	sections := func(ps []piece) (out []string) {
		for _, p := range ps {
			if p.level == levelSection {
				out = append(out, p.base+"|"+p.text)
			}
		}
		return out
	}
	if a, j := sections(apart), sections(joined); len(a) < 2 || strings.Join(a, "\n") != strings.Join(j, "\n") {
		t.Fatalf("the parts changed with join:\n%q\n%q", a, j)
	}
	parents := map[string]bool{}
	for _, p := range joined {
		if p.level == levelSection {
			parents[p.base] = true
			continue
		}
		if !parents[p.parent] {
			t.Errorf("paragraph %q of parent %s", p.body, p.parent)
		}
	}
	if len(joined) >= len(apart) {
		t.Errorf("no paragraph joined: %d pieces, %d apart", len(joined), len(apart))
	}
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

	// With media_numbers: the images numbered, and each chunk's.
	out = run(map[string]any{"path": "docs/stampanti", "media_numbers": true, "media_words": map[string]any{"icon": "icona", "image": "immagine"}})
	nchunks := out["chunks"].([]any)
	if txt := nchunks[0].(map[string]any)["text"].(string); !strings.Contains(txt, "[immagine 1: Elenco delle stampanti] Una griglia") || !strings.Contains(txt, "[icona: Binocolo: ricerca]") {
		t.Errorf("numbered section %q", txt)
	}
	// The output went through JSON: its keys in order.
	wantMedia := `[{"label":"Elenco delle stampanti","n":1,"note":"p/docs/stampanti_img/002.md","path":"p/attachments/shot.png"}]`
	if m := mustJSON(nchunks[0].(map[string]any)["media"]); m != wantMedia {
		t.Errorf("section media %s", m)
	}
	if m := mustJSON(nchunks[1].(map[string]any)["media"]); m != "[]" {
		t.Errorf("first paragraph media %s", m)
	}
	if m := mustJSON(nchunks[2].(map[string]any)["media"]); m != wantMedia {
		t.Errorf("second paragraph media %s", m)
	}
	if _, err := noteSections(ctx, env, map[string]any{"path": "docs/stampanti", "media": "keep", "media_numbers": true}); err == nil {
		t.Error("media_numbers with media: keep: no error")
	}
	// Without media_numbers, no media in a chunk.
	if _, ok := chunks[0].(map[string]any)["media"]; ok {
		t.Errorf("media without media_numbers: %v", chunks[0])
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
	// media_paragraphs: join: the screenshot goes with the sentence before
	// it; the section and its hash stay as they were.
	out = run(map[string]any{"path": "docs/stampanti.md", "media_paragraphs": "join", "media_words": map[string]any{"icon": "icona", "image": "immagine"}})
	joined := out["chunks"].([]any)
	if out["paragraphs"] != 2.0 || len(joined) != 3 ||
		joined[1].(map[string]any)["text"] != "## La schermata\n\nPremere [icona: Binocolo: ricerca] per cercare.\n\n[immagine: Elenco delle stampanti] Una griglia con le stampanti." ||
		joined[2].(map[string]any)["text"] != "## La schermata\n\nFine." ||
		joined[0].(map[string]any)["meta"].(map[string]any)["hash"] != meta["hash"] {
		t.Errorf("media_paragraphs join: %v", joined)
	}
	// icon_descriptions: first: the icon's description after its label, in
	// the section and in its paragraph.
	out = run(map[string]any{"path": "docs/stampanti.md", "icon_descriptions": "first", "media_words": map[string]any{"icon": "icona", "image": "immagine"}})
	described := out["chunks"].([]any)
	if !strings.Contains(described[0].(map[string]any)["text"].(string), "[icona: Binocolo: ricerca (Apre la ricerca.)] per cercare.") ||
		described[1].(map[string]any)["text"] != "## La schermata\n\nPremere [icona: Binocolo: ricerca (Apre la ricerca.)] per cercare." {
		t.Errorf("icon_descriptions first: %v", described)
	}
	for name, with := range map[string]map[string]any{
		"icon_descriptions":      {"path": "docs/stampanti.md", "icon_descriptions": "some"},
		"icon_descriptions keep": {"path": "docs/stampanti.md", "icon_descriptions": "all", "media": "keep"},
		"media_paragraphs":       {"path": "docs/stampanti.md", "media_paragraphs": "drop"},
		"media_paragraphs keep":  {"path": "docs/stampanti.md", "media_paragraphs": "join", "media": "keep"},
		"levels":                 {"path": "docs/stampanti.md", "levels": []any{"page"}},
		"context":                {"path": "docs/stampanti.md", "context": "all"},
		"max_chars":              {"path": "docs/stampanti.md", "max_chars": 10},
		"version":                {"path": "docs/stampanti.md", "version": "a b"},
		"media":                  {"path": "docs/stampanti.md", "media": "drop"},
		"words":                  {"path": "docs/stampanti.md", "media_words": map[string]any{"icon": "[x]"}},
		"outside":                {"path": "../q/x.md"},
		"missing":                {"path": "docs/nessuna.md"},
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
