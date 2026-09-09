package vault

import "testing"

const sampleMediaNote = `---
title: Diagramma plancia
type: image
media: proj/attachments/abc.png
tags: [proj, type:image]
---

La plancia in modalità tiling con tre finestre affiancate.
`

const sampleTableNote = `---
title: Audit accessi
type: table
media: proj/attachments/audit.csv
tags: [proj, type:table]
---

Accessi al portale, export luglio.

Columns: user, action, ts
Rows: 2
`

func TestMediaRefForNote_FlagGating(t *testing.T) {
	dir := t.TempDir()
	v := New(dir)
	// The bytes are irrelevant: MediaRefForNote validates the extension and
	// stats the file, it does not sniff content.
	write(t, v.Root, "proj/attachments/abc.png", "\x89PNG\r\n\x1a\nfake")
	if err := v.Save("proj/diagram.md", []byte(sampleMediaNote)); err != nil {
		t.Fatal(err)
	}
	n, err := v.Load("proj/diagram.md")
	if err != nil {
		t.Fatal(err)
	}

	// Flag off (default): a type:image note is treated as ordinary markdown.
	if ref, kind := v.MediaRefForNote(n.Path, n.Content); kind != "" || ref != nil {
		t.Fatalf("flag off: want (nil,\"\"), got (%+v,%q)", ref, kind)
	}

	// Flag on: the pointer resolves to the image attachment.
	v.SetMediaNotes(true)
	ref, kind := v.MediaRefForNote(n.Path, n.Content)
	if kind != "image" {
		t.Fatalf("flag on: kind = %q, want image", kind)
	}
	if ref.Broken {
		t.Fatalf("expected resolvable media, got broken: %+v", ref)
	}
	if ref.Path != "proj/attachments/abc.png" {
		t.Errorf("path = %q, want proj/attachments/abc.png", ref.Path)
	}
	if ref.URL != "/vault-files/proj/attachments/abc.png" {
		t.Errorf("url = %q", ref.URL)
	}
	if ref.MIME != "image/png" {
		t.Errorf("mime = %q, want image/png", ref.MIME)
	}
	if ref.Size == 0 {
		t.Error("size should be non-zero for an existing attachment")
	}
}

func TestMediaRefForNote_NonMedia(t *testing.T) {
	dir := t.TempDir()
	v := New(dir)
	v.SetMediaNotes(true)

	// Ordinary note with a different type → not a media note.
	if err := v.Save("proj/plain.md", []byte("---\ntitle: plain\ntype: memory\ntags: [proj, type:memory]\n---\n\nbody")); err != nil {
		t.Fatal(err)
	}
	n, _ := v.Load("proj/plain.md")
	if ref, kind := v.MediaRefForNote(n.Path, n.Content); kind != "" || ref != nil {
		t.Fatalf("non-media note: want (nil,\"\"), got (%+v,%q)", ref, kind)
	}

	// Note without any frontmatter → not a media note.
	if err := v.Save("proj/bare.md", []byte("just text, no frontmatter")); err != nil {
		t.Fatal(err)
	}
	nb, _ := v.Load("proj/bare.md")
	if _, kind := v.MediaRefForNote(nb.Path, nb.Content); kind != "" {
		t.Error("note without frontmatter must not be a media note")
	}
}

func TestMediaRefForNote_BrokenPointers(t *testing.T) {
	dir := t.TempDir()
	v := New(dir)
	v.SetMediaNotes(true)
	// A real non-image file so the "non-image ext" case finds a file but a bad kind.
	write(t, v.Root, "proj/attachments/note.txt", "hello")

	cases := []struct{ name, file, body string }{
		{"missing media key", "proj/m1.md", "---\ntitle: x\ntype: image\ntags: [proj, type:image]\n---\n\ncap"},
		{"pointer not found", "proj/m2.md", "---\ntitle: x\ntype: image\nmedia: proj/attachments/nope.png\n---\n\ncap"},
		{"non-image ext", "proj/m3.md", "---\ntitle: x\ntype: image\nmedia: proj/attachments/note.txt\n---\n\ncap"},
		{"path escape", "proj/m4.md", "---\ntitle: x\ntype: image\nmedia: ../../etc/passwd\n---\n\ncap"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := v.Save(tc.file, []byte(tc.body)); err != nil {
				t.Fatal(err)
			}
			n, _ := v.Load(tc.file)
			ref, kind := v.MediaRefForNote(n.Path, n.Content)
			if kind != "image" {
				t.Fatalf("a type:image note must be recognised (kind=image) even with a broken pointer, got %q", kind)
			}
			if !ref.Broken {
				t.Errorf("expected Broken=true, got %+v", ref)
			}
		})
	}
}

func TestMediaRefForNote_TableNotes(t *testing.T) {
	dir := t.TempDir()
	v := New(dir)
	write(t, v.Root, "proj/attachments/audit.csv", "user,action,ts\nalice,login,1\nbob,logout,2\n")
	if err := v.Save("proj/audit.md", []byte(sampleTableNote)); err != nil {
		t.Fatal(err)
	}
	n, err := v.Load("proj/audit.md")
	if err != nil {
		t.Fatal(err)
	}

	// table_notes off (default): ordinary markdown, even with media_notes on —
	// the two flags gate independently.
	v.SetMediaNotes(true)
	if ref, kind := v.MediaRefForNote(n.Path, n.Content); kind != "" || ref != nil {
		t.Fatalf("flag off: want (nil,\"\"), got (%+v,%q)", ref, kind)
	}

	v.SetTableNotes(true)
	ref, kind := v.MediaRefForNote(n.Path, n.Content)
	if kind != "table" {
		t.Fatalf("kind = %q, want table", kind)
	}
	if ref.Broken {
		t.Fatalf("expected resolvable table, got broken: %+v", ref)
	}
	if ref.URL != "/vault-files/proj/attachments/audit.csv" {
		t.Errorf("url = %q", ref.URL)
	}
	if ref.MIME != "text/csv" {
		t.Errorf("mime = %q, want text/csv", ref.MIME)
	}

	// A type:table note whose pointer is not a .csv is recognised but broken —
	// e.g. pointing at an image.
	write(t, v.Root, "proj/attachments/abc.png", "\x89PNG\r\n\x1a\nfake")
	bad := "---\ntitle: x\ntype: table\nmedia: proj/attachments/abc.png\n---\n\ncap"
	if err := v.Save("proj/t2.md", []byte(bad)); err != nil {
		t.Fatal(err)
	}
	nb, _ := v.Load("proj/t2.md")
	ref, kind = v.MediaRefForNote(nb.Path, nb.Content)
	if kind != "table" || !ref.Broken {
		t.Fatalf("non-csv pointer: want (broken, table), got (%+v,%q)", ref, kind)
	}
}
