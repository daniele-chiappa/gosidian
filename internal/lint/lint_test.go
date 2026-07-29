package lint

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
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

func TestLint_FrontmatterMissing_HTMLNote(t *testing.T) {
	l, v, idx := newTestLinter(t)

	// HTML notes carry frontmatter inside a leading <!-- --> comment (ADR-011);
	// the indexer reads it the same way, so a well-formed one must NOT be
	// flagged frontmatter-missing — only a truly headerless one (BUG-012).
	seed(t, v, idx, "proj/widget.html", "<!--\n---\ntitle: widget\ntags: [proj, type:doc]\n---\n-->\n<!DOCTYPE html>\n<html><body>hi</body></html>\n")
	seed(t, v, idx, "proj/raw.html", "<!DOCTYPE html>\n<html><body>no frontmatter</body></html>\n")

	issues, err := l.Run(context.Background(), "proj", []string{"frontmatter-missing"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].File != "proj/raw.html" {
		t.Fatalf("expected only proj/raw.html flagged frontmatter-missing, got %+v", issues)
	}
}

func TestLint_FrontmatterTagUnknown_HTMLNote(t *testing.T) {
	l, v, idx := newTestLinter(t)

	// The tag rule must read an HTML note's comment-wrapped frontmatter too —
	// before the dispatch fix it silently saw no frontmatter and skipped it.
	seed(t, v, idx, "proj/w.html", "<!--\n---\ntitle: w\ntags: [proj, type:doc, status:bogus]\n---\n-->\n<html><body>x</body></html>\n")

	issues, err := l.Run(context.Background(), "proj", []string{"frontmatter-tag-unknown"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].File != "proj/w.html" {
		t.Fatalf("expected 1 unknown-tag issue on proj/w.html (status:bogus), got %+v", issues)
	}
}

func TestLint_FrontmatterTagUnknown(t *testing.T) {
	l, v, idx := newTestLinter(t)

	// 3 unknown tags: "random", "type:bogus", "status:invented".
	// (topic: is an open namespace since IMP-075, so it can't serve as an
	// unknown example anymore.)
	seed(t, v, idx, "proj/n.md", "---\ntitle: n\ntags: [proj, type:memory, random, type:bogus, status:invented]\n---\n\n# n\n")

	issues, err := l.Run(context.Background(), "proj", []string{"frontmatter-tag-unknown"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 3 {
		t.Fatalf("expected 3 unknown-tag issues, got %d: %+v", len(issues), issues)
	}
}

func TestLint_FrontmatterTagUnknown_AcceptsInsight(t *testing.T) {
	l, v, idx := newTestLinter(t)
	// type:insight and status:pending are both in the built-in vocabulary,
	// so an insight note (self-improve loop) lints clean.
	seed(t, v, idx, "proj/i.md", "---\ntitle: i\ntags: [proj, type:insight, status:pending]\n---\n\n# i\n")
	issues, err := l.Run(context.Background(), "proj", []string{"frontmatter-tag-unknown"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues for type:insight + status:pending, got %d: %+v", len(issues), issues)
	}
}

func TestLint_FrontmatterTagUnknown_AcceptsImage(t *testing.T) {
	l, v, idx := newTestLinter(t)
	// type:image is in the built-in vocabulary (ADR-013 media notes), so an
	// image media note lints clean.
	seed(t, v, idx, "proj/pic.md", "---\ntitle: pic\ntype: image\nmedia: proj/attachments/x.png\ntags: [proj, type:image]\n---\n\ncaption\n")
	issues, err := l.Run(context.Background(), "proj", []string{"frontmatter-tag-unknown"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues for type:image, got %d: %+v", len(issues), issues)
	}
}

func TestLint_FrontmatterTagUnknown_ExtraAllowed(t *testing.T) {
	// Same vault as TestLint_FrontmatterTagUnknown, but the linter has
	// been extended via WithExtraAllowedTags. The 3 tags that were
	// flagged before must now be silenced — built-in vocabulary stays
	// untouched, the extension is purely additive.
	l, v, idx := newTestLinter(t)

	seed(t, v, idx, "proj/n.md", "---\ntitle: n\ntags: [proj, type:memory, random, type:bogus, status:invented]\n---\n\n# n\n")

	l = l.WithExtraAllowedTags([]string{
		"random",
		"type:bogus",
		"status:invented",
	})

	issues, err := l.Run(context.Background(), "proj", []string{"frontmatter-tag-unknown"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues with extra-allowed configured, got %d: %+v", len(issues), issues)
	}
}

func TestLint_FrontmatterTagUnknown_ExtraAllowedSkipsMalformed(t *testing.T) {
	// Malformed extra entries (empty, leading colon, internal whitespace)
	// must be skipped silently. Valid entries from the same list still
	// take effect — a single bad entry doesn't poison the rest.
	l, v, idx := newTestLinter(t)

	seed(t, v, idx, "proj/n.md", "---\ntitle: n\ntags: [proj, mytag, topic:fine]\n---\n\n# n\n")

	l = l.WithExtraAllowedTags([]string{
		"",               // empty — skip
		":missingns",     // leading colon — skip
		"missingval:",    // trailing colon — skip
		"with space:bad", // whitespace in ns — skip
		"ns:with space",  // whitespace in val — skip
		"ns:val:extra",   // double colon — skip
		"mytag",          // valid bare → applies
		"topic:fine",     // valid namespaced → applies
	})

	issues, err := l.Run(context.Background(), "proj", []string{"frontmatter-tag-unknown"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues — malformed entries skipped, valid ones honoured. got %d: %+v", len(issues), issues)
	}
}

func TestLint_FrontmatterTagUnknown_ExtraAllowedNotMaskingOtherUnknown(t *testing.T) {
	// Belt-and-braces: configuring extras for some tags must not
	// suppress warnings for tags that are still unknown. Tag isolation.
	l, v, idx := newTestLinter(t)

	seed(t, v, idx, "proj/n.md", "---\ntitle: n\ntags: [proj, allowed-extra, still-unknown, status:another-unknown]\n---\n\n# n\n")

	l = l.WithExtraAllowedTags([]string{"allowed-extra"})

	issues, err := l.Run(context.Background(), "proj", []string{"frontmatter-tag-unknown"}, "")
	if err != nil {
		t.Fatal(err)
	}
	// "allowed-extra" silenced. "still-unknown" + "status:another-unknown"
	// still flagged.
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues (still-unknown + status:another-unknown), got %d: %+v", len(issues), issues)
	}
}

func TestLint_FrontmatterTagUnknown_TopicOpen(t *testing.T) {
	// topic: is an open namespace (the directives define `topic:<area>` as
	// free-form, IMP-075): any well-formed value passes, malformed values
	// (empty, whitespace, extra colon) still flag.
	l, v, idx := newTestLinter(t)

	seed(t, v, idx, "proj/ok.md", "---\ntitle: ok\ntags: [proj, topic:cm-clienti, topic:whatever-new-area]\n---\n\n# ok\n")
	seed(t, v, idx, "proj/bad.md", "---\ntitle: bad\ntags: [proj, \"topic:\", \"topic:has space\", \"topic:a:b\"]\n---\n\n# bad\n")

	issues, err := l.Run(context.Background(), "proj", []string{"frontmatter-tag-unknown"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 3 {
		t.Fatalf("expected 3 issues (all on proj/bad.md), got %d: %+v", len(issues), issues)
	}
	for _, is := range issues {
		if is.File != "proj/bad.md" {
			t.Fatalf("unexpected issue outside proj/bad.md: %+v", is)
		}
	}
}

func TestLint_FrontmatterTagUnknown_ExtraAllowedWildcard(t *testing.T) {
	// "cm:*" accepts any well-formed value in the cm namespace — the
	// panthera-app case (IMP-075). Other unknowns still flag, and a
	// malformed value in the wildcarded namespace still flags.
	l, v, idx := newTestLinter(t)

	seed(t, v, idx, "proj/n.md", "---\ntitle: n\ntags: [proj, cm:clienti, cm:listini-vendita, \"cm:\", other-unknown]\n---\n\n# n\n")

	l = l.WithExtraAllowedTags([]string{"cm:*"})

	issues, err := l.Run(context.Background(), "proj", []string{"frontmatter-tag-unknown"}, "")
	if err != nil {
		t.Fatal(err)
	}
	// "cm:" (empty value) + "other-unknown" flagged; cm:clienti and
	// cm:listini-vendita silenced by the wildcard.
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues (cm: + other-unknown), got %d: %+v", len(issues), issues)
	}
}

func TestLint_FrontmatterTagUnknown_ProjectVocabulary(t *testing.T) {
	// The vocabulary declared in <project>/memory/conventions.md frontmatter
	// (`tag_vocabulary:`) takes effect only when the linter is armed via
	// WithProjectTagVocabulary (wired from the project's use_tag_vocabulary
	// flag). Same vault, flag off → declaration inert.
	l, v, idx := newTestLinter(t)

	seed(t, v, idx, "proj/memory/conventions.md",
		"---\ntitle: conventions\ntags: [proj, type:memory]\ntag_vocabulary: [\"cm:*\", anagrafica, \"bad entry\"]\n---\n\n# conventions\n")
	seed(t, v, idx, "proj/n.md", "---\ntitle: n\ntags: [proj, cm:clienti, anagrafica, still-unknown]\n---\n\n# n\n")

	// Flag off: cm:clienti + anagrafica + still-unknown all flagged.
	issues, err := l.Run(context.Background(), "proj", []string{"frontmatter-tag-unknown"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 3 {
		t.Fatalf("flag off: expected 3 issues, got %d: %+v", len(issues), issues)
	}

	// Flag on: wildcard + exact entry honoured, malformed entry skipped,
	// unrelated unknown still flagged.
	l = l.WithProjectTagVocabulary(true)
	issues, err = l.Run(context.Background(), "proj", []string{"frontmatter-tag-unknown"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].File != "proj/n.md" {
		t.Fatalf("flag on: expected 1 issue (still-unknown), got %d: %+v", len(issues), issues)
	}
}

func TestLint_FrontmatterTagUnknown_ProjectVocabularyCap(t *testing.T) {
	// Entries beyond MaxProjectVocabEntries are ignored — a runaway
	// declaration cannot void the closed vocabulary.
	l, v, idx := newTestLinter(t)

	var sb strings.Builder
	sb.WriteString("---\ntitle: conventions\ntags: [proj, type:memory]\ntag_vocabulary:\n")
	for i := 0; i <= MaxProjectVocabEntries; i++ { // one entry past the cap
		fmt.Fprintf(&sb, "  - extra-%d\n", i)
	}
	sb.WriteString("---\n\n# conventions\n")
	seed(t, v, idx, "proj/memory/conventions.md", sb.String())
	seed(t, v, idx, "proj/n.md", fmt.Sprintf(
		"---\ntitle: n\ntags: [proj, extra-0, extra-%d, extra-%d]\n---\n\n# n\n",
		MaxProjectVocabEntries-1, MaxProjectVocabEntries))

	l = l.WithProjectTagVocabulary(true)
	issues, err := l.Run(context.Background(), "proj", []string{"frontmatter-tag-unknown"}, "")
	if err != nil {
		t.Fatal(err)
	}
	// extra-0 and extra-63 inside the cap → silenced; extra-64 past it → flagged.
	if len(issues) != 1 || !strings.Contains(issues[0].Message, fmt.Sprintf("extra-%d", MaxProjectVocabEntries)) {
		t.Fatalf("expected 1 issue on the past-cap entry, got %d: %+v", len(issues), issues)
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

func TestLint_UnlinkedMentions(t *testing.T) {
	l, v, idx := newTestLinter(t)

	// parser.md is the mention target.
	seed(t, v, idx, "proj/parser.md", "---\ntitle: Parser\ntags: [proj, type:memory]\n---\n\n# Parser module\n")
	// main.md names "Parser" in prose without linking it → flagged.
	seed(t, v, idx, "proj/main.md", "---\ntitle: Main\ntags: [proj, type:memory]\n---\n\nThe Parser handles syntax. See [[proj/README]].\n")
	// linked.md names "Parser" AND links it → not flagged for that target.
	seed(t, v, idx, "proj/linked.md", "---\ntitle: Linked\ntags: [proj, type:memory]\n---\n\nThe Parser is great, see [[proj/parser]].\n")
	// README.md references parser/main only inside wikilinks (stripped) → clean.
	seed(t, v, idx, "proj/README.md", "---\ntitle: README\ntags: [proj, type:index]\n---\n\nlinks [[proj/parser]] and [[proj/main]]\n")

	// Opt-in: not part of the default run.
	def, err := l.Run(context.Background(), "proj", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range def {
		if i.Rule == "unlinked-mentions" {
			t.Errorf("unlinked-mentions must be opt-in, leaked into default run: %+v", i)
		}
	}

	issues, err := l.Run(context.Background(), "proj", []string{"unlinked-mentions"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected exactly 1 unlinked-mention (main→parser), got %d: %+v", len(issues), issues)
	}
	if issues[0].File != "proj/main.md" || issues[0].Severity != SeverityInfo {
		t.Errorf("unexpected issue: %+v", issues[0])
	}
	if !strings.Contains(issues[0].Message, "proj/parser.md") {
		t.Errorf("message should name the target note: %q", issues[0].Message)
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

func TestLint_HotOversize(t *testing.T) {
	l, v, idx := newTestLinter(t)
	big := "---\ntitle: Hot\ntags: [type:index]\n---\n\n# Hot\n\n" + strings.Repeat("x", 300)
	seed(t, v, idx, "p/hot.md", big)

	// Under the (default) threshold: silent.
	issues, err := l.Run(context.Background(), "p", []string{"hot-oversize"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("under default threshold, issues = %+v", issues)
	}

	// Over a tightened threshold: one warning pointing at hot.md.
	issues, err = l.WithHotOversizeLimit(100).Run(context.Background(), "p", []string{"hot-oversize"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Rule != "hot-oversize" || issues[0].Severity != SeverityWarning || issues[0].File != "p/hot.md" {
		t.Fatalf("issues = %+v", issues)
	}

	// No hot.md at all: rule stays silent (scaffold rules cover absence).
	issues, err = l.Run(context.Background(), "empty-project", []string{"hot-oversize"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("missing hot.md must not fire hot-oversize: %+v", issues)
	}
}

func TestLint_AttachmentEmbedNotBroken(t *testing.T) {
	l, v, idx := newTestLinter(t)

	// A real webp under the vault-root attachments/ dir, embedded by bare
	// name (the Obsidian image-embed shape the UI guides use) and by
	// qualified path — neither may be flagged (they render fine, ADR-013).
	if err := v.Save("attachments/aabbccdd.webp", []byte("RIFFxxxxWEBPVP8 ")); err != nil {
		t.Fatal(err)
	}
	seed(t, v, idx, "proj/guide.md", "---\ntitle: guide\ntags: [proj, type:doc]\n---\n\n# g\n\n![[aabbccdd.webp]]\n\n![[attachments/aabbccdd.webp]]\n\n[[proj/guide]]\n")

	issues, err := l.Run(context.Background(), "proj", []string{"broken-wikilink"}, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("resolving attachment embeds must not be flagged, got: %+v", issues)
	}

	// A genuinely missing embed IS flagged, with attachment wording.
	seed(t, v, idx, "proj/bad.md", "---\ntitle: bad\ntags: [proj, type:doc]\n---\n\n# b\n\n![[phantom.webp]]\n")
	issues, err = l.Run(context.Background(), "proj", []string{"broken-wikilink"}, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "attachment") {
		t.Errorf("missing embed should be flagged with attachment wording, got: %+v", issues)
	}
}
