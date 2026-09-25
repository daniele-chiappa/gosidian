package index

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func paths(hits []SearchHit) []string {
	out := make([]string, len(hits))
	for k, h := range hits {
		out[k] = h.Path
	}
	return out
}

func hasWhy(h SearchHit, prefix string) bool {
	for _, w := range h.Why {
		if strings.HasPrefix(w, prefix) {
			return true
		}
	}
	return false
}

// BUG-058: a big project whose notes outrank everything must not starve a
// filtered search of the small project's matches.
func TestSearchWith_ScopeAppliesBeforeLimit(t *testing.T) {
	idx := openTest(t)
	for k := 0; k < 60; k++ {
		upsert(t, idx, fmt.Sprintf("big/n%02d.md", k), "", fmt.Sprintf("# Docker notes %d\n\ndocker docker docker", k))
	}
	for k := 0; k < 3; k++ {
		upsert(t, idx, fmt.Sprintf("small/s%d.md", k), "", fmt.Sprintf("# Small %d\n\nlong prose that mentions docker once among many other words here", k))
	}

	hits, err := idx.SearchWith("docker", SearchOptions{Limit: 5, Projects: []string{"small"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("scoped search = %v, want the 3 small notes", paths(hits))
	}
	for _, h := range hits {
		if !strings.HasPrefix(h.Path, "small/") {
			t.Errorf("hit outside scope: %s", h.Path)
		}
	}

	if hits, _ := idx.SearchWith("docker", SearchOptions{Limit: 100, Exclude: []string{"big"}}); len(hits) != 3 {
		t.Errorf("exclude big = %v, want 3 small notes", paths(hits))
	}
	if hits, _ := idx.SearchWith("docker", SearchOptions{Projects: []string{}}); len(hits) != 0 {
		t.Errorf("empty non-nil scope must match nothing, got %v", paths(hits))
	}
	// A project name with LIKE wildcards matches literally.
	upsert(t, idx, "a_b/x.md", "", "docker")
	upsert(t, idx, "axb/y.md", "", "docker")
	if hits, _ := idx.SearchWith("docker", SearchOptions{Projects: []string{"a_b"}}); len(hits) != 1 || hits[0].Path != "a_b/x.md" {
		t.Errorf("a_b scope = %v, want only a_b/x.md", paths(hits))
	}
}

func TestSearchWith_FieldWeights(t *testing.T) {
	idx := openTest(t)
	filler := strings.Repeat("filler words about nothing in particular. ", 5)
	upsert(t, idx, "p/body.md", "", "---\ntitle: Body\n---\n\n"+filler+"kubernetes appears here.")
	upsert(t, idx, "p/meta.md", "", "---\ntitle: Meta\ntags: [kubernetes]\n---\n\n"+filler)
	upsert(t, idx, "p/title.md", "", "---\ntitle: Kubernetes guide\n---\n\n"+filler)

	hits, err := idx.Search("kubernetes", 10)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(paths(hits), ",")
	if got != "p/title.md,p/meta.md,p/body.md" {
		t.Errorf("order = %s, want title > frontmatter > body", got)
	}
	if !hasWhy(hits[0], "title match") {
		t.Errorf("title hit why = %v, want title match", hits[0].Why)
	}
	if hits[0].Score != 1 || hits[2].Score >= hits[0].Score {
		t.Errorf("scores = %v, %v, %v: want 1 for the best, then lower", hits[0].Score, hits[1].Score, hits[2].Score)
	}
}

func TestSearchWith_StructuralFactors(t *testing.T) {
	idx := openTest(t)
	body := "zebra quartz lantern"
	upsert(t, idx, "p/plain.md", "", "# Plain\n\n"+body)
	upsert(t, idx, "p/hub.md", "", "# Hub\n\n"+body)
	for k := 0; k < 3; k++ {
		upsert(t, idx, fmt.Sprintf("p/ref%d.md", k), "", "see [[p/hub]]")
	}
	upsert(t, idx, "p/pinned.md", "", "---\ntags: [pinned]\n---\n\n# Pinned\n\n"+body)
	upsert(t, idx, "p/archived.md", "", "---\ntags: [status:archived]\n---\n\n# Archived\n\n"+body)
	upsert(t, idx, "p/important.md", "", "---\nimportance: 5\n---\n\n# Important\n\n"+body)

	hits, err := idx.Search("zebra", 10)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for k, h := range hits {
		pos[h.Path] = k
	}
	for _, better := range []string{"p/hub.md", "p/pinned.md", "p/important.md"} {
		if pos[better] > pos["p/plain.md"] {
			t.Errorf("%s ranked below the plain note: %v", better, paths(hits))
		}
	}
	if pos["p/archived.md"] != len(hits)-1 {
		t.Errorf("archived note not last: %v", paths(hits))
	}
	why := map[string]SearchHit{}
	for _, h := range hits {
		why[h.Path] = h
	}
	if !hasWhy(why["p/hub.md"], "backlinks 3") {
		t.Errorf("hub why = %v", why["p/hub.md"].Why)
	}
	if !hasWhy(why["p/pinned.md"], "pinned") || !hasWhy(why["p/archived.md"], "archived") || !hasWhy(why["p/important.md"], "importance 5") {
		t.Errorf("why missing factors: %+v", hits)
	}
	if len(why["p/plain.md"].Why) != 0 {
		t.Errorf("plain note should carry no factor, got %v", why["p/plain.md"].Why)
	}
}

func TestSearchWith_Recency(t *testing.T) {
	idx := openTest(t)
	fixed := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = time.Now })
	for _, n := range []struct {
		path string
		age  time.Duration
	}{{"p/old.md", 400 * 24 * time.Hour}, {"p/fresh.md", 24 * time.Hour}} {
		if err := idx.Upsert(NoteDoc{Path: n.path, Body: "orchid", ModTime: fixed.Add(-n.age).Unix(), Size: 6}); err != nil {
			t.Fatal(err)
		}
	}
	hits, _ := idx.Search("orchid", 10)
	if len(hits) != 2 || hits[0].Path != "p/fresh.md" || !hasWhy(hits[0], "updated 1d ago") {
		t.Errorf("recency = %+v, want fresh first with its factor", hits)
	}
	if hasWhy(hits[1], "updated") {
		t.Errorf("a note older than 180 days gets no recency factor: %v", hits[1].Why)
	}
}

func TestSearchWith_VariantsFusedByRank(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "p/s.md", "", "i segreti del server")
	upsert(t, idx, "p/c.md", "", "le credenziali del server")
	upsert(t, idx, "p/both.md", "", "segreti e credenziali")
	upsert(t, idx, "p/other.md", "", "nulla di rilevante")

	hits, err := idx.SearchWith("segreti", SearchOptions{Variants: []string{"credenziali", " Segreti ", ""}})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 || hits[0].Path != "p/both.md" {
		t.Fatalf("fused = %v, want both.md first among 3", paths(hits))
	}
	if !hasWhy(hits[0], "matched: segreti | credenziali") {
		t.Errorf("why = %v, want both phrasings", hits[0].Why)
	}
	// Found by both phrasings but outranked in each list: fusion still lifts
	// it, even with a small limit.
	for k := 0; k < 5; k++ {
		upsert(t, idx, fmt.Sprintf("q/s%d.md", k), "", fmt.Sprintf("# Segreti %d\n\nsegreti segreti", k))
		upsert(t, idx, fmt.Sprintf("q/c%d.md", k), "", fmt.Sprintf("# Credenziali %d\n\ncredenziali credenziali", k))
	}
	upsert(t, idx, "q/both.md", "", "long prose "+strings.Repeat("filler ", 60)+"segreti and credenziali")
	hits, _ = idx.SearchWith("segreti", SearchOptions{Limit: 2, Projects: []string{"q"}, Variants: []string{"credenziali"}})
	if len(hits) != 2 || hits[0].Path != "q/both.md" {
		t.Errorf("deep fusion = %v, want q/both.md first", paths(hits))
	}

	if got := distinctQueries("a", []string{"b", "c", "d", "e", "f", "g", "h", "i", "j"}); len(got) != 1+MaxVariants {
		t.Errorf("variants not capped: %v", got)
	}
}

// A pre-v1 index file (two-column FTS, no importance) is upgraded in place
// by Open; the boot scan then refills the recreated FTS table.
func TestOpen_MigratesV0Index(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE notes (id INTEGER PRIMARY KEY, path TEXT NOT NULL UNIQUE, title TEXT NOT NULL, mtime INTEGER NOT NULL, size INTEGER NOT NULL)`,
		`CREATE VIRTUAL TABLE notes_fts USING fts5(title, body, tokenize='unicode61 remove_diacritics 2')`,
		`INSERT INTO notes(id, path, title, mtime, size) VALUES (1, 'p/a.md', 'A', 1, 1)`,
		`INSERT INTO notes_fts(rowid, title, body) VALUES (1, 'A', 'legacy body')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	db.Close()

	idx, err := Open(path)
	if err != nil {
		t.Fatalf("open v0 index: %v", err)
	}
	defer idx.Close()
	var v int
	if err := idx.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != schemaVersion {
		t.Fatalf("user_version = %d (%v), want %d", v, err, schemaVersion)
	}
	upsert(t, idx, "p/a.md", "A", "---\nimportance: 4\n---\n\nnew body")
	var imp int
	if err := idx.db.QueryRow(`SELECT importance FROM notes WHERE path = 'p/a.md'`).Scan(&imp); err != nil || imp != 4 {
		t.Errorf("importance = %d (%v), want 4", imp, err)
	}
	if hits, _ := idx.Search("new", 10); len(hits) != 1 {
		t.Errorf("search after migration = %+v", hits)
	}
	// Reopening a v1 file is a no-op.
	idx.Close()
	idx2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer idx2.Close()
	if hits, _ := idx2.Search("new", 10); len(hits) != 1 {
		t.Errorf("v1 reopen lost rows: %+v", hits)
	}
}
