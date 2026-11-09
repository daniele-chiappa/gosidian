package vault

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// What names a file of the renamed project follows it, where that file is
// there under the new name: the wiki-links with its path, in the body and
// in the frontmatter, the links to its attachments and the media pointer
// (BUG-153). The rest stays as written.
func TestRewriteProjectRefs(t *testing.T) {
	const fence = "```"
	there := map[string]bool{"new/x.md": true, "new/sub/y.md": true, "new/attachments/a.png": true, "new/t.csv": true}
	exists := func(rel string) bool { return there[rel] }
	cases := []struct{ name, in, want string }{
		{"full path", "See [[old/x]].", "See [[new/x]]."},
		{"with extension", "[[old/x.md]]", "[[new/x.md]]"},
		{"subfolder, fragment and alias", "[[old/sub/y#H|why]]", "[[new/sub/y#H|why]]"},
		{"table escape kept", `| [[old/x\|first]] |`, `| [[new/x\|first]] |`},
		{"embed", "![[old/attachments/a.png]]", "![[new/attachments/a.png]]"},
		{"case of the project", "[[Old/x]]", "[[new/x]]"},
		{"in the frontmatter", "---\nfixes: [\"[[old/x]]\"]\n---\nbody", "---\nfixes: [\"[[new/x]]\"]\n---\nbody"},
		{"attachment link", "![](/vault-files/old/attachments/a.png) and [a](/vault-files/old/attachments/a.png)", "![](/vault-files/new/attachments/a.png) and [a](/vault-files/new/attachments/a.png)"},
		{"media pointer", "---\ntitle: T\nmedia: old/attachments/a.png\n---\ncap", "---\ntitle: T\nmedia: new/attachments/a.png\n---\ncap"},
		{"quoted media pointer", "---\nmedia: \"old/t.csv\"\n---\n", "---\nmedia: \"new/t.csv\"\n---\n"},
		{"media outside the frontmatter", "media: old/attachments/a.png", "media: old/attachments/a.png"},
		{"a note's design", "---\nlayout_file: old/attachments/a.png\nlayout_preview: old/attachments/a.png\nlayout_pdf: \"old/t.csv\"\n---\n", "---\nlayout_file: new/attachments/a.png\nlayout_preview: new/attachments/a.png\nlayout_pdf: \"new/t.csv\"\n---\n"},
		{"the PDFs of a design", "---\nlayout_pdfs: [\"V3: old/attachments/a.png\", \"draft: old/t.csv\", \"V4: other/t.csv\"]\n---\n", "---\nlayout_pdfs: [\"V3: new/attachments/a.png\", \"draft: new/t.csv\", \"V4: other/t.csv\"]\n---\n"},
		{"by name untouched", "[[x]] and [[sub/y]]", "[[x]] and [[sub/y]]"},
		{"another project untouched", "[[older/x]] [[other/old/x]] /vault-files/older/attachments/a.png", "[[older/x]] [[other/old/x]] /vault-files/older/attachments/a.png"},
		{"a link to nothing untouched", "[[old/gone]] ![](/vault-files/old/attachments/gone.png)", "[[old/gone]] ![](/vault-files/old/attachments/gone.png)"},
		{"the project itself untouched", "[[old]]", "[[old]]"},
		{"inline code untouched", "`[[old/x]]` and [[old/x]]", "`[[old/x]]` and [[new/x]]"},
		{"fenced code untouched", fence + "\n[[old/x]]\n/vault-files/old/attachments/a.png\n" + fence + "\n[[old/x]]", fence + "\n[[old/x]]\n/vault-files/old/attachments/a.png\n" + fence + "\n[[new/x]]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(rewriteProjectRefs([]byte(tc.in), "old", "new", exists)); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// The notes that name a file of a project, its own included; a note whose
// links lead nowhere, hidden folders and files that are not notes are left
// out.
func TestVault_ProjectReferrers(t *testing.T) {
	v := newTestVault(t)
	write(t, v.Root, "old/x.md", "# X\n[[old/y]]")
	write(t, v.Root, "old/y.md", "# Y")
	write(t, v.Root, "old/attachments/a.png", "png")
	write(t, v.Root, "b/link.md", "See [[Old/x]].")
	write(t, v.Root, "b/gone.md", "See [[old/gone]] and ![](/vault-files/old/attachments/gone.png).")
	write(t, v.Root, "b/img.md", "![](/vault-files/old/attachments/a.png)")
	write(t, v.Root, "c/media.md", "---\nmedia: old/attachments/a.png\n---\n")
	write(t, v.Root, "c/byname.md", "See [[x]].")
	write(t, v.Root, "c/older.md", "See [[older/x]] and [[other/old/x]].")
	write(t, v.Root, ".gosidian/trash/z.md", "[[old/x]]")
	write(t, v.Root, "b/data.csv", "old/x")
	got, err := v.ProjectReferrers("old")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	want := []string{"b/img.md", "b/link.md", "c/media.md", "old/x.md"}
	if !slices.Equal(got, want) {
		t.Errorf("referrers = %q, want %q", got, want)
	}
}

// A project renamed rewrites the notes it is given, its own under the new
// name, reindexes them so their links resolve, and says which it rewrote;
// a note it is not given keeps the old link (BUG-153).
func TestVault_RenameProjectRewriting(t *testing.T) {
	v := newTestVault(t)
	idx := openIndex(t)
	for _, p := range []string{"old", "b", "c"} {
		if _, err := v.CreateProject(p); err != nil {
			t.Fatal(err)
		}
	}
	write(t, v.Root, "old/x.md", "# X\nSee [[old/y]].")
	write(t, v.Root, "old/y.md", "# Y")
	write(t, v.Root, "old/attachments/a.png", "png")
	write(t, v.Root, "old/pic.md", "---\ntype: image\nmedia: old/attachments/a.png\n---\nA picture.")
	write(t, v.Root, "b/link.md", "See [[old/x]] and ![](/vault-files/old/attachments/a.png).")
	write(t, v.Root, "c/link.md", "See [[old/x]].")
	if err := v.ScanInto(idx); err != nil {
		t.Fatal(err)
	}
	_ = idx.ResolveAll()

	got, err := v.RenameProjectRewriting(idx, "old", "new", []string{"old/x.md", "old/pic.md", "b/link.md"})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if want := []string{"b/link.md", "new/pic.md", "new/x.md"}; !slices.Equal(got, want) {
		t.Errorf("rewritten = %q, want %q", got, want)
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(v.Root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if s := read("new/x.md"); s != "# X\nSee [[new/y]]." {
		t.Errorf("new/x.md = %q", s)
	}
	if s := read("new/pic.md"); s != "---\ntype: image\nmedia: new/attachments/a.png\n---\nA picture." {
		t.Errorf("new/pic.md = %q", s)
	}
	if s := read("b/link.md"); s != "See [[new/x]] and ![](/vault-files/new/attachments/a.png)." {
		t.Errorf("b/link.md = %q", s)
	}
	if s := read("c/link.md"); s != "See [[old/x]]." {
		t.Errorf("c/link.md, not given, = %q", s)
	}
	backs, err := idx.Backlinks("new/x.md")
	if err != nil {
		t.Fatal(err)
	}
	var from []string
	for _, b := range backs {
		from = append(from, b.Path)
	}
	slices.Sort(from)
	if !slices.Equal(from, []string{"b/link.md"}) {
		t.Errorf("backlinks of new/x.md = %q, want b/link.md", from)
	}
	if backs, _ := idx.Backlinks("new/y.md"); len(backs) != 1 || backs[0].Path != "new/x.md" {
		t.Errorf("backlinks of new/y.md = %+v, want new/x.md", backs)
	}
}
