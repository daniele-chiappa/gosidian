package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/projects"
)

// ---- /api/v1/tree ----

func TestTree_BuildsHierarchy(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "alpha/README.md", "# alpha")
	f.seedNote(t, "alpha/plans/p1.md", "# plan 1")
	f.seedNote(t, "alpha/notes/n1.md", "# note 1")
	f.seedNote(t, "beta/x.md", "# x")

	w := f.doAuthRecorder(http.MethodGet, "/api/v1/tree", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	for _, want := range []string{
		`"name":"alpha"`,
		`"name":"beta"`,
		`"is_project_root":true`,
		`"kind":"index"`, // README.md classifies to index
		`"kind":"plan"`,  // plans/ subfolder
	} {
		if !strings.Contains(w.body, want) {
			t.Errorf("missing %q in tree body", want)
		}
	}
}

func TestTree_ProjectFilter(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "alpha/a.md", "x")
	f.seedNote(t, "beta/b.md", "y")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/tree?project=alpha", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d", w.code)
	}
	if !strings.Contains(w.body, `"alpha/a.md"`) {
		t.Errorf("missing alpha entry: %s", w.body)
	}
	if strings.Contains(w.body, `"beta/b.md"`) {
		t.Errorf("beta leaked: %s", w.body)
	}
}

// The tree carries an ETag of what it holds (IMP-225): asked again with
// it, an unchanged tree answers 304 without a body, so a write that leaves
// the tree as it was costs the web UI nothing; a changed tree answers 200
// with a new ETag. It is never stored by the browser.
func TestTree_ETag(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "alpha/a.md", "x")
	f.seedNote(t, "beta/b.md", "y")
	const url = "/api/v1/tree?project=alpha"
	first := f.doAuthRecorder(http.MethodGet, url, "", nil)
	etag := first.headers.Get("ETag")
	if first.code != http.StatusOK || !strings.HasPrefix(etag, `W/"`) || first.headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("first: %d etag %q cache %q", first.code, etag, first.headers.Get("Cache-Control"))
	}
	again := func() *recorder {
		t.Helper()
		return f.doAuthRecorder(http.MethodGet, url, "", map[string]string{"If-None-Match": etag})
	}
	if r := again(); r.code != http.StatusNotModified || r.body != "" || r.headers.Get("ETag") != etag {
		t.Errorf("unchanged: %d %q etag %q", r.code, r.body, r.headers.Get("ETag"))
	}
	// A note of another project, and the body of one of this project, leave
	// its tree as it was.
	f.seedNote(t, "beta/c.md", "z")
	f.seedNote(t, "alpha/a.md", "another body")
	if r := again(); r.code != http.StatusNotModified {
		t.Errorf("after writes that do not change it: %d", r.code)
	}
	f.seedNote(t, "alpha/new.md", "# New")
	r := again()
	if r.code != http.StatusOK || !strings.Contains(r.body, `"alpha/new.md"`) || r.headers.Get("ETag") == etag {
		t.Errorf("a new note: %d etag %q", r.code, r.headers.Get("ETag"))
	}
}

func TestTree_AnnotatesProjectFlags(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "private/secret.md", "s")
	if err := f.projects.Set("private", projects.Flags{HiddenFromMCP: true, SkipGitSync: true}); err != nil {
		t.Fatal(err)
	}
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/tree", "", nil)
	if w.code != http.StatusOK {
		t.Fatal(w.body)
	}
	if !strings.Contains(w.body, `"hidden_from_mcp":true`) {
		t.Errorf("missing hidden_from_mcp annotation: %s", w.body)
	}
	if !strings.Contains(w.body, `"skip_git_sync":true`) {
		t.Errorf("missing skip_git_sync annotation: %s", w.body)
	}
}

// ---- /api/v1/projects ----

func TestProjects_ListEmpty(t *testing.T) {
	f := newNotesFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/projects", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `"total":0`) {
		t.Errorf("expected empty list: %s", w.body)
	}
}

func TestProjects_CreateAndList(t *testing.T) {
	f := newNotesFixture(t)
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/projects", `{"name":"alpha"}`, nil)
	if w.code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `"name":"alpha"`) {
		t.Errorf("missing name: %s", w.body)
	}
	w2 := f.doAuthRecorder(http.MethodGet, "/api/v1/projects", "", nil)
	if !strings.Contains(w2.body, `"name":"alpha"`) {
		t.Errorf("alpha not in list: %s", w2.body)
	}
}

func TestProjects_CreateDuplicate(t *testing.T) {
	f := newNotesFixture(t)
	_ = f.doAuthRecorder(http.MethodPost, "/api/v1/projects", `{"name":"alpha"}`, nil)
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/projects", `{"name":"alpha"}`, nil)
	if w.code != http.StatusConflict {
		t.Errorf("status=%d, want 409", w.code)
	}
}

func TestProjects_Get(t *testing.T) {
	f := newNotesFixture(t)
	_ = f.doAuthRecorder(http.MethodPost, "/api/v1/projects", `{"name":"alpha"}`, nil)
	f.seedNote(t, "alpha/n.md", "x")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/projects/alpha", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `"name":"alpha"`) {
		t.Errorf("missing name: %s", w.body)
	}
}

func TestProjects_GetNotFound(t *testing.T) {
	f := newNotesFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/projects/ghost", "", nil)
	if w.code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.code)
	}
}

func TestProjects_UpdateFlags(t *testing.T) {
	f := newNotesFixture(t)
	_ = f.doAuthRecorder(http.MethodPost, "/api/v1/projects", `{"name":"alpha"}`, nil)
	body := `{"hidden_from_mcp":true,"skip_git_sync":true}`
	w := f.doAuthRecorder(http.MethodPut, "/api/v1/projects/alpha", body, nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `"hidden_from_mcp":true`) {
		t.Errorf("flag not echoed: %s", w.body)
	}
	// Persisted
	if got := f.projects.Get("alpha"); !got.HiddenFromMCP || !got.SkipGitSync {
		t.Errorf("flags not persisted: %+v", got)
	}
}

func TestProjects_Rename(t *testing.T) {
	f := newNotesFixture(t)
	_ = f.doAuthRecorder(http.MethodPost, "/api/v1/projects", `{"name":"alpha"}`, nil)
	f.seedNote(t, "alpha/n.md", "body")
	body := `{"new_name":"alpha2"}`
	w := f.doAuthRecorder(http.MethodPut, "/api/v1/projects/alpha", body, nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `"name":"alpha2"`) {
		t.Errorf("rename not echoed: %s", w.body)
	}
	// Old gone, new visible
	w2 := f.doAuthRecorder(http.MethodGet, "/api/v1/projects/alpha", "", nil)
	if w2.code != http.StatusNotFound {
		t.Errorf("old name still found: %d", w2.code)
	}
	w3 := f.doAuthRecorder(http.MethodGet, "/api/v1/projects/alpha2", "", nil)
	if w3.code != http.StatusOK {
		t.Errorf("new name missing: %d", w3.code)
	}
}

func TestProjects_RenameWithFlags(t *testing.T) {
	f := newNotesFixture(t)
	_ = f.doAuthRecorder(http.MethodPost, "/api/v1/projects", `{"name":"alpha"}`, nil)
	if err := f.projects.Set("alpha", projects.Flags{HiddenFromMCP: true}); err != nil {
		t.Fatal(err)
	}
	body := `{"new_name":"alpha2"}`
	if w := f.doAuthRecorder(http.MethodPut, "/api/v1/projects/alpha", body, nil); w.code != http.StatusOK {
		t.Fatalf("rename failed: %s", w.body)
	}
	// Flags follow rename
	if got := f.projects.Get("alpha2"); !got.HiddenFromMCP {
		t.Errorf("flags lost on rename: %+v", got)
	}
}

func TestProjects_DeleteNotFound(t *testing.T) {
	f := newNotesFixture(t)
	w := f.doAuthRecorder(http.MethodDelete, "/api/v1/projects/ghost", "", nil)
	if w.code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.code)
	}
}

func TestProjects_DeleteHard(t *testing.T) {
	f := newNotesFixture(t)
	_ = f.doAuthRecorder(http.MethodPost, "/api/v1/projects", `{"name":"alpha"}`, nil)
	f.seedNote(t, "alpha/n.md", "x")
	w := f.doAuthRecorder(http.MethodDelete, "/api/v1/projects/alpha", "", nil)
	if w.code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	w2 := f.doAuthRecorder(http.MethodGet, "/api/v1/projects/alpha", "", nil)
	if w2.code != http.StatusNotFound {
		t.Errorf("project still exists after delete: %d", w2.code)
	}
}

// ---- /api/v1/tags ----

func TestTags_ListAndPerName(t *testing.T) {
	f := newNotesFixture(t)
	// Notes with tags via frontmatter (parser.Extract picks them up)
	f.seedNote(t, "a.md", "---\ntags: [type:plan, status:in-progress]\n---\n# a")
	f.seedNote(t, "b.md", "---\ntags: [type:plan]\n---\n# b")
	f.seedNote(t, "c.md", "---\ntags: [type:doc]\n---\n# c")

	w := f.doAuthRecorder(http.MethodGet, "/api/v1/tags", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	for _, want := range []string{`"type:plan"`, `"type:doc"`, `"status:in-progress"`} {
		if !strings.Contains(w.body, want) {
			t.Errorf("missing tag %q: %s", want, w.body)
		}
	}

	w2 := f.doAuthRecorder(http.MethodGet, "/api/v1/tags/type:plan", "", nil)
	if w2.code != http.StatusOK {
		t.Fatalf("status=%d", w2.code)
	}
	if !strings.Contains(w2.body, `"a.md"`) || !strings.Contains(w2.body, `"b.md"`) {
		t.Errorf("missing matches: %s", w2.body)
	}
	if strings.Contains(w2.body, `"c.md"`) {
		t.Errorf("c.md should not match: %s", w2.body)
	}
}

func TestTags_PerNameProjectFilter(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "alpha/a.md", "---\ntags: [shared]\n---\n# a")
	f.seedNote(t, "beta/b.md", "---\ntags: [shared]\n---\n# b")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/tags/shared?project=alpha", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `"alpha/a.md"`) {
		t.Errorf("missing alpha: %s", w.body)
	}
	if strings.Contains(w.body, `"beta/b.md"`) {
		t.Errorf("beta leaked: %s", w.body)
	}
}

// ---- /api/v1/search ----

func TestSearch_Hits(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "x.md", "the quick brown fox")
	f.seedNote(t, "y.md", "lazy dog by the river")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/search?q=fox", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `"x.md"`) {
		t.Errorf("missing match: %s", w.body)
	}
}

func TestSearch_RequiresQ(t *testing.T) {
	f := newNotesFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/search", "", nil)
	if w.code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.code)
	}
}

func TestSearch_ProjectScope(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "alpha/x.md", "shared content")
	f.seedNote(t, "beta/y.md", "shared content")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/search?q=shared&project=alpha", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d", w.code)
	}
	if !strings.Contains(w.body, `"alpha/x.md"`) {
		t.Errorf("missing alpha hit: %s", w.body)
	}
	if strings.Contains(w.body, `"beta/y.md"`) {
		t.Errorf("beta leaked into project=alpha: %s", w.body)
	}
}

func TestSearch_StripsMarkTags(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "a.md", "snippet body with keyword inside it")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/search?q=keyword", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d", w.code)
	}
	if strings.Contains(w.body, "<mark>") || strings.Contains(w.body, "</mark>") {
		t.Errorf("snippet not stripped: %s", w.body)
	}
}

// A partial hit says which words of the query its note lacks; a full one
// carries no missing at all.
func TestSearch_MissingWordsOfPartialHits(t *testing.T) {
	f := newNotesFixture(t)
	for i := range 30 {
		f.seedNote(t, fmt.Sprintf("w%02d.md", i), "weather report")
	}
	f.seedNote(t, "full.md", "gopher burrow")
	f.seedNote(t, "half.md", "gopher tunnel")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/search?q=gopher+burrow", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	var out struct {
		Hits []map[string]any `json:"hits"`
	}
	if err := json.Unmarshal([]byte(w.body), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Hits) != 2 || out.Hits[0]["path"] != "full.md" || out.Hits[1]["path"] != "half.md" {
		t.Fatalf("hits = %s, want full.md then half.md", w.body)
	}
	if _, ok := out.Hits[0]["missing"]; ok {
		t.Errorf("the full hit carries missing: %s", w.body)
	}
	if m, _ := out.Hits[1]["missing"].([]any); len(m) != 1 || m[0] != "burrow" {
		t.Errorf("half.md missing = %v, want [burrow]", out.Hits[1]["missing"])
	}
}
