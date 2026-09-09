package vault

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// ADR-021: the vault layer mutates only note files. Attachment bytes enter
// through SaveAttachment/DeleteAttachment, confined to attachments/ and to an
// extension allowlist.

func TestVault_SaveRejectsNonNote(t *testing.T) {
	v := newTestVault(t)
	for _, rel := range []string{"proj/x.svg", "proj/attachments/a.png", "proj/evil.sh", "proj/data.csv"} {
		if err := v.Save(rel, []byte("x")); !errors.Is(err, ErrNotNote) {
			t.Errorf("Save(%q) err=%v, want ErrNotNote", rel, err)
		}
		if _, err := os.Stat(filepath.Join(v.Root, filepath.FromSlash(rel))); err == nil {
			t.Errorf("Save(%q) wrote the file anyway", rel)
		}
	}
	if err := v.Save("proj/note.md", []byte("# ok")); err != nil {
		t.Fatalf("Save note: %v", err)
	}
}

func TestVault_DeleteRejectsNonNote(t *testing.T) {
	v := newTestVault(t)
	rel := "proj/attachments/a.png"
	abs := filepath.Join(v.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := v.Delete(rel); !errors.Is(err, ErrNotNote) {
		t.Errorf("Delete(%q) err=%v, want ErrNotNote", rel, err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Errorf("attachment removed through Delete: %v", err)
	}
}

func TestVault_SaveAttachmentConfined(t *testing.T) {
	v := newTestVault(t)
	allowed := map[string]bool{".png": true, ".csv": true}
	if err := v.SaveAttachment("proj/attachments/a.png", []byte("png"), allowed); err != nil {
		t.Fatalf("SaveAttachment ok path: %v", err)
	}
	bad := []string{"proj/a.png", "proj/attachments/a.exe", "proj/attachments/n.md", "attachments.png"}
	for _, rel := range bad {
		if err := v.SaveAttachment(rel, []byte("x"), allowed); !errors.Is(err, ErrNotAttachment) {
			t.Errorf("SaveAttachment(%q) err=%v, want ErrNotAttachment", rel, err)
		}
	}
	if err := v.DeleteAttachment("proj/attachments/a.png", allowed); err != nil {
		t.Errorf("DeleteAttachment ok path: %v", err)
	}
	if err := v.DeleteAttachment("proj/note.md", allowed); !errors.Is(err, ErrNotAttachment) {
		t.Errorf("DeleteAttachment(note) err=%v, want ErrNotAttachment", err)
	}
}

func TestVault_LoadRejectsNonNote(t *testing.T) {
	v := newTestVault(t)
	write(t, v.Root, "proj/attachments/a.png", "\x89PNG\r\n\x1a\nfake")
	write(t, v.Root, "proj/x.sh", "#!/bin/sh\n")
	write(t, v.Root, "proj/n.md", "# n\n")
	write(t, v.Root, "proj/p.html", "<html><body>x</body></html>")
	for _, rel := range []string{"proj/attachments/a.png", "proj/x.sh", "proj/p.html"} {
		if _, err := v.Load(rel); !errors.Is(err, ErrNotNote) {
			t.Errorf("Load(%q) err=%v, want ErrNotNote", rel, err)
		}
	}
	if _, err := v.Load("proj/n.md"); err != nil {
		t.Errorf("Load(note): %v", err)
	}
	v.SetHTMLNotes(true)
	if _, err := v.Load("proj/p.html"); err != nil {
		t.Errorf("Load(html, flag on): %v", err)
	}
	if _, err := v.Load("proj/missing.md"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load(missing) err=%v, want ErrNotExist", err)
	}
}
