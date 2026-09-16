package v1

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/attach"
)

// uploadFixture is the smallest setup needed for the upload/attach
// endpoints. Reuses the notes fixture (vault + index + auth) and
// only adds a small helper for multipart bodies.
type uploadFixture struct {
	*notesFixture
}

func newUploadFixture(t *testing.T) *uploadFixture {
	return &uploadFixture{notesFixture: newNotesFixture(t)}
}

// uploadFile builds a multipart/form-data body with one `file` field.
// The PNG signature ensures the payload passes the magic-bytes check
// in internal/attach without needing an actual decoded image — we
// pad with zeros after the 8-byte header.
func uploadFile(t *testing.T, filename string, body []byte) (contentType string, raw []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return mw.FormDataContentType(), buf.Bytes()
}

// pngBody returns the minimal PNG signature followed by junk bytes,
// long enough to clear the magic-bytes check but not a structured
// image. internal/attach validates by extension + magic bytes only.
func pngBody() []byte {
	const sig = "\x89PNG\r\n\x1a\n"
	out := make([]byte, 0, 64)
	out = append(out, []byte(sig)...)
	out = append(out, bytes.Repeat([]byte{0x00}, 56)...)
	return out
}

func (f *uploadFixture) doMultipart(t *testing.T, method, path string, ct string, body []byte) *recorder {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", ct)
	r.Header.Set("Authorization", "Bearer "+f.bearer)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return &recorder{code: w.Code, body: w.Body.String(), headers: w.Header()}
}

// ---- /api/v1/attach ----

func TestAttach_StoresAndReturnsMarkdown(t *testing.T) {
	f := newUploadFixture(t)
	ct, body := uploadFile(t, "shot.png", pngBody())
	w := f.doMultipart(t, http.MethodPost, "/api/v1/attach?project=alpha", ct, body)
	if w.code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	var resp attachResponse
	if err := json.Unmarshal([]byte(w.body), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Path, "alpha/attachments/") {
		t.Errorf("path not under alpha/attachments: %q", resp.Path)
	}
	if !strings.Contains(resp.Markdown, "![") {
		t.Errorf("markdown missing image embed: %q", resp.Markdown)
	}
}

func TestAttach_RejectsUnsupportedExtension(t *testing.T) {
	f := newUploadFixture(t)
	ct, body := uploadFile(t, "foo.exe", []byte("MZjunk"))
	w := f.doMultipart(t, http.MethodPost, "/api/v1/attach", ct, body)
	if w.code != http.StatusUnsupportedMediaType {
		t.Errorf("status=%d, want 415 body=%s", w.code, w.body)
	}
}

func TestAttach_RejectsMissingFileField(t *testing.T) {
	f := newUploadFixture(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("hello", "world")
	mw.Close()
	w := f.doMultipart(t, http.MethodPost, "/api/v1/attach", mw.FormDataContentType(), buf.Bytes())
	if w.code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 body=%s", w.code, w.body)
	}
}

// ---- /api/v1/upload ----

func TestUpload_RequiresProject(t *testing.T) {
	f := newUploadFixture(t)
	ct, body := uploadFile(t, "x.png", pngBody())
	w := f.doMultipart(t, http.MethodPost, "/api/v1/upload", ct, body)
	if w.code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 body=%s", w.code, w.body)
	}
}

func TestUpload_StoresAndEnriches(t *testing.T) {
	f := newUploadFixture(t)
	ct, body := uploadFile(t, "a.png", pngBody())
	w := f.doMultipart(t, http.MethodPost, "/api/v1/upload?project=alpha", ct, body)
	if w.code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	var resp uploadResponse
	if err := json.Unmarshal([]byte(w.body), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Kind != "image" || !strings.HasPrefix(resp.MIME, "image/") {
		t.Errorf("classification wrong: %+v", resp)
	}
	if !strings.HasPrefix(resp.URL, "/vault-files/") {
		t.Errorf("url shape wrong: %q", resp.URL)
	}
	if resp.Hash == "" || resp.OriginalFilename != "a.png" {
		t.Errorf("metadata missing: %+v", resp)
	}
}

// ---- /api/v1/command-palette ----

func TestCommandPalette_DataShape(t *testing.T) {
	f := newUploadFixture(t)
	f.seedNote(t, "alpha/note.md", "---\ntags: [type:plan]\n---\n# alpha")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/command-palette", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `"notes"`) || !strings.Contains(w.body, `"projects"`) || !strings.Contains(w.body, `"tags"`) {
		t.Errorf("missing top-level keys: %s", w.body)
	}
	if !strings.Contains(w.body, `"alpha/note.md"`) {
		t.Errorf("seeded note missing: %s", w.body)
	}
	if !strings.Contains(w.body, `"type:plan"`) {
		t.Errorf("seeded tag missing: %s", w.body)
	}
}

func TestCommandPalette_RequiresAuth(t *testing.T) {
	f := newUploadFixture(t)
	w := f.request(http.MethodGet, "/api/v1/command-palette", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", w.Code)
	}
}

// ---- /api/v1/notes/{path}/history ----

func TestHistory_RequiresGitSync(t *testing.T) {
	// notesFixture wires no GitSync, so /history should 503 with a
	// clear message instead of returning empty data.
	f := newUploadFixture(t)
	f.seedNote(t, "x.md", "x")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/x.md/history", "", nil)
	if w.code != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503 body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, "git sync") {
		t.Errorf("missing git sync hint: %s", w.body)
	}
}

// A body far beyond the attachment cap must be refused by the parser (413)
// instead of being spooled to a temp file first (BUG-034).
func TestUpload_OversizedBodyIs413(t *testing.T) {
	f := newUploadFixture(t)
	huge := bytes.Repeat([]byte("x"), int(attach.MaxBytes)+3<<20)
	ct, raw := uploadFile(t, "huge.png", huge)
	rec := f.doMultipart(t, http.MethodPost, "/api/v1/upload?project=proj", ct, raw)
	if rec.code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", rec.code, rec.body)
	}
}
