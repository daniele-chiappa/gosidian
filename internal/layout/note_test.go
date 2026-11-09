package layout

import (
	"errors"
	"fmt"
	"path"
	"reflect"
	"strings"
	"testing"
)

// files is a small vault: the attachments of a test, by path.
type files map[string][]byte

func (f files) read(link string, max int64) ([]byte, string, bool) {
	b, ok := f[link]
	if !ok || int64(len(b)) > max {
		return nil, "", false
	}
	return b, strings.TrimPrefix(path.Ext(link), "."), true
}

// store keeps a file under a name its bytes give, as the attachments do.
func (f files) store(data []byte, name string) (string, error) {
	p := fmt.Sprintf("p/attachments/%04d%s", len(data), path.Ext(name))
	f[p] = data
	return p, nil
}

var limits = Limits{MaxBytes: 1 << 20, MaxDocument: 1 << 20, MaxEntries: 16}

func noCheck([]byte, string) error { return nil }

func TestETagOfADesign(t *testing.T) {
	if ETag("") != NoDesign || ETag("p/attachments/ab12.json") != "ab12.json" {
		t.Fatal(ETag(""), ETag("p/attachments/ab12.json"))
	}
}

func TestOpenANoteWithNoDesign(t *testing.T) {
	got, err := Open([]byte("---\ntitle: x\n---\nbody\n"), limits, files{}.read)
	if err != nil || got.File != "" || got.Package != nil {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestOpenADesignThatDoesNotRead(t *testing.T) {
	var unreadable *Unreadable
	_, err := Open([]byte("---\nlayout_file: p/attachments/none.json\n---\n"), limits, files{}.read)
	if !errors.As(err, &unreadable) || !strings.Contains(err.Error(), "p/attachments/none.json") {
		t.Fatalf("%v", err)
	}
	_, err = Open([]byte("---\nlayout_file: p/attachments/bad.json\n---\n"), limits, files{"p/attachments/bad.json": []byte("{")}.read)
	if !errors.As(err, &unreadable) {
		t.Fatalf("%v", err)
	}
}

func TestSaveAndOpenAgain(t *testing.T) {
	vault := files{}
	taken, problem := Take(designcraftPackage(t), limits, noCheck)
	if problem != nil {
		t.Fatal(problem)
	}
	content, saved, problem := taken.Store("p/n.md", []byte("---\ntitle: x\n---\nbody\n"), vault.store)
	if problem != nil {
		t.Fatal(problem)
	}
	if saved.ETag != path.Base(saved.File) || !strings.Contains(string(content), "layout_file: "+saved.File) || !strings.Contains(string(content), "body") {
		t.Fatalf("%+v\n%s", saved, content)
	}
	if DesignFile(content) != saved.File {
		t.Fatalf("DesignFile = %q", DesignFile(content))
	}
	opened, err := Open(content, limits, vault.read)
	if err != nil || opened.File != saved.File || len(opened.Missing) != 0 {
		t.Fatalf("%+v %v", opened, err)
	}
	back, err := Unpack(opened.Package, limits)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Assets) != len(saved.Assets) {
		t.Fatalf("assets back: %d of %d", len(back.Assets), len(saved.Assets))
	}
}

func TestOpenNamesTheAssetsItCannotRead(t *testing.T) {
	vault := files{}
	taken, problem := Take(designcraftPackage(t), limits, noCheck)
	if problem != nil {
		t.Fatal(problem)
	}
	content, saved, problem := taken.Store("p/n.md", []byte("---\ntitle: x\n---\n"), vault.store)
	if problem != nil {
		t.Fatal(problem)
	}
	var gone string
	for id, p := range saved.Assets {
		delete(vault, p)
		gone = id
	}
	opened, err := Open(content, limits, vault.read)
	if err != nil || len(opened.Missing) == 0 || !strings.Contains(strings.Join(opened.Missing, ","), gone) {
		t.Fatalf("%+v %v", opened, err)
	}
}

func TestTakeRefuses(t *testing.T) {
	if _, p := Take([]byte("no zip"), limits, noCheck); p == nil || !p.Malformed {
		t.Fatalf("not a package: %+v", p)
	}
	refuse := func(_ []byte, ext string) error {
		if ext == ".json" {
			return nil
		}
		return errors.New("no " + ext)
	}
	_, p := Take(designcraftPackage(t), limits, refuse)
	if p == nil || p.Malformed || p.Asset == nil {
		t.Fatalf("an asset refused: %+v", p)
	}
}

func TestStoreSaysWhatFailed(t *testing.T) {
	taken, problem := Take(designcraftPackage(t), limits, noCheck)
	if problem != nil {
		t.Fatal(problem)
	}
	_, _, p := taken.Store("p/n.md", []byte("x"), func([]byte, string) (string, error) { return "", errors.New("full") })
	if p == nil || p.Malformed || !strings.Contains(p.Msg, "full") {
		t.Fatalf("%+v", p)
	}
}

func TestImageTarget(t *testing.T) {
	bare := func(name string) string { return "p/attachments/" + name }
	for src, want := range map[string]string{
		"https://example.com/a.png":        "",
		"/vault-files/p/attachments/a.png": "p/attachments/a.png",
		"attachments/a%20b.png":            "p/d/attachments/a b.png",
		"a.png":                            "p/attachments/a.png",
	} {
		if got := ImageTarget("p/d/n.md", src, bare); got != want {
			t.Errorf("%q: %q, want %q", src, got, want)
		}
	}
}

// The PDFs of a design: one for each version, by its label, and the
// draft's, which an older layout_pdf was (IMP-229).
func TestPDFs(t *testing.T) {
	content := []byte("---\nlayout_pdfs: [\"V3: p/attachments/a.pdf\", \"draft: p/attachments/b.pdf\", \"bad\"]\n---\n")
	got := PDFs(content)
	want := []PDF{{Label: "V3", Path: "p/attachments/a.pdf"}, {Label: DraftPDF, Path: "p/attachments/b.pdf"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PDFs = %+v", got)
	}
	if got := WithPDF(got, DraftPDF, "p/attachments/c.pdf"); !reflect.DeepEqual(got, []string{"V3: p/attachments/a.pdf", "draft: p/attachments/c.pdf"}) {
		t.Errorf("the draft again = %q", got)
	}
	if got := WithPDF(got, "V4", "p/attachments/d.pdf"); !reflect.DeepEqual(got, []string{"V3: p/attachments/a.pdf", "draft: p/attachments/b.pdf", "V4: p/attachments/d.pdf"}) {
		t.Errorf("a new version = %q", got)
	}
	old := PDFs([]byte("---\nlayout_pdf: p/attachments/e.pdf\n---\n"))
	if !reflect.DeepEqual(old, []PDF{{Label: DraftPDF, Path: "p/attachments/e.pdf"}}) {
		t.Errorf("an older layout_pdf = %+v", old)
	}
	if PDFs([]byte("no frontmatter")) != nil {
		t.Error("PDFs of a note without them")
	}
}

func TestStart(t *testing.T) {
	page, template := Start([]byte("---\nlayout_page: \" A5 \"\nlayout_template: p/modello.md\n---\n"))
	if page != "A5" || template != "p/modello.md" {
		t.Fatalf("%q %q", page, template)
	}
	if page, template := Start([]byte("no frontmatter")); page != "" || template != "" {
		t.Fatalf("%q %q", page, template)
	}
}
