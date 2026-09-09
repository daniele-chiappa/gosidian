package attach

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateExt(t *testing.T) {
	tests := []struct {
		ext     string
		wantOK  bool
		isImage bool
	}{
		{".png", true, true},
		{".PNG", true, true},
		{".pdf", true, false},
		{".csv", true, false},
		{".exe", false, false},
		{".sh", false, false},
		{"", false, false},
	}
	for _, tt := range tests {
		mime, isImg, err := ValidateExt(tt.ext)
		if tt.wantOK {
			if err != nil {
				t.Errorf("ValidateExt(%q) unexpected error: %v", tt.ext, err)
			}
			if mime == "" {
				t.Errorf("ValidateExt(%q) returned empty MIME", tt.ext)
			}
			if isImg != tt.isImage {
				t.Errorf("ValidateExt(%q) isImage = %v, want %v", tt.ext, isImg, tt.isImage)
			}
		} else {
			if err == nil {
				t.Errorf("ValidateExt(%q) expected error", tt.ext)
			}
		}
	}
}

func TestHashFilename(t *testing.T) {
	name := HashFilename([]byte("hello"), ".png")
	if !strings.HasSuffix(name, ".png") {
		t.Errorf("expected .png suffix, got %q", name)
	}
	// 16 hex chars + ext
	if len(name) != 16+4 {
		t.Errorf("expected length 20, got %d: %q", len(name), name)
	}
	// Deterministic
	name2 := HashFilename([]byte("hello"), ".png")
	if name != name2 {
		t.Errorf("not deterministic: %q vs %q", name, name2)
	}
	// Different data = different hash
	name3 := HashFilename([]byte("world"), ".png")
	if name == name3 {
		t.Errorf("different data produced same hash")
	}
}

func TestRelPath(t *testing.T) {
	if got := RelPath("Work", "abc.png"); got != "Work/attachments/abc.png" {
		t.Errorf("with project: %q", got)
	}
	if got := RelPath("", "abc.png"); got != "attachments/abc.png" {
		t.Errorf("without project: %q", got)
	}
}

func TestMarkdownRef(t *testing.T) {
	img := MarkdownRef("Work/attachments/abc.png", "photo.png", true)
	if img != "![](/vault-files/Work/attachments/abc.png)" {
		t.Errorf("image ref: %q", img)
	}
	doc := MarkdownRef("attachments/abc.pdf", "report.pdf", false)
	if doc != "[report.pdf](/vault-files/attachments/abc.pdf)" {
		t.Errorf("doc ref: %q", doc)
	}
}

type fakeSaver struct {
	saved map[string][]byte
}

func (f *fakeSaver) Rel(p string) (string, error) { return p, nil }
func (f *fakeSaver) SaveAttachment(rel string, content []byte, _ map[string]bool) error {
	f.saved[rel] = content
	return nil
}

// pdfContent returns a minimal byte sequence that http.DetectContentType
// recognises as application/pdf. Used by tests that need a "valid" PDF
// payload after the magic-bytes verification was added in v1.11.
func pdfContent() []byte {
	return []byte("%PDF-1.4\n%minimal valid header for tests\n")
}

// pngContent returns a minimal byte sequence detected as image/png.
func pngContent() []byte {
	return []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 'I', 'H', 'D', 'R',
		0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0}
}

func TestStore(t *testing.T) {
	s := &fakeSaver{saved: make(map[string][]byte)}
	res, err := Store(s, pdfContent(), "report.pdf", "Work")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Path, "Work/attachments/") {
		t.Errorf("path: %q", res.Path)
	}
	if !strings.HasSuffix(res.Path, ".pdf") {
		t.Errorf("path should end .pdf: %q", res.Path)
	}
	if !strings.Contains(res.Markdown, "[report.pdf]") {
		t.Errorf("markdown should be link for PDF: %q", res.Markdown)
	}
	if len(s.saved) != 1 {
		t.Errorf("expected 1 saved file, got %d", len(s.saved))
	}
}

// TestStoreRejectsMIMESpoof: caller declares .png but uploads JS-like
// content. The magic-bytes verification (added 2026-04-25) must catch this.
func TestStoreRejectsMIMESpoof(t *testing.T) {
	s := &fakeSaver{saved: make(map[string][]byte)}
	jsLike := []byte("alert('xss');\nfunction stuff(){return 1}")
	_, err := Store(s, jsLike, "fake.png", "")
	if err == nil {
		t.Error("expected MIME mismatch error for JS content declared as .png")
	}
	if !strings.Contains(err.Error(), "MIME mismatch") {
		t.Errorf("expected 'MIME mismatch' in error, got: %v", err)
	}
}

func TestStoreRejectsUnsupported(t *testing.T) {
	s := &fakeSaver{saved: make(map[string][]byte)}
	_, err := Store(s, []byte("MZ"), "evil.exe", "")
	if err == nil {
		t.Error("expected error for .exe")
	}
}

func TestStoreRejectsTooLarge(t *testing.T) {
	s := &fakeSaver{saved: make(map[string][]byte)}
	big := make([]byte, MaxBytes+1)
	_, err := Store(s, big, "big.png", "")
	if err == nil {
		t.Error("expected error for oversized file")
	}
}

func TestValidateSourcePath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.pdf")
	os.WriteFile(file, []byte("pdf"), 0o644)
	subdir := filepath.Join(dir, "sub")
	os.MkdirAll(subdir, 0o755)

	tests := []struct {
		name    string
		path    string
		roots   []string
		wantErr bool
	}{
		{"valid", file, []string{dir}, false},
		{"relative path", "relative/test.pdf", []string{dir}, true},
		{"outside roots", "/etc/passwd", []string{dir}, true},
		{"nonexistent", filepath.Join(dir, "nope.pdf"), []string{dir}, true},
		{"directory", subdir, []string{dir}, true},
		{"no roots", file, nil, true},
		{"empty roots", file, []string{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSourcePath(tt.path, tt.roots)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSourcePath(%q, %v) error = %v, wantErr %v", tt.path, tt.roots, err, tt.wantErr)
			}
		})
	}
}

func TestStoreFromPath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "report.pdf")
	os.WriteFile(file, pdfContent(), 0o644)

	s := &fakeSaver{saved: make(map[string][]byte)}
	res, err := StoreFromPath(s, file, "", "Work", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Path, "Work/attachments/") {
		t.Errorf("path: %q", res.Path)
	}
	if !strings.Contains(res.Markdown, "[report.pdf]") {
		t.Errorf("markdown should use basename: %q", res.Markdown)
	}
}

func TestStoreFromPathOverrideFilename(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "data.pdf")
	os.WriteFile(file, pdfContent(), 0o644)

	s := &fakeSaver{saved: make(map[string][]byte)}
	res, err := StoreFromPath(s, file, "custom-name.pdf", "", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Markdown, "[custom-name.pdf]") {
		t.Errorf("markdown should use override name: %q", res.Markdown)
	}
}

// TestVerifyMIME locks down the magic-bytes guard added in v1.11 to
// prevent MIME spoofing on uploads. Covers the canonical happy paths plus
// the four spoof shapes that motivated the helper (JS-as-PNG,
// HTML-as-PDF, plain-text-as-zip, and the SVG-as-text legitimate case).
func TestVerifyMIME(t *testing.T) {
	// Build a tiny but real ZIP archive (PK header).
	zipContent := []byte{0x50, 0x4B, 0x03, 0x04, 0x14, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

	tests := []struct {
		name    string
		content []byte
		ext     string
		wantErr bool
	}{
		// Happy paths
		{"valid PDF", pdfContent(), ".pdf", false},
		{"valid PNG", pngContent(), ".png", false},
		{"valid ZIP", zipContent, ".zip", false},
		{"valid SVG (text/xml detected)", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`), ".svg", false},
		{"valid CSV (text/plain detected)", []byte("col1,col2\nvalue,42\n"), ".csv", false},
		{"valid TXT", []byte("plain text content for testing\n"), ".txt", false},
		{"valid JSON", []byte(`{"key":"value"}`), ".json", false},

		// Spoofs (caller lies about content)
		{"JS-as-PNG spoof", []byte("alert('xss')\nfunction f(){}"), ".png", true},
		{"HTML-as-PDF spoof", []byte("<html><body>fake</body></html>"), ".pdf", true},
		{"plain-text-as-ZIP spoof", []byte("not a zip at all"), ".zip", true},
		{"binary-as-CSV spoof", pngContent(), ".csv", true},

		// Edge cases
		{"empty content", []byte{}, ".pdf", true},
		{"unknown extension", pdfContent(), ".exe", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := VerifyMIME(tt.content, tt.ext)
			if (err != nil) != tt.wantErr {
				t.Errorf("VerifyMIME(%q) error = %v, wantErr %v", tt.ext, err, tt.wantErr)
			}
		})
	}
}

func TestStoreFromPathBlocksOutsideRoots(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "secret.pdf")
	os.WriteFile(file, []byte("secret"), 0o644)

	s := &fakeSaver{saved: make(map[string][]byte)}
	_, err := StoreFromPath(s, file, "", "", []string{"/some/other/root"})
	if err == nil {
		t.Error("expected error for path outside allowed roots")
	}
}

// TestValidateSourcePath_RemoteSetupHint locks down the IMP-036 fix: the
// errors that typically hit users running gosidian behind an SSH tunnel
// (path not in allowed roots, or path simply missing on the server) now
// carry a hint that points them at the 'data' parameter instead of letting
// them guess at GOSIDIAN_MCP_ALLOWED_UPLOAD_ROOTS configuration.
func TestValidateSourcePath_RemoteSetupHint(t *testing.T) {
	dir := t.TempDir()
	allowed := []string{dir}

	// Case 1: path outside allowed roots (typical remote-deployment error).
	err := ValidateSourcePath("/home/someone/local/file.pdf", allowed)
	if err == nil {
		t.Fatal("expected error for path outside allowed roots")
	}
	for _, want := range []string{"bridge_filename", "transfer:\"http\"", "data"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should teach the channel hierarchy (missing %q), got: %v", want, err)
		}
	}

	// Case 2: path inside allowed root but file missing (also typical when
	// client and server filesystems differ).
	missing := filepath.Join(dir, "phantom.pdf")
	err = ValidateSourcePath(missing, allowed)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "not mounted in the container") || !strings.Contains(err.Error(), "bridge_filename") {
		t.Errorf("missing-file error should teach the channel hierarchy, got: %v", err)
	}

	// Case 3: legitimate happy path keeps working without hint pollution.
	good := filepath.Join(dir, "ok.pdf")
	os.WriteFile(good, pdfContent(), 0o644)
	if err := ValidateSourcePath(good, allowed); err != nil {
		t.Errorf("happy path failed: %v", err)
	}
}
