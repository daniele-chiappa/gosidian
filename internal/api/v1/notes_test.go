package v1

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/gosidian/gosidian/internal/webauth"
)

// notesFixture wires the full stack notes endpoints need: real vault
// directory + SQLite index + parser renderer + auth machinery. The
// vault root is a t.TempDir so each test owns its own filesystem.
type notesFixture struct {
	*authFixture
	vaultRoot string
	idx       *index.Index
	projects  *projects.Store
	bearer    string
}

func newNotesFixture(t *testing.T) *notesFixture {
	t.Helper()
	dir := t.TempDir()

	wa, err := webauth.Open(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wa.Setup("owner", "supersecret", false, "test"); err != nil {
		t.Fatal(err)
	}
	owner := wa.FirstOwner()
	spa, _ := auth.OpenSpaTokens(filepath.Join(dir, "spa.json"))
	al, _ := audit.Open(filepath.Join(dir, "audit.jsonl"))

	vaultRoot := filepath.Join(dir, "vault")
	if err := os.MkdirAll(vaultRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	v := vault.New(vaultRoot)

	idx, err := index.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })

	hub := events.New(events.HubOptions{Logger: slog.Default()})
	pstore, err := projects.Open(filepath.Join(dir, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(&Deps{
		Auth:     &AuthDeps{WebAuth: wa, SpaAuth: spa, Logger: slog.Default()},
		Audit:    al,
		Vault:    v,
		Events:   hub,
		Index:    idx,
		Renderer: parser.NewRenderer(),
		Projects: pstore,
	})

	// Mint a token so tests don't repeat the login dance for every
	// request. This bypasses the public /login path; the auth flow
	// itself is covered by auth_test.go.
	bearer, _, err := spa.Create(owner.ID, "test-agent")
	if err != nil {
		t.Fatal(err)
	}

	af := &authFixture{
		t:         t,
		router:    router,
		webauth:   wa,
		spaTokens: spa,
		auditLog:  al,
		username:  "owner",
		password:  "supersecret",
		owner:     owner,
	}
	return &notesFixture{authFixture: af, vaultRoot: vaultRoot, idx: idx, projects: pstore, bearer: bearer}
}

func (f *notesFixture) doAuthRecorder(method, path, body string, extra map[string]string) *recorder {
	headers := map[string]string{"Authorization": "Bearer " + f.bearer}
	for k, v := range extra {
		headers[k] = v
	}
	w := f.request(method, path, body, headers)
	return &recorder{code: w.Code, body: w.Body.String(), headers: w.Header()}
}

type recorder struct {
	code    int
	body    string
	headers http.Header
}

// seedNote writes a note directly through the vault + index so tests
// can populate state without reaching for the public POST endpoint.
func (f *notesFixture) seedNote(t *testing.T, rel, content string) {
	t.Helper()
	if err := f.vaultRoot != "" && os.MkdirAll(filepath.Dir(filepath.Join(f.vaultRoot, rel)), 0o755) == nil; !err {
		// MkdirAll handled by vault.Save indirectly; ignore false negative.
	}
	v := vault.New(f.vaultRoot)
	if err := v.Save(rel, []byte(content)); err != nil {
		t.Fatal(err)
	}
	note, err := v.Load(rel)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.idx.Upsert(index.NoteDoc{
		Path:    note.Path,
		Title:   note.Title,
		Body:    string(note.Content),
		ModTime: note.ModTime.Unix(),
		Size:    note.Size,
	}); err != nil {
		t.Fatal(err)
	}
}

// ---- tests ----

func TestNotes_CreateAndRead(t *testing.T) {
	f := newNotesFixture(t)
	body := `{"path":"hello.md","content":"# hi\n\nbody"}`
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/notes", body, nil)
	if w.code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `"path":"hello.md"`) {
		t.Errorf("missing path in body: %s", w.body)
	}
	if etag := w.headers.Get("ETag"); etag == "" {
		t.Errorf("missing ETag header")
	}

	// Read back
	w2 := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/hello.md", "", nil)
	if w2.code != http.StatusOK {
		t.Fatalf("read: status=%d body=%s", w2.code, w2.body)
	}
	if !strings.Contains(w2.body, `"# hi`) {
		t.Errorf("body not returned: %s", w2.body)
	}
	if w2.headers.Get("ETag") == "" {
		t.Errorf("missing ETag on read")
	}
}

func TestNotes_CreateRejectsDuplicate(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "exists.md", "old")
	body := `{"path":"exists.md","content":"new"}`
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/notes", body, nil)
	if w.code != http.StatusConflict {
		t.Errorf("status=%d, want 409", w.code)
	}
}

func TestNotes_CreateRejectsBadPath(t *testing.T) {
	f := newNotesFixture(t)
	for _, bad := range []string{
		`{"path":"../escape.md","content":""}`,
		`{"path":"no-extension","content":""}`,
		`{"path":"","content":""}`,
	} {
		w := f.doAuthRecorder(http.MethodPost, "/api/v1/notes", bad, nil)
		if w.code != http.StatusBadRequest {
			t.Errorf("path=%q got status=%d, want 400 body=%s", bad, w.code, w.body)
		}
	}
}

func TestNotes_ReadNotFound(t *testing.T) {
	f := newNotesFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/ghost.md", "", nil)
	if w.code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.code)
	}
}

func TestNotes_Read304IfNoneMatch(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "x.md", "hello")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/x.md", "", nil)
	etag := w.headers.Get("ETag")
	if etag == "" {
		t.Fatal("no etag")
	}
	w2 := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/x.md", "",
		map[string]string{"If-None-Match": etag})
	if w2.code != http.StatusNotModified {
		t.Errorf("status=%d, want 304", w2.code)
	}
}

func TestNotes_UpdateWithIfMatch(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "doc.md", "v1")
	r := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/doc.md", "", nil)
	etag := r.headers.Get("ETag")
	if etag == "" {
		t.Fatal("no etag")
	}
	w := f.doAuthRecorder(http.MethodPut, "/api/v1/notes/doc.md",
		`{"content":"v2"}`, map[string]string{"If-Match": etag})
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `"content":"v2"`) {
		t.Errorf("body not updated: %s", w.body)
	}
	newEtag := w.headers.Get("ETag")
	if newEtag == "" || newEtag == etag {
		t.Errorf("etag should rotate after update: old=%q new=%q", etag, newEtag)
	}
}

func TestNotes_UpdateRejectsStaleIfMatch(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "doc.md", "v1")
	w := f.doAuthRecorder(http.MethodPut, "/api/v1/notes/doc.md",
		`{"content":"v2"}`, map[string]string{"If-Match": `"stale-etag"`})
	if w.code != http.StatusPreconditionFailed {
		t.Fatalf("status=%d, want 412 body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, CodeConcurrencyEtag) {
		t.Errorf("missing etag mismatch code: %s", w.body)
	}
	if !strings.Contains(w.body, "current_etag") {
		t.Errorf("missing current_etag in details: %s", w.body)
	}
}

func TestNotes_UpdateWithoutIfMatchAllowed(t *testing.T) {
	// If-Match is optional — skipping it lets the SPA force-overwrite
	// after a manual conflict resolution. Documented in the SPA's
	// ConflictDialog.
	f := newNotesFixture(t)
	f.seedNote(t, "doc.md", "v1")
	w := f.doAuthRecorder(http.MethodPut, "/api/v1/notes/doc.md",
		`{"content":"forced"}`, nil)
	if w.code != http.StatusOK {
		t.Errorf("status=%d body=%s", w.code, w.body)
	}
}

func TestNotes_UpdateNotFound(t *testing.T) {
	f := newNotesFixture(t)
	w := f.doAuthRecorder(http.MethodPut, "/api/v1/notes/missing.md",
		`{"content":"x"}`, nil)
	if w.code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.code)
	}
}

func TestNotes_DeleteHard(t *testing.T) {
	// Trash is nil by default in this fixture, so DELETE hard-deletes.
	f := newNotesFixture(t)
	f.seedNote(t, "tmp.md", "x")
	w := f.doAuthRecorder(http.MethodDelete, "/api/v1/notes/tmp.md", "", nil)
	if w.code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	w2 := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/tmp.md", "", nil)
	if w2.code != http.StatusNotFound {
		t.Errorf("note still readable after delete: %d", w2.code)
	}
}

func TestNotes_DeleteNotFound(t *testing.T) {
	f := newNotesFixture(t)
	w := f.doAuthRecorder(http.MethodDelete, "/api/v1/notes/ghost.md", "", nil)
	if w.code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.code)
	}
}

func TestNotes_ListVaultWide(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "a.md", "1")
	f.seedNote(t, "sub/b.md", "2")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/notes", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `"a.md"`) || !strings.Contains(w.body, `"sub/b.md"`) {
		t.Errorf("missing entries: %s", w.body)
	}
	if !strings.Contains(w.body, `"total":2`) {
		t.Errorf("missing total: %s", w.body)
	}
}

func TestNotes_ListProjectFilter(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "alpha/a.md", "1")
	f.seedNote(t, "beta/b.md", "2")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/notes?project=alpha", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d", w.code)
	}
	if !strings.Contains(w.body, `"alpha/a.md"`) {
		t.Errorf("missing alpha: %s", w.body)
	}
	if strings.Contains(w.body, `"beta/b.md"`) {
		t.Errorf("beta leaked: %s", w.body)
	}
}

func TestNotes_ListPagination(t *testing.T) {
	f := newNotesFixture(t)
	for i := 0; i < 5; i++ {
		f.seedNote(t, "n"+string(rune('a'+i))+".md", "x")
	}
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/notes?limit=2&offset=1", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d", w.code)
	}
	if !strings.Contains(w.body, `"total":5`) {
		t.Errorf("total wrong: %s", w.body)
	}
	if !strings.Contains(w.body, `"limit":2`) {
		t.Errorf("limit wrong: %s", w.body)
	}
}

func TestPreview_RendersMarkdown(t *testing.T) {
	f := newNotesFixture(t)
	body := `{"markdown":"# Hello\n\nWorld"}`
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/preview", body, nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	// json.Encoder html-escapes <, > and &, so the wire form has
	// `<h1`. Decode to recover the rendered HTML.
	var decoded previewResponse
	if err := json.NewDecoder(strings.NewReader(w.body)).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(decoded.HTML, "<h1") || !strings.Contains(decoded.HTML, "Hello") {
		t.Errorf("missing rendered heading: %q", decoded.HTML)
	}
}

// `![[logo.png]]` takes the embedding note's own attachment first, and
// never one from a project the reader cannot see: the first file of that
// name in the whole vault exposed a private project's path (BUG-112, S2-7).
func TestPreview_ImageEmbedResolution(t *testing.T) {
	f := newNotesFixture(t)
	f.writeRaw(t, "Beta/attachments/logo.png", "PNG")
	f.writeRaw(t, "Zeta/attachments/logo.png", "PNG")
	f.writeRaw(t, "Open/attachments/other.png", "PNG")
	f.setVisibility(t, "Open", projects.VisibilityInternal)
	render := func(note, bearer string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"markdown": "![[logo.png]]", "path": note})
		rec := f.req(t, http.MethodPost, "/api/v1/preview", string(body), bearer)
		var out previewResponse
		if err := json.NewDecoder(strings.NewReader(rec.body)).Decode(&out); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.body)
		}
		return out.HTML
	}
	if html := render("Zeta/n.md", f.bearer); !strings.Contains(html, "/vault-files/Zeta/attachments/logo.png") {
		t.Errorf("the note's own attachment is not first: %s", html)
	}
	m, bearer := f.memberUser(t, "mel")
	if err := f.webauth.SetRestricted(m.ID, false); err != nil {
		t.Fatal(err)
	}
	if html := render("Open/n.md", bearer); strings.Contains(html, "Beta/") || strings.Contains(html, "Zeta/") {
		t.Errorf("a private project's attachment reached a member: %s", html)
	}
}

func TestPreview_EmptyBody(t *testing.T) {
	f := newNotesFixture(t)
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/preview", `{"markdown":""}`, nil)
	if w.code != http.StatusOK {
		t.Errorf("status=%d body=%s", w.code, w.body)
	}
}

func TestNoteTitles_FetchesByQuery(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "meeting.md", "# Meeting notes")
	f.seedNote(t, "todo.md", "# Todo")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/note-titles?q=meeting", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, "meeting.md") {
		t.Errorf("missing match: %s", w.body)
	}
}

func TestNoteTitles_EmptyQueryReturnsAll(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "a.md", "x")
	f.seedNote(t, "b.md", "y")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/note-titles", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d", w.code)
	}
	if !strings.Contains(w.body, "a.md") {
		t.Errorf("missing a.md: %s", w.body)
	}
}

func TestNotes_RequiresAuth(t *testing.T) {
	f := newNotesFixture(t)
	w := f.request(http.MethodGet, "/api/v1/notes/anything.md", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", w.Code)
	}
}

// IMP-127: a ```view block is rendered as its computed table, with links
// the SPA can follow, and this.<field> resolves against the previewed note.
func TestPreview_RendersViews(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "p/docs/improvements/IMP-001.md", "---\ntitle: \"IMP-001 — open one\"\nid: IMP-001\nstatus: open\n---\n")
	f.seedNote(t, "p/docs/improvements/IMP-002.md", "---\ntitle: \"IMP-002 — done one\"\nid: IMP-002\nstatus: done\n---\n")
	f.seedNote(t, "p/plans/plan.md", "---\ntitle: The plan\nimplements_imp: [IMP-002]\n---\n")
	md := "---\nid: IMP-002\n---\n# Hot\n\n```view\nfrom: p/docs/improvements\nwhere:\n  - status = open\ncolumns: [id, title]\n```\n\n" +
		"```view\nfrom: p/plans\nwhere:\n  - implements_imp contains this.id\n```\n"
	req, _ := json.Marshal(map[string]string{"markdown": md, "path": "p/docs/improvements/IMP-002.md"})
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/preview", string(req), nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	var decoded previewResponse
	if err := json.NewDecoder(strings.NewReader(w.body)).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	h := decoded.HTML
	for _, want := range []string{`class="gosidian-view"`, "<table", `data-preview-path="p/docs/improvements/IMP-001.md"`, `data-preview-path="p/plans/plan.md"`} {
		if !strings.Contains(h, want) {
			t.Errorf("preview lacks %q:\n%s", want, h)
		}
	}
	if strings.Contains(h, "IMP-002 — done one") || strings.Contains(h, "from: p/docs") {
		t.Errorf("preview shows a filtered row or the raw spec:\n%s", h)
	}
}

// BUG-086: a full-path wikilink without extension resolves in the preview.
func TestPreview_ResolvesFullPathLinks(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "p/plans/a-plan.md", "---\ntitle: Something else\n---\n")
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/preview", `{"markdown":"See [[p/plans/a-plan]] and [[p/plans/a-plan.md|x]]."}`, nil)
	var decoded previewResponse
	if err := json.NewDecoder(strings.NewReader(w.body)).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(decoded.HTML, "unresolved") || strings.Count(decoded.HTML, `data-preview-path="p/plans/a-plan.md"`) != 2 {
		t.Errorf("full-path links unresolved:\n%s", decoded.HTML)
	}
}
