package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// /vault-files/ serves the files of an IN folder (IMP-172), inert, with a
// range, revalidated; a link under an attachments/ folder or in an IN
// folder is not followed (BUG-118).
func TestVaultFiles_INAndLinks(t *testing.T) {
	s := newTestServer(t)
	s.vault.SetINProjects(func(p string) bool { return p == "proj" })
	var seen string
	s.SetVaultFileAuthorizer(func(_ *http.Request, rel string) int { seen = rel; return 0 })
	root := s.vault.Root
	write := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("proj/IN/lotto 1/fattura è.xml", "<xml>0123456789</xml>")
	write("proj/IN/foto.png", "\x89PNG\r\n\x1a\n")
	write("proj/notes.md", "# N")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "proj", "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "proj", "attachments", "link.png")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "proj", "IN", "link.txt")); err != nil {
		t.Fatal(err)
	}
	get := func(path string, hdr map[string]string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		s.ServeHTTP(rec, req)
		return rec
	}

	xml := get("/vault-files/proj/IN/lotto%201/fattura%20%C3%A8.xml", nil)
	if xml.Code != http.StatusOK || xml.Body.String() != "<xml>0123456789</xml>" || seen != "proj/IN/lotto 1/fattura è.xml" {
		t.Fatalf("IN file: %d %q (authorized %q)", xml.Code, xml.Body.String(), seen)
	}
	h := xml.Header()
	if !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") || !strings.Contains(h.Get("Content-Disposition"), "filename*=utf-8''fattura%20%C3%A8.xml") {
		t.Errorf("Content-Disposition %q", h.Get("Content-Disposition"))
	}
	if h.Get("Content-Type") != "application/octet-stream" || h.Get("Content-Security-Policy") == "" || h.Get("Cache-Control") != "private, no-cache" {
		t.Errorf("headers %v", h)
	}
	part := get("/vault-files/proj/IN/lotto%201/fattura%20%C3%A8.xml", map[string]string{"Range": "bytes=5-8"})
	if part.Code != http.StatusPartialContent || part.Body.String() != "0123" {
		t.Errorf("range: %d %q", part.Code, part.Body.String())
	}
	if png := get("/vault-files/proj/IN/foto.png", nil); png.Code != http.StatusOK || png.Header().Get("Content-Disposition") != "" {
		t.Errorf("an image of IN: %d %q", png.Code, png.Header().Get("Content-Disposition"))
	}
	for _, p := range []string{"/vault-files/proj/IN/link.txt", "/vault-files/proj/attachments/link.png", "/vault-files/proj/notes.md"} {
		if rec := get(p, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d %q", p, rec.Code, rec.Body.String())
		}
	}
	s.vault.SetINProjects(nil)
	if rec := get("/vault-files/proj/IN/foto.png", nil); rec.Code != http.StatusNotFound {
		t.Errorf("a project without IN serves its folder IN: %d", rec.Code)
	}
}
