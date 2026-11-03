package v1

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A note read says its title, the frontmatter's, as the index, the list and
// the search do: the window bar showed the file name (IMP-219, F-19).
func TestNoteTitle_GetAndExcerpt(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "p/tasks/20261031-004154-nota-delta.md", "---\ntitle: Nota delta\n---\n\nbody\n")
	f.seedNote(t, "p/plain.md", "# Plain\n\nno frontmatter\n")

	for _, tc := range []struct{ path, want string }{
		{"p/tasks/20261031-004154-nota-delta.md", "Nota delta"},
		{"p/plain.md", "plain"},
	} {
		for _, url := range []string{"/api/v1/notes/" + tc.path, "/api/v1/notes/" + tc.path + "/excerpt"} {
			rec := f.req(t, http.MethodGet, url, "", f.bearer)
			if rec.code != http.StatusOK {
				t.Fatalf("GET %s = %d %s", url, rec.code, rec.body)
			}
			var out struct {
				Title string `json:"title"`
			}
			if err := json.Unmarshal([]byte(rec.body), &out); err != nil {
				t.Fatal(err)
			}
			if out.Title != tc.want {
				t.Errorf("GET %s title = %q, want %q", url, out.Title, tc.want)
			}
		}
	}
}

// The tree gives a note's title only where it is not its file name, so a
// row can show it without a request a note (IMP-219, F-19).
func TestTree_TitleWhereItIsNotTheName(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "p/tasks/20261031-004154-nota-delta.md", "---\ntitle: Nota delta\n---\n\nbody\n")
	f.seedNote(t, "p/same.md", "---\ntitle: same\n---\n")
	f.seedNote(t, "p/plain.md", "# Plain\n")

	rec := f.req(t, http.MethodGet, "/api/v1/tree", "", f.bearer)
	if rec.code != http.StatusOK {
		t.Fatalf("tree = %d %s", rec.code, rec.body)
	}
	var out struct {
		Root *apiTreeNode `json:"root"`
	}
	if err := json.Unmarshal([]byte(rec.body), &out); err != nil {
		t.Fatal(err)
	}
	titles := map[string]string{}
	var walk func(n *apiTreeNode)
	walk = func(n *apiTreeNode) {
		if !n.IsDir {
			titles[n.Path] = n.Title
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(out.Root)
	want := map[string]string{
		"p/tasks/20261031-004154-nota-delta.md": "Nota delta",
		"p/same.md":                             "",
		"p/plain.md":                            "",
	}
	for p, w := range want {
		if got, ok := titles[p]; !ok || got != w {
			t.Errorf("tree title of %s = %q (present %v), want %q", p, got, ok, w)
		}
	}
}
