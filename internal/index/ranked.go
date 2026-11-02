package index

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Ranking (IMP-095). A search runs in two stages: SQLite picks a pool of
// candidates by weighted bm25 inside the caller's scope, then Go reorders the
// pool with a title factor and a few bounded structural factors, and
// explains each one in Why. The structural factors stay close to 1 on
// purpose: the text match decides, the structure only breaks near-ties —
// hubs such as log.md collect backlinks from templates, not from topicality.
const (
	// bm25 column weights, in notes_fts column order: title, meta (the raw
	// frontmatter: tags, description, aliases…), body. FTS5 applies them to
	// the term frequency before saturation and normalizes by the length of
	// the whole row, so they cannot lift a title hit far on their own: the
	// title factor below does that.
	weightTitle = 8.0
	weightMeta  = 3.0
	weightBody  = 1.0

	// titleBoost scales with the share of query words found in the title:
	// ×(1 + titleBoost × found/words).
	titleBoost = 0.5
	// backlinkBoost: ×(1 + backlinkBoost × ln(1+inbound notes)).
	backlinkBoost = 0.1
	// importanceStep per importance point away from the default 3.
	importanceStep = 0.1
	// recencyBoost decays with age: ×(1 + recencyBoost × e^(−days/90)),
	// skipped past recencyHorizon days.
	recencyBoost   = 0.1
	recencyHorizon = 180

	// Candidates fetched per query before the structural reorder: three
	// times the hits, at least minPool. The structure is no tie-break: on a
	// vault of 29,000 notes a title match and the backlinks of a hub lift
	// notes from past the 50th place in bm25 into the first hits. At 33
	// a search of memory_search's eleven hits lost 38 of the 836 hits a
	// pool of 50 found there, some of them first; level B's vaults never
	// showed it. A deeper pool finds more still: the hits depend on the
	// pool (IMP-215), and 50 is where it was.
	minPool = 50
	maxPool = 500
	// minFusedDepth is how deep each phrasing of a search with variants
	// ranks, at least: the fusion scores a note by its rank in every list,
	// so their depth changes the fused order, and it stays as it was.
	minFusedDepth = 50

	// snippetTokens is the length of the search snippet. 24 tokens usually
	// hold a whole sentence around the match; at 12 agents answered from a
	// fragment that stopped halfway through the fact (benchmark, IMP-096).
	snippetTokens = 24

	// MaxVariants caps SearchOptions.Variants.
	MaxVariants = 8
	// rrfK is the Reciprocal Rank Fusion constant: 1/(k+rank). At the usual
	// 60 the fusion was flat, the first place worth 1/61 and the tenth 1/70,
	// so a note in the tails of two lists outranked the first of one: on the
	// lab's 1,257 questions of IMP-215 a search with variants put the
	// expected note first less often than its query alone (R@1 0.356
	// against 0.398). At 5, 0.403, with MRR +0.045 and level R as it was.
	rrfK = 5.0

	pinnedFactor   = 1.2
	archivedFactor = 0.7
)

// longNote is the size, in bytes, from which a candidate's snippet waits
// until the candidate is a hit; tests lower it. FTS5 cuts a snippet from the
// whole body, about 10 ms a megabyte, and on a vault of long documents the
// snippets of candidates the search then left out were most of its time. A
// snippet that waits costs the match one more run, which a shorter note
// would not repay.
var longNote int64 = 8 << 10

var (
	bm25Expr    = fmt.Sprintf("bm25(notes_fts, %.1f, %.1f, %.1f)", weightTitle, weightMeta, weightBody)
	snippetExpr = fmt.Sprintf(`snippet(notes_fts, 2, '<mark>', '</mark>', '…', %d)`, snippetTokens)
)

// now is the clock for the recency factor; tests pin it.
var now = time.Now

// SearchOptions narrows and shapes a ranked search.
type SearchOptions struct {
	// Limit caps the hits (<= 0 → 50).
	Limit int
	// Projects, when non-nil, keeps only notes under these top-level folders.
	// The filter runs inside the SQL query, so the limit counts visible notes
	// only (BUG-058). A non-nil empty slice matches nothing.
	Projects []string
	// Exclude drops notes under these top-level folders.
	Exclude []string
	// Variants are alternative phrasings (synonyms, translations) searched
	// alongside the query; the ranked lists are fused by reciprocal rank.
	Variants []string
	// TextOnly ranks by weighted bm25 alone, without the title and
	// structural factors: the baseline of the retrieval benchmark (bench/).
	TextOnly bool
}

// SearchWith runs a scoped, ranked FTS search: the query and each variant
// are sanitized as in Search, ranked independently, then fused. When the
// notes with every word are fewer than the limit, partial hits follow them
// (partial.go): a query's own, or with variants those of every phrasing,
// fused.
func (i *Index) SearchWith(q string, opts SearchOptions) ([]SearchHit, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	// The SQL stage never returns more than maxPool candidates, so a larger
	// limit only sizes allocations from the caller's value (memory_search
	// asks for at most 201).
	if limit > maxPool {
		limit = maxPool
	}
	if opts.Projects != nil && len(opts.Projects) == 0 {
		return nil, nil
	}
	queries := distinctQueries(q, opts.Variants)
	if len(queries) == 0 {
		return nil, nil
	}
	if len(queries) == 1 {
		parts := i.queryParts(queries[0])
		hits, err := i.rankedMatch(joinParts(parts, " AND "), queries[0], opts, limit, longNote)
		if err != nil {
			return nil, err
		}
		if len(hits) < limit {
			if hits, err = i.withPartial(queries[0], parts, opts, limit, hits); err != nil {
				return nil, err
			}
		}
		return i.withSnippets(hits)
	}
	// Each phrasing ranks a full candidate pool, not just limit hits, so the
	// fusion can see a note that several phrasings place below the top.
	depth := min(max(limit*3, minFusedDepth), maxPool)
	lists := make([][]rankedHit, 0, len(queries))
	parts := make([][]queryPart, 0, len(queries))
	for _, query := range queries {
		ps := i.queryParts(query)
		hits, err := i.rankedMatch(joinParts(ps, " AND "), query, opts, depth, longNote)
		if err != nil {
			return nil, err
		}
		lists = append(lists, hits)
		parts = append(parts, ps)
	}
	hits := fuse(queries, lists, limit)
	if len(hits) < limit {
		// Short of the limit, the fusion holds every full hit of every
		// phrasing: the partial ones of each, fused, follow.
		s := i.partialSearch(opts)
		partial := make([][]rankedHit, len(queries))
		for k, query := range queries {
			var err error
			if partial[k], err = s.hits(query, parts[k], limit, lists[k]); err != nil {
				return nil, err
			}
		}
		seen := make(map[string]bool, len(hits))
		for _, h := range hits {
			seen[h.Path] = true
		}
		var extra []rankedHit
		for _, h := range fuse(queries, partial, limit) {
			if len(hits)+len(extra) == limit {
				break
			}
			if !seen[h.Path] {
				extra = append(extra, h)
			}
		}
		if err := s.label(extra); err != nil {
			return nil, err
		}
		hits = append(hits, scoreAfter(hits, extra)...)
	}
	return i.withSnippets(hits)
}

// distinctQueries returns q followed by the variants, trimmed, without empty
// or case-insensitive duplicates, capped at 1+MaxVariants.
func distinctQueries(q string, variants []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append([]string{q}, variants...) {
		s = strings.TrimSpace(s)
		key := strings.ToLower(s)
		if s == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
		if len(out) == 1+MaxVariants {
			break
		}
	}
	return out
}

type candidate struct {
	hit        rankedHit
	mtime      int64
	importance int
	score      float64 // text relevance, then × structural factors; unrounded
}

// rankedHit is a hit as one query ranks it, with what its snippet needs
// when it waits (longNote): the note's id, that query's MATCH expression,
// and for a huge note (hugeNote) the query itself. score is the unrounded
// relevance its Score is relative to.
type rankedHit struct {
	SearchHit
	id    int64
	size  int64
	score float64
	match string // set while the snippet waits
	query string
}

// rankedMatch returns up to limit hits of the query q, best first: the notes
// match finds. The snippets of the notes from snippetFrom bytes up are left
// to withSnippets.
func (i *Index) rankedMatch(match, q string, opts SearchOptions, limit int, snippetFrom int64) ([]rankedHit, error) {
	if match == "" {
		return nil, nil
	}
	scope, args := scopeClause(opts.Projects, opts.Exclude)
	pool := poolSize(limit)
	// Prepared once per scope shape: the text changes only with the number
	// of projects in or out, never with the query.
	rows, err := prepDB{i.stmts}.Query(`
        SELECT n.id, n.path, n.title, n.mtime, n.importance, n.size, `+bm25Expr+`,
               CASE WHEN n.size < ? THEN `+snippetExpr+` END
        FROM notes_fts
        JOIN notes n ON n.id = notes_fts.rowid
        WHERE notes_fts MATCH ?`+scope+`
        ORDER BY `+bm25Expr+`
        LIMIT ?
    `, append(append([]any{snippetFrom, match}, args...), pool)...)
	if err != nil {
		return nil, err
	}
	var cands []*candidate
	// Scanned into once per search: declared in the loop, each row's would
	// escape to the heap.
	var bm25 float64
	var snippet sql.NullString
	for rows.Next() {
		c := &candidate{}
		if err := rows.Scan(&c.hit.id, &c.hit.Path, &c.hit.Title, &c.mtime, &c.importance, &c.hit.size, &bm25, &snippet); err != nil {
			rows.Close()
			return nil, err
		}
		c.score = -bm25 // bm25() is negative, lower = better
		if snippet.Valid {
			c.hit.Snippet = snippet.String
		} else {
			c.hit.match, c.hit.query = match, q
		}
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !opts.TextOnly {
		if err := i.applyStructure(cands, q); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(cands, func(a, b int) bool {
		if cands[a].score != cands[b].score {
			return cands[a].score > cands[b].score
		}
		return cands[a].hit.Path < cands[b].hit.Path
	})
	// Sized by the candidates, which the SQL pool bounds, not by limit:
	// CodeQL does not see the cap SearchWith puts on limit (IMP-114).
	out := make([]rankedHit, 0, len(cands))
	for _, c := range cands {
		if len(out) == limit {
			break
		}
		c.hit.Score = relativeScore(c.score, cands[0].score)
		c.hit.score = c.score
		out = append(out, c.hit)
	}
	return out, nil
}

// withSnippets cuts the snippets that waited, with one statement for the
// hits of each query, and returns the hits. The statement runs the query's
// match again over the hits' rows only: the unary + keeps SQLite from
// handing the list to FTS5, which would run the whole match once per row.
// A huge note's snippet is cut from its body here (cutSnippet).
func (i *Index) withSnippets(hits []rankedHit) ([]SearchHit, error) {
	type key struct {
		match string
		id    int64
	}
	snippets := map[key]string{}
	var matches []string
	ids := map[string][]int64{}
	terms := map[string][]snippetTerm{}
	db := prepDB{i.stmts}
	for _, h := range hits {
		if h.match == "" {
			continue
		}
		if h.size >= hugeNote {
			if terms[h.query] == nil {
				terms[h.query] = i.snippetTerms(h.query)
			}
			var body string
			if err := db.QueryRow(`SELECT body FROM notes_fts WHERE rowid = ?`, h.id).Scan(&body); err != nil {
				return nil, err
			}
			snippets[key{h.match, h.id}] = cutSnippet(body, terms[h.query])
			continue
		}
		if ids[h.match] == nil {
			matches = append(matches, h.match)
		}
		ids[h.match] = append(ids[h.match], h.id)
	}
	for _, match := range matches {
		list, err := json.Marshal(ids[match])
		if err != nil {
			return nil, err
		}
		rows, err := db.Query(`
        SELECT rowid, `+snippetExpr+`
        FROM notes_fts
        WHERE notes_fts MATCH ? AND +rowid IN (SELECT value FROM json_each(?))`, match, string(list))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var snippet string
			if err := rows.Scan(&id, &snippet); err != nil {
				rows.Close()
				return nil, err
			}
			snippets[key{match, id}] = snippet
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	out := make([]SearchHit, len(hits))
	for k, h := range hits {
		if h.match != "" {
			h.Snippet = snippets[key{h.match, h.id}]
		}
		out[k] = h.SearchHit
	}
	return out, nil
}

// poolSize is how many candidates the SQL stage returns for limit hits.
func poolSize(limit int) int {
	return min(max(limit*3, minPool), maxPool)
}

// relativeScore expresses score as a fraction of the best one, rounded for
// display. Raw bm25 values are not meaningful on their own (FTS5 floors the
// IDF of a term found in most notes at 1e-6, so they can be tiny); ordering
// always uses the raw value.
func relativeScore(score, best float64) float64 {
	if best <= 0 {
		return 0
	}
	return math.Round(score/best*1000) / 1000
}

// applyStructure multiplies every candidate's text relevance by the title,
// backlink, importance, recency, pinned and archived factors and records them
// in Why.
func (i *Index) applyStructure(cands []*candidate, q string) error {
	if len(cands) == 0 {
		return nil
	}
	backlinks, tags, err := i.structureOf(cands)
	if err != nil {
		return err
	}
	terms := strings.Fields(strings.ToLower(strings.ReplaceAll(q, `"`, ``)))
	nowUnix := now().Unix()
	for _, c := range cands {
		var why []string
		score := c.score
		title := strings.ToLower(c.hit.Title)
		found := 0
		for _, t := range terms {
			if strings.Contains(title, t) {
				found++
			}
		}
		if found > 0 {
			f := 1 + titleBoost*float64(found)/float64(len(terms))
			score *= f
			why = append(why, fmt.Sprintf("title match ×%.2f", f))
		}
		if n := backlinks[c.hit.Path]; n > 0 {
			f := 1 + backlinkBoost*math.Log1p(float64(n))
			score *= f
			why = append(why, fmt.Sprintf("backlinks %d ×%.2f", n, f))
		}
		if c.importance != 3 {
			f := 1 + importanceStep*float64(c.importance-3)
			score *= f
			why = append(why, fmt.Sprintf("importance %d ×%.2f", c.importance, f))
		}
		if days := max(float64(nowUnix-c.mtime)/86400, 0); days < recencyHorizon {
			f := 1 + recencyBoost*math.Exp(-days/90)
			score *= f
			why = append(why, fmt.Sprintf("updated %dd ago ×%.2f", int(days), f))
		}
		if tags[c.hit.id]["pinned"] {
			score *= pinnedFactor
			why = append(why, fmt.Sprintf("pinned ×%.2f", pinnedFactor))
		}
		if tags[c.hit.id]["status:archived"] {
			score *= archivedFactor
			why = append(why, fmt.Sprintf("archived ×%.2f", archivedFactor))
		}
		c.score = score
		c.hit.Why = why
	}
	return nil
}

// structureOf loads, for the candidate set, the inbound-link counts (distinct
// source notes, self-links excluded) and the pinned / status:archived tags.
//
// The candidates go as JSON lists, so that the two statements have one text
// whatever their number, prepared once (stmts.go).
func (i *Index) structureOf(cands []*candidate) (map[string]int, map[int64]map[string]bool, error) {
	paths := make([]string, len(cands))
	ids := make([]int64, len(cands))
	for k, c := range cands {
		paths[k], ids[k] = c.hit.Path, c.hit.id
	}
	pathList, err := json.Marshal(paths)
	if err != nil {
		return nil, nil, err
	}
	idList, err := json.Marshal(ids)
	if err != nil {
		return nil, nil, err
	}
	db := prepDB{i.stmts}
	backlinks := map[string]int{}
	rows, err := db.Query(`
        SELECT l.target_path, COUNT(DISTINCT l.src_id)
        FROM links l JOIN notes s ON s.id = l.src_id
        WHERE l.target_path IN (SELECT value FROM json_each(?)) AND s.path <> l.target_path
        GROUP BY l.target_path`, string(pathList))
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var p string
		var n int
		if err := rows.Scan(&p, &n); err != nil {
			rows.Close()
			return nil, nil, err
		}
		backlinks[p] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	tags := map[int64]map[string]bool{}
	rows, err = db.Query(`
        SELECT note_id, tag FROM tags
        WHERE note_id IN (SELECT value FROM json_each(?)) AND tag IN ('pinned', 'status:archived')`, string(idList))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var tag string
		if err := rows.Scan(&id, &tag); err != nil {
			return nil, nil, err
		}
		if tags[id] == nil {
			tags[id] = map[string]bool{}
		}
		tags[id][tag] = true
	}
	return backlinks, tags, rows.Err()
}

// fuse merges per-query rankings with Reciprocal Rank Fusion: a note's score
// is the sum of 1/(rrfK+rank) over the lists that contain it, so notes found
// by several phrasings rise. The first list's hit (snippet, Why) is kept and
// Why gains the phrasings that matched.
func fuse(queries []string, lists [][]rankedHit, limit int) []rankedHit {
	type fused struct {
		hit     rankedHit
		score   float64
		matched []string
	}
	byPath := map[string]*fused{}
	var order []*fused
	for li, list := range lists {
		for rank, h := range list {
			f := byPath[h.Path]
			if f == nil {
				f = &fused{hit: h}
				byPath[h.Path] = f
				order = append(order, f)
			}
			f.score += 1 / (rrfK + float64(rank+1))
			f.matched = append(f.matched, queries[li])
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		if order[a].score != order[b].score {
			return order[a].score > order[b].score
		}
		return order[a].hit.Path < order[b].hit.Path
	})
	// order unions up to nine lists of maxPool each; no response holds more
	// than maxPool hits.
	out := make([]rankedHit, 0, min(len(order), maxPool))
	for _, f := range order {
		if len(out) == limit {
			break
		}
		h := f.hit
		h.Score = relativeScore(f.score, order[0].score)
		h.score = f.score
		h.Why = append(append([]string(nil), h.Why...), "matched: "+strings.Join(f.matched, " | "))
		out = append(out, h)
	}
	return out
}

// scopeClause renders the project filter as SQL on alias n: an OR of
// top-level prefixes for projects (when non-nil) and one NOT LIKE per
// excluded project.
func scopeClause(projects, exclude []string) (string, []any) {
	var b strings.Builder
	var args []any
	if projects != nil {
		b.WriteString(" AND (")
		for k, p := range projects {
			if k > 0 {
				b.WriteString(" OR ")
			}
			b.WriteString(`n.path LIKE ? ESCAPE '\'`)
			args = append(args, likeUnder(p))
		}
		b.WriteString(")")
	}
	for _, p := range exclude {
		b.WriteString(` AND n.path NOT LIKE ? ESCAPE '\'`)
		args = append(args, likeUnder(p))
	}
	return b.String(), args
}

// likeUnder is the LIKE pattern (ESCAPE '\') for every path under a
// top-level folder.
func likeUnder(project string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(project) + "/%"
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
