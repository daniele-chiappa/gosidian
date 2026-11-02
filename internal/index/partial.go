package index

import (
	"encoding/json"
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
}

func (i *Index) partialSearch(opts SearchOptions) *partialSearch {
	return &partialSearch{i: i, opts: opts, most: -1, common: map[string]bool{}, parts: map[string][]queryPart{}}
}

// hits returns the partial hits of q (its parts) that fit in limit after
// full, the hits of q with every word. Their Why waits for label.
func (s *partialSearch) hits(q string, parts []queryPart, limit int, full []rankedHit) ([]rankedHit, error) {
	if len(parts) < 2 {
		return nil, nil
	}
	s.parts[q] = parts
	var rare []queryPart
	for _, p := range parts {
		common, err := s.isCommon(p.word)
		if err != nil {
			return nil, err
		}
		if !common {
			rare = append(rare, p)
		}
	}
	if len(rare) == 0 {
		return nil, nil
	}
	// Every snippet waits: most candidates are no hit. A waiting snippet
	// also keeps the query its hit came from, for label.
	cands, err := s.i.rankedMatch(joinParts(rare, " OR "), q, s.opts, limit, 0)
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

// isCommon reports whether word is in more than commonPercent of the notes.
func (s *partialSearch) isCommon(word string) (bool, error) {
	if s.most < 0 {
		var notes int
		if err := s.i.db.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&notes); err != nil {
			return false, err
		}
		s.most = notes * commonPercent / 100
	}
	key := foldWord(word)
	if common, ok := s.common[key]; ok {
		return common, nil
	}
	common, err := s.i.common(key, s.most)
	if err != nil {
		return false, err
	}
	s.common[key] = common
	return common, nil
}

// label marks each partial hit with the words of its query its note does
// not hold: only the hits a response keeps.
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
		missing, err := s.i.missingWords(s.parts[q], group)
		if err != nil {
			return err
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

// common reports whether prefix, a folded word as a search matches it, is in
// more than most notes: the documents of the indexed terms it starts, summed
// (an upper bound), until they pass most or maxCandidates terms.
func (i *Index) common(prefix string, most int) (bool, error) {
	rows, err := prepDB{i.stmts}.Query(`SELECT doc FROM notes_vocab WHERE term >= ? AND term < ?`, prefix, prefix+"￿")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	docs, terms := 0, 0
	for rows.Next() {
		var doc int
		if err := rows.Scan(&doc); err != nil {
			return false, err
		}
		docs += doc
		terms++
		if docs > most || terms == maxCandidates {
			return true, nil
		}
	}
	return false, rows.Err()
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
