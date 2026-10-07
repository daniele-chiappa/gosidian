package lint

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
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
	v.SetHTMLNotes(true) // HTML notes are first-class in these fixtures (ADR-011)
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

// Every status of a handoff's lifecycle is the server's own tag: a claimed or
// rejected handoff lints clean too (BUG-093).
func TestLint_FrontmatterTagUnknown_AcceptsHandoffLifecycle(t *testing.T) {
	l, v, idx := newTestLinter(t)
	for _, st := range []string{"pending", "claimed", "done", "rejected"} {
		seed(t, v, idx, "proj/handoffs/h-"+st+".md", "---\ntitle: h\ntype: handoff\nstatus: "+st+"\ntags: [type:handoff, status:"+st+"]\n---\n\n# h\n")
	}
	issues, err := l.Run(context.Background(), "proj", []string{"frontmatter-tag-unknown"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues for the handoff lifecycle, got %d: %+v", len(issues), issues)
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

// TestLint_StatusIncoherent_WikilinkForms: a plan linked from hot.md by any
// wikilink the index resolves to it counts, as for broken-wikilink and the
// backlinks; only the vault path did before (BUG-080).
func TestLint_StatusIncoherent_WikilinkForms(t *testing.T) {
	l, v, idx := newTestLinter(t)
	plan := func(name string) string {
		return "---\ntitle: " + name + "\ntype: plan\nstatus: in-progress\ntags: [proj, type:plan]\n---\n\n# " + name + "\n"
	}
	seed(t, v, idx, "proj/plans/20261001-bare.md", plan("bare"))
	seed(t, v, idx, "proj/plans/20261001-relative.md", plan("relative"))
	seed(t, v, idx, "proj/plans/20261001-aliased.md", plan("aliased"))
	seed(t, v, idx, "proj/plans/20261001-missing.md", plan("missing"))
	seed(t, v, idx, "proj/hot.md", "---\ntitle: hot\ntags: [proj, type:index]\n---\n\n# hot\n\n## Active plans\n\n"+
		"- [[20261001-bare]]\n- [[plans/20261001-relative]]\n- [[20261001-aliased|the aliased plan]]\n")

	issues, err := l.Run(context.Background(), "proj", []string{"status-incoherent"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].File != "proj/plans/20261001-missing.md" {
		t.Fatalf("expected only the unlinked plan to be flagged, got %+v", issues)
	}
	if !strings.Contains(issues[0].FixHint, "[[20261001-missing]]") {
		t.Errorf("fix hint should name the link to add: %q", issues[0].FixHint)
	}
}

// TestLint_StatusIncoherent_StatusFromTag: the rule reads type and status
// as memory_query does: a status:in-progress tag counts when the note has
// no status field, the field wins when both are there (even empty), case
// is ignored.
func TestLint_StatusIncoherent_StatusFromTag(t *testing.T) {
	l, v, idx := newTestLinter(t)
	seed(t, v, idx, "proj/hot.md", "---\ntitle: hot\ntags: [proj, type:index]\n---\n\n# hot\n\n## Active plans\n\nnone\n")
	seed(t, v, idx, "proj/plans/tag-only.md", "---\ntitle: tag only\ntags: [proj, type:plan, status:in-progress]\n---\n\n# t\n")
	seed(t, v, idx, "proj/plans/upper-case.md", "---\ntitle: upper case\ntype: Plan\nstatus: In-Progress\ntags: [proj]\n---\n\n# u\n")
	seed(t, v, idx, "proj/plans/field-wins.md", "---\ntitle: field wins\nstatus: done\ntags: [proj, type:plan, status:in-progress]\n---\n\n# f\n")
	seed(t, v, idx, "proj/plans/empty-field.md", "---\ntitle: empty field\nstatus: \"\"\ntags: [proj, type:plan, status:in-progress]\n---\n\n# e\n")
	seed(t, v, idx, "proj/notes/not-a-plan.md", "---\ntitle: not a plan\ntags: [proj, status:in-progress]\n---\n\n# n\n")

	issues, err := l.Run(context.Background(), "proj", []string{"status-incoherent"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, is := range issues {
		got = append(got, is.File)
	}
	want := "proj/plans/tag-only.md proj/plans/upper-case.md"
	if strings.Join(got, " ") != want {
		t.Fatalf("flagged %v, want %s", got, want)
	}
}

// TestLint_StatusIncoherent_HintWhenBasenameIsTaken: the fix hint names the
// bare [[basename]] only when it reaches the plan; with the same basename
// in another project it names the vault path.
func TestLint_StatusIncoherent_HintWhenBasenameIsTaken(t *testing.T) {
	l, v, idx := newTestLinter(t)
	plan := "---\ntitle: r\ntype: plan\nstatus: in-progress\ntags: [type:plan]\n---\n\n# r\n"
	seed(t, v, idx, "aaa/plans/roadmap.md", plan)
	seed(t, v, idx, "proj/plans/roadmap.md", plan)
	seed(t, v, idx, "proj/hot.md", "---\ntitle: hot\ntags: [proj, type:index]\n---\n\n## Active plans\n\n- [[roadmap]]\n")

	issues, err := l.Run(context.Background(), "proj", []string{"status-incoherent"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].FixHint, "[[proj/plans/roadmap]]") {
		t.Fatalf("want one issue hinting [[proj/plans/roadmap]], got %+v", issues)
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

// unlinkedTargets runs unlinked-mentions on proj and returns "source→target"
// for each issue.
func unlinkedTargets(t *testing.T, l *Linter) []string {
	t.Helper()
	issues, err := l.Run(context.Background(), "proj", []string{"unlinked-mentions"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, i := range issues {
		target := i.Message[strings.Index(i.Message, "(note ")+6 : strings.LastIndex(i.Message, ")")]
		out = append(out, i.File+"→"+target)
	}
	sort.Strings(out)
	return out
}

// The noise IMP-121 measured: index notes as sources, labels several notes
// share, and a database's template. A plain note naming a page still counts.
func TestLint_UnlinkedMentions_Noise(t *testing.T) {
	l, v, idx := newTestLinter(t)
	seed(t, v, idx, "proj/services/gitea.md", "---\ntitle: gitea\ntags: [proj, type:memory]\n---\n\n# gitea\n")
	seed(t, v, idx, "proj/docs/README.md", "---\ntitle: Docs index\ntags: [proj, type:index]\n---\n\n# Docs\n")
	seed(t, v, idx, "proj/skills/README.md", "---\ntitle: Skills index\ntags: [proj, type:index]\n---\n\n# Skills\n")
	seed(t, v, idx, "proj/docs/improvements.md", "---\ntitle: Improvements\ntags: [proj, type:index]\ntype: database\nsource: proj/docs/improvements\ntemplate: proj/docs/templates/improvement\nfields:\n  id: {type: text}\n---\n\n# Improvements\n")
	seed(t, v, idx, "proj/docs/templates/improvement.md", "---\ntitle: improvement\n---\n\nWhich gitea repo is it about?\n")
	// An index note (type: index, here by its field) names gitea: not scanned.
	seed(t, v, idx, "proj/log.md", "---\ntitle: Log\ntype: index\n---\n\nSet up gitea and read the README.\n")
	// A plain note: gitea counts, README (two notes) and improvement (the
	// template) do not.
	seed(t, v, idx, "proj/notes/setup.md", "---\ntitle: Setup\ntags: [proj, type:memory]\n---\n\nThe gitea runner: see the README, file an improvement.\n")

	got := unlinkedTargets(t, l)
	want := []string{"proj/notes/setup.md→proj/services/gitea.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("issues = %v, want %v", got, want)
	}
}

// lint_disable in a note's frontmatter turns a rule off for that note, as a
// list or as names separated by commas, for any rule.
func TestLint_LintDisable(t *testing.T) {
	l, v, idx := newTestLinter(t)
	seed(t, v, idx, "proj/parser.md", "---\ntitle: Parser\ntags: [proj, type:memory]\n---\n\n# Parser\n")
	seed(t, v, idx, "proj/a.md", "---\ntitle: A\ntags: [proj, type:memory]\nlint_disable: [unlinked-mentions]\n---\n\nThe Parser. [[proj/b]]\n")
	seed(t, v, idx, "proj/b.md", "---\ntitle: B\ntags: [proj, type:memory]\nlint_disable: orphan-note, unlinked-mentions\n---\n\nThe Parser. [[proj/a]]\n")
	seed(t, v, idx, "proj/c.md", "---\ntitle: C\ntags: [proj, type:memory]\n---\n\nThe Parser.\n")

	if got := unlinkedTargets(t, l); strings.Join(got, ",") != "proj/c.md→proj/parser.md" {
		t.Errorf("unlinked-mentions = %v, want only c", got)
	}
	// Another rule: c is an orphan and says nothing; a lonely note that
	// turns orphan-note off is not reported.
	seed(t, v, idx, "proj/lonely.md", "---\ntitle: lonely\ntags: [proj, type:memory]\nlint_disable: orphan-note\n---\n\n# lonely\n")
	issues, err := l.Run(context.Background(), "proj", []string{"orphan-note"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var orphans []string
	for _, i := range issues {
		orphans = append(orphans, i.File)
	}
	if slices.Contains(orphans, "proj/lonely.md") || !slices.Contains(orphans, "proj/c.md") {
		t.Errorf("orphan-note on %v: want c, not lonely", orphans)
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

// Same-note links ([[#heading]], [[#^block]]) resolve on the current note in
// Obsidian; the index leaves them unresolved on purpose, and the rule must
// not flag them (BUG-065).
func TestLint_SelfLinkNotBroken(t *testing.T) {
	l, v, idx := newTestLinter(t)

	seed(t, v, idx, "proj/bugs.md", "---\ntitle: bugs\ntags: [proj, type:doc]\n---\n\n# bugs\n\n## BUG-001\n\nsee [[#BUG-001]], [[#^blk|the block]] and [[ #BUG-001 ]]\n\n[[proj/nowhere]]\n")

	issues, err := l.Run(context.Background(), "proj", []string{"broken-wikilink"}, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "proj/nowhere") {
		t.Errorf("only the cross-note link should be flagged, got: %+v", issues)
	}
}

// Template notes carry scaffold placeholders on purpose: a link to
// {{PROJECT}}/... and a {{PROJECT}} tag are not problems, a real broken link
// in the same note still is.
func TestLint_TemplatePlaceholdersNotFlagged(t *testing.T) {
	l, v, idx := newTestLinter(t)
	seed(t, v, idx, "proj/templates/team/agents/devops.md", "---\ntitle: devops\ntags: [{{PROJECT}}, type:agent]\n---\n\n# devops\n\nsee [[{{PROJECT}}/memory/environments]] and [[proj/nowhere]]\n")

	issues, err := l.Run(context.Background(), "proj", []string{"broken-wikilink", "frontmatter-tag-unknown"}, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "proj/nowhere") {
		t.Errorf("only the real broken link should be flagged, got: %+v", issues)
	}
	if broken, _, err := idx.MaintenanceCounts("proj", 0, nil); err != nil || broken != 1 {
		t.Errorf("MaintenanceCounts broken = %d (err %v), want 1: placeholders do not count", broken, err)
	}
}

// A type:skill note larger than memory_get serves whole is flagged; a large
// note of another type, or a small skill, is not (IMP-115).
func TestLint_SkillOversize(t *testing.T) {
	l, v, idx := newTestLinter(t)
	big := strings.Repeat("step text. ", 20)
	seed(t, v, idx, "p/skills/big.md", "---\ntitle: big\ntype: skill\ntags: [p, type:skill]\n---\n\n# big\n\n"+big+"\n")
	seed(t, v, idx, "p/skills/tagged.md", "---\ntitle: tagged\ntags: [p, type:skill]\n---\n\n# tagged\n\n"+big+"\n")
	seed(t, v, idx, "p/docs/big-doc.md", "---\ntitle: big doc\ntype: doc\ntags: [p, type:doc]\n---\n\n# doc\n\n"+big+"\n")
	seed(t, v, idx, "p/skills/small.md", "---\ntitle: small\ntype: skill\ntags: [p, type:skill]\n---\n\n# small\n")

	issues, err := l.WithSkillOversizeLimit(150).Run(context.Background(), "p", []string{"skill-oversize"}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, is := range issues {
		if is.Rule != "skill-oversize" || is.Severity != SeverityWarning {
			t.Errorf("unexpected issue %+v", is)
		}
		got[is.File] = true
	}
	if len(issues) != 2 || !got["p/skills/big.md"] || !got["p/skills/tagged.md"] {
		t.Errorf("want the two big skills flagged, got %+v", issues)
	}

	// The default threshold is memory_get's 24 KiB: none of these reach it.
	if issues, _ := New(v, idx).Run(context.Background(), "p", []string{"skill-oversize"}, ""); len(issues) != 0 {
		t.Errorf("default threshold flagged small notes: %+v", issues)
	}
}

func TestLint_AttachmentEmbedNotBroken(t *testing.T) {
	l, v, idx := newTestLinter(t)

	// A real webp under the vault-root attachments/ dir, embedded by bare
	// name (the Obsidian image-embed shape the UI guides use) and by
	// qualified path — neither may be flagged (they render fine, ADR-013).
	if err := v.SaveAttachment("attachments/aabbccdd.webp", []byte("RIFFxxxxWEBPVP8 "), map[string]bool{".webp": true}); err != nil {
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
