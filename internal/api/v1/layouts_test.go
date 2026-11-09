package v1

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/layout"
	"github.com/gosidian/gosidian/internal/projects"
)

// The page layouts of the notes (IMP-226): the editor's package taken apart
// into attachments the note names, and put back. The note's text stays as
// it is.

const (
	layoutNote = "Alpha/impaginati/rivista.md"
	layoutURL  = "/api/v1/layouts/" + layoutNote
	pngOne     = "\x89PNG\r\n\x1a\nthe first image"
	jpgTwo     = "\xff\xd8\xff\xe0the second image"
	pngPreview = "\x89PNG\r\n\x1a\nthe first page"
)

func layoutDocument(links map[string]string) string {
	link := func(id string) string {
		if l, ok := links[id]; ok {
			return `, "link": "` + l + `"`
		}
		return ""
	}
	return `{"pages": [{"id": 3}], "stories": {"s1": {"text": "<b>Titolo</b>"}}, "assets": {` +
		`"1": {"id": 1, "name": "foto.png", "mime": "image/png"` + link("1") + `}, ` +
		`"2": {"id": 2, "name": "logo.jpg", "mime": "image/jpeg"` + link("2") + `}}}`
}

// editorPackage is a package as designcraft saves it, with extra entries.
func editorPackage(t *testing.T, doc string, extra map[string]string) []byte {
	t.Helper()
	p := &layout.Package{Meta: []byte(`{"format":"designcraft","version":4,"generator":"DesignCraft 0.17.0"}`),
		Document: []byte(doc), Preview: []byte(pngPreview),
		Assets: map[uint64]layout.File{1: {Ext: "png", Data: []byte(pngOne)}, 2: {Ext: "jpg", Data: []byte(jpgTwo)}}}
	b, err := layout.Pack(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(extra) == 0 {
		return b
	}
	z, _ := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range z.File {
		_ = w.Copy(f)
	}
	for name, body := range extra {
		e, _ := w.Create(name)
		_, _ = e.Write([]byte(body))
	}
	_ = w.Close()
	return buf.Bytes()
}

func seedLayoutNote(t *testing.T, f *notesFixture) string {
	t.Helper()
	f.seedNote(t, layoutNote, "---\ntitle: Rivista\ntype: layout\ntags: [rivista]\n---\n\n<!-- 1 -->\nIl testo della nota.\n")
	w := f.doAuthRecorder(http.MethodGet, layoutURL, "", nil)
	if w.code != http.StatusNoContent || w.headers.Get("ETag") == "" {
		t.Fatalf("a new layout: %d %q %s", w.code, w.headers.Get("ETag"), w.body)
	}
	return w.headers.Get("ETag")
}

// A note without a design says the page and the layout note its design
// starts from, until the first save takes them away (IMP-232).
func TestLayouts_ANewDesignSaysWhatItStartsFrom(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, layoutNote, "---\ntitle: Rivista\ntype: layout\nlayout_page: 148x210mm\nlayout_template: \"Alpha/modelli/città.md\"\n---\n")
	w := f.doAuthRecorder(http.MethodGet, layoutURL, "", nil)
	if w.code != http.StatusNoContent || w.headers.Get(pageHeader) != "148x210mm" {
		t.Fatalf("%d %q", w.code, w.headers.Get(pageHeader))
	}
	if got, _ := url.PathUnescape(w.headers.Get(templateHeader)); got != "Alpha/modelli/città.md" || strings.ContainsRune(w.headers.Get(templateHeader), 'à') {
		t.Fatalf("the template: %q", w.headers.Get(templateHeader))
	}
	w = f.doAuthRecorder(http.MethodPut, layoutURL, string(editorPackage(t, layoutDocument(nil), nil)),
		map[string]string{"If-Match": w.headers.Get("ETag"), "Content-Type": layout.MIME})
	if w.code != http.StatusOK {
		t.Fatalf("save: %d %s", w.code, w.body)
	}
	note, _ := os.ReadFile(filepath.Join(f.vaultRoot, layoutNote))
	if strings.Contains(string(note), "layout_page") || strings.Contains(string(note), "layout_template") || !strings.Contains(string(note), "layout_file:") {
		t.Fatalf("after the first save:\n%s", note)
	}
}

func TestLayouts_SaveAndOpen(t *testing.T) {
	f := newNotesFixture(t)
	etag := seedLayoutNote(t, f)
	w := f.doAuthRecorder(http.MethodPut, layoutURL, string(editorPackage(t, layoutDocument(nil), nil)),
		map[string]string{"If-Match": etag, "Content-Type": layout.MIME})
	if w.code != http.StatusOK {
		t.Fatalf("save: %d %s", w.code, w.body)
	}
	var saved struct {
		ETag    string            `json:"etag"`
		File    string            `json:"file"`
		Assets  map[string]string `json:"assets"`
		Preview string            `json:"preview"`
	}
	_ = json.Unmarshal([]byte(w.body), &saved)
	if !strings.HasPrefix(saved.File, "Alpha/attachments/") || path.Ext(saved.File) != ".json" {
		t.Fatalf("the design's file: %q", saved.File)
	}
	one := attach.RelPath("Alpha", attach.HashFilename([]byte(pngOne), ".png"))
	two := attach.RelPath("Alpha", attach.HashFilename([]byte(jpgTwo), ".jpg"))
	preview := attach.RelPath("Alpha", attach.HashFilename([]byte(pngPreview), ".png"))
	if saved.Assets["1"] != one || saved.Assets["2"] != two || saved.Preview != preview || saved.ETag == "" || saved.ETag == etag {
		t.Fatalf("saved %+v", saved)
	}
	for rel, want := range map[string]string{one: pngOne, two: jpgTwo, preview: pngPreview} {
		if b, err := os.ReadFile(filepath.Join(f.vaultRoot, rel)); err != nil || string(b) != want {
			t.Errorf("%s: %q %v", rel, b, err)
		}
	}
	note, _ := os.ReadFile(filepath.Join(f.vaultRoot, layoutNote))
	for _, want := range []string{"title: Rivista", "type: layout", "tags: [rivista]", "layout_format: 4", "layout_generator: DesignCraft 0.17.0",
		"layout_file: " + saved.File, "layout_preview: " + preview, "\n\n<!-- 1 -->\nIl testo della nota.\n"} {
		if !strings.Contains(string(note), want) {
			t.Errorf("the note lacks %q:\n%s", want, note)
		}
	}
	for _, not := range []string{"```", "Anteprima", "<b>Titolo</b>"} {
		if strings.Contains(string(note), not) {
			t.Errorf("the design is in the note, %q:\n%s", not, note)
		}
	}
	// Links are relative to the design's file, so renaming the project
	// leaves them right.
	design, _ := os.ReadFile(filepath.Join(f.vaultRoot, saved.File))
	for _, want := range []string{`"link": "` + path.Base(one) + `"`, "<b>Titolo</b>"} {
		if !strings.Contains(string(design), want) {
			t.Errorf("the design lacks %q:\n%s", want, design)
		}
	}

	w = f.doAuthRecorder(http.MethodGet, layoutURL, "", nil)
	if w.code != http.StatusOK || w.headers.Get("Content-Type") != layout.MIME || w.headers.Get("ETag") != `"`+strings.Trim(saved.ETag, `"`)+`"` {
		t.Fatalf("open: %d %v", w.code, w.headers)
	}
	p, err := layout.Unpack([]byte(w.body), layout.Limits{MaxBytes: 1 << 20, MaxDocument: 1 << 20, MaxEntries: 16})
	if err != nil {
		t.Fatal(err)
	}
	assets, _ := layout.DocumentAssets(p.Document)
	if string(p.Assets[1].Data) != pngOne || p.Assets[1].Ext != "png" || string(p.Assets[2].Data) != jpgTwo || string(p.Preview) != pngPreview ||
		assets[1].Link != path.Base(one) || !strings.Contains(string(p.Meta), `"version":4`) {
		t.Errorf("opened %+v %+v", p, assets)
	}
	if m := w.headers.Get("Gosidian-Layout-Missing"); m != "" {
		t.Errorf("missing %q", m)
	}

	if w := f.doAuthRecorder(http.MethodPut, layoutURL, string(editorPackage(t, layoutDocument(nil), nil)), map[string]string{"If-Match": etag}); w.code != http.StatusPreconditionFailed {
		t.Errorf("a stale etag: %d %s", w.code, w.body)
	}
	// The same images saved again are the same files.
	w = f.doAuthRecorder(http.MethodPut, layoutURL, string(editorPackage(t, string(p.Document), nil)), map[string]string{"If-Match": saved.ETag})
	if w.code != http.StatusOK || !strings.Contains(w.body, one) {
		t.Errorf("saved again: %d %s", w.code, w.body)
	}
	entries, _ := os.ReadDir(filepath.Join(f.vaultRoot, "Alpha", "attachments"))
	if len(entries) != 4 {
		t.Errorf("attachments %d, want 4: the same bytes are the same file", len(entries))
	}

	// Another version is another file; the one before stays for the note's
	// history, until the GC takes it.
	changed := strings.Replace(string(p.Document), "Titolo", "Sommario", 1)
	w = f.doAuthRecorder(http.MethodPut, layoutURL, string(editorPackage(t, changed, nil)), nil)
	var again struct {
		File string `json:"file"`
	}
	_ = json.Unmarshal([]byte(w.body), &again)
	if w.code != http.StatusOK || again.File == "" || again.File == saved.File {
		t.Fatalf("a new version: %d %s", w.code, w.body)
	}
	if _, err := os.Stat(filepath.Join(f.vaultRoot, saved.File)); err != nil {
		t.Errorf("the version before: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.vaultRoot, layoutNote)); !strings.Contains(string(b), "layout_file: "+again.File) {
		t.Errorf("the note:\n%s", b)
	}
}

// The note's text is written while its design is open, that is the point of
// having it apart: only another version of the design is a conflict.
func TestLayouts_ConflictIsTheDesigns(t *testing.T) {
	f := newNotesFixture(t)
	etag := seedLayoutNote(t, f)
	put := func(doc, tag string) *recorder {
		return f.doAuthRecorder(http.MethodPut, layoutURL, string(editorPackage(t, doc, nil)), map[string]string{"If-Match": tag})
	}
	w := put(layoutDocument(nil), etag)
	if w.code != http.StatusOK {
		t.Fatalf("first save: %d %s", w.code, w.body)
	}
	first := w.headers.Get("ETag")
	b, _ := os.ReadFile(filepath.Join(f.vaultRoot, layoutNote))
	f.seedNote(t, layoutNote, strings.Replace(string(b), "Il testo della nota.", "Il testo cambiato.", 1))
	w = put(strings.Replace(layoutDocument(nil), "Titolo", "Sommario", 1), first)
	if w.code != http.StatusOK {
		t.Fatalf("the text changed, the design did not: %d %s", w.code, w.body)
	}
	if b, _ := os.ReadFile(filepath.Join(f.vaultRoot, layoutNote)); !strings.Contains(string(b), "Il testo cambiato.") {
		t.Errorf("the save lost the text:\n%s", b)
	}
	if w := put(strings.Replace(layoutDocument(nil), "Titolo", "Indice", 1), first); w.code != http.StatusPreconditionFailed {
		t.Errorf("another version of the design: %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodGet, layoutURL, "", nil); w.headers.Get("ETag") == first {
		t.Errorf("open: the etag of the version before")
	}
}

// Any markdown note can have a design: its type and its text stay.
func TestLayouts_AnyNote(t *testing.T) {
	f := newNotesFixture(t)
	const rel = "Alpha/docs/plain.md"
	url := "/api/v1/layouts/" + rel
	f.seedNote(t, rel, "---\ntype: doc\n---\n# Testo\n")
	if w := f.doAuthRecorder(http.MethodGet, url, "", nil); w.code != http.StatusNoContent {
		t.Fatalf("no design yet: %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodPut, url, string(editorPackage(t, layoutDocument(nil), nil)), nil); w.code != http.StatusOK {
		t.Fatalf("save: %d %s", w.code, w.body)
	}
	b, _ := os.ReadFile(filepath.Join(f.vaultRoot, rel))
	for _, want := range []string{"type: doc", "layout_file: Alpha/attachments/", "# Testo\n"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the note lacks %q:\n%s", want, b)
		}
	}
	if w := f.doAuthRecorder(http.MethodGet, url, "", nil); w.code != http.StatusOK {
		t.Errorf("open: %d %s", w.code, w.body)
	}
}

func TestLayouts_Pieces(t *testing.T) {
	f := newNotesFixture(t)
	const rel = "Alpha/docs/plain.md"
	url := "/api/v1/layouts/" + rel + "/pieces"
	f.seedNote(t, rel, "---\ntype: doc\n---\n\n## Titolo\n\nUn paragrafo.\n\nUn altro.\n")
	type answer struct {
		ETag   string `json:"etag"`
		Pieces []struct {
			N      int    `json:"n"`
			Kind   string `json:"kind"`
			Text   string `json:"text"`
			Blocks int    `json:"blocks"`
			Image  *struct {
				Path string `json:"path"`
			} `json:"image"`
		} `json:"pieces"`
	}
	read := func(w *recorder) answer {
		t.Helper()
		var a answer
		if w.code != http.StatusOK {
			t.Fatalf("%d %s", w.code, w.body)
		}
		_ = json.Unmarshal([]byte(w.body), &a)
		return a
	}
	got := read(f.doAuthRecorder(http.MethodGet, url, "", nil))
	if len(got.Pieces) != 1 || got.Pieces[0].N != 0 || got.Pieces[0].Blocks != 3 {
		t.Fatalf("no numbers yet: %+v", got)
	}
	if w := f.doAuthRecorder(http.MethodPost, url+"/number", "", map[string]string{"If-Match": `"stale"`}); w.code != http.StatusPreconditionFailed {
		t.Errorf("a stale etag: %d %s", w.code, w.body)
	}
	got = read(f.doAuthRecorder(http.MethodPost, url+"/number", "", map[string]string{"If-Match": got.ETag}))
	if len(got.Pieces) != 3 || got.Pieces[2].N != 3 || got.Pieces[2].Text != "Un altro." {
		t.Fatalf("numbered: %+v", got)
	}
	b, _ := os.ReadFile(filepath.Join(f.vaultRoot, rel))
	if !strings.Contains(string(b), "<!-- 1 -->\n## Titolo\n\n<!-- 2 -->\nUn paragrafo.\n\n<!-- 3 -->\nUn altro.\n") {
		t.Errorf("the note:\n%s", b)
	}
	if again := read(f.doAuthRecorder(http.MethodPost, url+"/number", "", nil)); again.ETag != got.ETag {
		t.Errorf("nothing to number wrote the note: %s %s", again.ETag, got.ETag)
	}
	// Joined by hand, then apart again with {"n": …}.
	f.seedNote(t, rel, "<!-- 1 -->\nUno.\n\nDue.\n\n<!-- 2 -->\nTre.\n\nQuattro.\n")
	got = read(f.doAuthRecorder(http.MethodPost, url+"/number", `{"n": 1}`, nil))
	if len(got.Pieces) != 3 || got.Pieces[1].N != 3 || got.Pieces[1].Text != "Due." || got.Pieces[2].Blocks != 2 {
		t.Errorf("a piece apart: %+v", got)
	}
	// Each way a note names an image comes back as the attachment's path.
	f.writeRaw(t, "Alpha/attachments/foto.png", pngOne)
	f.seedNote(t, rel, "<!-- 1 -->\n![a](/vault-files/Alpha/attachments/foto.png)\n\n<!-- 2 -->\n![[foto.png]]\n\n<!-- 3 -->\n![b](../attachments/foto.png)\n\n<!-- 4 -->\n![c](https://example.com/x.png)\n")
	got = read(f.doAuthRecorder(http.MethodGet, url, "", nil))
	for i, want := range []string{"Alpha/attachments/foto.png", "Alpha/attachments/foto.png", "Alpha/attachments/foto.png", ""} {
		if got.Pieces[i].Image == nil || got.Pieces[i].Image.Path != want {
			t.Errorf("image %d: %+v", i, got.Pieces[i].Image)
		}
	}
	// What the image's own note says stands beside what this note says.
	f.seedNote(t, "Alpha/docs/plain_img/foto.md", "---\ntype: image\narticle: \"[[Alpha/docs/plain]]\"\nmedia: Alpha/attachments/foto.png\n---\nLa foto, vista dall'alto.\n")
	if w := f.doAuthRecorder(http.MethodGet, url, "", nil); !strings.Contains(w.body, `"described":"La foto, vista dall'alto."`) {
		t.Errorf("the image note's description: %s", w.body)
	}
	// Added from the design: after a piece, into the note.
	f.seedNote(t, rel, "<!-- 1 -->\nUno.\n\n<!-- 2 -->\nDue.\n")
	add := func(body string) *recorder { return f.doAuthRecorder(http.MethodPost, url, body, nil) }
	w := add(`{"after": 1, "text": "Nuovo."}`)
	got = read(w)
	if len(got.Pieces) != 3 || got.Pieces[1].N != 3 || got.Pieces[1].Text != "Nuovo." {
		t.Fatalf("added: %+v", got)
	}
	if !strings.Contains(w.body, `"added":{"n":3`) {
		t.Errorf("the answer says which: %s", w.body)
	}
	got = read(add(`{"image": "Alpha/attachments/foto.png", "alt": "Una foto"}`))
	if last := got.Pieces[len(got.Pieces)-1]; last.Kind != "image" || last.Image == nil || last.Image.Path != "Alpha/attachments/foto.png" {
		t.Errorf("an image added: %+v", last)
	}
	for why, body := range map[string]string{
		"nothing":            `{}`,
		"both":               `{"text": "a", "image": "Alpha/attachments/foto.png"}`,
		"not an attachment":  `{"image": "Alpha/docs/plain.md"}`,
		"out of the project": `{"image": "privproj/attachments/x.png"}`,
	} {
		if w := add(body); w.code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", why, w.code, w.body)
		}
	}
	if w := f.doAuthRecorder(http.MethodPost, url+"/number", `{"n": -2}`, nil); w.code != http.StatusBadRequest {
		t.Errorf("a number below zero: %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodPut, url, "", nil); w.code != http.StatusMethodNotAllowed {
		t.Errorf("PUT: %d", w.code)
	}
	guest := f.seedTwoProjects(t)
	if w := f.req(t, http.MethodGet, url, "", guest); w.code != http.StatusNotFound {
		t.Errorf("a guest reads the pieces of a project it does not see: %d", w.code)
	}
	if w := f.req(t, http.MethodPost, url+"/number", "", guest); w.code == http.StatusOK {
		t.Errorf("a guest numbers: %d", w.code)
	}
	if w := f.req(t, http.MethodPost, url, `{"text": "a"}`, guest); w.code == http.StatusOK {
		t.Errorf("a guest adds: %d", w.code)
	}
}

// A note goes out of the vault as a normal markdown: its design stays behind.
func TestLayouts_NoteExport(t *testing.T) {
	f := newNotesFixture(t)
	etag := seedLayoutNote(t, f)
	if w := f.doAuthRecorder(http.MethodPut, layoutURL, string(editorPackage(t, layoutDocument(nil), nil)), map[string]string{"If-Match": etag}); w.code != http.StatusOK {
		t.Fatalf("save: %d %s", w.code, w.body)
	}
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/"+layoutNote+"?export=zip", "", nil)
	if w.code != http.StatusOK || !strings.HasPrefix(w.headers.Get("Content-Type"), "text/markdown") {
		t.Fatalf("export: %d %v %s", w.code, w.headers, w.body)
	}
	if strings.Contains(w.body, "layout_") || strings.Contains(w.body, "<!--") || !strings.Contains(w.body, "title: Rivista") || !strings.Contains(w.body, "Il testo della nota.") {
		t.Errorf("exported:\n%s", w.body)
	}
}

func TestLayouts_Refusals(t *testing.T) {
	f := newNotesFixture(t)
	etag := seedLayoutNote(t, f)
	put := func(body []byte) *recorder {
		return f.doAuthRecorder(http.MethodPut, layoutURL, string(body), map[string]string{"If-Match": etag})
	}
	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/layouts/Alpha/docs/plain.csv", "", nil); w.code != http.StatusBadRequest {
		t.Errorf("a file that is no markdown note: %d", w.code)
	}
	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/layouts/Alpha/impaginati/none.md", "", nil); w.code != http.StatusNotFound {
		t.Errorf("no note: %d", w.code)
	}
	if w := put([]byte("not a zip")); w.code != http.StatusBadRequest {
		t.Errorf("not a package: %d %s", w.code, w.body)
	}
	if w := put(editorPackage(t, layoutDocument(nil), map[string]string{"run.sh": "x"})); w.code != http.StatusBadRequest {
		t.Errorf("an entry it does not know: %d %s", w.code, w.body)
	}
	if w := put(editorPackage(t, layoutDocument(nil), map[string]string{"assets/3.tif": "II*\x00"})); w.code != http.StatusUnprocessableEntity || !strings.Contains(w.body, "3") {
		t.Errorf("a file the attachments do not take: %d %s", w.code, w.body)
	}
	if w := put(editorPackage(t, `{"assets": {"1": {"id": 1}}`, nil)); w.code != http.StatusBadRequest {
		t.Errorf("a document that is no JSON: %d %s", w.code, w.body)
	}
	if b, _ := os.ReadFile(filepath.Join(f.vaultRoot, layoutNote)); strings.Contains(string(b), "layout_file") {
		t.Errorf("a refused save wrote the note:\n%s", b)
	}
	guest := f.seedTwoProjects(t)
	if w := f.req(t, http.MethodGet, layoutURL, "", guest); w.code != http.StatusNotFound {
		t.Errorf("a guest reads a layout of a project it does not see: %d", w.code)
	}
	if w := f.req(t, http.MethodPut, layoutURL, string(editorPackage(t, layoutDocument(nil), nil)), guest); w.code != http.StatusForbidden && w.code != http.StatusNotFound {
		t.Errorf("a guest saves: %d", w.code)
	}
	if w := f.doAuthRecorder(http.MethodPost, layoutURL, "", nil); w.code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", w.code)
	}
}

// A link the reader may not follow (another project's, a file that is no
// attachment) leaves its asset without bytes, named in a header.
func TestLayouts_OpenMissingLinks(t *testing.T) {
	f := newNotesFixture(t)
	f.writeRaw(t, "Alpha/attachments/ok.png", pngOne)
	f.writeRaw(t, "privproj/attachments/x.jpg", jpgTwo)
	f.writeRaw(t, "Alpha/attachments/design.json", layoutDocument(map[string]string{"1": "ok.png", "2": "../../privproj/attachments/x.jpg"}))
	f.seedNote(t, layoutNote, "---\ntype: layout\nlayout_format: 4\nlayout_file: Alpha/attachments/design.json\n---\n")
	guest := f.seedTwoProjects(t)
	if err := f.projects.Set("Alpha", projects.Flags{Visibility: projects.VisibilityPublic}); err != nil {
		t.Fatal(err)
	}
	w := f.req(t, http.MethodGet, layoutURL, "", guest)
	if w.code != http.StatusOK || w.headers.Get("Gosidian-Layout-Missing") != "2" {
		t.Fatalf("open: %d missing %q %s", w.code, w.headers.Get("Gosidian-Layout-Missing"), w.body)
	}
	p, err := layout.Unpack([]byte(w.body), layout.Limits{MaxBytes: 1 << 20, MaxDocument: 1 << 20, MaxEntries: 16})
	if err != nil || string(p.Assets[1].Data) != pngOne || len(p.Assets) != 1 {
		t.Errorf("opened %+v %v", p, err)
	}
	f.writeRaw(t, "Alpha/attachments/design.json", layoutDocument(map[string]string{"1": "../impaginati/rivista.md", "2": "../../etc/passwd"}))
	w = f.doAuthRecorder(http.MethodGet, layoutURL, "", nil)
	if w.code != http.StatusOK || w.headers.Get("Gosidian-Layout-Missing") != "1,2" {
		t.Errorf("links that are no attachments: %d %q", w.code, w.headers.Get("Gosidian-Layout-Missing"))
	}
	// A design that is gone is no new layout: the editor must not start anew
	// and save over it.
	f.seedNote(t, layoutNote, "---\nlayout_file: Alpha/attachments/gone.json\n---\n")
	if w := f.doAuthRecorder(http.MethodGet, layoutURL, "", nil); w.code != http.StatusUnprocessableEntity {
		t.Errorf("the design is gone: %d %s", w.code, w.body)
	}
}

func TestLayouts_Export(t *testing.T) {
	f := newNotesFixture(t)
	seedLayoutNote(t, f)
	pdf := "%PDF-1.7 the pages"
	w := f.doAuthRecorder(http.MethodPost, layoutURL+"/export?name=rivista.pdf", pdf, map[string]string{"Content-Type": "application/pdf"})
	want := attach.RelPath("Alpha", attach.HashFilename([]byte(pdf), ".pdf"))
	if w.code != http.StatusOK || !strings.Contains(w.body, want) {
		t.Fatalf("export: %d %s", w.code, w.body)
	}
	if b, _ := os.ReadFile(filepath.Join(f.vaultRoot, want)); string(b) != pdf {
		t.Errorf("the PDF: %q", b)
	}
	if note, _ := os.ReadFile(filepath.Join(f.vaultRoot, layoutNote)); !strings.Contains(string(note), `layout_pdfs: ["draft: `+want+`"]`) {
		t.Errorf("the note:\n%s", note)
	}
	for why, target := range map[string]string{"no name": "/export", "not a PDF": "/export?name=rivista.exe"} {
		if w := f.doAuthRecorder(http.MethodPost, layoutURL+target, pdf, nil); w.code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", why, w.code, w.body)
		}
	}
	if w := f.doAuthRecorder(http.MethodPost, layoutURL+"/export?name=rivista.pdf", "<html>not a pdf", nil); w.code != http.StatusUnprocessableEntity {
		t.Errorf("bytes that are no PDF: %d %s", w.code, w.body)
	}
}

// A PDF of each version, by its label, beside the draft's, which takes the
// place of the one before; an older layout_pdf goes into the list (IMP-229).
func TestLayouts_ExportVersions(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, layoutNote, "---\ntitle: Rivista\ntype: layout\nlayout_pdf: Alpha/attachments/old.pdf\n---\n\n<!-- 1 -->\nIl testo.\n")
	tag := f.tagNote(t, layoutNote, "V3")
	export := func(version, pdf, bearer string) *recorder {
		url := layoutURL + "/export?name=rivista.pdf"
		if version != "" {
			url += "&version=" + version
		}
		w := f.request(http.MethodPost, url, pdf, map[string]string{"Authorization": "Bearer " + bearer, "Content-Type": "application/pdf"})
		return &recorder{code: w.Code, body: w.Body.String(), headers: w.Header()}
	}
	path := func(pdf string) string { return attach.RelPath("Alpha", attach.HashFilename([]byte(pdf), ".pdf")) }

	if w := export(tag.Tag, "%PDF-1.7 V3", f.bearer); w.code != http.StatusOK || !strings.Contains(w.body, `"label":"V3"`) {
		t.Fatalf("V3: %d %s", w.code, w.body)
	}
	if w := export("", "%PDF-1.7 the draft", f.bearer); w.code != http.StatusOK || !strings.Contains(w.body, `"label":"draft"`) {
		t.Fatalf("the draft: %d %s", w.code, w.body)
	}
	if w := export("", "%PDF-1.7 the draft again", f.bearer); w.code != http.StatusOK {
		t.Fatalf("the draft again: %d %s", w.code, w.body)
	}
	note, _ := os.ReadFile(filepath.Join(f.vaultRoot, layoutNote))
	want := `layout_pdfs: ["draft: ` + path("%PDF-1.7 the draft again") + `", "V3: ` + path("%PDF-1.7 V3") + `"]`
	if !strings.Contains(string(note), want) || strings.Contains(string(note), "layout_pdf:") {
		t.Errorf("the note:\n%s\nwants %s", note, want)
	}
	for why, c := range map[string]struct {
		version string
		code    int
	}{
		"a tag the note does not have":       {"note/" + layoutNote + "/V9", http.StatusNotFound},
		"the valid version, which is no tag": {"valid", http.StatusBadRequest},
	} {
		if w := export(c.version, "%PDF-1.7 x", f.bearer); w.code != c.code {
			t.Errorf("%s: %d %s", why, w.code, w.body)
		}
	}
	f.setVisibility(t, "Alpha", projects.VisibilityInternal)
	_, reader := f.memberUser(t, "r1")
	if w := export(tag.Tag, "%PDF-1.7 x", reader); w.code != http.StatusForbidden {
		t.Errorf("who only reads: %d %s", w.code, w.body)
	}
}

// The pieces in another order, each read with its hash: a save of the design
// meanwhile does not stop it, a change to a piece does (IMP-231).
func TestLayouts_ReorderPieces(t *testing.T) {
	f := newNotesFixture(t)
	const rel = "Alpha/docs/order.md"
	url := "/api/v1/layouts/" + rel + "/pieces"
	f.seedNote(t, rel, "---\ntype: doc\n---\n\n<!-- 1 -->\nUno.\n\n<!-- 2 -->\nDue.\n\n<!-- 3 -->\nTre.\n")
	type piece struct {
		N    int    `json:"n"`
		Hash string `json:"hash"`
	}
	list := func() map[int]string {
		t.Helper()
		w := f.doAuthRecorder(http.MethodGet, url, "", nil)
		var a struct {
			Pieces []piece `json:"pieces"`
		}
		if w.code != http.StatusOK || json.Unmarshal([]byte(w.body), &a) != nil {
			t.Fatalf("%d %s", w.code, w.body)
		}
		out := map[int]string{}
		for _, p := range a.Pieces {
			out[p.N] = p.Hash
		}
		return out
	}
	reorder := func(bearer string, ns ...int) *recorder {
		t.Helper()
		hashes := list()
		order := make([]map[string]any, len(ns))
		for i, n := range ns {
			order[i] = map[string]any{"n": n, "hash": hashes[n]}
		}
		body, _ := json.Marshal(map[string]any{"order": order})
		w := f.request(http.MethodPost, url+"/reorder", string(body), map[string]string{"Authorization": "Bearer " + bearer})
		return &recorder{code: w.Code, body: w.Body.String(), headers: w.Header()}
	}

	// The frontmatter written meanwhile, as a save of the design does.
	f.seedNote(t, rel, "---\ntype: doc\nlayout_preview: Alpha/attachments/p.png\n---\n\n<!-- 1 -->\nUno.\n\n<!-- 2 -->\nDue.\n\n<!-- 3 -->\nTre.\n")
	if w := reorder(f.bearer, 3, 1, 2); w.code != http.StatusOK || !strings.Contains(w.body, `"n":3`) {
		t.Fatalf("reorder = %d %s", w.code, w.body)
	}
	note, _ := os.ReadFile(filepath.Join(f.vaultRoot, rel))
	if want := "---\ntype: doc\nlayout_preview: Alpha/attachments/p.png\n---\n\n<!-- 3 -->\nTre.\n\n<!-- 1 -->\nUno.\n\n<!-- 2 -->\nDue.\n"; string(note) != want {
		t.Errorf("the note:\n%s", note)
	}
	if w := reorder(f.bearer, 3, 1); w.code != http.StatusBadRequest {
		t.Errorf("a piece left out = %d %s", w.code, w.body)
	}
	body := `{"order":[{"n":1,"hash":"old"},{"n":2,"hash":"old"},{"n":3,"hash":"old"}]}`
	if w := f.doAuthRecorder(http.MethodPost, url+"/reorder", body, nil); w.code != http.StatusConflict {
		t.Errorf("pieces changed since = %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodPost, url+"/reorder", `{"order":[]}`, nil); w.code != http.StatusBadRequest {
		t.Errorf("no order = %d %s", w.code, w.body)
	}
	f.setVisibility(t, "Alpha", projects.VisibilityInternal)
	_, reader := f.memberUser(t, "r2")
	if w := reorder(reader, 1, 2, 3); w.code != http.StatusForbidden {
		t.Errorf("who only reads = %d %s", w.code, w.body)
	}
}

// A piece written from the design: only its stretch changes, a change
// elsewhere does not stop it, one to the piece does (IMP-228).
func TestLayouts_EditPiece(t *testing.T) {
	f := newNotesFixture(t)
	const rel = "Alpha/docs/edit.md"
	url := "/api/v1/layouts/" + rel + "/pieces"
	f.seedNote(t, rel, "---\ntype: doc\n---\n\n<!-- 1 -->\nUno.\n\n<!-- 2 -->\nDue.\n")
	type piece struct {
		N    int    `json:"n"`
		Text string `json:"text"`
		Hash string `json:"hash"`
		Kind string `json:"kind"`
	}
	list := func() []piece {
		t.Helper()
		w := f.doAuthRecorder(http.MethodGet, url, "", nil)
		var a struct {
			Pieces []piece `json:"pieces"`
		}
		if w.code != http.StatusOK || json.Unmarshal([]byte(w.body), &a) != nil {
			t.Fatalf("%d %s", w.code, w.body)
		}
		return a.Pieces
	}
	edit := func(n int, hash, text string) *recorder {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"n": n, "hash": hash, "text": text})
		return f.doAuthRecorder(http.MethodPost, url+"/edit", string(body), nil)
	}

	old := list()[0]
	if w := edit(1, old.Hash, "Uno, riscritto."); w.code != http.StatusOK || !strings.Contains(w.body, "Uno, riscritto.") {
		t.Fatalf("edit = %d %s", w.code, w.body)
	}
	if w := edit(1, old.Hash, "Sopra."); w.code != http.StatusConflict {
		t.Errorf("a piece changed since = %d %s", w.code, w.body)
	}
	if w := edit(2, list()[1].Hash, "Due.\n\n![n](/vault-files/Alpha/attachments/n.png)"); w.code != http.StatusOK {
		t.Fatalf("with an image = %d %s", w.code, w.body)
	}
	got := list()
	if len(got) != 3 || got[1].Text != "Due." || got[2].Kind != "image" || got[2].N != 3 {
		t.Errorf("the image comes apart: %+v", got)
	}
	if w := edit(1, got[0].Hash, "  "); w.code != http.StatusUnprocessableEntity {
		t.Errorf("no text = %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodPost, url+"/edit", `{"n":1}`, nil); w.code != http.StatusBadRequest {
		t.Errorf("no hash = %d %s", w.code, w.body)
	}
}
