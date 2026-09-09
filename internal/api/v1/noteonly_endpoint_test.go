package v1

import (
	"net/http"
	"os"
	"path/filepath"
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
