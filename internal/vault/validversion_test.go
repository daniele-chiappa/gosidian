package vault

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func readNote(t *testing.T, v *Vault, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v.Root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Whoever writes a note, its valid version stays what it was: a write may
// not change it, remove it, or bring one in (IMP-201). Only SetValidVersion
// does, as that is reserved to the project's admin.
func TestVault_SaveKeepsTheValidVersion(t *testing.T) {
	v := newTestVault(t)
	if err := v.Save("p/n.md", []byte("---\ntitle: N\n---\nbody\n")); err != nil {
		t.Fatal(err)
	}
	if err := v.SetValidVersion("p/n.md", "note/p/n.md/V1"); err != nil {
		t.Fatal(err)
	}
	if got := ValidVersion("p/n.md", []byte(readNote(t, v, "p/n.md"))); got != "note/p/n.md/V1" {
		t.Fatalf("after SetValidVersion: %q in %q", got, readNote(t, v, "p/n.md"))
	}
	for _, c := range []struct{ name, write, want string }{
		{"as it is", "---\ntitle: N\nvalid_version: note/p/n.md/V1\n---\nnew\n", "---\ntitle: N\nvalid_version: note/p/n.md/V1\n---\nnew\n"},
		{"changed", "---\ntitle: N\nvalid_version: note/p/n.md/V2\n---\nnew\n", "---\ntitle: N\nvalid_version: note/p/n.md/V1\n---\nnew\n"},
		{"removed", "---\ntitle: N\n---\nnew\n", "---\ntitle: N\nvalid_version: note/p/n.md/V1\n---\nnew\n"},
		{"emptied", "---\nvalid_version:\ntitle: N\n---\nnew\n", "---\nvalid_version: note/p/n.md/V1\ntitle: N\n---\nnew\n"},
		{"no frontmatter", "new\n", "---\nvalid_version: note/p/n.md/V1\n---\nnew\n"},
	} {
		if err := v.Save("p/n.md", []byte(c.write)); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := readNote(t, v, "p/n.md"); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}

	// A write that cannot keep it does not go through.
	for _, write := range []string{"---\ntitle: never closed\n", "---\nvalid_version: |\n  x\n---\n", "---\nvalid_version: a\nvalid_version: b\n---\n"} {
		before := readNote(t, v, "p/n.md")
		if err := v.Save("p/n.md", []byte(write)); !errors.Is(err, ErrValidVersion) {
			t.Errorf("%q: %v", write, err)
		}
		if got := readNote(t, v, "p/n.md"); got != before {
			t.Errorf("%q changed the note to %q", write, got)
		}
	}

	// With it set to none a write keeps none, and the text is untouched.
	if err := v.SetValidVersion("p/n.md", ""); err != nil {
		t.Fatal(err)
	}
	if got := readNote(t, v, "p/n.md"); got != "new\n" {
		t.Errorf("after clearing: %q", got)
	}
	if err := v.Save("p/n.md", []byte("---\ntitle: N\nvalid_version: note/x/V9\n---\nnew\n")); err != nil {
		t.Fatal(err)
	}
	if got := readNote(t, v, "p/n.md"); got != "---\ntitle: N\n---\nnew\n" {
		t.Errorf("one brought in: %q", got)
	}
}

// A new note brings no valid version, as a copy of another note would. The
// body is the note's own: the words there, in any note, are text.
func TestVault_ValidVersionOnlyInTheFrontmatter(t *testing.T) {
	v := newTestVault(t)
	if err := v.Save("p/new.md", []byte("---\nvalid_version: note/x/V1\ntitle: T\n---\nbody\n")); err != nil {
		t.Fatal(err)
	}
	if got := readNote(t, v, "p/new.md"); got != "---\ntitle: T\n---\nbody\n" {
		t.Errorf("new note: %q", got)
	}
	for _, text := range []string{"valid_version: note/x/V1\n", "---\ntitle: T\n---\nvalid_version: note/x/V1\n"} {
		if err := v.Save("p/text.md", []byte(text)); err != nil {
			t.Fatal(err)
		}
		if got := readNote(t, v, "p/text.md"); got != text {
			t.Errorf("%q became %q", text, got)
		}
		if got := ValidVersion("p/text.md", []byte(text)); got != "" {
			t.Errorf("%q has the valid version %q", text, got)
		}
	}
}

// Only a markdown note has a frontmatter.
func TestVault_ValidVersionOfANoteWithout(t *testing.T) {
	v := newTestVault(t)
	v.SetHTMLNotes(true)
	text := "---\nvalid_version: note/x/V1\n---\n<p>page</p>\n"
	if err := v.Save("p/page.html", []byte(text)); err != nil {
		t.Fatal(err)
	}
	if got := readNote(t, v, "p/page.html"); got != text {
		t.Errorf("html note: %q", got)
	}
	if err := v.SetValidVersion("p/page.html", "note/x/V1"); !errors.Is(err, ErrValidVersion) {
		t.Errorf("html note: %v", err)
	}
	if err := v.SetValidVersion("p/missing.md", "note/x/V1"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing note: %v", err)
	}
}

// Two versions compare without the key, whether the frontmatter is theirs or
// the key made it; a note that cannot lose it stays as it is.
func TestWithoutVersionKeys(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"---\ntitle: T\nvalid_version: note/x/V1\n---\nbody\n", "---\ntitle: T\n---\nbody\n"},
		{"---\nvalid_version: note/x/V1\n---\nbody\n", "body\n"},
		{"---\r\nvalid_version: note/x/V1\r\n---\r\nbody\r\n", "body\r\n"},
		{"body\n", "body\n"},
		{"---\ntitle: T\n---\nvalid_version: note/x/V1\n", "---\ntitle: T\n---\nvalid_version: note/x/V1\n"},
	} {
		if got := string(WithoutVersionKeys("p/n.md", []byte(c.text))); got != c.want {
			t.Errorf("%q: %q, want %q", c.text, got, c.want)
		}
	}
	if got := string(WithoutVersionKeys("p/n.html", []byte("---\nvalid_version: x\n---\n"))); got != "---\nvalid_version: x\n---\n" {
		t.Errorf("html: %q", got)
	}
}

// A rename and a move keep it, as they move the file.
func TestVault_RenameKeepsTheValidVersion(t *testing.T) {
	v := newTestVault(t)
	if err := v.Save("p/n.md", []byte("---\ntitle: N\n---\nbody\n")); err != nil {
		t.Fatal(err)
	}
	if err := v.SetValidVersion("p/n.md", "note/p/n.md/V1"); err != nil {
		t.Fatal(err)
	}
	idx := openIndex(t)
	if err := v.ScanInto(idx); err != nil {
		t.Fatal(err)
	}
	if _, err := v.RenameNote(idx, "p/n.md", "p/m.md"); err != nil {
		t.Fatal(err)
	}
	if got := ValidVersion("p/m.md", []byte(readNote(t, v, "p/m.md"))); got != "note/p/n.md/V1" {
		t.Errorf("after the rename: %q", got)
	}
}

// A save of a note with no valid version reads the file it replaces: how
// much that costs, on a note of about 8 KiB (see write in the guard).
func BenchmarkVault_SaveNote(b *testing.B) {
	v := New(b.TempDir())
	body := []byte("---\ntitle: N\n---\n" + strings.Repeat("a line of the note\n", 420))
	for b.Loop() {
		if err := v.Save("p/n.md", body); err != nil {
			b.Fatal(err)
		}
	}
}

// The boot scan searches a note by what the index asks, on its readers, as
// an upsert does.
func TestVault_ScanSearchesTheValidVersion(t *testing.T) {
	v := newTestVault(t)
	write(t, v.Root, "p/a.md", "---\nvalid_version: note/p/a.md/V1\n---\nunapproved\n")
	idx := openIndex(t)
	idx.SetSearchText(func(path, body string) string {
		if ValidVersion(path, []byte(body)) != "" {
			return "validated\n"
		}
		return ""
	})
	if err := v.ScanInto(idx); err != nil {
		t.Fatal(err)
	}
	for q, n := range map[string]int{"validated": 1, "unapproved": 0} {
		hits, err := idx.Search(q, 10)
		if err != nil || len(hits) != n {
			t.Errorf("%q: %+v %v", q, hits, err)
		}
	}
}

// What a note chooses for who calls over MCP is kept as the valid version is,
// apart from it: a write changes neither, and each has its own way (IMP-228).
func TestVault_SaveKeepsTheMCPVersion(t *testing.T) {
	v := newTestVault(t)
	if err := v.Save("p/n.md", []byte("---\ntitle: N\n---\nbody\n")); err != nil {
		t.Fatal(err)
	}
	if err := v.SetMCPVersion("p/n.md", "note/p/n.md/V1"); err != nil {
		t.Fatal(err)
	}
	if err := v.SetValidVersion("p/n.md", "note/p/n.md/V2"); err != nil {
		t.Fatal(err)
	}
	for _, write := range []string{
		"---\ntitle: N\n---\nnew\n",
		"---\ntitle: N\nmcp_version: note/p/n.md/V9\nvalid_version: note/p/n.md/V9\n---\nnew\n",
		"new\n",
	} {
		if err := v.Save("p/n.md", []byte(write)); err != nil {
			t.Fatalf("%q: %v", write, err)
		}
		text := []byte(readNote(t, v, "p/n.md"))
		if got := MCPVersion("p/n.md", text); got != "note/p/n.md/V1" {
			t.Errorf("%q: mcp %q in %q", write, got, text)
		}
		if got := ValidVersion("p/n.md", text); got != "note/p/n.md/V2" {
			t.Errorf("%q: valid %q in %q", write, got, text)
		}
	}
	if err := v.SetValidVersion("p/n.md", ""); err != nil {
		t.Fatal(err)
	}
	if got := MCPVersion("p/n.md", []byte(readNote(t, v, "p/n.md"))); got != "note/p/n.md/V1" {
		t.Errorf("removing the valid version took the other: %q", got)
	}
	if err := v.SetMCPVersion("p/n.md", ""); err != nil {
		t.Fatal(err)
	}
	if got := readNote(t, v, "p/n.md"); got != "new\n" {
		t.Errorf("without either the frontmatter they made stays: %q", got)
	}
	if err := v.Save("p/n.md", []byte("---\nmcp_version: valid\n---\nnew\n")); err != nil {
		t.Fatal(err)
	}
	if got := readNote(t, v, "p/n.md"); got != "new\n" {
		t.Errorf("a write brought one in: %q", got)
	}
}

// A version compares without the keys that only say which one it is.
func TestWithoutVersionKeys_Both(t *testing.T) {
	got := WithoutVersionKeys("p/n.md", []byte("---\nvalid_version: a\nmcp_version: valid\ntitle: T\n---\nbody\n"))
	if string(got) != "---\ntitle: T\n---\nbody\n" {
		t.Errorf("%q", got)
	}
}

func TestChosenTags(t *testing.T) {
	for _, c := range []struct {
		text string
		want []string
	}{
		{"body\n", nil},
		{"---\nvalid_version: a\n---\n", []string{"a"}},
		{"---\nmcp_version: b\n---\n", []string{"b"}},
		{"---\nvalid_version: a\nmcp_version: b\n---\n", []string{"a", "b"}},
		{"---\nvalid_version: a\nmcp_version: a\n---\n", []string{"a"}},
		{"---\nvalid_version: a\nmcp_version: valid\n---\n", []string{"a"}},
	} {
		if got := chosenTags("p/n.md", []byte(c.text)); !slices.Equal(got, c.want) {
			t.Errorf("%q: %q, want %q", c.text, got, c.want)
		}
	}
}
