package pkgimport

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var lim = Limits{MaxFiles: 100, MaxBytes: 1 << 20}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func paths(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Path)
	}
	return out
}

func TestReadDir(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{"INDEX.md": "# I", "a/b.md": "b", ".git/HEAD": "x", "a/.DS_Store": "x", "img/p.png": "png"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "a", "link.md")); err != nil {
		t.Fatal(err)
	}
	es, sk, err := ReadDir(root, lim)
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(es); !reflect.DeepEqual(got, []string{"INDEX.md", "a/b.md", "img/p.png"}) {
		t.Errorf("entries = %v", got)
	}
	if len(sk) != 1 || sk[0].Path != "a/link.md" || !strings.Contains(sk[0].Reason, "symbolic link") {
		t.Errorf("skipped = %+v", sk)
	}
	if _, _, err := ReadDir(root, Limits{MaxFiles: 2, MaxBytes: 1 << 20}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("file cap: %v", err)
	}
	if _, _, err := ReadDir(root, Limits{MaxFiles: 10, MaxBytes: 5}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("byte cap: %v", err)
	}
}

func TestReadZip(t *testing.T) {
	// The zip of a folder: the folder that wraps everything is dropped.
	es, _, err := ReadZip(zipOf(t, map[string]string{"guide/INDEX.md": "# I", "guide/a/b.md": "b", "__MACOSX/guide/._b.md": "x"}), lim)
	if err != nil {
		t.Fatal(err)
	}
	got := paths(es)
	if len(got) != 2 || !strings.Contains(strings.Join(got, ","), "INDEX.md") || !strings.Contains(strings.Join(got, ","), "a/b.md") ||
		strings.Contains(strings.Join(got, ","), "guide/") {
		t.Errorf("entries = %v", got)
	}
	for name, files := range map[string]map[string]string{
		"climbs":   {"a.md": "a", "../evil.md": "x"},
		"absolute": {"/etc/evil.md": "x"},
		"windows":  {`..\evil.md`: "x"},
		"drive":    {"C:/evil.md": "x"},
	} {
		if _, _, err := ReadZip(zipOf(t, files), lim); !errors.Is(err, ErrUnsafe) {
			t.Errorf("%s: err = %v, want ErrUnsafe", name, err)
		}
	}
	// A zip bomb is read up to the limit, never past it.
	bomb := zipOf(t, map[string]string{"big.md": strings.Repeat("0", 2<<20)})
	if len(bomb) > 1<<20 {
		t.Fatalf("the test zip should be small: %d", len(bomb))
	}
	if _, _, err := ReadZip(bomb, lim); !errors.Is(err, ErrTooLarge) {
		t.Errorf("bomb: %v", err)
	}
	if _, _, err := ReadZip([]byte("not a zip"), lim); err == nil {
		t.Error("not a zip")
	}
}

func opts() Options {
	return Options{
		Project: "p",
		Dest:    "p/docs/guide",
		IsNote: func(name string) bool {
			ext := path.Ext(name)
			return ext == ".md" || ext == ".html"
		},
		AttachmentPath: func(name string, data []byte) (string, error) {
			if path.Ext(name) != ".png" {
				return "", errors.New("not accepted")
			}
			return "p/attachments/hash.png", nil
		},
	}
}

func TestBuild_Markdown(t *testing.T) {
	es := []Entry{
		{Path: "INDEX.md", Data: []byte("# Guide\n\n- [Setup](setup/install.md)\n- [Ports](setup/install.md#ports \"title\")\n- ![logo](img/logo.png)\n" +
			"- [site](https://example.org/x.md) and [top](#guide) and [abs](/notes/x.md)\n- [gone](setup/missing.md) [out](../outside.md)\n" +
			"- `[code](setup/install.md)` stays\n\n```md\n[fenced](setup/install.md)\n```\n")},
		{Path: "setup/install.md", Data: []byte("---\ntitle: Install\n---\n\nBack to [the index](../INDEX.md) and [x|y](../INDEX.md).\n")},
		{Path: "img/logo.png", Data: []byte("png")},
		{Path: "notes.txt", Data: []byte("x")},
	}
	p, err := Build(es, []Skipped{{Path: "link.md", Reason: "symbolic link, not followed"}}, opts())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Notes) != 2 || p.Notes[0].Path != "p/docs/guide/INDEX.md" || p.Notes[1].Path != "p/docs/guide/setup/install.md" {
		t.Fatalf("notes = %+v", p.Notes)
	}
	idx := string(p.Notes[0].Data)
	for _, want := range []string{
		"---\ntitle: \"Guide\"\ntags: [\"p\"]\n---\n\n# Guide",
		"- [[p/docs/guide/setup/install|Setup]]",
		"- [[p/docs/guide/setup/install#ports|Ports]]",
		"- ![logo](/vault-files/p/attachments/hash.png)",
		"[site](https://example.org/x.md) and [top](#guide) and [abs](/notes/x.md)",
		"[gone](setup/missing.md) [out](../outside.md)",
		"`[code](setup/install.md)` stays",
		"[fenced](setup/install.md)\n```",
	} {
		if !strings.Contains(idx, want) {
			t.Errorf("INDEX lacks %q:\n%s", want, idx)
		}
	}
	if !p.Notes[0].FrontmatterAdded || !reflect.DeepEqual(p.Notes[0].Unresolved, []string{"setup/missing.md", "../outside.md"}) {
		t.Errorf("INDEX: added %v, unresolved %v", p.Notes[0].FrontmatterAdded, p.Notes[0].Unresolved)
	}
	inst := string(p.Notes[1].Data)
	if p.Notes[1].FrontmatterAdded || !strings.HasPrefix(inst, "---\ntitle: Install\n---") ||
		!strings.Contains(inst, "[[p/docs/guide/INDEX|the index]] and [[p/docs/guide/INDEX|x-y]]") {
		t.Errorf("install:\n%s", inst)
	}
	if len(p.Attachments) != 1 || p.Attachments[0].Path != "p/attachments/hash.png" {
		t.Errorf("attachments = %+v", p.Attachments)
	}
	if want := []Skipped{{"link.md", "symbolic link, not followed"}, {"notes.txt", "not accepted"}}; !reflect.DeepEqual(p.Skipped, want) {
		t.Errorf("skipped = %+v", p.Skipped)
	}
}

func TestBuild_HTML(t *testing.T) {
	es := []Entry{
		{Path: "report.html", Data: []byte("<html><head><title>The <b>report</b></title></head><body><a href=\"part/b.html#x\">b</a> <a href='other.md'>o</a> <img src=\"img/logo.png\"> <a href=\"gone.html\">g</a> <a href=\"https://x.org\">x</a></body></html>")},
		{Path: "part/b.html", Data: []byte("<!--\n---\ntitle: B\n---\n-->\n<p>b</p>")},
		{Path: "other.md", Data: []byte("# Other")},
		{Path: "img/logo.png", Data: []byte("png")},
	}
	p, err := Build(es, nil, opts())
	if err != nil {
		t.Fatal(err)
	}
	var report, b string
	var rep Note
	for _, n := range p.Notes {
		switch n.Src {
		case "report.html":
			report, rep = string(n.Data), n
		case "part/b.html":
			b = string(n.Data)
		}
	}
	for _, want := range []string{"<!--\n---\ntitle: \"The report\"\ntags: [\"p\"]\n---\n-->\n<html>", `href="part/b.html#x"`, `href='other.md'`,
		`src="/vault-files/p/attachments/hash.png"`, `href="https://x.org"`} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	if !rep.FrontmatterAdded || !reflect.DeepEqual(rep.Unresolved, []string{"gone.html"}) {
		t.Errorf("report: %+v", rep)
	}
	if !strings.HasPrefix(b, "<!--\n---\ntitle: B") {
		t.Errorf("b keeps its frontmatter:\n%s", b)
	}
}

func TestBuild_CaseCollision(t *testing.T) {
	if _, err := Build([]Entry{{Path: "A.md"}, {Path: "a.md"}}, nil, opts()); err == nil || !strings.Contains(err.Error(), "differ only by case") {
		t.Errorf("err = %v", err)
	}
}
