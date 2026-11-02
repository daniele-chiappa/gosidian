package parser

import (
	"bytes"
	"strconv"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// highlighting renders fenced code with chroma. Classes, not inline
// colours: web/src/styles/code.css paints them from the preset's tokens,
// so code follows every theme, the light ones included (IMP-154).
//
// It takes the place of goldmark-highlighting, which loaded chroma's
// styles at start (5-8 ms and 1.9 MB of allocations) only to pick colours
// that classes leave out: with classes the HTML is the same under any
// style. testdata/code_blocks.html fixes the output.
type highlighting struct{}

// noStyle is the style the formatter asks for; with classes it paints
// nothing.
var noStyle = chroma.MustNewStyle("classes", chroma.StyleEntries{})

func (highlighting) Extend(m goldmark.Markdown) {
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(highlighting{}, 200)))
}

func (highlighting) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, renderFencedCode)
}

// renderFencedCode highlights a block in a language chroma knows, and
// writes any other as <pre><code class="language-…">, escaped.
func renderFencedCode(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.FencedCodeBlock)
	language := n.Language(source)
	var info []byte
	if n.Info != nil {
		info = n.Info.Segment.Value(source)
	}
	opts, nohl := codeOptions(info)
	var code bytes.Buffer
	for i := 0; i < n.Lines().Len(); i++ {
		line := n.Lines().At(i)
		code.Write(line.Value(source))
	}

	var lexer chroma.Lexer
	if language != nil && !nohl {
		lexer = lexerOf(string(language))
	}
	if lexer != nil {
		if it, err := chroma.Coalesce(lexer).Tokenise(nil, code.String()); err == nil {
			_ = chromahtml.New(opts...).Format(w, noStyle, it)
			return ast.WalkContinue, nil
		}
	}

	_, _ = w.WriteString("<pre><code")
	if language != nil {
		_, _ = w.WriteString(` class="language-`)
		gmhtml.DefaultWriter.Write(w, language)
		_ = w.WriteByte('"')
	}
	_ = w.WriteByte('>')
	gmhtml.DefaultWriter.RawWrite(w, code.Bytes())
	_, _ = w.WriteString("</code></pre>\n")
	return ast.WalkContinue, nil
}

// lexerCache keeps chroma's answer for a language, nil for one it does not
// know. chroma tries such a name against the file names of all its lexers,
// about 5 ms a block for mermaid or view at every render. The names come
// from the notes, so the cache keeps up to lexerCacheMax short ones.
var lexerCache = struct {
	sync.Mutex
	m map[string]chroma.Lexer
}{m: map[string]chroma.Lexer{}}

const (
	lexerCacheMax     = 256
	lexerCacheNameMax = 32
)

func lexerOf(language string) chroma.Lexer {
	lexerCache.Lock()
	l, ok := lexerCache.m[language]
	lexerCache.Unlock()
	if ok {
		return l
	}
	l = lexers.Get(language)
	if len(language) <= lexerCacheNameMax {
		lexerCache.Lock()
		if len(lexerCache.m) < lexerCacheMax {
			lexerCache.m[language] = l
		}
		lexerCache.Unlock()
	}
	return l
}

// codeOptions reads the attributes that may follow the language in the
// info string, as Hugo writes them: ```go {linenos=table, linenostart=10,
// hl_lines=[2,"4-5"]}. nohl turns the highlighting off; style, which picked
// the colours, has nothing left to pick.
func codeOptions(info []byte) (opts []chromahtml.Option, nohl bool) {
	opts = []chromahtml.Option{chromahtml.WithClasses(true), chromahtml.WithPreWrapper(codePre{})}
	i := bytes.IndexByte(info, '{')
	if i <= 0 {
		return opts, false
	}
	attrs, ok := parser.ParseAttributes(text.NewReader(info[i:]))
	if !ok {
		return opts, false
	}
	// The last of two attributes with the same name wins.
	attr := func(name string) (any, bool) {
		for j := len(attrs) - 1; j >= 0; j-- {
			if string(attrs[j].Name) == name {
				return attrs[j].Value, true
			}
		}
		return nil, false
	}

	base := 1
	if v, ok := attr("linenostart"); ok {
		if f, ok := v.(float64); ok {
			base = int(f)
			opts = append(opts, chromahtml.BaseLineNumber(base))
		}
	}
	if v, ok := attr("hl_lines"); ok {
		if lines, ok := v.([]any); ok {
			var ranges [][2]int
			for _, l := range lines {
				switch l := l.(type) {
				case float64:
					ranges = append(ranges, [2]int{int(l) + base - 1, int(l) + base - 1})
				case []byte:
					ends := strings.Split(string(l), "-")
					lo, err := strconv.Atoi(ends[0])
					if err != nil {
						continue
					}
					hi := lo
					if len(ends) > 1 {
						if hi, err = strconv.Atoi(ends[1]); err != nil {
							continue
						}
					}
					ranges = append(ranges, [2]int{lo + base - 1, hi + base - 1})
				}
			}
			opts = append(opts, chromahtml.HighlightLines(ranges))
		}
	}
	if _, ok := attr("nohl"); ok {
		nohl = true
	}
	if v, ok := attr("linenos"); ok {
		switch v := v.(type) {
		case bool:
			opts = append(opts, chromahtml.WithLineNumbers(v))
		case []byte:
			opts = append(opts, chromahtml.WithLineNumbers(true))
			switch string(v) {
			case "table":
				opts = append(opts, chromahtml.LineNumbersInTable(true))
			case "inline":
				opts = append(opts, chromahtml.LineNumbersInTable(false))
			}
		}
	}
	return opts, nohl
}
