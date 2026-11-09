package fonts

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gosidian/gosidian/internal/fonts/fontstest"
)

func TestInspectReadsNameFormatAndEmbedding(t *testing.T) {
	cases := []struct {
		name      string
		data      []byte
		format    string
		embedding string
	}{
		{"truetype, installable", fontstest.Build("\x00\x01\x00\x00", "Corpo", "Regular", 0), "ttf", EmbedInstallable},
		{"opentype, editable", fontstest.Build("OTTO", "Corpo", "Regular", 8), "otf", EmbedEditable},
		{"restricted", fontstest.Build("true", "Corpo", "Regular", 2), "ttf", EmbedRestricted},
		{"preview and print", fontstest.Build("\x00\x01\x00\x00", "Corpo", "Regular", 4), "ttf", EmbedPreview},
		// A font with both bits: the least restrictive one counts.
		{"restricted and preview", fontstest.Build("\x00\x01\x00\x00", "Corpo", "Regular", 6), "ttf", EmbedPreview},
		{"no OS/2", fontstest.Build("\x00\x01\x00\x00", "Corpo", "Regular", -1), "ttf", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Inspect(c.data)
			if err != nil {
				t.Fatal(err)
			}
			if got.Family != "Corpo" || got.Style != "Regular" || got.Format != c.format || got.Embedding != c.embedding {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestInspectPrefersTheTypographicNames(t *testing.T) {
	data := fontstest.Build("\x00\x01\x00\x00", "Corpo Light", "Regular", 0)
	// The same file with the names a family of more than four styles has.
	name := fontstest.NameTable(map[uint16]string{1: "Corpo Light", 2: "Regular", 16: "Corpo", 17: "Light"})
	data = replaceTable(t, data, "name", name)
	got, err := Inspect(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Family != "Corpo" || got.Style != "Light" {
		t.Fatalf("got %+v", got)
	}
}

func replaceTable(t *testing.T, data []byte, tag string, table []byte) []byte {
	t.Helper()
	n := int(binary.BigEndian.Uint16(data[4:]))
	for i := 0; i < n; i++ {
		rec := data[12+16*i:]
		if string(rec[:4]) == tag {
			out := append([]byte{}, data...)
			binary.BigEndian.PutUint32(out[12+16*i+8:], uint32(len(out)))
			binary.BigEndian.PutUint32(out[12+16*i+12:], uint32(len(table)))
			return append(out, table...)
		}
	}
	t.Fatalf("no table %s", tag)
	return nil
}

func TestInspectRefusesWhatIsNoFont(t *testing.T) {
	font := fontstest.Build("\x00\x01\x00\x00", "Corpo", "Regular", 0)
	cut := append([]byte{}, font...)
	binary.BigEndian.PutUint32(cut[12+16+8:], 1<<30) // a name table past the end of the file
	woff := append([]byte("wOFF"), font...)
	for name, data := range map[string][]byte{
		"empty":        nil,
		"text":         []byte("not a font at all, but long enough"),
		"cut":          font[:20],
		"bad offset":   cut,
		"web font":     woff,
		"no name":      fontstest.Build("\x00\x01\x00\x00", "", "", 0),
		"tables claim": {0, 1, 0, 0, 0xff, 0xff, 0, 0, 0, 0, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := Inspect(data); !errors.Is(err, ErrNotAFont) {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}

func TestInspectReadsTheFirstFaceOfACollection(t *testing.T) {
	face := fontstest.Build("\x00\x01\x00\x00", "Corpo", "Regular", 8)
	ttc := make([]byte, 16)
	copy(ttc, "ttcf")
	binary.BigEndian.PutUint32(ttc[4:], 0x00010000)
	binary.BigEndian.PutUint32(ttc[8:], 2)
	binary.BigEndian.PutUint32(ttc[12:], 20) // the offset table of the first face
	ttc = append(ttc, 0, 0, 0, 0)            // the second face's offset, not read
	base := len(ttc)
	// Table offsets in a collection count from the start of the file.
	for i := 0; i < int(binary.BigEndian.Uint16(face[4:])); i++ {
		o := binary.BigEndian.Uint32(face[12+16*i+8:])
		binary.BigEndian.PutUint32(face[12+16*i+8:], o+uint32(base))
	}
	got, err := Inspect(append(ttc, face...))
	if err != nil {
		t.Fatal(err)
	}
	if got.Family != "Corpo" || got.Format != "ttc" || got.Faces != 2 || got.Embedding != EmbedEditable {
		t.Fatalf("got %+v", got)
	}
}

func TestStoreKeepsAFontByItsContent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fonts")
	s := New(dir)
	if got, err := s.List(); err != nil || len(got) != 0 {
		t.Fatalf("a store with no folder yet is empty: %v, %v", got, err)
	}
	data := fontstest.Build("\x00\x01\x00\x00", "Corpo", "Bold", 0)
	f, added, err := s.Add(data)
	if err != nil || !added {
		t.Fatalf("add: %v, %v", added, err)
	}
	if f.Family != "Corpo" || f.Style != "Bold" || f.Bytes != int64(len(data)) || len(f.ID) != 16 {
		t.Fatalf("got %+v", f)
	}
	again, added, err := s.Add(data)
	if err != nil || added || again.ID != f.ID {
		t.Fatalf("the same file again: %+v, %v, %v", again, added, err)
	}
	if list, _ := s.List(); len(list) != 1 || list[0].ID != f.ID {
		t.Fatalf("got %+v", list)
	}
	back, got, err := s.Read(f.ID)
	if err != nil || string(back) != string(data) || got.ID != f.ID {
		t.Fatalf("read: %v", err)
	}
	if err := s.Delete(f.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Read(f.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("after delete: %v", err)
	}
	if err := s.Delete(f.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestStoreListsInFamilyOrderAndSkipsWhatIsNotAFont(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	for _, f := range [][2]string{{"Zeta", "Regular"}, {"Alfa", "Regular"}, {"Alfa", "Bold"}} {
		if _, _, err := s.Add(fontstest.Build("\x00\x01\x00\x00", f[0], f[1], 0)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "0000000000000000.ttf"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range list {
		got = append(got, f.Family+" "+f.Style)
	}
	if len(got) != 3 || got[0] != "Alfa Bold" || got[1] != "Alfa Regular" || got[2] != "Zeta Regular" {
		t.Fatalf("got %v", got)
	}
}

func TestStoreNamesOnlyItsOwnFiles(t *testing.T) {
	s := New(t.TempDir())
	for _, id := range []string{"", "..", "../x", "abc", "0123456789abcdeg", "0123456789ABCDEF", "0123456789abcdef/.."} {
		if _, _, err := s.Read(id); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("read %q: %v", id, err)
		}
		if err := s.Delete(id); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("delete %q: %v", id, err)
		}
	}
}
