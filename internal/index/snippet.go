package index

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// hugeNote is the size, in bytes, from which a hit's snippet is cut here
// rather than by FTS5; tests lower it. FTS5's snippet() tries every match
// in the body as the start of its window and scores each one by reading
// every match again, so its time grows with the square of the matches: on
// a report of 2.4 MB where a short word of the query matches all through
// it, one snippet took 35 s in native SQLite. cutSnippet reads the body
// once. Its window is the one with the most words of the query, as FTS5's
// is, but it does not look for the start of a sentence: the snippet of
// such a note can start a few words earlier or later than FTS5's.
var hugeNote int64 = 256 << 10

// snippetTerm is a word of a query as its match reads it (matchExpr): the
// tokens of the word, the last one a prefix, or one of the inflections the
// index found for it, a whole token.
type snippetTerm struct {
	tokens []string
	forms  []string
}

// snippetTerms reads q as matchExpr does.
func (i *Index) snippetTerms(q string) []snippetTerm {
	var terms []snippetTerm
	for _, w := range strings.Fields(q) {
		w = strings.ReplaceAll(w, `"`, ``)
		var toks []string
		for _, t := range tokenize(w) {
			toks = append(toks, fold(w[t.start:t.end]))
		}
		if len(toks) > 0 {
			terms = append(terms, snippetTerm{tokens: toks, forms: i.inflections(w)})
		}
	}
	return terms
}

// at is how many tokens, from p on, the term matches: 0 for none. word
// gives a token folded.
func (t snippetTerm) at(word func(int) string, n, p int) int {
	if k := len(t.tokens); p+k <= n {
		same := true
		for j := 0; j < k-1 && same; j++ {
			same = word(p+j) == t.tokens[j]
		}
		if same && strings.HasPrefix(word(p+k-1), t.tokens[k-1]) {
			return k
		}
	}
	for _, f := range t.forms {
		if word(p) == f {
			return 1
		}
	}
	return 0
}

// span is a token of a text, text[start:end].
type span struct{ start, end int }

// tokenize cuts text as the unicode61 tokenizer does: a token is a run of
// letters, numbers, private-use characters and the marks that follow them.
func tokenize(text string) []span {
	out := make([]span, 0, len(text)/8)
	start := -1
	for k := 0; k < len(text); {
		r, size := rune(text[k]), 1
		var in bool
		if r < utf8.RuneSelf {
			in = 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9'
		} else {
			r, size = utf8.DecodeRuneInString(text[k:])
			in = unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Co, r) ||
				(start >= 0 && unicode.Is(unicode.Mn, r))
		}
		switch {
		case in && start < 0:
			start = k
		case !in && start >= 0:
			out = append(out, span{start, k})
			start = -1
		}
		k += size
	}
	if start >= 0 {
		out = append(out, span{start, len(text)})
	}
	return out
}

// fold lowers a token and drops its diacritics, as remove_diacritics 2
// does. A token of lower-case ASCII, most of them, comes back as it is.
func fold(w string) string {
	lower := true
	for k := 0; k < len(w); k++ {
		c := w[k]
		if c >= utf8.RuneSelf {
			var b strings.Builder
			for _, r := range norm.NFD.String(w) {
				if !unicode.Is(unicode.Mn, r) {
					b.WriteRune(unicode.ToLower(r))
				}
			}
			return b.String()
		}
		lower = lower && !('A' <= c && c <= 'Z')
	}
	if lower {
		return w
	}
	return strings.ToLower(w)
}

// cutSnippet is the snippet of body for the terms, in FTS5's form: about
// snippetTokens tokens with the matches between <mark> and </mark>, and an
// ellipsis where the body goes on. The window is the one where the most
// terms match, then the most matches, the earliest of them; it is centered
// on its matches as FTS5 centers its own. A body the terms do not match
// shows its start.
func cutSnippet(body string, terms []snippetTerm) string {
	toks := tokenize(body)
	if len(toks) == 0 {
		return ""
	}
	word := func(p int) string { return fold(body[toks[p].start:toks[p].end]) }
	// The ASCII letters a match can start with: a token that starts with
	// another one is not folded or compared at all. A token that starts
	// beyond ASCII may fold to any of them, and is always.
	var starts [utf8.RuneSelf]bool
	for _, t := range terms {
		for _, w := range append([]string{t.tokens[0]}, t.forms...) {
			if w[0] < utf8.RuneSelf {
				starts[w[0]] = true
			}
		}
	}
	type match struct{ pos, n, term int }
	var ms []match
	for p, t := range toks {
		if c := body[t.start]; c < utf8.RuneSelf {
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			if !starts[c] {
				continue
			}
		}
		for ti, t := range terms {
			if n := t.at(word, len(toks), p); n > 0 {
				ms = append(ms, match{p, n, ti})
			}
		}
	}
	// Two pointers over the matches, in order: the window starts at the
	// match a and holds those before e.
	start := 0
	if len(ms) > 0 {
		count := make([]int, len(terms))
		distinct, e, l := 0, 0, 0
		best, bestA, bestE := -1, 0, 0
		for a := range ms {
			for ; l < a; l++ {
				if count[ms[l].term]--; count[ms[l].term] == 0 {
					distinct--
				}
			}
			for ; e < len(ms) && ms[e].pos < ms[a].pos+snippetTokens; e++ {
				if count[ms[e].term]++; count[ms[e].term] == 1 {
					distinct++
				}
			}
			if score := 1000*distinct + (e - a - distinct); score > best {
				best, bestA, bestE = score, a, e
			}
		}
		first, last := ms[bestA].pos, 0
		for _, m := range ms[bestA:bestE] {
			last = max(last, m.pos+m.n)
		}
		start = first - (snippetTokens-(last-first))/2
		start = max(min(start, len(toks)-snippetTokens), 0)
	}
	end := min(start+snippetTokens, len(toks))
	marks := map[int]int{} // a token of the window → the tokens its match covers
	for _, m := range ms {
		if m.pos >= start && m.pos < end {
			marks[m.pos] = max(marks[m.pos], min(m.n, end-m.pos))
		}
	}
	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	// From the start of the body when the window is there, as FTS5 does.
	from := 0
	if start > 0 {
		from = toks[start].start
	}
	for p := start; p < end; p++ {
		n := marks[p]
		if n == 0 {
			continue
		}
		last := p + n - 1
		b.WriteString(body[from:toks[p].start])
		b.WriteString("<mark>")
		b.WriteString(body[toks[p].start:toks[last].end])
		b.WriteString("</mark>")
		from = toks[last].end
		p = last
	}
	b.WriteString(body[from:toks[end-1].end])
	if end < len(toks) {
		b.WriteString("…")
	}
	return b.String()
}
