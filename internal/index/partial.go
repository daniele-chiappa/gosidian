package index

import (
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
)

// Partial hits (M1.3). Every word of a query is required, so a note that
// lacks one is no hit even when it holds all the others: "tls certificate"
// missed the skill on "certificati TLS", and on the lab's vault one search in
// eight of the 1,257 of IMP-215's questions came back empty. When the notes
// with every word are fewer than the limit, the notes with one of the
// query's less common words follow them, ranked like the others and marked
// partial in Why. A word in more than commonPercent of the notes brings
// none: alone it is noise, and scoring the thousands of notes it is in made
// the p95 of a search on the benchmark's large vault three to five times
// slower.
const commonPercent = 10

// withPartial appends to full, the hits of q with every word (its parts),
// the partial hits that fit in limit.
func (i *Index) withPartial(q string, parts []queryPart, opts SearchOptions, limit int, full []rankedHit) ([]rankedHit, error) {
	s := i.partialSearch(opts)
	partial, err := s.hits(q, parts, limit, full)
	if err != nil {
		return nil, err
	}
	if err := s.label(partial); err != nil {
		return nil, err
	}
	return append(full, scoreAfter(full, partial)...), nil
}

// partialSearch is what the partial hits of one search share, so that a
// search with variants counts the notes and each word's documents once.
type partialSearch struct {
	i      *Index
	opts   SearchOptions
	most   int // the notes a word is in at most to be less common; -1 until counted
	common map[string]bool
	parts  map[string][]queryPart // each query's, for label
	held   map[string]int         // the part every partial hit of a query holds, its only rare one
}

func (i *Index) partialSearch(opts SearchOptions) *partialSearch {
	return &partialSearch{i: i, opts: opts, most: -1, common: map[string]bool{}, parts: map[string][]queryPart{}, held: map[string]int{}}
}

// hits returns the partial hits of q (its parts) that fit in limit after
// full, the hits of q with every word. Their Why waits for label.
func (s *partialSearch) hits(q string, parts []queryPart, limit int, full []rankedHit) ([]rankedHit, error) {
	if len(parts) < 2 {
		return nil, nil
	}
	s.parts[q] = parts
	var rare []queryPart
	for k, p := range parts {
		common, err := s.isCommon(p)
		if err != nil {
			return nil, err
		}
		if !common {
			rare = append(rare, p)
			s.held[q] = k
		}
	}
	if len(rare) == 0 {
		return nil, nil
	}
	if len(rare) > 1 {
		delete(s.held, q)
	}
	// A candidate keeps the query its hit came from, for label.
	cands, err := s.i.rankedMatch(joinParts(rare, " OR "), q, s.opts, limit)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(full))
	for _, h := range full {
		seen[h.Path] = true
	}
	var partial []rankedHit
	for _, h := range cands {
		if len(full)+len(partial) == limit {
			break
		}
		if !seen[h.Path] {
			partial = append(partial, h)
		}
	}
	return partial, nil
}

// isCommon reports whether the word of p is in more than commonPercent of
// the notes its expression matches. A word's own is an upper bound from the
// vocabulary. A word the tokenizer cuts in several tokens ("2026-05") is a
// phrase, in no more notes than its rarest token; the inflections the
// expression adds ("name", "names" to "naming") count too. For those two
// the bound is loose (a date's tokens are each common, the date rare): past
// most, the notes are counted. Counted on the word alone, as before, the
// first was in no note and the second in its prefix's only: both rare, and
// their hundreds of notes scored at every partial search.
func (s *partialSearch) isCommon(p queryPart) (bool, error) {
	if s.most < 0 {
		var notes int
		if err := s.i.db.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&notes); err != nil {
			return false, err
		}
		s.most = notes * commonPercent / 100
	}
	if common, ok := s.common[p.expr]; ok {
		return common, nil
	}
	docs, err := s.i.phraseDocs(p.word, s.most)
	for _, f := range p.forms {
		if err != nil || docs > s.most {
			break
		}
		var n int
		n, err = s.i.termDocs(f, false, s.most-docs)
		docs += n
	}
	if err == nil && docs > s.most && (len(tokenize(p.word)) > 1 || len(p.forms) > 0) {
		docs, err = s.i.matchDocs(p.expr, s.most)
	}
	if err != nil {
		return false, err
	}
	common := docs > s.most
	s.common[p.expr] = common
	return common, nil
}

// phraseDocs bounds the notes that word, as a search matches it (a phrase
// of its tokens, the last one a prefix), is in: the fewest of any token's,
// counted up to past most.
func (i *Index) phraseDocs(word string, most int) (int, error) {
	toks := tokenize(word)
	fewest := -1
	for k, t := range toks {
		n, err := i.termDocs(fold(word[t.start:t.end]), k == len(toks)-1, most)
		if err != nil {
			return 0, err
		}
		if fewest < 0 || n < fewest {
			fewest = n
		}
	}
	return max(fewest, 0), nil
}

// matchDocs counts the notes expr matches, up to one past most.
func (i *Index) matchDocs(expr string, most int) (int, error) {
	var n int
	err := prepDB{i.stmts}.QueryRow(`SELECT COUNT(*) FROM (SELECT 1 FROM notes_fts WHERE notes_fts MATCH ? LIMIT ?)`, expr, most+1).Scan(&n)
	return n, err
}

// termDocs counts the notes of term in the vocabulary, or as a prefix the
// documents of the terms it starts, summed (an upper bound): until they pass
// most, or maxCandidates terms, which count as past most.
func (i *Index) termDocs(term string, prefix bool, most int) (int, error) {
	if !prefix {
		var n int
		err := prepDB{i.stmts}.QueryRow(`SELECT doc FROM notes_vocab WHERE term = ?`, term).Scan(&n)
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return n, err
	}
	rows, err := prepDB{i.stmts}.Query(`SELECT doc FROM notes_vocab WHERE term >= ? AND term < ?`, term, term+"￿")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	docs, terms := 0, 0
	for rows.Next() {
		var doc int
		if err := rows.Scan(&doc); err != nil {
			return 0, err
		}
		docs += doc
		terms++
		if docs > most || terms == maxCandidates {
			return most + 1, nil
		}
	}
	return docs, rows.Err()
}

// label marks each partial hit with the words of its query its note does
// not hold: only the hits a response keeps. With one rare word the hits all
// hold it, matched by it alone, and of two words they lack the other, being
// no full hits: neither is matched again.
func (s *partialSearch) label(hits []rankedHit) error {
	byQuery := map[string][]int{}
	var queries []string
	for k, h := range hits {
		if byQuery[h.query] == nil {
			queries = append(queries, h.query)
		}
		byQuery[h.query] = append(byQuery[h.query], k)
	}
	for _, q := range queries {
		group := make([]rankedHit, len(byQuery[q]))
		for n, k := range byQuery[q] {
			group[n] = hits[k]
		}
		ask := s.parts[q]
		k, held := s.held[q]
		if held {
			ask = slices.Delete(slices.Clone(ask), k, k+1)
		}
		var missing [][]string
		if held && len(ask) == 1 {
			missing = make([][]string, len(group))
			for n := range missing {
				missing[n] = []string{ask[0].word}
			}
		} else {
			var err error
			if missing, err = s.i.missingWords(ask, group); err != nil {
				return err
			}
		}
		for n, k := range byQuery[q] {
			hits[k].Why = append([]string{`partial: without "` + strings.Join(missing[n], `", "`) + `"`}, hits[k].Why...)
			hits[k].Missing = missing[n]
		}
	}
	return nil
}

// scoreAfter scores the partial hits against the best full hit, never above
// the last one; without full hits, against the best partial one.
func scoreAfter(full, partial []rankedHit) []rankedHit {
	if len(partial) == 0 {
		return partial
	}
	best, ceiling := partial[0].score, 1.0
	if len(full) > 0 {
		best, ceiling = full[0].score, full[len(full)-1].Score
	}
	for k := range partial {
		partial[k].Score = min(relativeScore(partial[k].score, best), ceiling)
	}
	return partial
}

// missingWords lists, for each hit, the words of parts its note does not
// hold. The unary + keeps the list of rows from FTS5, as in withSnippets.
func (i *Index) missingWords(parts []queryPart, hits []rankedHit) ([][]string, error) {
	ids := make([]int64, len(hits))
	for k, h := range hits {
		ids[k] = h.id
	}
	list, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	db := prepDB{i.stmts}
	missing := make([][]string, len(hits))
	for _, p := range parts {
		rows, err := db.Query(`SELECT rowid FROM notes_fts WHERE notes_fts MATCH ? AND +rowid IN (SELECT value FROM json_each(?))`, p.expr, string(list))
		if err != nil {
			return nil, err
		}
		found := map[int64]bool{}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			found[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for k, h := range hits {
			if !found[h.id] {
				missing[k] = append(missing[k], p.word)
			}
		}
	}
	return missing, nil
}
