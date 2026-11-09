package v1

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/layout"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/vault"
)

type versionsBody struct {
	Items    []noteTagView `json:"items"`
	Valid    *validView    `json:"valid"`
	MCP      *mcpView      `json:"mcp"`
	CanAdmin bool          `json:"can_admin"`
}

func (f *notesFixture) versions(t *testing.T, rel, bearer string) versionsBody {
	t.Helper()
	rec := f.req(t, http.MethodGet, "/api/v1/notes/"+rel+"/versions", "", bearer)
	if rec.code != http.StatusOK {
		t.Fatalf("versions of %s = %d %s", rel, rec.code, rec.body)
	}
	var out versionsBody
	if err := json.Unmarshal([]byte(rec.body), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *notesFixture) tagNote(t *testing.T, rel, label string) noteTagView {
	t.Helper()
	rec := f.req(t, http.MethodPost, "/api/v1/notes/"+rel+"/versions", `{"label":"`+label+`"}`, f.bearer)
	if rec.code != http.StatusCreated {
		t.Fatalf("tag %s = %d %s", label, rec.code, rec.body)
	}
	var out struct {
		Tag noteTagView `json:"tag"`
	}
	if err := json.Unmarshal([]byte(rec.body), &out); err != nil {
		t.Fatal(err)
	}
	return out.Tag
}

func (f *notesFixture) setValid(t *testing.T, rel, tag string) {
	t.Helper()
	if rec := f.req(t, http.MethodPut, "/api/v1/notes/"+rel+"/valid", `{"tag":"`+tag+`"}`, f.bearer); rec.code != http.StatusOK {
		t.Fatalf("valid %s = %d %s", tag, rec.code, rec.body)
	}
}

func (f *notesFixture) readNoteBody(t *testing.T, rel, query, bearer string) noteResponse {
	t.Helper()
	rec := f.req(t, http.MethodGet, "/api/v1/notes/"+rel+query, "", bearer)
	if rec.code != http.StatusOK {
		t.Fatalf("read %s%s = %d %s", rel, query, rec.code, rec.body)
	}
	var out noteResponse
	if err := json.Unmarshal([]byte(rec.body), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *notesFixture) disk(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.vaultRoot, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// An admin names a version and makes it the valid one: the note says so,
// the read gives the latest with what it takes to show the valid one, and
// the choice is committed at once, in the admin's name (IMP-201).
func TestNoteValid_TagAndChoose(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/a.md", "---\ntitle: First\n---\nV1 body\n")

	tag := f.tagNote(t, "p/a.md", "V1")
	if tag.Tag != "note/p/a.md/V1" || tag.Label != "V1" || tag.By != "owner" || tag.Path != "p/a.md" || tag.SHA == "" || tag.Date == "" {
		t.Fatalf("tag %+v", tag)
	}
	v := f.versions(t, "p/a.md", f.bearer)
	if len(v.Items) != 1 || v.Items[0].Tag != tag.Tag || v.Valid != nil || !v.CanAdmin {
		t.Fatalf("versions %+v", v)
	}

	f.setValid(t, "p/a.md", "V1")
	if got := f.disk(t, "p/a.md"); !strings.Contains(got, "valid_version: note/p/a.md/V1\n") {
		t.Fatalf("the note: %q", got)
	}
	h := f.history(t, "p/a.md", f.bearer)
	if len(h) != 2 || h[0].Author != "owner <gosidian@localhost>" || h[0].Subject != "valid version of p/a.md: V1" {
		t.Fatalf("history %+v", h)
	}
	n := f.readNoteBody(t, "p/a.md", "", f.bearer)
	if n.Valid == nil || n.Valid.Tag != tag.Tag || n.Valid.Label != "V1" || n.Valid.SHA != tag.SHA || !n.Valid.Same || n.Valid.Missing || n.Valid.Date == "" {
		t.Fatalf("read as the valid version: %+v", n.Valid)
	}

	// The note goes on: the read gives the draft, and tells.
	if rec := f.req(t, http.MethodPut, "/api/v1/notes/p/a.md", `{"content":"---\ntitle: Draft\nvalid_version: note/p/a.md/V1\n---\ndraft body\n"}`, f.bearer); rec.code != http.StatusOK {
		t.Fatalf("update %d %s", rec.code, rec.body)
	}
	n = f.readNoteBody(t, "p/a.md", "", f.bearer)
	if n.Valid == nil || n.Valid.Same || !strings.Contains(n.Content, "draft body") {
		t.Fatalf("read of the draft: %+v %q", n.Valid, n.Content)
	}
	at := f.readNoteBody(t, "p/a.md", "?version=valid", f.bearer)
	if !strings.Contains(at.Content, "V1 body") || at.Title != "First" || at.Valid == nil {
		t.Fatalf("read at the valid version: %+v", at)
	}
	if bytag := f.readNoteBody(t, "p/a.md", "?version=note/p/a.md/V1", f.bearer); bytag.Content != at.Content {
		t.Errorf("read at the tag: %+v", bytag)
	}
	if rec := f.req(t, http.MethodGet, "/api/v1/notes/p/a.md?version=note/none/V1", "", f.bearer); rec.code != http.StatusNotFound {
		t.Errorf("read at no tag = %d", rec.code)
	}

	entries, err := f.auditLog.Tail(10)
	if err != nil {
		t.Fatal(err)
	}
	var said []string
	for _, e := range entries {
		if e.Action == audit.ActionNoteTag || e.Action == audit.ActionNoteValid {
			said = append(said, string(e.Action)+" "+e.Path+" "+e.To+" "+e.Actor)
		}
	}
	if strings.Join(said, ",") != "note_tag p/a.md V1 owner,note_valid p/a.md V1 owner" {
		t.Errorf("audit %q", said)
	}
}

// Removing the valid version gives the note back to everyone, as it was.
func TestNoteValid_Clear(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/a.md", "plain body\n")
	f.tagNote(t, "p/a.md", "V1")
	f.setValid(t, "p/a.md", "V1")
	if n := f.readNoteBody(t, "p/a.md", "", f.bearer); n.Valid == nil || !n.Valid.Same {
		t.Fatalf("a note without frontmatter is its version: %+v", n.Valid)
	}
	if rec := f.req(t, http.MethodDelete, "/api/v1/notes/p/a.md/valid", "", f.bearer); rec.code != http.StatusOK {
		t.Fatalf("clear = %d %s", rec.code, rec.body)
	}
	if got := f.disk(t, "p/a.md"); got != "plain body\n" {
		t.Errorf("the note: %q", got)
	}
	if h := f.history(t, "p/a.md", f.bearer); h[0].Subject != "no valid version of p/a.md" {
		t.Errorf("history %+v", h[0])
	}
	if n := f.readNoteBody(t, "p/a.md", "?version=valid", f.bearer); n.Valid != nil || n.Content != "plain body\n" {
		t.Errorf("read %+v", n)
	}
}

// Who only writes may not name a version nor choose one, and neither may
// who only reads, who can still read them. A write cannot do it instead.
func TestNoteValid_Rights(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/a.md", "---\ntitle: T\n---\nbody\n")
	f.tagNote(t, "p/a.md", "V1")
	f.setValid(t, "p/a.md", "V1")
	want := f.disk(t, "p/a.md")

	f.setVisibility(t, "p", projects.VisibilityInternal)
	_, reader := f.memberUser(t, "r1")
	w1, writer := f.memberUser(t, "w1")
	if err := f.projects.SetMember("p", w1.ID, "write"); err != nil {
		t.Fatal(err)
	}

	if v := f.versions(t, "p/a.md", reader); len(v.Items) != 1 || v.Valid == nil || v.CanAdmin {
		t.Errorf("the reader's versions %+v", v)
	}
	for _, who := range []string{reader, writer} {
		if rec := f.req(t, http.MethodPost, "/api/v1/notes/p/a.md/versions", `{"label":"V2"}`, who); rec.code != http.StatusForbidden {
			t.Errorf("tag = %d %s", rec.code, rec.body)
		}
		if rec := f.req(t, http.MethodPut, "/api/v1/notes/p/a.md/valid", `{"tag":"V1"}`, who); rec.code != http.StatusForbidden {
			t.Errorf("valid = %d %s", rec.code, rec.body)
		}
		if rec := f.req(t, http.MethodDelete, "/api/v1/notes/p/a.md/valid", "", who); rec.code != http.StatusForbidden {
			t.Errorf("clear = %d %s", rec.code, rec.body)
		}
	}
	if rec := f.req(t, http.MethodPatch, "/api/v1/notes/p/a.md/frontmatter", `{"set":{"valid_version":"note/x/V9"}}`, writer); rec.code != http.StatusUnprocessableEntity {
		t.Errorf("patch = %d %s", rec.code, rec.body)
	}
	if rec := f.req(t, http.MethodPatch, "/api/v1/notes/p/a.md/frontmatter", `{"unset":["valid_version"]}`, writer); rec.code != http.StatusUnprocessableEntity {
		t.Errorf("patch unset = %d %s", rec.code, rec.body)
	}
	for _, body := range []string{`{"content":"---\ntitle: T\n---\nbody\n"}`, `{"content":"---\ntitle: T\nvalid_version: note/x/V9\n---\nbody\n"}`} {
		if rec := f.req(t, http.MethodPut, "/api/v1/notes/p/a.md", body, writer); rec.code != http.StatusOK {
			t.Fatalf("the writer's save = %d %s", rec.code, rec.body)
		}
		if got := f.disk(t, "p/a.md"); got != want {
			t.Errorf("after the writer's save: %q", got)
		}
	}
	if rec := f.req(t, http.MethodPost, "/api/v1/notes", `{"path":"p/new.md","content":"---\nvalid_version: note/p/a.md/V1\n---\nbody\n"}`, writer); rec.code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.code, rec.body)
	}
	if got := f.disk(t, "p/new.md"); strings.Contains(got, "valid_version") {
		t.Errorf("a new note brought one in: %q", got)
	}
}

// A restore of an old version leaves the valid version as it was.
func TestNoteValid_SurvivesARestore(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/a.md", "---\ntitle: T\n---\nolder text\nwith lines enough to be found as a rename\nby git, when it moves\n")
	old := f.history(t, "p/a.md", f.bearer)[0].SHA
	f.commitNote(t, s, "p/a.md", "---\ntitle: T\n---\nolder text\nwith lines enough to be found as a rename\nby git, when it moves\nand the tagged one\n")
	f.tagNote(t, "p/a.md", "V1")
	f.setValid(t, "p/a.md", "V1")

	if code, out, raw := f.restore(t, "p/a.md", restoreJSON(old, ""), f.bearer); code != http.StatusOK || !out.Restored {
		t.Fatalf("restore = %d %s", code, raw)
	}
	if got := f.disk(t, "p/a.md"); !strings.Contains(got, "valid_version: note/p/a.md/V1") || strings.Contains(got, "and the tagged one") {
		t.Errorf("after the restore: %q", got)
	}
	if n := f.readNoteBody(t, "p/a.md", "", f.bearer); n.Valid == nil || n.Valid.Same || n.Valid.Missing {
		t.Errorf("read after the restore: %+v", n.Valid)
	}

}

// The trash moves the file, and takes the valid version along.
func TestNoteValid_SurvivesTheTrash(t *testing.T) {
	f := newAdminFixture(t)
	f.seedNote(t, "p/a.md", "---\ntitle: T\n---\nbody\n")
	if err := f.router.deps.Vault.SetValidVersion("p/a.md", "note/p/a.md/V1"); err != nil {
		t.Fatal(err)
	}
	if rec := f.doAuthRecorder(http.MethodDelete, "/api/v1/notes/p/a.md", "", nil); rec.code >= 300 {
		t.Fatalf("delete = %d %s", rec.code, rec.body)
	}
	var trash struct {
		Items []trashView `json:"items"`
	}
	rec := f.doAuthRecorder(http.MethodGet, "/api/v1/trash", "", nil)
	if err := json.Unmarshal([]byte(rec.body), &trash); err != nil || len(trash.Items) != 1 {
		t.Fatalf("trash %s %v", rec.body, err)
	}
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/trash/"+url.PathEscape(trash.Items[0].ID)+"/restore", "", nil); rec.code != http.StatusOK {
		t.Fatalf("back from the trash = %d %s", rec.code, rec.body)
	}
	note, err := f.router.deps.Vault.Load("p/a.md")
	if err != nil {
		t.Fatal(err)
	}
	if got := vault.ValidVersion("p/a.md", note.Content); got != "note/p/a.md/V1" {
		t.Errorf("after the trash: %q in %q", got, note.Content)
	}
}

// A tag that is gone leaves the note readable: the latest, with a warning,
// no error. So does a server that has no git sync.
func TestNoteValid_Missing(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/a.md", "---\ntitle: T\n---\nbody\n")
	f.tagNote(t, "p/a.md", "V1")
	f.setValid(t, "p/a.md", "V1")
	out, err := exec.Command("git", "-C", f.vaultRoot, "tag", "-d", "note/p/a.md/V1").CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	for _, query := range []string{"", "?version=valid"} {
		if n := f.readNoteBody(t, "p/a.md", query, f.bearer); n.Valid == nil || !n.Valid.Missing || n.Valid.Same || n.Valid.Label != "V1" || !strings.Contains(n.Content, "body") {
			t.Errorf("read%s: %+v", query, n.Valid)
		}
	}
	f.router.deps.GitSync = nil
	if n := f.readNoteBody(t, "p/a.md", "", f.bearer); n.Valid == nil || !n.Valid.Missing {
		t.Errorf("no git sync: %+v", n.Valid)
	}
	if rec := f.req(t, http.MethodGet, "/api/v1/notes/p/a.md/versions", "", f.bearer); rec.code != http.StatusServiceUnavailable {
		t.Errorf("versions = %d", rec.code)
	}
	if rec := f.req(t, http.MethodPost, "/api/v1/notes/p/a.md/versions", `{"label":"V2"}`, f.bearer); rec.code != http.StatusServiceUnavailable {
		t.Errorf("tag = %d", rec.code)
	}
}

// What is refused, and why.
func TestNoteValid_Refusals(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/a.md", "one\n")
	f.tagNote(t, "p/a.md", "V1")
	f.commitNote(t, s, "p/a.md", "two\n")
	f.commitNote(t, s, "p/b.md", "other\n")
	other := f.history(t, "p/b.md", f.bearer)[0].SHA
	f.tagNote(t, "p/b.md", "B1")

	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "p/a.md/versions", `{"label":"V1"}`, http.StatusConflict},
		{http.MethodPost, "p/a.md/versions", `{"label":""}`, http.StatusUnprocessableEntity},
		{http.MethodPost, "p/a.md/versions", `{"label":"a b"}`, http.StatusUnprocessableEntity},
		{http.MethodPost, "p/a.md/versions", `{"label":"V9","sha":"` + other + `"}`, http.StatusNotFound},
		{http.MethodPost, "p/missing.md/versions", `{"label":"V1"}`, http.StatusNotFound},
		{http.MethodPut, "p/a.md/valid", `{"tag":""}`, http.StatusBadRequest},
		{http.MethodPut, "p/a.md/valid", `{"tag":"B1"}`, http.StatusNotFound},
		{http.MethodPut, "p/a.md/valid", `{"tag":"note/p/b.md/B1"}`, http.StatusNotFound},
		{http.MethodPut, "p/a.md/versions", `{}`, http.StatusMethodNotAllowed},
		{http.MethodGet, "p/a.md/valid", ``, http.StatusMethodNotAllowed},
	} {
		if rec := f.req(t, c.method, "/api/v1/notes/"+c.path, c.body, f.bearer); rec.code != c.want {
			t.Errorf("%s %s %s = %d %s, want %d", c.method, c.path, c.body, rec.code, rec.body, c.want)
		}
	}
	// A stale window does not choose over a note it has not seen.
	if rec := f.req(t, http.MethodPut, "/api/v1/notes/p/a.md/valid", `{"tag":"V1","if_match":"\"stale\""}`, f.bearer); rec.code != http.StatusPreconditionFailed {
		t.Errorf("stale = %d %s", rec.code, rec.body)
	}
}

// A version of a design opens as the version holds it, with files the GC
// took coming back from git; the note's design stays the draft's (IMP-201).
func TestNoteValid_LayoutOfAVersion(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	etag := seedLayoutNote(t, f)
	save := func(etag, doc string) string {
		t.Helper()
		w := f.doAuthRecorder(http.MethodPut, layoutURL, string(editorPackage(t, doc, nil)), map[string]string{"If-Match": etag, "Content-Type": layout.MIME})
		if w.code != http.StatusOK {
			t.Fatalf("save: %d %s", w.code, w.body)
		}
		var saved struct {
			ETag string `json:"etag"`
			File string `json:"file"`
		}
		if err := json.Unmarshal([]byte(w.body), &saved); err != nil {
			t.Fatal(err)
		}
		return saved.File
	}
	first := save(etag, layoutDocument(nil))
	s.TriggerCommit()
	s.Flush()
	f.tagNote(t, layoutNote, "V1")
	f.setValid(t, layoutNote, "V1")
	second := save(`"`+filepath.Base(first)+`"`, strings.Replace(layoutDocument(nil), "Titolo", "Bozza", 1))
	if first == second {
		t.Fatal("one file for two designs")
	}
	if err := os.Remove(filepath.Join(f.vaultRoot, first)); err != nil {
		t.Fatal(err)
	}

	open := func(query string) (int, string, string) {
		t.Helper()
		w := f.doAuthRecorder(http.MethodGet, layoutURL+query, "", nil)
		if w.code != http.StatusOK {
			return w.code, "", ""
		}
		p, err := layout.Unpack([]byte(w.body), layout.Limits{MaxBytes: 1 << 20, MaxDocument: 1 << 20, MaxEntries: 16})
		if err != nil {
			t.Fatal(err)
		}
		return w.code, string(p.Document), w.headers.Get("ETag")
	}
	if _, doc, etag := open("?version=valid"); !strings.Contains(doc, "Titolo") || etag != quoteETag(layout.ETag(first)) {
		t.Errorf("the valid version: %s %s", etag, doc)
	}
	if _, doc, _ := open("?version=note/" + layoutNote + "/V1"); !strings.Contains(doc, "Titolo") {
		t.Errorf("by tag: %s", doc)
	}
	if _, doc, etag := open(""); !strings.Contains(doc, "Bozza") || etag != quoteETag(layout.ETag(second)) {
		t.Errorf("the draft: %s %s", etag, doc)
	}
	if code, _, _ := open("?version=note/none/V1"); code != http.StatusNotFound {
		t.Errorf("no such version: %d", code)
	}

	// The pieces are those of the text the design was made from.
	text := func(query string) string {
		t.Helper()
		w := f.doAuthRecorder(http.MethodGet, layoutURL+"/pieces"+query, "", nil)
		if w.code != http.StatusOK {
			t.Fatalf("pieces%s: %d %s", query, w.code, w.body)
		}
		return w.body
	}
	if rec := f.req(t, http.MethodPut, "/api/v1/notes/"+layoutNote, `{"content":"<!-- 1 -->\nAltro testo.\n"}`, f.bearer); rec.code != http.StatusOK {
		t.Fatalf("update %d %s", rec.code, rec.body)
	}
	if got := text(""); !strings.Contains(got, "Altro testo") {
		t.Errorf("the draft's pieces: %s", got)
	}
	if got := text("?version=valid"); !strings.Contains(got, "Il testo della nota") || strings.Contains(got, "Altro testo") {
		t.Errorf("the version's pieces: %s", got)
	}
}

// Who chose the valid version could read it, and who reads the note reads
// it, wherever it sat: a project renamed, or the note moved from a project
// the reader cannot see. A tag asked by name stays what the history is: left
// out where its project is out of sight.
func TestNoteValid_FromAProjectOutOfSight(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	body := "# X\n\nA text long enough for git to find the move as a rename.\n"
	f.commitNote(t, s, "secret/x.md", body)
	tag := f.tagNote(t, "secret/x.md", "V1")
	if err := os.MkdirAll(filepath.Join(f.vaultRoot, "pub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(f.vaultRoot, "secret/x.md"), filepath.Join(f.vaultRoot, "pub/x.md")); err != nil {
		t.Fatal(err)
	}
	s.RecordWrite("rename", "secret/x.md", "pub/x.md", "owner")
	s.TriggerCommit()
	s.Flush()
	f.setVisibility(t, "pub", projects.VisibilityInternal)
	f.setValid(t, "pub/x.md", "V1")
	if rec := f.req(t, http.MethodPut, "/api/v1/notes/pub/x.md", `{"content":"draft\n"}`, f.bearer); rec.code != http.StatusOK {
		t.Fatalf("update %d %s", rec.code, rec.body)
	}
	_, member := f.memberUser(t, "m1")

	n := f.readNoteBody(t, "pub/x.md", "", member)
	if n.Valid == nil || n.Valid.Missing || n.Valid.Same || n.Valid.Label != "V1" {
		t.Fatalf("the member's read: %+v", n.Valid)
	}
	if at := f.readNoteBody(t, "pub/x.md", "?version=valid", member); at.Content != body {
		t.Errorf("the member's version: %q", at.Content)
	}
	if rec := f.req(t, http.MethodGet, "/api/v1/notes/pub/x.md?version="+tag.Tag, "", member); rec.code != http.StatusNotFound {
		t.Errorf("the tag by name = %d", rec.code)
	}
	if v := f.versions(t, "pub/x.md", member); len(v.Items) != 0 || v.Valid == nil {
		t.Errorf("the member's versions: %+v", v)
	}
}

// A project renamed takes the valid version along: the tag stays, the key
// is not a link to rewrite, and readers of the new project read it.
func TestNoteValid_SurvivesAProjectRename(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	body := "# X\n\nA text long enough for git to find the move as a rename.\n"
	f.commitNote(t, s, "old/x.md", body)
	f.tagNote(t, "old/x.md", "V1")
	f.setValid(t, "old/x.md", "V1")
	want := f.disk(t, "old/x.md")
	if rec := f.req(t, http.MethodPut, "/api/v1/projects/old", `{"new_name":"fresh"}`, f.bearer); rec.code != http.StatusOK {
		t.Fatalf("rename %d %s", rec.code, rec.body)
	}
	s.TriggerCommit()
	s.Flush()
	if got := f.disk(t, "fresh/x.md"); got != want {
		t.Errorf("after the rename: %q", got)
	}
	if rec := f.req(t, http.MethodPut, "/api/v1/notes/fresh/x.md", `{"content":"draft\n"}`, f.bearer); rec.code != http.StatusOK {
		t.Fatalf("update %d %s", rec.code, rec.body)
	}
	f.setVisibility(t, "fresh", projects.VisibilityInternal)
	_, member := f.memberUser(t, "m1")
	n := f.readNoteBody(t, "fresh/x.md", "", member)
	if n.Valid == nil || n.Valid.Missing || n.Valid.Same {
		t.Fatalf("the member's read: %+v", n.Valid)
	}
	if at := f.readNoteBody(t, "fresh/x.md", "?version=valid", member); at.Content != body {
		t.Errorf("the member's version: %q", at.Content)
	}
}

// The search finds the valid version: a word of the draft alone is found by
// nobody, through a save, until there is no valid version any more (IMP-201).
func TestNoteValid_SearchReadsTheValidVersion(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/a.md", "---\ntitle: T\n---\nvalidated wording\nwith lines enough to be found as a rename\nby git, when it moves\n")
	f.tagNote(t, "p/a.md", "V1")
	f.setValid(t, "p/a.md", "V1")
	draft := "---\ntitle: T\n---\nunapproved wording\nwith lines enough to be found as a rename\nby git, when it moves\n"
	if rec := f.req(t, http.MethodPut, "/api/v1/notes/p/a.md", `{"content":`+strconv.Quote(draft)+`}`, f.bearer); rec.code != http.StatusOK {
		t.Fatalf("update %d %s", rec.code, rec.body)
	}
	hits := func(q string) string {
		t.Helper()
		found, err := f.idx.Search(q, 10)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, h := range found {
			out = append(out, h.Path)
		}
		return strings.Join(out, " ")
	}
	if got := hits("validated"); got != "p/a.md" {
		t.Errorf("a word of the version: %q", got)
	}
	if got := hits("unapproved"); got != "" {
		t.Errorf("a word of the draft: %q", got)
	}

	if rec := f.req(t, http.MethodDelete, "/api/v1/notes/p/a.md/valid", "", f.bearer); rec.code != http.StatusOK {
		t.Fatalf("clear = %d %s", rec.code, rec.body)
	}
	if got := hits("unapproved"); got == "" {
		t.Error("with no valid version the draft is the note")
	}
	if got := hits("validated"); got != "" {
		t.Errorf("a word of the old version: %q", got)
	}
}

// The version who calls over MCP reads is the admin's to choose, as the valid
// version is, committed at once, and apart from it (IMP-228).
func TestNoteMCPVersion_Choose(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/a.md", "---\ntitle: T\n---\nbody\n")
	f.tagNote(t, "p/a.md", "V1")
	put := func(tag string) *recorder {
		t.Helper()
		return f.req(t, http.MethodPut, "/api/v1/notes/p/a.md/mcp-version", `{"tag":"`+tag+`"}`, f.bearer)
	}

	if rec := put("V1"); rec.code != http.StatusOK {
		t.Fatalf("put = %d %s", rec.code, rec.body)
	}
	if v := f.versions(t, "p/a.md", f.bearer); v.MCP == nil || v.MCP.Label != "V1" || v.MCP.Tag != "note/p/a.md/V1" || v.Valid != nil {
		t.Fatalf("versions %+v %+v", v.MCP, v.Valid)
	}
	if !strings.Contains(f.disk(t, "p/a.md"), "mcp_version: note/p/a.md/V1") {
		t.Errorf("the note: %q", f.disk(t, "p/a.md"))
	}
	if h := f.history(t, "p/a.md", f.bearer); h[0].Subject != "version for MCP of p/a.md: V1" {
		t.Errorf("history %+v", h[0])
	}
	// The choice alone does not make a draft of the note.
	f.setValid(t, "p/a.md", "V1")
	if n := f.readNoteBody(t, "p/a.md", "", f.bearer); n.Valid == nil || !n.Valid.Same {
		t.Errorf("valid %+v", n.Valid)
	}

	if rec := put("valid"); rec.code != http.StatusOK {
		t.Fatalf("put valid = %d %s", rec.code, rec.body)
	}
	if v := f.versions(t, "p/a.md", f.bearer); v.MCP == nil || v.MCP.Tag != "valid" || v.MCP.Label != "V1" {
		t.Fatalf("following %+v", v.MCP)
	}
	if rec := put("none"); rec.code != http.StatusNotFound {
		t.Errorf("no such tag = %d %s", rec.code, rec.body)
	}
	if rec := f.req(t, http.MethodPut, "/api/v1/notes/p/a.md/valid", `{"tag":"valid"}`, f.bearer); rec.code != http.StatusNotFound {
		t.Errorf("the valid version following itself = %d %s", rec.code, rec.body)
	}
	if rec := f.req(t, http.MethodDelete, "/api/v1/notes/p/a.md/mcp-version", "", f.bearer); rec.code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.code, rec.body)
	}
	if v := f.versions(t, "p/a.md", f.bearer); v.MCP != nil || v.Valid == nil {
		t.Fatalf("after delete %+v %+v", v.MCP, v.Valid)
	}
	if h := f.history(t, "p/a.md", f.bearer); h[0].Subject != "no version for MCP of p/a.md" {
		t.Errorf("history %+v", h[0])
	}
}

func TestNoteMCPVersion_Rights(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/a.md", "---\ntitle: T\n---\nbody\n")
	f.tagNote(t, "p/a.md", "V1")
	f.setVisibility(t, "p", projects.VisibilityInternal)
	w1, writer := f.memberUser(t, "w1")
	if err := f.projects.SetMember("p", w1.ID, "write"); err != nil {
		t.Fatal(err)
	}
	if rec := f.req(t, http.MethodPut, "/api/v1/notes/p/a.md/mcp-version", `{"tag":"V1"}`, writer); rec.code != http.StatusForbidden {
		t.Errorf("put = %d %s", rec.code, rec.body)
	}
	if rec := f.req(t, http.MethodDelete, "/api/v1/notes/p/a.md/mcp-version", "", writer); rec.code != http.StatusForbidden {
		t.Errorf("delete = %d %s", rec.code, rec.body)
	}
	if rec := f.req(t, http.MethodPatch, "/api/v1/notes/p/a.md/frontmatter", `{"set":{"mcp_version":"valid"}}`, writer); rec.code != http.StatusUnprocessableEntity {
		t.Errorf("patch = %d %s", rec.code, rec.body)
	}
	if rec := f.req(t, http.MethodPut, "/api/v1/notes/p/a.md", `{"content":"---\ntitle: T\nmcp_version: valid\n---\nbody\n"}`, writer); rec.code != http.StatusOK {
		t.Fatalf("the writer's save = %d %s", rec.code, rec.body)
	}
	if got := f.disk(t, "p/a.md"); strings.Contains(got, "mcp_version") {
		t.Errorf("a write brought one in: %q", got)
	}
}
