package layout

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const piecesNote = `---
title: Rivista
---

<!-- 1 -->
## Introduzione

<!-- 2 -->
Il primo paragrafo.

<!-- 3 -->
![Schema dell'impianto](attachments/ab12.png)

<!-- 4 escluso -->
Un appunto che resta nell'MD.

<!-- 5 -->
- una voce
- un'altra

<!-- 6 -->
| a | b |
|---|---|
| 1 | 2 |

<!-- 7 -->
` + "```go\n<!-- 8 -->\nfunc main() {}\n```" + `

<!-- 9 -->
> [!note] Avviso
> Una citazione.
`

func TestPieces(t *testing.T) {
	got := Pieces([]byte(piecesNote))
	want := []struct {
		n        int
		kind     string
		excluded bool
		text     string
		line     int
	}{
		{1, "heading", false, "## Introduzione", 6},
		{2, "paragraph", false, "Il primo paragrafo.", 9},
		{3, "image", false, "![Schema dell'impianto](attachments/ab12.png)", 12},
		{4, "paragraph", true, "Un appunto che resta nell'MD.", 15},
		{5, "list", false, "- una voce\n- un'altra", 18},
		{6, "table", false, "| a | b |\n|---|---|\n| 1 | 2 |", 22},
		{7, "code", false, "```go\n<!-- 8 -->\nfunc main() {}\n```", 27},
		{9, "quote", false, "> [!note] Avviso\n> Una citazione.", 33},
	}
	if len(got) != len(want) {
		t.Fatalf("%d pieces, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		p := got[i]
		if p.N != w.n || p.Kind != w.kind || p.Excluded != w.excluded || p.Text != w.text || p.Line != w.line {
			t.Errorf("piece %d: %+v, want %+v", i, p, w)
		}
		if len(p.Hash) != 16 {
			t.Errorf("piece %d: hash %q", i, p.Hash)
		}
	}
	img := got[2].Image
	if img == nil || img.Src != "attachments/ab12.png" || img.Alt != "Schema dell'impianto" {
		t.Errorf("the image: %+v", img)
	}
}

// A piece runs to the next comment: deleting a comment joins its block to
// the piece before.
func TestPieces_Joined(t *testing.T) {
	got := Pieces([]byte("<!-- 1 -->\nUno.\n\nDue.\n\n- tre\n\n<!-- 2 -->\nQuattro.\n"))
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if got[0].Text != "Uno.\n\nDue.\n\n- tre" || got[0].Blocks != 3 || got[0].Kind != "paragraph" {
		t.Errorf("joined: %+v", got[0])
	}
	if got[1].Blocks != 1 {
		t.Errorf("alone: %+v", got[1])
	}
}

func TestPieces_Unnumbered(t *testing.T) {
	got := Pieces([]byte("# Prima\n\nTesto.\n\n<!-- 4 -->\nDopo.\n"))
	if len(got) != 2 || got[0].N != 0 || got[0].Blocks != 2 || got[1].N != 4 {
		t.Fatalf("%+v", got)
	}
	if got := Pieces([]byte("---\ntitle: T\n---\n")); len(got) != 0 {
		t.Errorf("no text, no pieces: %+v", got)
	}
}

func TestPieces_Comments(t *testing.T) {
	got := Pieces([]byte("<!-- 1 -->\nUno.\n\n<!-- una nota -->\n\nDue.\n\n<!-- 2 -->\n<!-- 3 -->\nTre.\n\n<!-- 4 -->\n"))
	if len(got) != 2 || got[0].N != 1 || got[1].N != 3 {
		t.Fatalf("a comment is a mark only with a number, and a mark needs a block: %+v", got)
	}
	if !strings.Contains(got[0].Text, "<!-- una nota -->") || got[0].Blocks != 3 {
		t.Errorf("another comment stays in its piece: %+v", got[0])
	}
}

func TestPieces_Duplicates(t *testing.T) {
	got := Pieces([]byte("<!-- 2 -->\nUno.\n\n<!-- 2 -->\nDue.\n"))
	if len(got) != 2 || got[0].Duplicate || !got[1].Duplicate {
		t.Fatalf("the first one holds: %+v", got)
	}
}

// The hash is of the text: the panel compares it with what was placed.
func TestPieces_Hash(t *testing.T) {
	a := Pieces([]byte("<!-- 1 -->\nUno.\n"))[0]
	b := Pieces([]byte("<!-- 7 -->\nUno.\n"))[0]
	c := Pieces([]byte("<!-- 1 -->\nDue.\n"))[0]
	if a.Hash != b.Hash || a.Hash == c.Hash {
		t.Errorf("%s %s %s", a.Hash, b.Hash, c.Hash)
	}
}

// An image's hash is of its file: the description is a piece of its own, with
// a hash of its own, and changing it leaves the picture as it is.
func TestPieces_ImageHashes(t *testing.T) {
	a := Pieces([]byte("<!-- 1 -->\n![Una foto](a.png)\n"))[0]
	b := Pieces([]byte("<!-- 1 -->\n![Un'altra foto](a.png)\n"))[0]
	if a.Hash != b.Hash || a.Image.AltHash == b.Image.AltHash || a.Image.AltHash == "" {
		t.Errorf("%+v %+v", a, b)
	}
}

func TestPieces_Images(t *testing.T) {
	got := Pieces([]byte("<!-- 1 -->\n![[foto.png|La foto]]\n\n<!-- 2 -->\nUna ![foto](a.png) nel testo.\n\n<!-- 3 -->\n![](/vault-files/r/attachments/a.png)\n"))
	if got[0].Kind != "image" || got[0].Image == nil || got[0].Image.Src != "foto.png" || got[0].Image.Alt != "La foto" {
		t.Errorf("an embed: %+v", got[0])
	}
	if got[1].Kind != "paragraph" || got[1].Image != nil {
		t.Errorf("an image in a sentence: %+v", got[1])
	}
	if got[2].Kind != "image" || got[2].Image.Src != "/vault-files/r/attachments/a.png" || got[2].Image.Alt != "" {
		t.Errorf("no alt: %+v", got[2])
	}
}

// An image joined to the text after it is no image piece: the piece is text.
func TestPieces_ImageJoined(t *testing.T) {
	got := Pieces([]byte("<!-- 1 -->\n![foto](a.png)\n\nLa didascalia.\n"))
	if len(got) != 1 || got[0].Image != nil || got[0].Kind != "paragraph" || got[0].Blocks != 2 {
		t.Errorf("%+v", got)
	}
}

func TestPieces_CRLF(t *testing.T) {
	got := Pieces([]byte("<!-- 1 -->\r\nUno.\r\n\r\n<!-- 2 -->\r\nDue.\r\n"))
	if len(got) != 2 || got[0].Text != "Uno." || got[1].Text != "Due." {
		t.Fatalf("%+v", got)
	}
}

func TestAdd(t *testing.T) {
	note := "---\ntitle: T\n---\n\n<!-- 1 -->\nUno.\n\n<!-- 5 -->\nCinque.\n"
	got, n := Add([]byte(note), 1, "Nuovo.")
	if n != 6 || string(got) != "---\ntitle: T\n---\n\n<!-- 1 -->\nUno.\n\n<!-- 6 -->\nNuovo.\n\n<!-- 5 -->\nCinque.\n" {
		t.Errorf("after a piece: %d\n%s", n, got)
	}
	got, n = Add([]byte(note), -1, "Prima.")
	if n != 6 || string(got) != "---\ntitle: T\n---\n\n<!-- 6 -->\nPrima.\n\n<!-- 1 -->\nUno.\n\n<!-- 5 -->\nCinque.\n" {
		t.Errorf("before every piece: %d\n%s", n, got)
	}
	got, n = Add([]byte(note), 0, "In fondo.")
	if n != 6 || string(got) != note+"\n<!-- 6 -->\nIn fondo.\n" {
		t.Errorf("at the end: %d\n%s", n, got)
	}
	got, _ = Add([]byte("<!-- 1 -->\nUno."), 0, "Due.")
	if string(got) != "<!-- 1 -->\nUno.\n\n<!-- 2 -->\nDue.\n" {
		t.Errorf("a note without its last newline:\n%s", got)
	}
	got, _ = Add([]byte("<!-- 1 -->\nUno.\n"), 77, "Due.")
	if string(got) != "<!-- 1 -->\nUno.\n\n<!-- 2 -->\nDue.\n" {
		t.Errorf("after a piece that is not there, the end:\n%s", got)
	}
	got, n = Add([]byte("---\ntitle: T\n---\n"), 0, "Primo.")
	if n != 1 || string(got) != "---\ntitle: T\n---\n\n<!-- 1 -->\nPrimo.\n" {
		t.Errorf("an empty note: %d\n%s", n, got)
	}
	got, _ = Add([]byte("<!-- 1 -->\nUno.\n"), 0, "Riga.\n<!-- 1 -->\nAltra.\n")
	if strings.Count(string(got), "<!-- 1 -->") != 1 || len(Pieces(got)) != 2 {
		t.Errorf("a comment in the text is no number:\n%s", got)
	}
}

func TestNumber_All(t *testing.T) {
	note := "---\ntitle: T\n---\n\n## Titolo\n\nUn paragrafo.\n\n- una voce\n\n![foto](a.png)\n"
	got := string(Number([]byte(note), All))
	want := "---\ntitle: T\n---\n\n<!-- 1 -->\n## Titolo\n\n<!-- 2 -->\nUn paragrafo.\n\n<!-- 3 -->\n- una voce\n\n<!-- 4 -->\n![foto](a.png)\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if again := string(Number([]byte(got), All)); again != got {
		t.Errorf("numbered twice:\n%s", again)
	}
}

// What is numbered keeps its number, and a new one comes after the highest:
// a number that left the note may still be on a page.
func TestNumber_Keeps(t *testing.T) {
	got := string(Number([]byte("Prima.\n\n<!-- 7 -->\nUno.\n\n<!-- 2 escluso -->\nDue.\n"), All))
	want := "<!-- 8 -->\nPrima.\n\n<!-- 7 -->\nUno.\n\n<!-- 2 escluso -->\nDue.\n"
	if got != want {
		t.Errorf("got:\n%s", got)
	}
}

// A piece of joined blocks comes apart, the others stay joined.
func TestNumber_Piece(t *testing.T) {
	note := "<!-- 1 -->\nUno.\n\nDue.\n\n<!-- 2 -->\nTre.\n\nQuattro.\n"
	got := string(Number([]byte(note), 1))
	want := "<!-- 1 -->\nUno.\n\n<!-- 3 -->\nDue.\n\n<!-- 2 -->\nTre.\n\nQuattro.\n"
	if got != want {
		t.Errorf("got:\n%s", got)
	}
	head := string(Number([]byte("Prima.\n\nSeconda.\n\n<!-- 4 -->\nUno.\n\nDue.\n"), 0))
	if head != "<!-- 5 -->\nPrima.\n\n<!-- 6 -->\nSeconda.\n\n<!-- 4 -->\nUno.\n\nDue.\n" {
		t.Errorf("the blocks before the first comment:\n%s", head)
	}
}

const editNote = "---\ntitle: T\n---\n\n<!-- 1 -->\nUno.\n\n<!-- 2 -->\nDue.\n\nAncora due.\n\n<!-- 3 -->\n![foto](a.png)\n"

func pieceOf(t *testing.T, content []byte, n int) Piece {
	t.Helper()
	for _, p := range Pieces(content) {
		if p.N == n {
			return p
		}
	}
	t.Fatalf("no piece %d in:\n%s", n, content)
	return Piece{}
}

// The text of a piece goes in its place, the rest of the note stays as it
// was, and the comment that numbers it is not the text's to change (IMP-228).
func TestReplace(t *testing.T) {
	note := []byte(editNote)
	got, err := Replace(note, 1, pieceOf(t, note, 1).Hash, "  Primo, riscritto.\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(editNote, "Uno.", "Primo, riscritto.", 1); string(got) != want {
		t.Errorf("got:\n%s", got)
	}
	got, err = Replace(note, 2, pieceOf(t, note, 2).Hash, "Solo due.")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(editNote, "Due.\n\nAncora due.", "Solo due.", 1); string(got) != want {
		t.Errorf("joined blocks:\n%s", got)
	}
	got, err = Replace(note, 3, pieceOf(t, note, 3).Hash, "![altra](b.png)")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(got), "<!-- 3 -->\n![altra](b.png)\n") {
		t.Errorf("an image:\n%s", got)
	}
	got, err = Replace(note, 1, pieceOf(t, note, 1).Hash, "<!-- 9 -->\nCon un commento.")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(editNote, "Uno.", "Con un commento.", 1); string(got) != want {
		t.Errorf("a comment in the text:\n%s", got)
	}
}

// Blocks stay joined when they are text: a frame shows them together. An
// image needs a frame of its own, so what it stands among comes apart, the
// first block keeping the number.
func TestReplace_AnImageComesApart(t *testing.T) {
	note := []byte(editNote)
	got, err := Replace(note, 1, pieceOf(t, note, 1).Hash, "Uno.\n\n![nuova](/vault-files/p/attachments/n.png)\n\nDopo.")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(editNote, "Uno.\n", "Uno.\n\n<!-- 4 -->\n![nuova](/vault-files/p/attachments/n.png)\n\n<!-- 5 -->\nDopo.\n", 1)
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if p := pieceOf(t, got, 4); p.Kind != "image" {
		t.Errorf("the image: %+v", p)
	}
	got, err = Replace(note, 1, pieceOf(t, note, 1).Hash, "Uno.\n\nUniti.")
	if err != nil {
		t.Fatal(err)
	}
	if p := pieceOf(t, got, 1); p.Blocks != 2 {
		t.Errorf("text stays joined: %+v", p)
	}
}

// A piece that changed since it was read is not written over, and one with
// the same number is found by its hash.
func TestReplace_Refusals(t *testing.T) {
	note := []byte(editNote)
	for _, c := range []struct {
		name string
		n    int
		hash string
	}{{"another text", 1, "0000"}, {"no such piece", 9, pieceOf(t, note, 1).Hash}} {
		if _, err := Replace(note, c.n, c.hash, "x"); !errors.Is(err, ErrPieceChanged) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if _, err := Replace(note, 1, pieceOf(t, note, 1).Hash, " \n "); !errors.Is(err, ErrEmptyPiece) {
		t.Errorf("empty: %v", err)
	}
	if _, err := Replace(note, 1, pieceOf(t, note, 1).Hash, "<!-- 9 -->\n"); !errors.Is(err, ErrEmptyPiece) {
		t.Errorf("only a comment: %v", err)
	}
	twice := []byte("<!-- 1 -->\nUno.\n\n<!-- 1 -->\nAltro.\n")
	second := Pieces(twice)[1]
	got, err := Replace(twice, 1, second.Hash, "Cambiato.")
	if err != nil || string(got) != "<!-- 1 -->\nUno.\n\n<!-- 1 -->\nCambiato.\n" {
		t.Errorf("the same number twice: %v\n%s", err, got)
	}
}

// The pieces in another order, each with its comment and its blocks; the
// frontmatter and piece 0 stay first, a number written again follows the
// first piece that has it, a comment with nothing after it the piece before
// (IMP-231).
func TestReorder(t *testing.T) {
	note := "---\ntitle: T\n---\n\nIn cima.\n\n<!-- 1 -->\n# Uno\n\n<!-- 4 -->\nQuattro.\n\nAncora quattro.\n\n<!-- 2 -->\nDue.\n"
	placed := func(content string, ns ...int) []Placed {
		hashes := map[int]string{}
		for _, p := range Pieces([]byte(content)) {
			if !p.Duplicate {
				hashes[p.N] = p.Hash
			}
		}
		out := make([]Placed, len(ns))
		for i, n := range ns {
			out[i] = Placed{N: n, Hash: hashes[n]}
		}
		return out
	}
	got, err := Reorder([]byte(note), placed(note, 2, 1, 4))
	want := "---\ntitle: T\n---\n\nIn cima.\n\n<!-- 2 -->\nDue.\n\n<!-- 1 -->\n# Uno\n\n<!-- 4 -->\nQuattro.\n\nAncora quattro.\n"
	if err != nil || string(got) != want {
		t.Fatalf("Reorder = %v\n%s", err, got)
	}
	var ns []int
	for _, p := range Pieces(got) {
		ns = append(ns, p.N)
	}
	if !slices.Equal(ns, []int{0, 2, 1, 4}) {
		t.Errorf("pieces after = %v", ns)
	}
	if same, err := Reorder([]byte(note), placed(note, 1, 4, 2)); err != nil || string(same) != note {
		t.Errorf("the same order leaves the note as it is: %v\n%s", err, same)
	}
	for why, order := range map[string][]Placed{
		"a piece left out":  placed(note, 2, 1),
		"a piece twice":     placed(note, 2, 1, 1),
		"a piece not there": append(placed(note, 2, 1), Placed{N: 9, Hash: "x"}),
	} {
		if _, err := Reorder([]byte(note), order); !errors.Is(err, ErrOrder) {
			t.Errorf("%s: %v", why, err)
		}
	}
	stale := placed(note, 2, 1, 4)
	stale[0].Hash = "old"
	if _, err := Reorder([]byte(note), stale); !errors.Is(err, ErrPieceChanged) {
		t.Errorf("a piece changed since: %v", err)
	}

	again := "<!-- 1 -->\nUno.\n\n<!-- 3 -->\n<!-- 2 -->\nDue.\n\n<!-- 1 -->\nUno di nuovo.\n"
	got, err = Reorder([]byte(again), placed(again, 2, 1))
	want = "<!-- 2 -->\nDue.\n\n<!-- 1 -->\nUno.\n\n<!-- 3 -->\n\n<!-- 1 -->\nUno di nuovo.\n"
	if err != nil || string(got) != want {
		t.Errorf("a number again, an empty comment = %v\n%s", err, got)
	}
	if ps := Pieces(got); len(ps) < 2 || ps[1].N != 1 || ps[1].Text != "Uno." {
		t.Errorf("piece 1 is still the first that has it: %+v", ps)
	}

	crlf := "<!-- 1 -->\r\nUno.\r\n\r\n<!-- 2 -->\r\nDue.\r\n"
	got, err = Reorder([]byte(crlf), placed(crlf, 2, 1))
	if err != nil || string(got) != "<!-- 2 -->\r\nDue.\r\n\r\n<!-- 1 -->\r\nUno.\r\n" {
		t.Errorf("CRLF = %v %q", err, got)
	}
}
