package index

import (
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

	// Candidates fetched per query before the structural reorder.
	minPool = 50
	maxPool = 500

	// snippetTokens is the length of the search snippet. 24 tokens usually
	// hold a whole sentence around the match; at 12 agents answered from a
	// fragment that stopped halfway through the fact (benchmark, IMP-096).
	snippetTokens = "24"

	// MaxVariants caps SearchOptions.Variants.
	MaxVariants = 8
	// rrfK is the usual Reciprocal Rank Fusion constant: 1/(k+rank).
	rrfK = 60.0

	pinnedFactor   = 1.2
	archivedFactor = 0.7
)

var bm25Expr = fmt.Sprintf("bm25(notes_fts, %.1f, %.1f, %.1f)", weightTitle, weightMeta, weightBody)

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
// are sanitized as in Search, ranked independently, then fused.
func (i *Index) SearchWith(q string, opts SearchOptions) ([]SearchHit, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	if opts.Projects != nil && len(opts.Projects) == 0 {
		return nil, nil
	}
	queries := distinctQueries(q, opts.Variants)
	if len(queries) == 0 {
		return nil, nil
	}
	if len(queries) == 1 {
		return i.rankedSearch(queries[0], opts, limit)
	}
	// Each phrasing ranks a full candidate pool, not just limit hits, so the
	// fusion can see a note that several phrasings place below the top.
	depth := poolSize(limit)
	lists := make([][]SearchHit, 0, len(queries))
	for _, query := range queries {
		hits, err := i.rankedSearch(query, opts, depth)
		if err != nil {
			return nil, err
		}
		lists = append(lists, hits)
	}
	return fuse(queries, lists, limit), nil
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
	hit        SearchHit
	id         int64
	mtime      int64
	importance int
	score      float64 // text relevance, then × structural factors; unrounded
}

// rankedSearch returns up to limit hits for one query, best first.
func (i *Index) rankedSearch(q string, opts SearchOptions, limit int) ([]SearchHit, error) {
	match := i.matchExpr(q)
	if match == "" {
		return nil, nil
	}
	scope, args := scopeClause(opts.Projects, opts.Exclude)
	pool := poolSize(limit)
	rows, err := i.db.Query(`
        SELECT n.id, n.path, n.title, n.mtime, n.importance, `+bm25Expr+`,
               snippet(notes_fts, 2, '<mark>', '</mark>', '…', `+snippetTokens+`)
        FROM notes_fts
        JOIN notes n ON n.id = notes_fts.rowid
        WHERE notes_fts MATCH ?`+scope+`
        ORDER BY `+bm25Expr+`
        LIMIT ?
    `, append(append([]any{match}, args...), pool)...)
	if err != nil {
		return nil, err
	}
	var cands []*candidate
	for rows.Next() {
		c := &candidate{}
		var bm25 float64
		if err := rows.Scan(&c.id, &c.hit.Path, &c.hit.Title, &c.mtime, &c.importance, &bm25, &c.hit.Snippet); err != nil {
			rows.Close()
			return nil, err
		}
		c.score = -bm25 // bm25() is negative, lower = better
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
	out := make([]SearchHit, 0, min(limit, len(cands)))
	for _, c := range cands[:min(limit, len(cands))] {
		c.hit.Score = relativeScore(c.score, cands[0].score)
		out = append(out, c.hit)
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
		if tags[c.id]["pinned"] {
			score *= pinnedFactor
			why = append(why, fmt.Sprintf("pinned ×%.2f", pinnedFactor))
		}
		if tags[c.id]["status:archived"] {
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
func (i *Index) structureOf(cands []*candidate) (map[string]int, map[int64]map[string]bool, error) {
	paths := make([]any, len(cands))
	ids := make([]any, len(cands))
	for k, c := range cands {
		paths[k], ids[k] = c.hit.Path, c.id
	}
	backlinks := map[string]int{}
	rows, err := i.db.Query(`
        SELECT l.target_path, COUNT(DISTINCT l.src_id)
        FROM links l JOIN notes s ON s.id = l.src_id
        WHERE l.target_path IN (`+placeholders(len(paths))+`) AND s.path <> l.target_path
        GROUP BY l.target_path`, paths...)
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
	rows, err = i.db.Query(`
        SELECT note_id, tag FROM tags
        WHERE note_id IN (`+placeholders(len(ids))+`) AND tag IN ('pinned', 'status:archived')`, ids...)
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
func fuse(queries []string, lists [][]SearchHit, limit int) []SearchHit {
	type fused struct {
		hit     SearchHit
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
	out := make([]SearchHit, 0, min(limit, len(order)))
	for _, f := range order[:min(limit, len(order))] {
		h := f.hit
		h.Score = relativeScore(f.score, order[0].score)
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
