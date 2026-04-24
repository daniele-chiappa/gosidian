package lint

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/vault"
)

// newTestLinter wires a Linter over a fresh temp vault + index. Caller
// seeds notes via vault.Save; each seeded note is immediately reindexed.
func newTestLinter(t *testing.T) (*Linter, *vault.Vault, *index.Index) {
	t.Helper()
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	v := vault.New(dir)
	return New(v, idx), v, idx
}

// seed saves a note and upserts the index so rules relying on Outlinks/
// Backlinks/NotesByPrefix see it.
func seed(t *testing.T, v *vault.Vault, idx *index.Index, path, content string) {
	t.Helper()
	if err := v.Save(path, []byte(content)); err != nil {
		t.Fatalf("save %s: %v", path, err)
	}
	note, err := v.Load(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	if err := idx.Upsert(index.NoteDoc{
		Path:    note.Path,
		Title:   note.Title,
		Body:    string(note.Content),
		ModTime: note.ModTime.Unix(),
		Size:    note.Size,
	}); err != nil {
		t.Fatalf("upsert %s: %v", path, err)
	}
}

func TestLint_HealthyVault(t *testing.T) {
	l, v, idx := newTestLinter(t)

	seed(t, v, idx, "proj/README.md", "---\ntitle: readme\ntags: [proj, type:index]\n---\n\n# proj\n\nsee [[proj/memory/arch]]\n")
	seed(t, v, idx, "proj/memory/arch.md", "---\ntitle: arch\ntags: [proj, type:memory]\n---\n\n# arch\n\nsee [[proj/README]]\n")

	issues, err := l.Run(context.Background(), "proj", nil, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// README.md is exempt from orphan, arch.md has both in/out links. No
	// error-severity issues expected on a coherent vault.
	for _, i := range issues {
		if i.Severity == SeverityError {
			t.Errorf("healthy vault produced error-severity issue: %+v", i)
		}
	}
}

func TestLint_BrokenWikilink(t *testing.T) {
	l, v, idx := newTestLinter(t)

	seed(t, v, idx, "proj/README.md", "---\ntitle: r\ntags: [proj, type:index]\n---\n\n# r\n\nlink [[proj/nonesiste]]\n")
	seed(t, v, idx, "proj/memory/arch.md", "---\ntitle: a\ntags: [proj, type:memory]\n---\n\n# a\n")

	issues, err := l.Run(context.Background(), "proj", []string{"broken-wikilink"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 broken-wikilink issue, got %d: %+v", len(issues), issues)
	}
	if issues[0].Rule != "broken-wikilink" || issues[0].File != "proj/README.md" {
		t.Errorf("unexpected issue: %+v", issues[0])
	}
}

func TestLint_OrphanNote(t *testing.T) {
	l, v, idx := newTestLinter(t)

	seed(t, v, idx, "proj/README.md", "---\ntitle: r\ntags: [proj, type:index]\n---\n\n# r\n")
	seed(t, v, idx, "proj/memory/lonely.md", "---\ntitle: lonely\ntags: [proj, type:memory]\n---\n\n# lonely\n")

	issues, err := l.Run(context.Background(), "proj", []string{"orphan-note"}, "")
	if err != nil {
		t.Fatal(err)
	}
	// README.md is exempt; lonely.md has no in/out links.
	if len(issues) != 1 {
		t.Fatalf("expected 1 orphan-note issue, got %d: %+v", len(issues), issues)
	}
	if issues[0].File != "proj/memory/lonely.md" {
		t.Errorf("unexpected orphan file: %+v", issues[0])
	}
	// docs/ exemption: a file under docs/ should NOT be flagged.
	seed(t, v, idx, "proj/docs/bugs.md", "---\ntitle: bugs\ntags: [proj, type:doc]\n---\n\n# bugs\n")
	issues, err = l.Run(context.Background(), "proj", []string{"orphan-note"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Errorf("docs/ files must be exempt from orphan, got %d issues: %+v", len(issues), issues)
	}
}

func TestLint_FrontmatterMissing(t *testing.T) {
	l, v, idx := newTestLinter(t)

	seed(t, v, idx, "proj/ok.md", "---\ntitle: ok\ntags: [proj]\n---\n\n# ok\n")
	seed(t, v, idx, "proj/bad.md", "# bad\n\nno frontmatter here\n")

	issues, err := l.Run(context.Background(), "proj", []string{"frontmatter-missing"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].File != "proj/bad.md" || issues[0].Severity != SeverityError {
		t.Fatalf("expected 1 error on proj/bad.md, got %+v", issues)
	}
}

func TestLint_FrontmatterTagUnknown(t *testing.T) {
	l, v, idx := newTestLinter(t)

	// 3 unknown tags: "random", "topic:bogus", "status:invented".
	seed(t, v, idx, "proj/n.md", "---\ntitle: n\ntags: [proj, type:memory, random, topic:bogus, status:invented]\n---\n\n# n\n")

	issues, err := l.Run(context.Background(), "proj", []string{"frontmatter-tag-unknown"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 3 {
		t.Fatalf("expected 3 unknown-tag issues, got %d: %+v", len(issues), issues)
	}
}

func TestLint_StatusIncoherent(t *testing.T) {
	l, v, idx := newTestLinter(t)

	// hot.md without a mention of plan-a.md.
	seed(t, v, idx, "proj/hot.md", "---\ntitle: hot\ntags: [proj, type:index]\n---\n\n# hot\n\n## Active plans\n\n- [[proj/plans/plan-b]]\n")
	seed(t, v, idx, "proj/plans/plan-a.md", "---\ntitle: a\ntype: plan\nstatus: in-progress\ntags: [proj, type:plan, status:in-progress]\n---\n\n# a\n")
	seed(t, v, idx, "proj/plans/plan-b.md", "---\ntitle: b\ntype: plan\nstatus: in-progress\ntags: [proj, type:plan, status:in-progress]\n---\n\n# b\n")

	issues, err := l.Run(context.Background(), "proj", []string{"status-incoherent"}, "")
	if err != nil {
		t.Fatal(err)
	}
	// plan-b IS mentioned (wikilink), plan-a is NOT → 1 incoherence.
	if len(issues) != 1 || issues[0].File != "proj/plans/plan-a.md" {
		t.Fatalf("expected only plan-a to be flagged, got %+v", issues)
	}
}

func TestLint_UnknownRuleErrors(t *testing.T) {
	l, _, _ := newTestLinter(t)
	_, err := l.Run(context.Background(), "proj", []string{"does-not-exist"}, "")
	if err == nil {
		t.Error("expected error for unknown rule name")
	}
}

func TestLint_MinSeverityFilter(t *testing.T) {
	l, v, idx := newTestLinter(t)

	// One error (missing frontmatter) + one info (orphan, because lonely has
	// no links and no exemption).
	seed(t, v, idx, "proj/bad.md", "# no frontmatter\n")
	seed(t, v, idx, "proj/orphan.md", "---\ntitle: lonely\ntags: [proj, type:memory]\n---\n\n# lonely\n")

	errOnly, err := l.Run(context.Background(), "proj", nil, SeverityError)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range errOnly {
		if i.Severity != SeverityError {
			t.Errorf("min_severity=error leaked %+v", i)
		}
	}
	warnOnly, err := l.Run(context.Background(), "proj", nil, SeverityWarning)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range warnOnly {
		if i.Severity == SeverityInfo {
			t.Errorf("min_severity=warning leaked info %+v", i)
		}
	}
}

func TestLint_ProjectRequired(t *testing.T) {
	l, _, _ := newTestLinter(t)
	_, err := l.Run(context.Background(), "", nil, "")
	if err == nil {
		t.Error("expected error when project is empty")
	}
}
