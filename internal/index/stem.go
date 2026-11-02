package index

import (
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"unicode"
)

const (
	// minStemLen keeps short stems out: "running" → "run" would reach
	// "runtime" and "runner" through their shared prefix.
	minStemLen = 4
	// minPrefixLen bounds the vocabulary scan: candidates share at least
	// this prefix with the word ("naming" → "name": scan "nam…"). Precision
	// comes from the stem comparison, not from the prefix.
	minPrefixLen = 3
	// maxCandidates caps the vocabulary scanned for one search word.
	maxCandidates = 200
	// maxKnownStems caps the stems the stemmer remembers: a few hundred KB.
	maxKnownStems = 4096
)

// stemmer derives English Porter stems with SQLite's own FTS5 porter
// tokenizer, run on a private in-memory database: the stems are the ones
// FTS5 itself would produce, with no stemming code or dependency of our own.
// The index keeps its plain tokens; stems only add the inflections of a
// search word to the query (see matchExpr).
//
// A call runs in one transaction it rolls back, with statements prepared
// once: the words in with one insert, their tokens read, nothing written.
// Committing and deleting the words, each statement prepared again, made
// most of the inflections' cost, which were 30% of a search on the
// benchmark's large vault: a search stems each word twice.
//
// A word's stem never changes, and the words a search stems come back:
// the vocabulary under a prefix is the same from one search to the next.
// The stemmer remembers the stems it found, up to maxKnownStems words, and
// forgets them all when it has that many.
type stemmer struct {
	mu                 sync.Mutex
	db                 *sql.DB
	insert, tokensRead *sql.Stmt
	known              map[string]string
}

func newStemmer() (*stemmer, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	// One connection: an in-memory database lives and dies with it.
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`CREATE VIRTUAL TABLE w USING fts5(t, tokenize='porter unicode61 remove_diacritics 2')`,
		`CREATE VIRTUAL TABLE v USING fts5vocab(w, 'instance')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, err
		}
	}
	s := &stemmer{db: db, known: map[string]string{}}
	// The k-th word of the JSON list is document k+1.
	if s.insert, err = db.Prepare(`INSERT INTO w(rowid, t) SELECT key + 1, value FROM json_each(?)`); err == nil {
		s.tokensRead, err = db.Prepare(`SELECT doc, term FROM v`)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// stems returns the Porter stem of each word, or "" for a word that is not
// a single token (identifiers such as BUG-017 split into several) and for
// every word when anything fails: stemming only ever adds to a search.
func (s *stemmer) stems(words []string) []string {
	out := make([]string, len(words))
	if s == nil || len(words) == 0 {
		return out
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var unknown []string
	var at []int
	for k, w := range words {
		if st, ok := s.known[w]; ok {
			out[k] = st
		} else {
			unknown, at = append(unknown, w), append(at, k)
		}
	}
	if len(unknown) == 0 {
		return out
	}
	found, ok := s.lookup(unknown)
	if !ok {
		return make([]string, len(words))
	}
	if len(s.known)+len(unknown) > maxKnownStems {
		clear(s.known)
	}
	for j, k := range at {
		out[k] = found[j]
		s.known[unknown[j]] = found[j]
	}
	return out
}

// lookup stems words with the porter tokenizer (under s.mu); ok is false
// when anything failed.
func (s *stemmer) lookup(words []string) (out []string, ok bool) {
	out = make([]string, len(words))
	list, err := json.Marshal(words)
	if err != nil {
		return out, false
	}
	tx, err := s.db.Begin()
	if err != nil {
		return out, false
	}
	defer tx.Rollback()
	if _, err := tx.Stmt(s.insert).Exec(string(list)); err != nil {
		return out, false
	}
	rows, err := tx.Stmt(s.tokensRead).Query()
	if err != nil {
		return out, false
	}
	defer rows.Close()
	tokens := make([]int, len(words))
	for rows.Next() {
		var doc int
		var term string
		if rows.Scan(&doc, &term) != nil || doc < 1 || doc > len(words) {
			continue
		}
		tokens[doc-1]++
		out[doc-1] = term
	}
	for k := range out {
		if tokens[k] != 1 {
			out[k] = ""
		}
	}
	return out, rows.Err() == nil
}

func (s *stemmer) stem(word string) string { return s.stems([]string{word})[0] }

func (s *stemmer) close() {
	if s != nil {
		s.db.Close()
	}
}

// matchExpr turns free text into an FTS5 query: every word quoted (so FTS
// operators in user input are inert), prefix-matched and ANDed. A word with
// indexed inflections outside its own prefix also matches them exactly:
// "retry" → ("retry"* OR "retries" OR "retried").
func (i *Index) matchExpr(q string) string {
	return joinParts(i.queryParts(q), " AND ")
}

// queryPart is one word of a query: the word as typed, and its FTS5
// expression.
type queryPart struct {
	word string
	expr string
}

// queryParts returns the words of q that are terms to the tokenizer, each
// with its expression.
func (i *Index) queryParts(q string) []queryPart {
	var parts []queryPart
	for _, t := range strings.Fields(q) {
		t = strings.ReplaceAll(t, `"`, ``)
		// A word of punctuation alone (the " — " of most titles, "&", "-")
		// is no term to the tokenizer, and ANDed in it matched nothing:
		// the whole search came back empty (BUG-114, S4-3).
		if !strings.ContainsFunc(t, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) {
			continue
		}
		part := `"` + t + `"*`
		if forms := i.inflections(t); len(forms) > 0 {
			part = `(` + part + ` OR "` + strings.Join(forms, `" OR "`) + `")`
		}
		parts = append(parts, queryPart{word: t, expr: part})
	}
	return parts
}

// joinParts joins the parts' expressions with op. Explicit AND: FTS5 rejects
// an implicit one after a parenthesized group.
func joinParts(parts []queryPart, op string) string {
	exprs := make([]string, len(parts))
	for k, p := range parts {
		exprs[k] = p.expr
	}
	return strings.Join(exprs, op)
}

// inflections lists the indexed words that share word's Porter stem but are
// not already matched by word as a prefix. Candidates come from the index
// vocabulary under the common prefix of word and its stem, and each is kept
// only when its own stem is the same: "retry" reaches "retries", never
// "retrieval".
func (i *Index) inflections(word string) []string {
	folded := foldWord(word)
	st := i.stems.stem(word)
	if len(st) < minStemLen || st == folded {
		return nil
	}
	prefix := commonPrefix(folded, st)
	if len(prefix) < minPrefixLen {
		return nil
	}
	rows, err := prepDB{i.stmts}.Query(`SELECT term FROM notes_vocab WHERE term >= ? AND term < ? LIMIT ?`,
		prefix, prefix+"￿", maxCandidates)
	if err != nil {
		return nil
	}
	var cands []string
	for rows.Next() {
		var term string
		if rows.Scan(&term) == nil && !strings.HasPrefix(term, folded) {
			cands = append(cands, term)
		}
	}
	rows.Close()
	var forms []string
	for k, cs := range i.stems.stems(cands) {
		if cs == st {
			forms = append(forms, cands[k])
		}
	}
	return forms
}

// foldWord lowercases a word and drops what the tokenizer would split on,
// to compare it with index terms and stems.
func foldWord(w string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, w)
}

func commonPrefix(a, b string) string {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return a[:n]
}
