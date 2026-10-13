package pipeline

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/gosidian/gosidian/internal/parser"
)

// The image notes beside a note (IMP-179): in.import makes one for each
// image of a document it brings into the project, in <note>_img/, and
// llm.describe_media writes their descriptions. The helpers below read the
// images of a markdown body and the text around them.

// Folders is what a host offers, beyond Host, to the nodes that keep image
// notes beside a note: the notes of a folder, and deleting one (into the
// trash when the server keeps one). Without it in.import writes the image
// notes without looking at those there, and llm.describe_media fails.
type Folders interface {
	// NotesIn lists the notes directly in a folder of the project.
	NotesIn(project, folder string) ([]string, error)
	// DeleteNote deletes a note of the project, as the pipeline (actor).
	DeleteNote(project, path, actor string) error
}

// mediaFolder is the folder of the image notes of a note.
func mediaFolder(note string) string { return strings.TrimSuffix(note, ".md") + "_img" }

// linkTarget is the note a wikilink value names, as a path without .md:
// "[[a/b#c|d]]" is "a/b".
func linkTarget(v any) string {
	s, _ := v.(string)
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[[") || !strings.HasSuffix(s, "]]") {
		return ""
	}
	s = s[2 : len(s)-2]
	if i := strings.IndexAny(s, "|#"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSuffix(strings.TrimSpace(s), ".md")
}

// isMediaOf says whether a note's frontmatter makes it an image note of the
// article (a note path).
func isMediaOf(fm map[string]any, article string) bool {
	t, _ := fm["type"].(string)
	return t == "image" && linkTarget(fm["article"]) == strings.TrimSuffix(article, ".md")
}

// withBody is a note's content with its body, after the frontmatter,
// replaced by body.
func withBody(raw []byte, body string) []byte {
	head := string(raw[:len(raw)-len(parser.BodyAfterFrontmatter(raw))])
	if head != "" && !strings.HasSuffix(head, "\n") {
		head += "\n"
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return []byte(head)
	}
	return []byte(head + "\n" + body + "\n")
}

// imageRef is an image of a markdown body: its target as written, its
// text, the line it is on, and whether other text shares the line (an icon
// inside a sentence).
type imageRef struct {
	URL    string
	Alt    string
	Line   int
	Inline bool
}

var (
	imageRe   = regexp.MustCompile(`!\[([^\]\n]*)\]\(\s*<?([^)\s>]+)>?(\s+"[^"\n]*")?\s*\)`)
	headingRe = regexp.MustCompile(`^(#{1,6})[ \t]+(.+?)[ \t#]*$`)
)

// scanImages lists the images of a body's lines in order, outside code.
func scanImages(lines []string) []imageRef {
	var out []imageRef
	var fences parser.Fences
	for i, line := range lines {
		if fences.Code(line) {
			continue
		}
		parts := strings.Split(line, "`")
		if len(parts)%2 == 0 {
			parts = []string{line} // an unclosed backtick: no code span
		}
		var found []imageRef
		rest := ""
		for j := 0; j < len(parts); j += 2 {
			for _, m := range imageRe.FindAllStringSubmatch(parts[j], -1) {
				found = append(found, imageRef{URL: m[2], Alt: m[1], Line: i})
			}
			rest += imageRe.ReplaceAllString(parts[j], "")
		}
		inline := strings.TrimSpace(rest) != "" || len(parts) > 1
		for _, f := range found {
			f.Inline = inline
			out = append(out, f)
		}
	}
	return out
}

// headingText is a heading's text without its emphasis marks.
func headingText(s string) string {
	return strings.TrimSpace(strings.NewReplacer("**", "", "__", "", "*", "", "`", "").Replace(s))
}

// sectionAt is the path of the headings line is under, but the first
// level's (the title): "Setup > Requirements".
func sectionAt(lines []string, line int) string {
	var path [7]string
	var fences parser.Fences
	for i := 0; i < line && i < len(lines); i++ {
		if fences.Code(lines[i]) {
			continue
		}
		m := headingRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		level := len(m[1])
		path[level] = headingText(m[2])
		for l := level + 1; l < len(path); l++ {
			path[l] = ""
		}
	}
	var parts []string
	for _, p := range path[2:] {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " > ")
}

// imageMark is where the image stands in the context given to a model.
const imageMark = "[image]"

// imageContext is the text around an image for a model, about chars long:
// for an image alone on its line, the paragraphs before it (two thirds) and
// after it (a third), within its section; for one inside a sentence, its
// line. The other images are left out; the image is imageMark.
func imageContext(lines []string, at imageRef, chars int) string {
	if at.Inline {
		marked := false
		line := imageRe.ReplaceAllStringFunc(lines[at.Line], func(m string) string {
			if !marked && imageRe.FindStringSubmatch(m)[2] == at.URL {
				marked = true
				return imageMark
			}
			return ""
		})
		return clipTail(strings.Join(strings.Fields(line), " "), chars)
	}
	before := clipTail(textAround(lines, at.Line, -1, chars*2/3), chars*2/3)
	after := clipHead(textAround(lines, at.Line, +1, chars-len(before)), chars-len(before))
	return strings.TrimSpace(before + "\n\n" + imageMark + "\n\n" + after)
}

// textAround gathers the text of the lines from line, going by step (-1 up,
// +1 down), until a heading or about chars of text; lines made of images
// only are skipped.
func textAround(lines []string, line, step, chars int) string {
	var got []string
	n := 0
	for i := line + step; i >= 0 && i < len(lines) && n < chars; i += step {
		if headingRe.MatchString(lines[i]) {
			break
		}
		t := strings.Join(strings.Fields(imageRe.ReplaceAllString(lines[i], "")), " ")
		if t == "" {
			if len(got) > 0 && got[len(got)-1] != "" {
				got = append(got, "")
			}
			continue
		}
		got = append(got, t)
		n += len(t)
	}
	if step < 0 {
		for i, j := 0, len(got)-1; i < j; i, j = i+1, j-1 {
			got[i], got[j] = got[j], got[i]
		}
	}
	return strings.TrimSpace(strings.Join(got, "\n"))
}

// clipTail keeps the last chars bytes of s, from a word's start.
func clipTail(s string, chars int) string {
	if chars <= 0 {
		return ""
	}
	if len(s) <= chars {
		return s
	}
	cut := len(s) - chars
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	s = s[cut:]
	if i := strings.IndexAny(s, " \n"); i >= 0 {
		s = s[i+1:]
	}
	return "…" + s
}

// clipHead keeps the first chars bytes of s, to a word's end.
func clipHead(s string, chars int) string {
	if chars <= 0 {
		return ""
	}
	if len(s) <= chars {
		return s
	}
	cut := chars
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	s = s[:cut]
	if i := strings.LastIndexAny(s, " \n"); i >= 0 {
		s = s[:i]
	}
	return s + "…"
}
