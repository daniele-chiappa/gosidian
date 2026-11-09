package gitsync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/vault"
)

func commitSHA(t *testing.T, s *Sync, rel string, at int) string {
	t.Helper()
	h, err := s.History(rel, 0)
	if err != nil || len(h) <= at {
		t.Fatalf("history of %s: %+v %v", rel, h, err)
	}
	return h[at].SHA
}

// Without a sha, what is pending is committed and the tag goes on the
// note's last commit, in the name of who asked, naming the note; the same
// label on the same commit is the same tag, on another commit it is
// refused, as a tag never moves (IMP-201).
func TestSync_TagNote(t *testing.T) {
	s, dir, write := startWrites(t)
	ctx := context.Background()
	write("p/a.md", "V1\n")
	flushNow(s)
	write("p/other.md", "other\n")
	flushNow(s)
	write("p/a.md", "V2\n")

	done, err := s.TagNote(ctx, "p/a.md", "", "V2", "", "daniele")
	if err != nil {
		t.Fatal(err)
	}
	last := commitSHA(t, s, "p/a.md", 0)
	if done.Name != "note/p/a.md/V2" || done.Label != "V2" || done.SHA != last || !strings.HasPrefix(last, done.ShortSHA) || done.Path != "p/a.md" || done.By != "daniele" || done.Date.IsZero() {
		t.Fatalf("tag %+v, last commit %s", done, last)
	}
	if got := gitOut(t, dir, "show", "note/p/a.md/V2:p/a.md"); got != "V2" {
		t.Errorf("the note from its tag: %s", got)
	}
	if got := gitOut(t, dir, "cat-file", "-t", "refs/tags/note/p/a.md/V2"); got != "tag" {
		t.Errorf("not an annotated tag: %s", got)
	}
	if got := gitOut(t, dir, "for-each-ref", "--format=%(taggername)", "refs/tags/note/p/a.md/V2"); got != "daniele" {
		t.Errorf("tagger %s", got)
	}

	again, err := s.TagNote(ctx, "p/a.md", "", "V2", "", "someone else")
	if err != nil || again.Name != done.Name || again.SHA != done.SHA || again.By != "daniele" {
		t.Fatalf("again: %+v %v", again, err)
	}

	write("p/a.md", "V3\n")
	flushNow(s)
	if _, err := s.TagNote(ctx, "p/a.md", "", "V2", "", "daniele"); !errors.Is(err, ErrLabelTaken) {
		t.Fatalf("moving a tag: %v", err)
	}
	if got := gitOut(t, dir, "rev-parse", "note/p/a.md/V2^{commit}"); got != done.SHA {
		t.Errorf("the tag moved to %s", got)
	}
}

// With a sha the tag goes on that version, which must be one of the note;
// what the sync says about a note with no version.
func TestSync_TagNoteOnAVersion(t *testing.T) {
	s, _, write := startWrites(t)
	ctx := context.Background()
	write("p/a.md", "V1\n")
	flushNow(s)
	old := commitSHA(t, s, "p/a.md", 0)
	write("p/other.md", "other\n")
	flushNow(s)
	foreign := commitSHA(t, s, "p/other.md", 0)
	write("p/a.md", "V2\n")
	flushNow(s)

	done, err := s.TagNote(ctx, "p/a.md", old[:8], "V1", "first", "daniele")
	if err != nil || done.SHA != old || done.Message != "first" {
		t.Fatalf("%+v %v", done, err)
	}
	if _, err := s.TagNote(ctx, "p/a.md", foreign, "V9", "", "daniele"); !errors.Is(err, ErrNoVersion) {
		t.Errorf("a commit of another note: %v", err)
	}
	if _, err := s.TagNote(ctx, "p/missing.md", "", "V1", "", "daniele"); !errors.Is(err, ErrNoVersion) {
		t.Errorf("no such note: %v", err)
	}

	cfg := testCfg()
	cfg.Enabled = false
	if _, err := New(t.TempDir(), cfg).TagNote(ctx, "p/a.md", "", "V1", "", "d"); !errors.Is(err, ErrDisabled) {
		t.Errorf("git sync off: %v", err)
	}
	if _, err := New(t.TempDir(), cfg).NoteTags("p/a.md"); !errors.Is(err, ErrDisabled) {
		t.Errorf("git sync off: %v", err)
	}
	if _, err := New(t.TempDir(), cfg).TagVersion("p/a.md", "note/x/V1", 1); !errors.Is(err, ErrDisabled) {
		t.Errorf("git sync off: %v", err)
	}
}

// A commit can hold more notes: a tag naming one is not the other's, while
// a pipeline's tag, which names none, is of the commit and so of each.
func TestSync_NoteTagsOfACommit(t *testing.T) {
	s, dir, write := startWrites(t)
	ctx := context.Background()
	write("p/a.md", "a\n")
	write("p/b.md", "b\n")
	flushNow(s)
	sha := commitSHA(t, s, "p/a.md", 0)
	if sha != commitSHA(t, s, "p/b.md", 0) {
		t.Fatal("the two notes are in two commits")
	}
	if _, err := s.TagNote(ctx, "p/a.md", "", "V1", "", "daniele"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitNow(ctx, "revision V2", "doc/manual/V2"); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "tag", "light/one", sha)

	names := func(rel string) string {
		t.Helper()
		tags, err := s.NoteTags(rel)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, tag := range tags {
			out = append(out, tag.Name)
		}
		return strings.Join(out, " ")
	}
	if got := names("p/a.md"); got != "doc/manual/V2 light/one note/p/a.md/V1" {
		t.Errorf("tags of a: %s", got)
	}
	if got := names("p/b.md"); got != "doc/manual/V2 light/one" {
		t.Errorf("tags of b: %s", got)
	}
}

// The tags come newest first, and a rename keeps them: the version reads
// under the name it had, and a label already used under that name is
// refused under the new one.
func TestSync_NoteTagsFollowARename(t *testing.T) {
	s, dir, write := startWrites(t)
	ctx := context.Background()
	write("p/a.md", "first version of the note\nwith some lines\nto follow\n")
	flushNow(s)
	if _, err := s.TagNote(ctx, "p/a.md", "", "V1", "first", "daniele"); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "mv", "p/a.md", "p/b.md")
	flushNow(s)
	write("p/b.md", "first version of the note\nwith some lines\nto follow\nand more\n")
	flushNow(s)
	if _, err := s.TagNote(ctx, "p/b.md", "", "V2", "", "daniele"); err != nil {
		t.Fatal(err)
	}
	tags, err := s.NoteTags("p/b.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 || tags[0].Label != "V2" || tags[0].Path != "p/b.md" || tags[0].Message != "V2 of p/b.md" || tags[1].Label != "V1" || tags[1].Path != "p/a.md" || tags[1].Message != "first" {
		t.Fatalf("tags %+v", tags)
	}
	v, err := s.TagVersion("p/b.md", "note/p/a.md/V1", 1<<20)
	if err != nil || string(v.Content) != "first version of the note\nwith some lines\nto follow\n" || v.Path != "p/a.md" {
		t.Fatalf("V1 under its name: %+v %v", v, err)
	}
	if _, err := s.TagNote(ctx, "p/b.md", "", "V1", "", "daniele"); !errors.Is(err, ErrLabelTaken) {
		t.Errorf("a label used before the rename: %v", err)
	}
}

// A tag that is not there, or is no name of git's, reads as no version;
// nor does a real tag of a commit outside the note's history.
func TestSync_TagVersionRefusals(t *testing.T) {
	s, dir, write := startWrites(t)
	write("p/a.md", "a\n")
	flushNow(s)
	write("p/b.md", "b\n")
	flushNow(s)
	gitOut(t, dir, "tag", "other/one", commitSHA(t, s, "p/b.md", 0))
	for _, tag := range []string{"", "note/missing", "note/a b", "-x", "note/x^{tree}", "other/one"} {
		if v, err := s.TagVersion("p/a.md", tag, 1<<20); !errors.Is(err, ErrNoVersion) {
			t.Errorf("TagVersion(%q) = %+v %v", tag, v, err)
		}
	}
}

// A note's path makes a tag's name as it is when git takes it. When it
// does not, it is made valid with a mark of the real path, so two notes
// never share a tag; too long, the mark is enough. The .md stays: a file
// is no folder, so no tag sits where another needs a folder.
func TestNoteTagName(t *testing.T) {
	for _, c := range []struct{ rel, want string }{
		{"p/a.md", "note/p/a.md/V3"},
		{"p/a/V3.md", "note/p/a/V3.md/V3"},
		{"libretti/città/scuotitore.md", "note/libretti/città/scuotitore.md/V3"},
		{"p/my note.md", "note/p/my-note.md-"},
		{"p/my-note [1].md", "note/p/my-note--1].md-"},
		{"p/a..b.md", "note/"},
		{"p/x.lock/a.md", "note/"},
		{strings.Repeat("long/", 60) + "a.md", "note/"},
	} {
		got, err := NoteTagName(c.rel, "V3")
		if err != nil {
			t.Errorf("%s: %v", c.rel, err)
			continue
		}
		if !strings.HasPrefix(got, c.want) || !strings.HasSuffix(got, "/V3") {
			t.Errorf("%s: %s, want %s…/V3", c.rel, got, c.want)
		}
		if err := CheckTag(got); err != nil {
			t.Errorf("%s: %v", c.rel, err)
		}
	}
	a, _ := NoteTagName("p/my note.md", "V3")
	b, _ := NoteTagName("p/my~note.md", "V3")
	if a == b {
		t.Errorf("two notes share %s", a)
	}
	for _, label := range []string{"", " ", "a/b", "a b", "a..b", "x.lock", ".x", strings.Repeat("x", MaxTagLabel+1)} {
		if got, err := NoteTagName("p/a.md", label); !errors.Is(err, ErrBadLabel) {
			t.Errorf("label %q: %s %v", label, got, err)
		}
	}
}

// A whole name finds its tag, and a label the one tag that has it.
func TestFindTag(t *testing.T) {
	tags := []NoteTag{{Name: "note/p/a.md/V1", Label: "V1"}, {Name: "doc/a/V2", Label: "V2"}, {Name: "note/p/a.md/V2", Label: "V2"}}
	for _, c := range []struct {
		want, name string
	}{{"V1", "note/p/a.md/V1"}, {"doc/a/V2", "doc/a/V2"}, {"V2", ""}, {"V3", ""}} {
		got, ok := FindTag(tags, c.want)
		if got.Name != c.name || ok != (c.name != "") {
			t.Errorf("FindTag(%q) = %+v %v", c.want, got, ok)
		}
	}
}

// The valid version a note names reads with its text, and says whether the
// note is still that text, but for the key; a tag that gives no text says
// so, and a note without one has none. A nil Sync, for a server without git
// sync, says the same as a tag that is not there.
func TestSync_Valid(t *testing.T) {
	s, _, write := startWrites(t)
	ctx := context.Background()
	write("p/a.md", "---\ntitle: A\n---\nV1\n")
	flushNow(s)
	done, err := s.TagNote(ctx, "p/a.md", "", "V1", "", "daniele")
	if err != nil {
		t.Fatal(err)
	}
	if v := s.Valid("p/a.md", []byte("---\ntitle: A\n---\nV1\n"), 1<<20); v != nil {
		t.Fatalf("a note without one: %+v", v)
	}
	same := []byte("---\ntitle: A\nvalid_version: " + done.Name + "\n---\nV1\n")
	v := s.Valid("p/a.md", same, 1<<20)
	if v == nil || v.Tag != done.Name || v.Label != "V1" || v.Missing || !v.Same || v.SHA != done.SHA || string(v.Content) != "---\ntitle: A\n---\nV1\n" {
		t.Fatalf("the note as the version: %+v", v)
	}
	// What a tag says of its commit is what the history says.
	h, err := s.Version("p/a.md", done.SHA, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if v.Author != h.Author || !v.Date.Equal(h.Date) || v.Subject != h.Subject || v.ShortSHA != h.ShortSHA || v.Path != h.Path || v.Size != h.Size {
		t.Errorf("from the tag %+v, from the history %+v", v.Version.Commit, h.Commit)
	}
	draft := []byte("---\ntitle: A\nvalid_version: " + done.Name + "\n---\ndraft\n")
	if v := s.Valid("p/a.md", draft, 1<<20); v == nil || v.Same || v.Missing {
		t.Errorf("a draft: %+v", v)
	}
	for _, c := range []struct {
		name string
		v    *Valid
	}{
		{"too large", s.Valid("p/a.md", draft, 1)},
		{"no such tag", s.Valid("p/a.md", []byte("---\nvalid_version: note/none/V1\n---\n"), 1<<20)},
		{"no git sync", (*Sync)(nil).Valid("p/a.md", draft, 1<<20)},
	} {
		if c.v == nil || !c.v.Missing || c.v.Same || c.v.Label == "" {
			t.Errorf("%s: %+v", c.name, c.v)
		}
	}
}

// The valid version a note names reads under the name it had, with no
// commit of the rename yet: the history does not know it, and the index asks
// at once. A tag asked by name still takes the history (IMP-201).
func TestSync_ValidAfterARename(t *testing.T) {
	s, dir, write := startWrites(t)
	ctx := context.Background()
	body := "first version of the note\nwith some lines\nto follow\n"
	write("p/a.md", body)
	flushNow(s)
	done, err := s.TagNote(ctx, "p/a.md", "", "V1", "", "d")
	if err != nil {
		t.Fatal(err)
	}
	text := "---\nvalid_version: " + done.Name + "\n---\ndraft\n"
	if err := os.Rename(filepath.Join(dir, "p/a.md"), filepath.Join(dir, "p/b.md")); err != nil {
		t.Fatal(err)
	}
	write("p/b.md", text)

	v := s.Valid("p/b.md", []byte(text), 1<<20)
	if v == nil || v.Missing || v.Path != "p/a.md" || string(v.Content) != body {
		t.Fatalf("after the rename: %+v", v)
	}
	if got := s.ValidText("p/b.md", text); got != body {
		t.Errorf("the text: %q", got)
	}
	if texts, err := s.ValidTexts("p/b.md", done.Name); err != nil || len(texts) != 1 || string(texts[0]) != body {
		t.Errorf("the GC's texts: %q %v", texts, err)
	}
	if _, err := s.TagVersion("p/b.md", done.Name, 1<<20); !errors.Is(err, ErrNoVersion) {
		t.Errorf("a tag by name, no commit of the rename: %v", err)
	}
}

// A pipeline's tag names no note: it reads at the note's path, and its walk
// finds it under an older name once the rename is in the history.
func TestSync_ValidOfAPipelineTag(t *testing.T) {
	s, dir, write := startWrites(t)
	ctx := context.Background()
	body := "first version of the note\nwith some lines\nto follow\n"
	write("p/a.md", body)
	if _, err := s.CommitNow(ctx, "revision V1", "doc/manual/V1"); err != nil {
		t.Fatal(err)
	}
	text := "---\nvalid_version: doc/manual/V1\n---\n" + body
	if v := s.Valid("p/a.md", []byte(text), 1<<20); v == nil || v.Missing || string(v.Content) != body || !v.Same {
		t.Errorf("at its path: %+v", v)
	}
	gitOut(t, dir, "mv", "p/a.md", "p/b.md")
	flushNow(s)
	if v := s.Valid("p/b.md", []byte(text), 1<<20); v == nil || v.Missing || v.Path != "p/a.md" {
		t.Errorf("under an older name: %+v", v)
	}
}

// Only a note that names a version has another text to search.
func TestSync_ValidTextOfAPlainNote(t *testing.T) {
	s, _, write := startWrites(t)
	write("p/a.md", "text\n")
	flushNow(s)
	for _, body := range []string{"text\n", "the words valid_version: in the body\n", "---\nvalid_version: note/none/V1\n---\ntext\n"} {
		if got := s.ValidText("p/a.md", body); got != "" {
			t.Errorf("%q: %q", body, got)
		}
	}
	if got := (*Sync)(nil).ValidText("p/a.md", "---\nvalid_version: x/V1\n---\n"); got != "" {
		t.Errorf("no git sync: %q", got)
	}
}

// What a valid version names: its text and its design's document, as the
// commit holds them, whatever the disk has now. The GC counts them, and a
// version chosen long after brings back what it lost.
func TestSync_VersionFiles(t *testing.T) {
	s, dir, write := startWrites(t)
	ctx := context.Background()
	text := "---\ntitle: A\nlayout_file: p/attachments/d1.json\n---\n![x](attachments/photo.png)\n"
	write("p/a.md", text)
	write("p/attachments/d1.json", `{"assets":[{"link":"placed.png"}]}`)
	write("p/attachments/placed.png", "PLACED")
	write("p/attachments/photo.png", "PHOTO")
	write("p/attachments/other.png", "OTHER")
	flushNow(s)
	done, err := s.TagNote(ctx, "p/a.md", "", "V1", "", "d")
	if err != nil {
		t.Fatal(err)
	}

	texts, err := s.ValidTexts("p/a.md", done.Name)
	if err != nil || len(texts) != 2 || string(texts[0]) != text || !strings.Contains(string(texts[1]), "placed.png") {
		t.Fatalf("texts %q %v", texts, err)
	}
	for _, c := range []*Sync{s, nil} {
		if texts, err := c.ValidTexts("p/a.md", "note/none/V1"); err != nil || texts != nil {
			t.Errorf("no such tag: %q %v", texts, err)
		}
	}

	// The draft goes on, and the GC takes the design and the images.
	write("p/a.md", "draft\n")
	for _, f := range []string{"d1.json", "placed.png", "photo.png", "other.png"} {
		if err := os.Remove(filepath.Join(dir, "p/attachments", f)); err != nil {
			t.Fatal(err)
		}
	}
	flushNow(s)
	v, err := s.TagVersion("p/a.md", done.Name, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	vlt := vault.New(dir)
	restored, err := s.RestoreFiles(v, vlt, map[string]bool{".png": true, ".json": true})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(restored)
	if got := strings.Join(restored, " "); got != "p/attachments/d1.json p/attachments/photo.png p/attachments/placed.png" {
		t.Errorf("restored %s", got)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "p/attachments/photo.png")); err != nil || string(b) != "PHOTO" {
		t.Errorf("the image %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "p/attachments/other.png")); err == nil {
		t.Error("a file the version does not name came back")
	}
	if restored, err := s.RestoreFiles(v, vlt, map[string]bool{".png": true, ".json": true}); err != nil || restored != nil {
		t.Errorf("again: %q %v", restored, err)
	}

	if _, err := s.FileAt(v.SHA, "p/attachments/none.png", 0); !errors.Is(err, ErrNoVersion) {
		t.Errorf("no such file: %v", err)
	}
	if _, err := s.FileAt(v.SHA, "p/attachments/photo.png", 2); !errors.Is(err, ErrNoVersion) {
		t.Errorf("too large: %v", err)
	}
}

// With push on, the tag reaches the remote; a failed push leaves it here
// and says why.
func TestSync_TagNotePushes(t *testing.T) {
	requireGit(t)
	bare := filepath.Join(t.TempDir(), "remote.git")
	gitOut(t, ".", "init", "-q", "--bare", bare)
	dir := t.TempDir()
	s := startSync(t, dir, func(s *Sync) { s.cfg.Push, s.cfg.Remote = true, bare })
	ctx := context.Background()

	writeFile(t, dir, "p/a.md", "V1")
	done, err := s.TagNote(ctx, "p/a.md", "", "V1", "", "daniele")
	if err != nil || !done.Pushed || done.PushError != "" {
		t.Fatalf("%+v %v", done, err)
	}
	if got := gitOut(t, bare, "rev-parse", "note/p/a.md/V1^{commit}"); got != done.SHA {
		t.Errorf("remote: %s", got)
	}

	gitOut(t, dir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
	writeFile(t, dir, "p/a.md", "V2")
	v2, err := s.TagNote(ctx, "p/a.md", "", "V2", "", "daniele")
	if err != nil || v2.Pushed || v2.PushError == "" {
		t.Fatalf("a failed push: %+v %v", v2, err)
	}
	if got := gitOut(t, dir, "rev-parse", "note/p/a.md/V2^{commit}"); got != v2.SHA {
		t.Errorf("the tag here: %s", got)
	}
}

// What a note gives who calls over MCP reads as the valid version does; it
// can follow that one, which may be none (IMP-228).
func TestSync_ForMCP(t *testing.T) {
	s, _, write := startWrites(t)
	ctx := context.Background()
	write("p/a.md", "V1\n")
	flushNow(s)
	done, err := s.TagNote(ctx, "p/a.md", "", "V1", "", "daniele")
	if err != nil {
		t.Fatal(err)
	}
	if v := s.ForMCP("p/a.md", []byte("V1\n"), 1<<20); v != nil {
		t.Fatalf("a note without a choice: %+v", v)
	}
	mcp := "---\nmcp_version: " + done.Name + "\n---\n"
	if v := s.ForMCP("p/a.md", []byte(mcp+"draft\n"), 1<<20); v == nil || v.Label != "V1" || v.Same || v.Missing || string(v.Content) != "V1\n" {
		t.Errorf("a tag: %+v", v)
	}
	if v := s.ForMCP("p/a.md", []byte(mcp+"V1\n"), 1<<20); v == nil || !v.Same {
		t.Errorf("the note is the version: %+v", v)
	}
	if v := s.ForMCP("p/a.md", []byte("---\nmcp_version: valid\n---\ndraft\n"), 1<<20); v != nil {
		t.Errorf("following a valid version that is not there: %+v", v)
	}
	both := "---\nvalid_version: " + done.Name + "\nmcp_version: valid\n---\ndraft\n"
	if v := s.ForMCP("p/a.md", []byte(both), 1<<20); v == nil || v.Label != "V1" || v.Missing {
		t.Errorf("following the valid version: %+v", v)
	}
	if v := s.ForMCP("p/a.md", []byte("---\nmcp_version: note/none/V1\n---\n"), 1<<20); v == nil || !v.Missing {
		t.Errorf("no such tag: %+v", v)
	}
	if v := (*Sync)(nil).ForMCP("p/a.md", []byte(mcp), 1<<20); v == nil || !v.Missing {
		t.Errorf("no git sync: %+v", v)
	}
}
