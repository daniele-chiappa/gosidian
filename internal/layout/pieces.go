package layout

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"

	"github.com/gosidian/gosidian/internal/parser"
)

// All asks Number for every block without a comment.
const All = -1

// ErrPieceChanged says a piece is no longer as it was read: it changed, or
// left the note.
var ErrPieceChanged = errors.New("the piece changed since it was read")

// ErrEmptyPiece is a piece given no text: taking one out of the note is a
// change of the note.
var ErrEmptyPiece = errors.New("a piece needs a text")

// ErrOrder is an order of the pieces that does not name each numbered piece
// of the note once.
var ErrOrder = errors.New("the order must name each numbered piece of the note once")

// Placed is a piece as an order names it: its number, and the hash its text
// had when it was read.
type Placed struct {
	N    int    `json:"n"`
	Hash string `json:"hash"`
}

// Piece is a stretch of a note's text the design can place: from the
// comment that numbers it to the next one, a block unless a comment was
// taken away to join two. The number is its identity: a frame on a page shows
// the piece of that number, whichever text has it now.
type Piece struct {
	// N is its number; 0 for the blocks before the first comment.
	N int `json:"n"`
	// Kind is its first block's: heading, paragraph, image, list, table,
	// code, quote or other.
	Kind string `json:"kind"`
	// Text is its markdown, without the comment.
	Text string `json:"text"`
	// Hash tells a text from another, an image by its file: a frame that shows
	// another is behind.
	Hash string `json:"hash"`
	// Line is the line it starts on, from 1, in the note with its frontmatter.
	Line int `json:"line"`
	// Blocks counts the blocks it holds.
	Blocks int `json:"blocks"`
	// Excluded stays out of the design.
	Excluded bool `json:"excluded,omitempty"`
	// Duplicate has the number of a piece before it, which holds it.
	Duplicate bool `json:"duplicate,omitempty"`
	// Image is the image a piece of a single image shows.
	Image *PieceImage `json:"image,omitempty"`
	// from and to are where Text stands in the note.
	from, to int
}

// PieceImage is an image as the note writes it. Its description is a piece
// of its own in the design: AltHash tells one from another.
type PieceImage struct {
	Src     string `json:"src"`
	Alt     string `json:"alt,omitempty"`
	AltHash string `json:"alt_hash,omitempty"`
	// Path is the attachment it names, for who answers with the file; empty
	// for an image that is no attachment.
	Path string `json:"path,omitempty"`
	// Described is what its image note says of it, which may differ from Alt:
	// the note's text is what goes on the page.
	Described string `json:"described,omitempty"`
}

var blockParser = goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser()

// embedRe is a paragraph that is only an embed, ![[target|alias]].
var embedRe = regexp.MustCompile(`^!\[\[([^\]|\n]+)(?:\|([^\]\n]*))?\]\]$`)

// block is a block at the top of a note's text, by its offsets in the note:
// from the start of its first line to where the next one starts.
type block struct {
	start, end int
	node       ast.Node
}

// blocks are the blocks of content's text, and that text, which their nodes'
// segments are relative to. goldmark gives their bounds: a comment in a fence
// is code, a table's lines are one block.
func blocks(content []byte) ([]block, []byte) {
	head := len(content) - len(parser.BodyAfterFrontmatter(content))
	body := content[head:]
	doc := blockParser.Parse(text.NewReader(body))
	var out []block
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		pos := n.Pos()
		if pos < 0 {
			continue // no place of its own: it stays with the block before
		}
		start := bytes.LastIndexByte(body[:pos], '\n') + 1
		if len(out) > 0 {
			out[len(out)-1].end = head + start
		}
		out = append(out, block{start: head + start, end: len(content), node: n})
	}
	return out, body
}

// mark reads the comment a block is, if it is one that numbers a piece.
func mark(content []byte, b block) (n int, excluded, ok bool) {
	if b.node.Kind() != ast.KindHTMLBlock {
		return 0, false, false
	}
	return parser.PieceMark(strings.TrimRight(string(content[b.start:b.end]), " \t\r\n"))
}

// Pieces are the pieces of a note, in the order of its text.
func Pieces(content []byte) []Piece {
	bs, body := blocks(content)
	var out []Piece
	seen := map[int]bool{}
	var open *Piece
	first, last := 0, 0
	closeOpen := func() {
		if open == nil {
			return
		}
		open.Text = strings.TrimRight(string(content[first:last]), " \t\r\n")
		open.Hash = hashOf(open.Text)
		open.from, open.to = first, first+len(open.Text)
		switch {
		case open.Image != nil && open.Blocks > 1:
			open.Image, open.Kind = nil, "paragraph"
		case open.Image != nil:
			open.Hash = hashOf(open.Image.Src)
			if open.Image.Alt != "" {
				open.Image.AltHash = hashOf(open.Image.Alt)
			}
		}
		open.Line = bytes.Count(content[:first], []byte("\n")) + 1
		out = append(out, *open)
		open = nil
	}
	pending, skip := 0, false
	for _, b := range bs {
		if n, excluded, ok := mark(content, b); ok {
			closeOpen()
			pending, skip = n, excluded
			continue
		}
		if open != nil {
			open.Blocks++
			last = b.end
			continue
		}
		p := Piece{N: pending, Excluded: skip, Blocks: 1, Kind: kindOf(b.node)}
		if pending > 0 {
			p.Duplicate = seen[pending]
			seen[pending] = true
		}
		p.Image = onlyImage(b.node, body, content[b.start:b.end])
		if p.Image != nil {
			p.Kind = "image"
		}
		open, first, last = &p, b.start, b.end
		pending, skip = 0, false
	}
	closeOpen()
	return out
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func kindOf(n ast.Node) string {
	switch n.(type) {
	case *ast.Heading:
		return "heading"
	case *ast.Paragraph, *ast.TextBlock:
		return "paragraph"
	case *ast.List:
		return "list"
	case *east.Table:
		return "table"
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		return "code"
	case *ast.Blockquote:
		return "quote"
	}
	return "other"
}

// onlyImage is the image of a paragraph that holds nothing else: raw is the
// paragraph's source, body the text its node's segments are relative to.
func onlyImage(n ast.Node, body, raw []byte) *PieceImage {
	if _, ok := n.(*ast.Paragraph); !ok {
		return nil
	}
	if m := embedRe.FindStringSubmatch(string(bytes.TrimSpace(raw))); m != nil {
		return &PieceImage{Src: m[1], Alt: m[2]}
	}
	var img *ast.Image
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *ast.Image:
			if img != nil {
				return nil
			}
			img = v
		case *ast.Text:
			if len(bytes.TrimSpace(v.Segment.Value(body))) > 0 {
				return nil
			}
		default:
			return nil
		}
	}
	if img == nil {
		return nil
	}
	var alt strings.Builder
	for c := img.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			alt.Write(t.Segment.Value(body))
		}
	}
	return &PieceImage{Src: string(img.Destination), Alt: alt.String()}
}

// Replace writes text in the place of the piece numbered n with hash hash,
// and leaves the rest of the note as it is. A frame shows a text or an image,
// so an image among the blocks of the piece comes apart from them, each its
// own piece, the first keeping n.
func Replace(content []byte, n int, hash, text string) ([]byte, error) {
	text = strings.TrimSpace(parser.WithoutPieceMarks(text))
	if text == "" {
		return nil, ErrEmptyPiece
	}
	for _, p := range Pieces(content) {
		if p.N == n && p.Hash == hash {
			return imagesApart(slices.Concat(content[:p.from], []byte(text), content[p.to:]), n), nil
		}
	}
	return nil, ErrPieceChanged
}

// imagesApart numbers the blocks of the pieces numbered which when they hold
// an image among others.
func imagesApart(content []byte, which int) []byte {
	bs, body := blocks(content)
	piece, count, image := 0, 0, false
	for _, b := range bs {
		if n, _, ok := mark(content, b); ok {
			piece = n
			continue
		}
		if piece == which {
			count++
			image = image || onlyImage(b.node, body, content[b.start:b.end]) != nil
		}
	}
	if count > 1 && image {
		return Number(content, which)
	}
	return content
}

// Number writes a comment before the blocks that have none, so that each is a
// piece of its own: every one with All, or those of the piece which, the
// blocks after its first; piece 0 is what stands before the first comment. A
// new number comes after the highest in the note: one that left the note may
// still be on a page, and must not come back as another text.
func Number(content []byte, which int) []byte {
	bs, _ := blocks(content)
	high := 0
	for _, b := range bs {
		if n, _, ok := mark(content, b); ok {
			high = max(high, n)
		}
	}
	var at []int
	piece, marked := 0, false
	for _, b := range bs {
		if n, _, ok := mark(content, b); ok {
			piece, marked = n, true
			continue
		}
		if !marked && (which == All || which == piece) {
			at = append(at, b.start)
		}
		marked = false
	}
	if len(at) == 0 {
		return content
	}
	var out bytes.Buffer
	out.Grow(len(content) + 16*len(at))
	from := 0
	for _, off := range at {
		high++
		out.Write(content[from:off])
		out.WriteString("<!-- " + strconv.Itoa(high) + " -->\n")
		from = off
	}
	out.Write(content[from:])
	return out.Bytes()
}

// Add writes text as a piece of its own after the piece numbered after, before
// every piece when after is negative, or at the end of the note when there is
// none, and says its number: the one after the highest, as in Number.
func Add(content []byte, after int, text string) ([]byte, int) {
	bs, _ := blocks(content)
	high, piece := 0, 0
	at := len(content)
	for _, b := range bs {
		if n, _, ok := mark(content, b); ok {
			if after < 0 && high == 0 {
				at = b.start
			}
			high = max(high, n)
			piece = n
			continue
		}
		if after > 0 && piece == after {
			at = b.end
		}
	}
	high++
	block := "<!-- " + strconv.Itoa(high) + " -->\n" + strings.TrimSpace(parser.WithoutPieceMarks(text)) + "\n"
	var out bytes.Buffer
	out.Write(content[:at])
	if at == len(content) {
		if !bytes.HasSuffix(content, []byte("\n")) {
			out.WriteByte('\n')
		}
		if !bytes.HasSuffix(content, []byte("\n\n")) {
			out.WriteByte('\n')
		}
		out.WriteString(block)
	} else {
		out.WriteString(block + "\n")
		out.Write(content[at:])
	}
	return out.Bytes(), high
}

// Reorder writes the numbered pieces of a note in the order given, each with
// its comment and its blocks (IMP-231): their numbers stay theirs, the frames
// that show them too. What stands before the first comment, the frontmatter
// and piece 0, stays first. order names each numbered piece once, with the
// hash it was read with: a piece that changed since is ErrPieceChanged. A
// number written again moves right after the first piece that has it, which
// stays the one its frames show; a comment with no block after it moves with
// the piece before.
func Reorder(content []byte, order []Placed) ([]byte, error) {
	hashes := map[int]string{}
	var now []int
	for _, p := range Pieces(content) {
		if p.N > 0 && !p.Duplicate {
			hashes[p.N] = p.Hash
			now = append(now, p.N)
		}
	}
	if len(order) != len(now) {
		return nil, ErrOrder
	}
	same := true
	named := map[int]bool{}
	for i, o := range order {
		hash, ok := hashes[o.N]
		if !ok || named[o.N] {
			return nil, ErrOrder
		}
		if hash != o.Hash {
			return nil, ErrPieceChanged
		}
		named[o.N] = true
		same = same && now[i] == o.N
	}
	if same {
		return content, nil
	}
	// The stretches of the text from a comment to the next, by number.
	bs, _ := blocks(content)
	head := len(content)
	var marks []int
	stretch := map[int][]byte{}
	last := 0
	for i, b := range bs {
		n, _, ok := mark(content, b)
		if !ok {
			continue
		}
		end := len(content)
		for _, next := range bs[i+1:] {
			if _, _, ok := mark(content, next); ok {
				end = next.start
				break
			}
		}
		if len(marks) == 0 {
			head = b.start
		}
		marks = append(marks, b.start)
		piece := bytes.TrimRight(content[b.start:end], " \t\r\n")
		if _, ok := hashes[n]; !ok {
			n = last // a comment with no block: with the piece before
		}
		if n == 0 {
			head = end
			continue
		}
		if stretch[n] != nil {
			stretch[n] = slices.Concat(stretch[n], []byte("\n\n"), piece)
		} else {
			stretch[n] = piece
		}
		last = n
	}
	nl := "\n"
	if bytes.Contains(content, []byte("\r\n")) {
		nl = "\r\n"
	}
	var out bytes.Buffer
	out.Grow(len(content) + 2*len(order))
	if top := bytes.TrimRight(content[:head], " \t\r\n"); len(top) > 0 {
		out.Write(top)
		out.WriteString(nl + nl)
	}
	for i, o := range order {
		if i > 0 {
			out.WriteString(nl + nl)
		}
		out.Write(stretch[o.N])
	}
	out.WriteString(nl)
	return out.Bytes(), nil
}
