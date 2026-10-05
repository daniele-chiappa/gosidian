package views

import (
	"strings"
	"testing"
	"time"
)

// A frozen note has its views, values and embeds as text: read again, it
// computes nothing.
func TestFreeze(t *testing.T) {
	idx, notes := embedIndex(t)
	c := embedContext(idx, notes, "p/notes/a.md")
	body := notes["p/notes/a.md"] + "\nNotes: `=count(p/notes)`.\n\n```view\nfrom: p/notes\nsort: path asc\n```\n"
	out, st := Freeze([]byte(body), c, idx.Query)
	s := string(out)
	if st.Views != 2 || st.Values != 1 || st.Embeds != 1 {
		t.Errorf("stats = %+v", st)
	}
	for _, want := range []string{"Notes: 3 (`` `=count(p/notes)` ``).", "- [[p/notes/b\\|B]]", "| title |", "[[p/notes/a\\|A]]", `class="gosidian-embed-start"`} {
		if !strings.Contains(s, want) {
			t.Errorf("frozen note lacks %q:\n%s", want, s)
		}
	}
	if len(FindBlocks(out)) != 0 || len(FindValues(out)) != 0 || CountEmbeds(out, c) != 0 || strings.Contains(s, "![[") {
		t.Errorf("a frozen note computes nothing when read:\n%s", s)
	}
}

func TestSnapshotPathAndNote(t *testing.T) {
	now := time.Date(2026, 10, 4, 21, 30, 5, 0, time.UTC)
	taken := map[string]bool{}
	exists := func(p string) bool { return taken[p] }
	for _, want := range []string{
		"p/hot.snapshots/2026-10-04.md", "p/hot.snapshots/2026-10-04-2130.md", "p/hot.snapshots/2026-10-04-213005.md",
	} {
		got := SnapshotPath("p/hot.md", now, exists)
		if got != want {
			t.Errorf("path = %s, want %s", got, want)
		}
		taken[got] = true
	}
	note := string(SnapshotNote("p/docs/plan.md", "plan", "p", []byte("---\ntitle: 'Plan \"A\"'\ntags: [p, type:plan]\n---\n\n# Plan\n\nBody.\n"), now))
	for _, want := range []string{
		"title: \"Plan \\\"A\\\" — snapshot 2026-10-04 21:30\"\n", "type: snapshot\n", "source: \"[[p/docs/plan]]\"\n",
		"date: 2026-10-04T21:30:05Z\n", "tags: [p]\n", "> Snapshot of [[p/docs/plan]] taken 2026-10-04 21:30 UTC", "# Plan\n\nBody.\n",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	if strings.Contains(note, "type:plan") {
		t.Error("the snapshot of a plan carries the project's tag only")
	}
}
