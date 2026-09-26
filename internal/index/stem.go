package index

import (
	"database/sql"
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
)

// stemmer derives English Porter stems with SQLite's own FTS5 porter
// tokenizer, run on a private in-memory database: the stems are the ones
// FTS5 itself would produce, with no stemming code or dependency of our own.
// The index keeps its plain tokens; stems only add the inflections of a
// search word to the query (see matchExpr).
type stemmer struct {
	mu sync.Mutex
	db *sql.DB
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
	return &stemmer{db: db}, nil
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
	defer s.db.Exec(`DELETE FROM w`)
	tx, err := s.db.Begin()
	if err != nil {
		return out
	}
	for k, w := range words {
		if _, err := tx.Exec(`INSERT INTO w(rowid, t) VALUES(?, ?)`, k+1, w); err != nil {
			tx.Rollback()
			return out
		}
	}
	if err := tx.Commit(); err != nil {
		return out
	}
	rows, err := s.db.Query(`SELECT doc, term FROM v`)
	if err != nil {
		return out
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
	return out
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
	var parts []string
	for _, t := range strings.Fields(q) {
		t = strings.ReplaceAll(t, `"`, ``)
		if t == "" {
			continue
		}
		part := `"` + t + `"*`
		if forms := i.inflections(t); len(forms) > 0 {
			part = `(` + part + ` OR "` + strings.Join(forms, `" OR "`) + `")`
		}
		parts = append(parts, part)
	}
	// Explicit AND: FTS5 rejects an implicit one after a parenthesized group.
	return strings.Join(parts, " AND ")
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
	rows, err := i.db.Query(`SELECT term FROM notes_vocab WHERE term >= ? AND term < ? LIMIT ?`,
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
