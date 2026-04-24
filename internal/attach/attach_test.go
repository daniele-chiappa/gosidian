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
func (f *fakeSaver) Save(rel string, content []byte) error {
	f.saved[rel] = content
	return nil
}

func TestStore(t *testing.T) {
	s := &fakeSaver{saved: make(map[string][]byte)}
	res, err := Store(s, []byte("hello"), "report.pdf", "Work")
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
	os.WriteFile(file, []byte("pdf-content"), 0o644)

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
	os.WriteFile(file, []byte("content"), 0o644)

	s := &fakeSaver{saved: make(map[string][]byte)}
	res, err := StoreFromPath(s, file, "custom-name.pdf", "", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Markdown, "[custom-name.pdf]") {
		t.Errorf("markdown should use override name: %q", res.Markdown)
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
