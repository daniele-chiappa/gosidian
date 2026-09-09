package v1

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR-021 on the HTTP API: PUT/DELETE /api/v1/notes/{path} only touch note
// files, like POST already did — attachments are not reachable through the
// notes endpoints.
func TestNotes_NoteOnly_PutAndDeleteRejectAttachmentPaths(t *testing.T) {
	f := newNotesFixture(t)
	abs := filepath.Join(f.vaultRoot, "scratch", "attachments", "data.csv")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("a,b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hdr := map[string]string{"Authorization": "Bearer " + f.bearer, "Content-Type": "application/json"}

	w := f.request(http.MethodPut, "/api/v1/notes/scratch/attachments/data.csv", `{"content":"pwned"}`, hdr)
	if w.Code != http.StatusBadRequest {
		t.Errorf("PUT attachment path: status=%d (want 400) body=%s", w.Code, w.Body.String())
	}
	if b, _ := os.ReadFile(abs); string(b) != "a,b\n" {
		t.Errorf("attachment rewritten through PUT: %q", b)
	}
	w = f.request(http.MethodDelete, "/api/v1/notes/scratch/attachments/data.csv", "", hdr)
	if w.Code != http.StatusBadRequest {
		t.Errorf("DELETE attachment path: status=%d (want 400) body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(abs); err != nil {
		t.Errorf("attachment removed through DELETE: %v", err)
	}
}

// IMP-080: reads are note-only too — an attachment path is never served as a
// note through /api/v1/notes/{path} and its sub-routes.
func TestNotes_NoteOnly_ReadsRejectAttachmentPaths(t *testing.T) {
	f := newNotesFixture(t)
	abs := filepath.Join(f.vaultRoot, "scratch", "attachments", "data.csv")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("secret,cell\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hdr := map[string]string{"Authorization": "Bearer " + f.bearer}
	for _, p := range []string{
		"/api/v1/notes/scratch/attachments/data.csv",
		"/api/v1/notes/scratch/attachments/data.csv/excerpt",
		"/api/v1/notes/scratch/attachments/data.csv/backlinks",
	} {
		w := f.request(http.MethodGet, p, "", hdr)
		if w.Code != http.StatusBadRequest {
			t.Errorf("GET %s: status=%d (want 400) body=%s", p, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "secret,cell") {
			t.Errorf("GET %s leaked the attachment bytes", p)
		}
	}
	f.seedNote(t, "scratch/ok.md", "# ok\n")
	if w := f.request(http.MethodGet, "/api/v1/notes/scratch/ok.md", "", hdr); w.Code != http.StatusOK {
		t.Errorf("GET note: status=%d", w.Code)
	}
}
