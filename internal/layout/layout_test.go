package layout

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/json"
	"hash/crc32"
	"strings"
	"testing"
)

const testDocument = `{
  "version": 1,
  "big": 12345678901234567890,
  "ratio": 0.1,
  "stories": {"s1": {"text": "<b>Titolo</b> & sottotitolo"}},
  "assets": {
    "1": {"id": 1, "name": "foto.png", "mime": "image/png", "pixels": [800, 600]},
    "2": {"id": 2, "name": "logo.jpg", "mime": "image/jpeg", "link": "/Users/x/logo.jpg"}
  }
}`

type entry struct {
	name   string
	body   []byte
	method uint16
}

// pkg writes a zip of entries in their order, as designcraft writes a
// package (mimetype first, stored).
func pkg(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := z.CreateHeader(&zip.FileHeader{Name: e.name, Method: e.method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func designcraftPackage(t *testing.T) []byte {
	return pkg(t,
		entry{"mimetype", []byte(MIME), zip.Store},
		entry{"meta.json", []byte(`{"format":"designcraft","version":4,"generator":"DesignCraft 0.17.0"}`), zip.Deflate},
		entry{"preview.png", []byte("\x89PNG preview"), zip.Store},
		entry{"document.json", []byte(testDocument), zip.Deflate},
		entry{"assets/1.png", []byte("\x89PNG one"), zip.Store},
		entry{"assets/2.jpg", []byte("\xff\xd8 two"), zip.Store},
	)
}

var testLimits = Limits{MaxBytes: 1 << 20, MaxDocument: 64 << 10, MaxEntries: 16}

func TestUnpackAndPack(t *testing.T) {
	p, err := Unpack(designcraftPackage(t), testLimits)
	if err != nil {
		t.Fatal(err)
	}
	if string(p.Document) != testDocument || string(p.Preview) != "\x89PNG preview" || !strings.Contains(string(p.Meta), `"version":4`) {
		t.Fatalf("unpacked %+v", p)
	}
	if len(p.Assets) != 2 || p.Assets[1].Ext != "png" || string(p.Assets[1].Data) != "\x89PNG one" || p.Assets[2].Ext != "jpg" {
		t.Fatalf("assets %+v", p.Assets)
	}
	b, err := Pack(p)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range z.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, " ") != "mimetype meta.json preview.png document.json assets/1.png assets/2.jpg" {
		t.Errorf("entries %v: mimetype first, as designcraft reads a package", names)
	}
	if z.File[0].Method != zip.Store || z.File[4].Method != zip.Store || z.File[3].Method != zip.Deflate {
		t.Errorf("methods: mimetype and the images stored, the document deflated")
	}
	again, err := Unpack(b, testLimits)
	if err != nil || string(again.Document) != testDocument || string(again.Assets[2].Data) != "\xff\xd8 two" {
		t.Errorf("packed and unpacked again: %+v %v", again, err)
	}
	if _, err := Unpack(pkg(t, entry{"mimetype", []byte(MIME), zip.Store}, entry{"document.json", []byte("{}"), zip.Deflate}), testLimits); err != nil {
		t.Errorf("a package without meta, preview and assets: %v", err)
	}
}

func TestUnpackRefusals(t *testing.T) {
	mime := entry{"mimetype", []byte(MIME), zip.Store}
	doc := entry{"document.json", []byte("{}"), zip.Deflate}
	many := []entry{mime, doc}
	for i := 0; i < testLimits.MaxEntries; i++ {
		many = append(many, entry{"assets/" + strings.Repeat("1", i+1) + ".png", []byte("x"), zip.Store})
	}
	for why, b := range map[string][]byte{
		"not a zip":                []byte("PK not really"),
		"no document":              pkg(t, mime),
		"no mimetype":              pkg(t, doc),
		"another mimetype":         pkg(t, entry{"mimetype", []byte("application/zip"), zip.Store}, doc),
		"an unknown entry":         pkg(t, mime, doc, entry{"run.sh", []byte("x"), zip.Deflate}),
		"a path out":               pkg(t, mime, doc, entry{"assets/../../x.png", []byte("x"), zip.Store}),
		"an asset id not a number": pkg(t, mime, doc, entry{"assets/a.png", []byte("x"), zip.Store}),
		"an entry twice":           pkg(t, mime, doc, doc),
		"too many entries":         pkg(t, many...),
		"too many bytes":           pkg(t, mime, doc, entry{"assets/1.png", make([]byte, testLimits.MaxBytes), zip.Deflate}),
		"a document too big":       pkg(t, mime, entry{"document.json", make([]byte, testLimits.MaxDocument+1), zip.Deflate}),
		"a size that lies":         lying(t, testLimits.MaxDocument*2),
	} {
		if _, err := Unpack(b, testLimits); err == nil {
			t.Errorf("%s: unpacked", why)
		}
	}
}

// lying is a package whose document.json says it is 2 bytes and inflates
// to n: the limit holds on the bytes read, not on what the zip declares.
func lying(t *testing.T, n int64) []byte {
	t.Helper()
	var raw bytes.Buffer
	fw, _ := flate.NewWriter(&raw, flate.BestCompression)
	data := bytes.Repeat([]byte(" "), int(n))
	_, _ = fw.Write(data)
	_ = fw.Close()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	w, _ := z.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	_, _ = w.Write([]byte(MIME))
	rw, err := z.CreateRaw(&zip.FileHeader{Name: "document.json", Method: zip.Deflate, CRC32: crc32.ChecksumIEEE(data),
		CompressedSize64: uint64(raw.Len()), UncompressedSize64: 2})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = rw.Write(raw.Bytes())
	_ = z.Close()
	return buf.Bytes()
}

func TestDocumentAssetsAndLinks(t *testing.T) {
	assets, err := DocumentAssets([]byte(testDocument))
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 2 || assets[1].Name != "foto.png" || assets[1].Mime != "image/png" || assets[1].Link != "" || assets[2].Link != "/Users/x/logo.jpg" {
		t.Fatalf("assets %+v", assets)
	}
	doc, err := WithLinks([]byte(testDocument), map[uint64]string{1: "rc/attachments/0123456789abcdef.png", 2: "rc/img/logo.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	again, _ := DocumentAssets(doc)
	if again[1].Link != "rc/attachments/0123456789abcdef.png" || again[2].Link != "rc/img/logo.jpg" || again[1].Name != "foto.png" {
		t.Errorf("links %+v", again)
	}
	for _, want := range []string{`"big": 12345678901234567890`, `"ratio": 0.1`, `"<b>Titolo</b> & sottotitolo"`, "\n  \"assets\": {", `"pixels": [`} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("the document lost %q:\n%s", want, doc)
		}
	}
	var v map[string]any
	if err := json.Unmarshal(doc, &v); err != nil {
		t.Errorf("not JSON: %v", err)
	}
	for why, bad := range map[string]string{
		"not JSON":           "{",
		"assets not a map":   `{"assets": []}`,
		"an id not a number": `{"assets": {"x": {"id": 1}}}`,
	} {
		if _, err := DocumentAssets([]byte(bad)); err == nil {
			t.Errorf("%s: read", why)
		}
	}
	if a, err := DocumentAssets([]byte(`{"pages": []}`)); err != nil || len(a) != 0 {
		t.Errorf("a document without assets: %v %v", a, err)
	}
}
