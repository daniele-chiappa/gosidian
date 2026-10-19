package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/parser"
)

// note.sections (IMP-170): a note cut by its headings into sections and
// paragraphs, the chunks of an archive of vectors, with its images as text:
// the label and description their image notes hold (IMP-179). The cut
// follows the headings the way in.import does (sectionAt): a line in a
// fenced block is no heading, and a section's path leaves out the title's
// level. Its chunks are what the database plugin's upsert takes: id, text,
// title, doc, parent, level and meta.

const (
	defaultSectionChars = 6000
	minSectionChars     = 200
)

// MediaWords are the words an image's text starts with: "[icon: …]",
// "[image: …]".
type MediaWords struct {
	Icon  string `json:"icon"`
	Image string `json:"image"`
}

// DefaultMediaWords are the words of an image's text when none are given.
var DefaultMediaWords = MediaWords{Icon: "icon", Image: "image"}

// MediaStats counts what putting the images in the text did.
type MediaStats struct {
	// Images are the images of the body, Icons those small inside a
	// sentence.
	Images int `json:"images"`
	Icons  int `json:"icons"`
	// Described had a label or a description in their image note; Missing
	// had neither, and kept their alt text, or a bare mark.
	Described int `json:"described"`
	Missing   int `json:"missing"`
	// WordAlt are alt texts Word wrote by itself, left out.
	WordAlt int `json:"word_alt"`
}

// NoteReader reads the notes of a project and lists a folder: what reading
// a note with its images as text needs of a host.
type NoteReader interface {
	ReadNote(project, path string) (NoteData, error)
	NotesIn(project, folder string) ([]string, error)
}

// mediaText is what an image note says of its image: and the image's path
// in the vault, and the note's.
type mediaText struct {
	label, description string
	side               int
	path, note         string
}

// MediaRef is an image numbered in a note's text, "[image 3: label]":
// its number, from 1 in the note's order, the image's path in the vault
// (none for an image from outside), its image note's path and its label.
type MediaRef struct {
	N     int    `json:"n"`
	Path  string `json:"path,omitempty"`
	Note  string `json:"note,omitempty"`
	Label string `json:"label,omitempty"`
}

// markerRe is a line's text that only marks a block: a quote, a list's
// item. An image with nothing else on its line is on a line of its own.
var markerRe = regexp.MustCompile(`^(>\s*)*([-*+]|\d+[.)])?$`)

// wordAltRe is the alt text Word writes by itself under an image it
// describes ("Immagine che contiene testo, schermata… Descrizione generata
// automaticamente"): noise, left out.
var wordAltRe = regexp.MustCompile(`(?i)descrizione generata automaticamente|description automatically generated|beschreibung automatisch generiert|description générée automatiquement|descripción generada automáticamente`)

// noteMedia reads the image notes of a note, in <note>_img/, by the URL its
// body links each image with.
func noteMedia(host NoteReader, project, note string) (map[string]mediaText, error) {
	paths, err := host.NotesIn(project, mediaFolder(note))
	if err != nil {
		return nil, err
	}
	out := map[string]mediaText{}
	for _, p := range paths {
		n, err := host.ReadNote(project, p)
		if err != nil || !isMediaOf(n.Frontmatter, note) {
			continue
		}
		img, _ := n.Frontmatter["media"].(string)
		if img == "" {
			continue
		}
		label, _ := n.Frontmatter["label"].(string)
		out[attach.FileURL(img)] = mediaText{label: flatText(label), description: flatText(n.Body), side: longestSide(n.Frontmatter), path: img, note: p}
	}
	return out, nil
}

// flatText is s on one line, its runs of spaces one space.
func flatText(s string) string { return strings.Join(strings.Fields(s), " ") }

// DefaultInlineMaxSide is the longest side of an icon, in pixels, when no
// other is given: an image in a sentence up to it is an icon.
const DefaultInlineMaxSide = defaultInlineMaxSide

// InlineNoteMedia is a note's body with its images as text, from its image
// notes: note.read and memory_get with media: inline. inlineMax is the
// longest side of an icon (0: any image in a sentence is one).
func InlineNoteMedia(host NoteReader, project, note, body string, words MediaWords, inlineMax int) (string, MediaStats, error) {
	text, st, _, err := noteMediaText(host, project, note, body, words, inlineMax, "", false)
	return text, st, err
}

// NumberedNoteMedia is InlineNoteMedia with the images numbered (not the
// icons): "[image 3: label]", and the images by their number.
func NumberedNoteMedia(host NoteReader, project, note, body string, words MediaWords, inlineMax int) (string, MediaStats, []MediaRef, error) {
	return noteMediaText(host, project, note, body, words, inlineMax, "", true)
}

// noteMediaText is a note's body with its images as text, the icons
// described as icons says (iconDescriptions), the images numbered with
// number.
func noteMediaText(host NoteReader, project, note, body string, words MediaWords, inlineMax int, icons string, number bool) (string, MediaStats, []MediaRef, error) {
	media, err := noteMedia(host, project, note)
	if err != nil {
		return "", MediaStats{}, nil, err
	}
	text, st, refs := inlineMediaOpt(body, media, words, inlineMax, icons, false, number)
	return text, st, refs, nil
}

// The icons with a short description after their label (note.sections
// icon_descriptions): "[icon: label (short description)]", the first
// sentence of the description, so that a question that says what an icon
// looks like finds the text it stands in. None ("" or none), the first
// time each is in the note, or all of them.
const (
	iconsFirst = "first"
	iconsAll   = "all"
)

// iconShortChars is the longest short description of an icon, in bytes.
const iconShortChars = 160

// iconDescriptions reads with.icon_descriptions: "", first or all.
func iconDescriptions(with map[string]any) (string, error) {
	switch v := str(with, "icon_descriptions"); v {
	case "", "none":
		return "", nil
	case iconsFirst, iconsAll:
		if str(with, "media") == "keep" {
			return "", errors.New("with.icon_descriptions: with media: inline, the images as text")
		}
		return v, nil
	default:
		return "", fmt.Errorf("with.icon_descriptions %q: none, first or all", v)
	}
}

// firstSentence is a description's first sentence, clipped at chars bytes.
func firstSentence(s string, chars int) string {
	for i := 0; i < len(s); i++ {
		if (s[i] == '.' || s[i] == '!' || s[i] == '?') && (i+1 == len(s) || s[i+1] == ' ') {
			s = s[:i+1]
			break
		}
	}
	return clipHead(s, chars)
}

// inlineMedia is a markdown body with its images as text. An icon (an
// image inside a sentence, up to inlineMax pixels on its longest side; any
// size when inlineMax is 0; any image without a note, whose size is not
// known) becomes "[icon: label]" in its sentence; a larger image in a
// sentence "[image: label (description)]"; an image on a line of its own a
// paragraph, "[image: label] description", in a quote or a list's item
// with nothing else on the line after its marks. Without a label or a
// description, the alt text stands for them, unless Word wrote it. Code is
// left as it is.
func inlineMedia(body string, media map[string]mediaText, words MediaWords, inlineMax int) (string, MediaStats) {
	return inlineMediaAs(body, media, words, inlineMax, "", false)
}

// inlineMediaAs is inlineMedia, and with show the text as the cut view
// shows it: each image of the vault, small, before its text, and the text
// of one without a description marked. Its blocks are the same.
func inlineMediaAs(body string, media map[string]mediaText, words MediaWords, inlineMax int, icons string, show bool) (string, MediaStats) {
	text, st, _ := inlineMediaOpt(body, media, words, inlineMax, icons, show, false)
	return text, st
}

// inlineMediaOpt is inlineMediaAs, and with number the images (not the
// icons) numbered in the order of the body, which it gives. icons is which
// icons have a short description (iconDescriptions).
func inlineMediaOpt(body string, media map[string]mediaText, words MediaWords, inlineMax int, icons string, show, number bool) (string, MediaStats, []MediaRef) {
	var (
		st   MediaStats
		refs []MediaRef
	)
	described := map[string]bool{}
	describe := func(url string) bool {
		if icons == iconsFirst && described[url] {
			return false
		}
		described[url] = true
		return icons == iconsFirst || icons == iconsAll
	}
	text := func(url, alt string, inline bool) string {
		n := 0
		if number {
			n = len(refs) + 1
		}
		t, icon := mediaPhrase(&st, media, words, inlineMax, url, alt, inline, n, describe)
		if number && !icon {
			m := media[url]
			ref := MediaRef{N: n, Path: m.path, Note: m.note, Label: m.label}
			if ref.Path == "" && strings.HasPrefix(url, "/vault-files/") {
				ref.Path = strings.TrimPrefix(url, "/vault-files/")
			}
			refs = append(refs, ref)
		}
		if !show {
			return t
		}
		if m, ok := media[url]; !ok || (m.label == "" && m.description == "") {
			t = `<span class="gosidian-cut-missing">` + html.EscapeString(t) + `</span>`
		}
		if strings.HasPrefix(url, "/vault-files/") {
			t = `<img class="gosidian-cut-thumb" src="` + html.EscapeString(url) + `" alt="">` + t
		}
		return t
	}
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	var fences parser.Fences
	blank := false // a paragraph of images was just put: the next line starts another
	for _, line := range lines {
		if fences.Code(line) || !strings.Contains(line, "![") {
			if blank && strings.TrimSpace(line) != "" {
				out = append(out, "")
			}
			blank = false
			out = append(out, line)
			continue
		}
		parts := strings.Split(line, "`")
		if len(parts)%2 == 0 {
			parts = []string{line} // an unclosed backtick: no code span
		}
		rest := ""
		for j := 0; j < len(parts); j += 2 {
			rest += imageRe.ReplaceAllString(parts[j], "")
		}
		if strings.TrimSpace(rest) == "" && len(parts) == 1 {
			var paras []string
			for _, m := range imageRe.FindAllStringSubmatch(line, -1) {
				paras = append(paras, text(m[2], m[1], false))
			}
			if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
				out = append(out, "")
			}
			out = append(out, strings.Join(paras, "\n\n"))
			blank = true
			continue
		}
		// In a quote or a list's item with nothing else, an image is on a
		// line of its own: it keeps the marks, so the block stays one.
		inline := len(parts) > 1 || !markerRe.MatchString(strings.TrimSpace(rest))
		for j := 0; j < len(parts); j += 2 {
			parts[j] = imageRe.ReplaceAllStringFunc(parts[j], func(m string) string {
				sm := imageRe.FindStringSubmatch(m)
				return text(sm[2], sm[1], inline)
			})
		}
		if blank {
			out = append(out, "")
		}
		blank = false
		out = append(out, strings.Join(parts, "`"))
	}
	return strings.Join(out, "\n"), st, refs
}

// mediaPhrase is the text of an image (inlineMedia), counted in st, and
// whether it is an icon. With n, an image's word has its number: "[image
// 3: label]"; an icon has none. An icon with a label and a description has
// its description's first sentence after the label when describe says so.
func mediaPhrase(st *MediaStats, media map[string]mediaText, words MediaWords, inlineMax int, url, alt string, inline bool, n int, describe func(url string) bool) (string, bool) {
	st.Images++
	m, ok := media[url]
	icon := inline && (inlineMax <= 0 || !ok || m.side <= inlineMax)
	word := words.Image
	if icon {
		st.Icons++
		word = words.Icon
	}
	label, desc := m.label, m.description
	if label == "" && desc == "" {
		st.Missing++
		if label = flatText(alt); wordAltRe.MatchString(label) {
			st.WordAlt++
			label = ""
		}
	} else {
		st.Described++
	}
	if !icon && n > 0 {
		word += " " + strconv.Itoa(n)
	}
	switch {
	case icon:
		if label == "" {
			label = clipHead(desc, 80)
		} else if short := firstSentence(desc, iconShortChars); short != "" && describe(url) {
			label += " (" + short + ")"
		}
		if label == "" {
			return "[" + word + "]", true
		}
		return "[" + word + ": " + label + "]", true
	case inline:
		if label == "" {
			label, desc = desc, ""
		}
		if desc != "" {
			label += " (" + desc + ")"
		}
		if label == "" {
			return "[" + word + "]", false
		}
		return "[" + word + ": " + label + "]", false
	}
	head := "[" + word + "]"
	if label != "" {
		head = "[" + word + ": " + label + "]"
	}
	if desc != "" {
		head += " " + desc
	}
	return head, false
}

// chunkMedia are the numbered images whose text is in a chunk's, in their
// order: "[image 3: label]", or "[image 3]" without a label.
func chunkMedia(text, word string, refs []MediaRef) []MediaRef {
	out := []MediaRef{}
	for _, r := range refs {
		mark := "[" + word + " " + strconv.Itoa(r.N)
		for i := 0; ; {
			j := strings.Index(text[i:], mark)
			if j < 0 {
				break
			}
			i += j + len(mark)
			if i < len(text) && (text[i] == ':' || text[i] == ']') {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// cutOptions are the rules of a cut.
type cutOptions struct {
	maxChars   int
	paragraphs bool
	// context is what a piece's text starts with: heading (its section's
	// heading), path (the title and the path of the headings), none.
	context string
	// joinMedia is the word of the images whose paragraphs join the text
	// beside them (media_paragraphs: join); "" keeps them apart.
	joinMedia string
}

// piece is a section, or a part of a long one, or a paragraph of a note.
type piece struct {
	base, parent string
	level        int // 1 a section, 2 a paragraph
	section      string
	title        string
	text         string
	// prefix is what text starts with (context), body the rest; part is
	// the part of a long section, from 1, 0 for a section in one piece.
	prefix, body string
	part         int
}

const (
	levelSection   = 1
	levelParagraph = 2
)

// cutNote cuts a note's body by its headings: a section for each heading
// with text of its own (and for the text before the first heading), cut
// into parts at its paragraphs when longer than maxChars, and a paragraph
// for each block between blank lines. A piece's base names it from one
// version of the note to the next: the note, the path of its headings (and
// which, when two have the same), its part and its paragraph.
func cutNote(doc, title, body string, opt cutOptions) []piece {
	type section struct {
		level   int
		heading string
		path    string
		lines   []string
	}
	var (
		secs   []section
		cur    section
		path   [7]string
		fences parser.Fences
	)
	for _, line := range strings.Split(body, "\n") {
		if !fences.Code(line) {
			if m := headingRe.FindStringSubmatch(line); m != nil {
				secs = append(secs, cur)
				level := len(m[1])
				path[level] = headingText(m[2])
				for l := level + 1; l < len(path); l++ {
					path[l] = ""
				}
				var parts []string
				for _, p := range path[2:] {
					if p != "" {
						parts = append(parts, p)
					}
				}
				cur = section{level: level, heading: headingText(m[2]), path: strings.Join(parts, " > ")}
				continue
			}
		}
		cur.lines = append(cur.lines, line)
	}
	secs = append(secs, cur)

	docKey := shortHash(doc, 10)
	seen := map[string]int{}
	var out []piece
	for _, s := range secs {
		paras := splitBlocks(s.lines)
		if len(paras) == 0 {
			continue
		}
		key := strconv.Itoa(min(s.level, 1)) + "\x00" + s.path
		seen[key]++
		base := docKey + "." + shortHash(key+"\x00"+strconv.Itoa(seen[key]), 10)
		full := title
		if s.path != "" {
			full += " > " + s.path
		}
		prefix := ""
		switch opt.context {
		case "heading":
			if s.level > 0 {
				prefix = strings.Repeat("#", s.level) + " " + s.heading + "\n\n"
			}
		case "path":
			prefix = full + "\n\n"
		}
		parts := groupBlocks(paras, opt.maxChars)
		for k, part := range parts {
			pb, pn := base, 0
			if len(parts) > 1 {
				pb, pn = base+".k"+strconv.Itoa(k+1), k+1
			}
			body := strings.Join(part, "\n\n")
			out = append(out, piece{base: pb, level: levelSection, section: s.path, title: full, text: prefix + body, prefix: prefix, body: body, part: pn})
			if !opt.paragraphs {
				continue
			}
			// The images join their text within the part: the sections and
			// their parts are the same either way, so a cut without the rule
			// (rispondi's sections) finds the parents of the paragraphs.
			if opt.joinMedia != "" {
				part = joinMedia(part, opt.joinMedia, opt.maxChars)
			}
			for i, p := range part {
				out = append(out, piece{base: pb + ".p" + strconv.Itoa(i+1), parent: pb, level: levelParagraph, section: s.path, title: full, text: prefix + p, prefix: prefix, body: p, part: pn})
			}
		}
	}
	return out
}

// splitBlocks are a section's blocks, between blank lines, a fenced block
// whole.
func splitBlocks(lines []string) []string {
	var (
		out    []string
		cur    []string
		fences parser.Fences
	)
	flush := func() {
		if t := strings.TrimSpace(strings.Join(cur, "\n")); t != "" {
			out = append(out, t)
		}
		cur = nil
	}
	for _, line := range lines {
		if !fences.Code(line) && strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return out
}

// joinMedia puts each image on a line of its own (mediaParagraph) in the
// paragraph before it, or, when images open the section, in the paragraph
// after them, as long as the paragraph stays within maxChars; past that the
// image starts a paragraph, which the images after it join. A description
// then counts with the text it illustrates, not as a paragraph alone that a
// search finds in place of the text (IMP-200: in the manuals of scope C the
// paragraphs of the descriptions pushed the technical tables out of the
// reranker's candidates).
func joinMedia(blocks []string, word string, maxChars int) []string {
	var out []string
	lead := false // out holds the section's images only: its first text joins them
	for _, b := range blocks {
		media := mediaParagraph(b, word)
		n := len(out)
		if n > 0 && (media || lead) && utf8.RuneCountInString(out[n-1])+2+utf8.RuneCountInString(b) <= maxChars {
			out[n-1] += "\n\n" + b
			lead = lead && media
			continue
		}
		out = append(out, b)
		lead = n == 0 && media
	}
	return out
}

// mediaParagraph says whether a block is an image on a line of its own as
// inlineMedia writes it, "[image: label] description", "[image 3]", "[image]";
// in the cut view after its thumbnail and inside its mark.
func mediaParagraph(block, word string) bool {
	if strings.Contains(block, "\n") {
		return false
	}
	for strings.HasPrefix(block, "<img ") || strings.HasPrefix(block, "<span ") {
		i := strings.IndexByte(block, '>')
		if i < 0 {
			return false
		}
		block = block[i+1:]
	}
	rest, ok := strings.CutPrefix(block, "["+word)
	if !ok {
		return false
	}
	if digits := strings.TrimLeft(strings.TrimPrefix(rest, " "), "0123456789"); strings.HasPrefix(rest, " ") && len(digits) < len(rest)-1 {
		rest = digits
	}
	return strings.HasPrefix(rest, "]") || strings.HasPrefix(rest, ": ")
}

// groupBlocks puts blocks in parts of up to maxChars characters each, in
// order; a block longer than that is a part of its own.
func groupBlocks(blocks []string, maxChars int) [][]string {
	var (
		parts [][]string
		cur   []string
		n     int
	)
	for _, b := range blocks {
		size := utf8.RuneCountInString(b)
		if len(cur) > 0 && n+2+size > maxChars {
			parts = append(parts, cur)
			cur, n = nil, 0
		}
		if len(cur) > 0 {
			n += 2
		}
		cur = append(cur, b)
		n += size
	}
	if len(cur) > 0 {
		parts = append(parts, cur)
	}
	return parts
}

func shortHash(s string, n int) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:n]
}

var versionRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func noteSections(_ context.Context, env *Env, with map[string]any) (any, error) {
	p, err := required(with, "path")
	if err != nil {
		return nil, err
	}
	rel, err := env.projectPath(p)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(rel, ".md") {
		rel += ".md"
	}
	opt := cutOptions{maxChars: int(number(with, "max_chars", defaultSectionChars)), paragraphs: true, context: str(with, "context")}
	// A section longer than a step's output could never come out of it.
	if maxChars := env.Engine.cfg.MaxOutputBytes; opt.maxChars < minSectionChars || opt.maxChars > maxChars {
		return nil, fmt.Errorf("with.max_chars: %d to %d ([pipelines] max_output_bytes)", minSectionChars, maxChars)
	}
	switch opt.context {
	case "":
		opt.context = "heading"
	case "heading", "path", "none":
	default:
		return nil, fmt.Errorf("with.context %q: heading, path or none", opt.context)
	}
	// The sections are always cut, as the paragraphs' parents and paths;
	// levels says which the node gives.
	giveSections := true
	if v, ok := with["levels"]; ok && v != nil {
		levels, ok := v.([]any)
		if !ok || len(levels) == 0 {
			return nil, errors.New("with.levels: a list of section, paragraph")
		}
		opt.paragraphs = false
		sections := false
		for _, l := range levels {
			switch l {
			case "section":
				sections = true
			case "paragraph":
				opt.paragraphs = true
			default:
				return nil, fmt.Errorf("with.levels: %v; section or paragraph", l)
			}
		}
		giveSections = sections
	}
	version := str(with, "version")
	if version != "" && !versionRe.MatchString(version) {
		return nil, fmt.Errorf("with.version %q: letters, digits, . _ and -, at most 64", version)
	}
	withText := true
	if v, ok := with["text"].(bool); ok {
		withText = v
	}
	var only map[string]bool
	if v, ok := with["only"]; ok && v != nil {
		list, ok := v.([]any)
		if maxOnly := env.Engine.cfg.MaxListItems; !ok || len(list) > maxOnly {
			return nil, fmt.Errorf("with.only: a list of up to %d bases ([pipelines] max_list_items)", maxOnly)
		}
		only = map[string]bool{}
		for _, b := range list {
			s, ok := b.(string)
			if !ok {
				return nil, errors.New("with.only: the bases of the pieces, strings")
			}
			only[s] = true
		}
	}
	extra, _ := with["meta"].(map[string]any)
	if b, _ := json.Marshal(extra); len(b) > env.Engine.cfg.MaxChunkMeta {
		return nil, fmt.Errorf("with.meta: over %d bytes ([pipelines] max_chunk_meta)", env.Engine.cfg.MaxChunkMeta)
	}
	words, err := mediaWords(with)
	if err != nil {
		return nil, err
	}
	switch str(with, "media_paragraphs") {
	case "", "apart":
	case "join":
		if str(with, "media") == "keep" {
			return nil, errors.New("with.media_paragraphs: join takes media: inline, the images as text")
		}
		opt.joinMedia = words.Image
	default:
		return nil, fmt.Errorf("with.media_paragraphs %q: apart or join", str(with, "media_paragraphs"))
	}
	icons, err := iconDescriptions(with)
	if err != nil {
		return nil, err
	}

	note, err := env.Engine.host.ReadNote(env.Project, rel)
	if err != nil {
		return nil, err
	}
	title := note.Title
	if t, _ := note.Frontmatter["title"].(string); t != "" {
		title = t
	}
	body := note.Body
	var (
		st   MediaStats
		refs []MediaRef
	)
	numbers, _ := with["media_numbers"].(bool)
	switch str(with, "media") {
	case "", "inline":
		reader, ok := env.Engine.host.(NoteReader)
		if !ok {
			return nil, errors.New("this server does not read the image notes of a note: media: keep")
		}
		inlineMax := int(number(with, "inline_max_side", DefaultInlineMaxSide))
		body, st, refs, err = noteMediaText(reader, env.Project, rel, body, words, inlineMax, icons, numbers)
		if err != nil {
			return nil, err
		}
	case "keep":
		if numbers {
			return nil, errors.New("with.media_numbers: with media: inline, the images as text")
		}
	default:
		return nil, fmt.Errorf("with.media %q: inline or keep", str(with, "media"))
	}

	pieces := cutNote(rel, title, body, opt)
	id := func(base string) string {
		if version == "" || base == "" {
			return base
		}
		return base + "@" + version
	}
	sections, paragraphs := 0, 0
	chunks := []any{}
	for pos, pc := range pieces {
		if pc.level == levelSection {
			sections++
		} else {
			paragraphs++
		}
		if only != nil && !only[pc.base] {
			continue
		}
		if pc.level == levelSection && !giveSections {
			continue
		}
		// Without the text, what tells which pieces changed: their base
		// and hash.
		meta := map[string]any{}
		if withText {
			for k, v := range extra {
				meta[k] = v
			}
			meta["section"], meta["position"] = pc.section, pos+1
		}
		meta["base"], meta["hash"], meta["chars"] = pc.base, shortHash(pc.text, 16), utf8.RuneCountInString(pc.text)
		ch := map[string]any{"id": id(pc.base), "level": pc.level, "meta": meta}
		if pc.parent != "" {
			ch["parent"] = id(pc.parent)
		}
		if withText {
			ch["text"], ch["title"], ch["doc"] = pc.text, pc.title, rel
			if numbers {
				ch["media"] = chunkMedia(pc.text, words.Image, refs)
			}
		}
		chunks = append(chunks, ch)
	}
	return map[string]any{"doc": rel, "title": title, "version": version, "sections": sections, "paragraphs": paragraphs,
		"media": st, "chunks": chunks, "count": len(chunks)}, nil
}

// mediaWords reads with.media_words, over the default words.
func mediaWords(with map[string]any) (MediaWords, error) {
	w := DefaultMediaWords
	v, ok := with["media_words"]
	if !ok || v == nil {
		return w, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return w, errors.New("with.media_words: an object, {icon: …, image: …}")
	}
	for k, x := range m {
		s, _ := x.(string)
		s = strings.TrimSpace(s)
		if s == "" || len(s) > 40 || strings.ContainsAny(s, "[]\n") {
			return w, fmt.Errorf("with.media_words.%s: a word, at most 40 bytes, without brackets", k)
		}
		switch k {
		case "icon":
			w.Icon = s
		case "image":
			w.Image = s
		default:
			return w, fmt.Errorf("with.media_words.%s: icon or image", k)
		}
	}
	return w, nil
}

// CutOptions are the rules of a cut the preview shows (IMP-170): those of
// note.sections.
type CutOptions struct {
	MaxChars  int
	Context   string // heading (default), path, none
	Words     MediaWords
	InlineMax int // the longest side of an icon; 0: any image in a sentence
	// JoinMedia joins an image's paragraph to the text beside it
	// (media_paragraphs: join).
	JoinMedia bool
	// Icons is which icons have a short description after their label
	// (icon_descriptions): first, all; "" none.
	Icons string
}

// CutBlock is a section, a part of a long one, or a paragraph of a note,
// as the cut view shows it: what note.sections makes of it (its base,
// hash and size), and its markdown as shown, its images small beside
// their text.
type CutBlock struct {
	ID      string `json:"id"`
	Parent  string `json:"parent,omitempty"`
	Level   int    `json:"level"`
	Section string `json:"section,omitempty"`
	Chars   int    `json:"chars"`
	Hash    string `json:"hash"`
	// Part is the part of a long section, from 1; Over a block longer than
	// max_chars, which no part could keep under it.
	Part int  `json:"part,omitempty"`
	Over bool `json:"over,omitempty"`
	// Prefix is what its text starts with (its heading), Show the rest as
	// shown, both markdown.
	Prefix string `json:"-"`
	Show   string `json:"-"`
}

// CutView cuts a note's body as note.sections does, for the preview.
func CutView(host NoteReader, project, note, title, body string, opt CutOptions) ([]CutBlock, MediaStats, error) {
	if opt.MaxChars <= 0 {
		opt.MaxChars = defaultSectionChars
	}
	if opt.Context == "" {
		opt.Context = "heading"
	}
	if opt.Words == (MediaWords{}) {
		opt.Words = DefaultMediaWords
	}
	media, err := noteMedia(host, project, note)
	if err != nil {
		return nil, MediaStats{}, err
	}
	text, st := inlineMediaAs(body, media, opt.Words, opt.InlineMax, opt.Icons, false)
	shown, _ := inlineMediaAs(body, media, opt.Words, opt.InlineMax, opt.Icons, true)
	co := cutOptions{maxChars: opt.MaxChars, paragraphs: true, context: opt.Context}
	if opt.JoinMedia {
		co.joinMedia = opt.Words.Image
	}
	pieces := cutNote(note, title, text, co)
	view := cutNote(note, title, shown, co)
	if len(view) != len(pieces) {
		view = pieces // the marks changed the blocks: show the text as it goes
	}
	out := make([]CutBlock, len(pieces))
	for i, p := range pieces {
		chars := utf8.RuneCountInString(p.text)
		out[i] = CutBlock{ID: p.base, Parent: p.parent, Level: p.level, Section: p.section, Chars: chars, Hash: shortHash(p.text, 16),
			Part: p.part, Over: chars > opt.MaxChars, Prefix: p.prefix, Show: view[i].body}
	}
	return out, st, nil
}
