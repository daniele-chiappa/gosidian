package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"path"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gosidian/gosidian/internal/infolder"
	"github.com/gosidian/gosidian/internal/jobs"
)

// pngOf is a PNG of w×h pixels of one colour.
func pngOf(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		for y := range h {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

const wikiDoc = `---
title: "Doc one"
id: 42
source: Zendesk
---

# Doc one

## Setup

### Window

The window opens with the defaults set.

![Alt text](Doc%20one_img/001.png)

The button ![](Doc%20one_img/002.png) saves it.

See [the other one](../Other.md) and [the site](https://example.com/x).

![](Doc%20one_img/404.png)

![out](../../../../x.png)

` + "```" + `
![in code](Doc%20one_img/003.png)
` + "```" + `

## Again

![Alt text](Doc%20one_img/001.png)
`

const importPipeline = `
trigger: {on: manual}
nodes:
  - id: import
    use: in.import
    with: {file: "{{ trigger.file }}", base: wiki, dest: documents/wiki}
`

func startImport(t *testing.T, e *Engine) (jobs.Run, map[string]any) {
	t.Helper()
	run, _, err := e.Start(StartRequest{Path: "p/pipelines/wiki-md.md", Trigger: TriggerManual,
		Input: map[string]any{"file": "p/IN/wiki/A/B/Doc one.md"}})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	var out map[string]any
	if err := json.Unmarshal(stepOf(t, e, run.ID, "import").Output, &out); err != nil {
		t.Fatal(err)
	}
	return run, out
}

func mustContain(t *testing.T, what, s string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Errorf("%s lacks %q:\n%s", what, w, s)
		}
	}
}

// A markdown file of IN becomes a note under dest with its tree under base:
// its images stored once each and linked as attachments, its frontmatter
// kept with where it comes from, an image note for each image.
func TestINImport_NoteAttachmentsImageNotes(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/pipelines/wiki-md.md"] = pipelineNote(importPipeline)
	host.att["p/IN/wiki/A/B/Doc one.md"] = []byte(wikiDoc)
	host.att["p/IN/wiki/A/B/Doc one_img/001.png"] = pngOf(t, 40, 30, color.White)
	host.att["p/IN/wiki/A/B/Doc one_img/002.png"] = pngOf(t, 8, 8, color.Black)
	run, out := startImport(t, e)

	host.mu.Lock()
	if len(host.stored) != 2 {
		t.Fatalf("stored %v, want the two images once each", host.stored)
	}
	var att1, att2 string
	for _, s := range host.stored {
		p := strings.Fields(s)[1]
		if bytes.Equal(host.att[p], host.att["p/IN/wiki/A/B/Doc one_img/001.png"]) {
			att1 = p
		} else {
			att2 = p
		}
	}
	host.mu.Unlock()
	note := host.note("p/documents/wiki/A/B/Doc one.md")
	mustContain(t, "the note", note,
		"title: \"Doc one\"\nid: 42\nsource: Zendesk\n",
		"in_file: p/IN/wiki/A/B/Doc one.md",
		"pipeline: \"[[p/pipelines/wiki-md]]\"",
		"pipeline_run: "+run.ID,
		"tags: [p]",
		"![Alt text](/vault-files/"+att1+")",
		"The button ![](/vault-files/"+att2+") saves it.",
		"[[p/documents/wiki/A/Other|the other one]]",
		"[the site](https://example.com/x)",
		"![](Doc%20one_img/404.png)",
		"![out](../../../../x.png)",
		"![in code](Doc%20one_img/003.png)")
	if strings.Count(note, "/vault-files/"+att1) != 2 {
		t.Errorf("the image used twice is not linked twice:\n%s", note)
	}

	img1 := host.note("p/documents/wiki/A/B/Doc one_img/001.md")
	mustContain(t, "the first image note", img1,
		"type: image", "media: "+att1,
		"article: \"[[p/documents/wiki/A/B/Doc one]]\"",
		"position: 1", "occurrences: 2", "inline: false", "section: Setup > Window", "in_sha256: ",
		"alt_original: Alt text", "width: 40", "height: 30",
		"in_file: p/IN/wiki/A/B/Doc one_img/001.png", "tags: [p, type:image]")
	img2 := host.note("p/documents/wiki/A/B/Doc one_img/002.md")
	mustContain(t, "the second image note", img2, "media: "+att2, "position: 2", "occurrences: 1", "inline: true", "width: 8")
	if strings.Contains(img2, "alt_original") || strings.Contains(img1, "title:") {
		t.Errorf("an empty alt, or a title before a description:\n%s\n%s", img1, img2)
	}

	if out["path"] != "p/documents/wiki/A/B/Doc one.md" || out["created"] != true || out["attachments"] != 2.0 {
		t.Errorf("output %v", out)
	}
	if imgs, _ := out["images"].([]any); len(imgs) != 2 || out["image_count"] != 2.0 {
		t.Errorf("images %v, count %v", out["images"], out["image_count"])
	}
	unresolved := strList(out["unresolved"])
	if !slices.Contains(unresolved, "Doc%20one_img/404.png") || !slices.Contains(unresolved, "../../../../x.png") {
		t.Errorf("unresolved %v", unresolved)
	}
	if sk, _ := out["skipped"].([]any); len(sk) != 1 || sk[0].(map[string]any)["src"] != "A/B/Doc one_img/404.png" {
		t.Errorf("skipped %v", out["skipped"])
	}
}

// Imported again, an image note keeps its description; an image that
// changed loses it, and the note of an image no longer there goes.
func TestINImport_AgainKeepsDescriptions(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/pipelines/wiki-md.md"] = pipelineNote(importPipeline)
	host.att["p/IN/wiki/A/B/Doc one.md"] = []byte(wikiDoc)
	host.att["p/IN/wiki/A/B/Doc one_img/001.png"] = pngOf(t, 40, 30, color.White)
	host.att["p/IN/wiki/A/B/Doc one_img/002.png"] = pngOf(t, 8, 8, color.Black)
	startImport(t, e)

	described := func(p string) {
		host.mu.Lock()
		c := host.notes[p]
		host.notes[p] = strings.Replace(c, "type: image\n", "type: image\ntitle: A window\nlabel: A window\nmodel: vis\nmine: kept\n", 1) + "\nA window with its defaults.\n"
		host.mu.Unlock()
	}
	img1, img2 := "p/documents/wiki/A/B/Doc one_img/001.md", "p/documents/wiki/A/B/Doc one_img/002.md"
	described(img1)
	described(img2)
	_, out := startImport(t, e)
	if out["created"] != false {
		t.Errorf("output %v", out)
	}
	for _, p := range []string{img1, img2} {
		mustContain(t, p, host.note(p), "title: A window", "label: A window", "mine: kept", "A window with its defaults.")
	}

	host.mu.Lock()
	host.att["p/IN/wiki/A/B/Doc one_img/001.png"] = pngOf(t, 40, 30, color.Gray{Y: 128})
	host.att["p/IN/wiki/A/B/Doc one.md"] = []byte(strings.Replace(wikiDoc, "The button ![](Doc%20one_img/002.png) saves it.", "", 1))
	host.mu.Unlock()
	_, out = startImport(t, e)
	c := host.note(img1)
	mustContain(t, "the changed image's note", c, "mine: kept")
	for _, gone := range []string{"title:", "label:", "model:", "A window with its defaults."} {
		if strings.Contains(c, gone) {
			t.Errorf("the changed image keeps %q:\n%s", gone, c)
		}
	}
	host.mu.Lock()
	deleted := slices.Clone(host.deleted)
	host.mu.Unlock()
	if host.note(img2) != "" || !slices.Contains(deleted, img2) {
		t.Errorf("the note of the image taken away is there: %v", deleted)
	}
	if r := strList(out["removed"]); len(r) != 1 || r[0] != img2 {
		t.Errorf("removed %v", out["removed"])
	}
}

// list_images: false leaves the image notes out of the output, for an each
// over many files, whose outputs make one step's.
func TestINImport_CountOnly(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/pipelines/wiki-md.md"] = pipelineNote(strings.Replace(importPipeline, "dest: documents/wiki}", "dest: documents/wiki, list_images: false}", 1))
	host.att["p/IN/wiki/A/B/Doc one.md"] = []byte(wikiDoc)
	host.att["p/IN/wiki/A/B/Doc one_img/001.png"] = pngOf(t, 40, 30, color.White)
	host.att["p/IN/wiki/A/B/Doc one_img/002.png"] = pngOf(t, 8, 8, color.Black)
	_, out := startImport(t, e)
	if _, listed := out["images"]; listed || out["image_count"] != 2.0 {
		t.Errorf("output %v", out)
	}
}

// A note in the image folder that is not an image note of the article is
// left alone: the image note takes another name.
func TestINImport_LeavesOtherNotes(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/pipelines/wiki-md.md"] = pipelineNote(importPipeline)
	host.att["p/IN/wiki/A/B/Doc one.md"] = []byte("# Doc one\n\n![](Doc%20one_img/001.png)\n")
	host.att["p/IN/wiki/A/B/Doc one_img/001.png"] = pngOf(t, 4, 4, color.White)
	host.notes["p/documents/wiki/A/B/Doc one_img/001.md"] = "---\ntitle: Mine\n---\n\nMy note.\n"
	_, out := startImport(t, e)
	if host.note("p/documents/wiki/A/B/Doc one_img/001.md") != "---\ntitle: Mine\n---\n\nMy note.\n" {
		t.Error("a note of the user was written over")
	}
	mustContain(t, "the image note", host.note("p/documents/wiki/A/B/Doc one_img/001-2.md"), "type: image", "position: 1")
	note := host.note("p/documents/wiki/A/B/Doc one.md")
	mustContain(t, "a note without frontmatter", note, "in_file: p/IN/wiki/A/B/Doc one.md", "tags: [p]")
	if len(strList(out["removed"])) != 0 {
		t.Errorf("removed %v", out["removed"])
	}
}

func TestINImport_Refusals(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.att["p/IN/wiki/a.txt"] = []byte("text")
	host.att["p/IN/other/a.md"] = []byte("# A\n")
	cases := map[string]struct{ with, want string }{
		"not markdown": {`{file: wiki/a.txt, dest: docs}`, "is not a markdown file"},
		"not in base":  {`{file: other/a.md, base: wiki, dest: docs}`, "is not under with.base"},
		"dest in IN":   {`{file: other/a.md, dest: IN/x}`, "is inside the IN folder"},
		"no dest":      {`{file: other/a.md}`, "with.dest is required"},
		"climbs":       {`{file: "../a.md", dest: docs}`, "a plain path inside the IN folder"},
	}
	for name, c := range cases {
		host.mu.Lock()
		host.notes["p/pipelines/x.md"] = pipelineNote("nodes:\n  - {id: import, use: in.import, with: " + c.with + "}")
		host.mu.Unlock()
		run, _, err := e.Start(StartRequest{Path: "p/pipelines/x.md", Trigger: TriggerManual})
		if err != nil {
			t.Fatal(name, err)
		}
		failed := waitStatus(t, e, run.ID, jobs.Failed)
		if !strings.Contains(failed.Error, c.want) {
			t.Errorf("%s: %q, want %q", name, failed.Error, c.want)
		}
	}
}

// The files in.import reads are done with its run; the document is done
// when the run ends, as any file an in run takes.
func TestINImport_MarksTheFilesItRead(t *testing.T) {
	f := newINFixture(t)
	f.host.notes["p/pipelines/wiki-md.md"] = pipelineNote(`
trigger: {on: in, folder: wiki, kinds: [md]}
nodes:
  - {id: import, use: in.import, with: {file: "{{ trigger.file }}", base: wiki, dest: docs}}
`)
	doc, img := "p/IN/wiki/a.md", "p/IN/wiki/a_img/1.png"
	data := pngOf(t, 2, 2, color.White)
	f.write(t, doc, "# A\n\n![](a_img/1.png)\n")
	f.write(t, img, string(data))
	f.host.att[doc] = []byte("# A\n\n![](a_img/1.png)\n")
	f.host.att[img] = data
	f.arrive(t)
	runs := f.runs(t, "p/pipelines/wiki-md.md")
	if len(runs) != 1 {
		t.Fatalf("%d runs, want 1 (the images are not taken)", len(runs))
	}
	waitStatus(t, f.e, runs[0].ID, jobs.Done)
	if got := f.status(t, img); got.Status != infolder.StatusDone || got.Run != runs[0].ID {
		t.Errorf("the image read: %+v", got)
	}
	eventually(t, func() bool { return f.status(t, doc).Status == infolder.StatusDone }, "the document done")
	mustContain(t, "the image note", f.host.note("p/docs/a_img/1.md"), "type: image")
}

// fakeConvert is a convert node as images.convert answers: it keeps the
// files named keep.png, fails on bad.png, and gives back the others as
// <stem>.webp, their bytes prefixed by version. It counts its calls.
type fakeConvert struct {
	mu      sync.Mutex
	calls   int
	version string
}

func (f *fakeConvert) Run(_ context.Context, env *Env, with map[string]any) (any, error) {
	f.mu.Lock()
	f.calls++
	version := f.version
	f.mu.Unlock()
	list, _ := with["files"].([]any)
	results, files := []any{}, []any{}
	for _, it := range list {
		p := it.(string)
		name := path.Base(p)
		switch name {
		case "keep.png":
			results = append(results, map[string]any{"name": name, "kept": true, "reason": "would weigh more"})
			continue
		case "bad.png":
			results = append(results, map[string]any{"name": name, "error": "not an image"})
			continue
		}
		_, data, err := env.ReadAttachment(p)
		if err != nil {
			return nil, err
		}
		out := strings.TrimSuffix(name, path.Ext(name)) + ".webp"
		rel, err := env.StoreAttachment(out, append([]byte(version), data...))
		if err != nil {
			return nil, err
		}
		results = append(results, map[string]any{"name": name, "output": out, "width": 7.0, "height": 5.0})
		files = append(files, map[string]any{"name": out, "path": rel})
	}
	return map[string]any{"results": results, "files": files}, nil
}

// convert: the images go to the convert node in batches, the converted ones
// are linked and get image notes; one kept or failed is stored as it is.
// A new conversion of the same originals keeps the descriptions.
func TestINImport_Convert(t *testing.T) {
	e, host := newEngine(t, Config{})
	fc := &fakeConvert{version: "v1"}
	e.Types().Register("fake.convert", fc)
	host.notes["p/pipelines/wiki-md.md"] = pipelineNote(strings.Replace(importPipeline, "dest: documents/wiki}",
		"dest: documents/wiki, convert: fake.convert, convert_with: {format: webp}}", 1))
	var doc strings.Builder
	doc.WriteString("# Doc one\n\n")
	for i := range 30 {
		name := fmt.Sprintf("%03d.png", i)
		doc.WriteString("![](Doc%20one_img/" + name + ")\n\n")
		host.att["p/IN/wiki/A/B/Doc one_img/"+name] = pngOf(t, 3+i, 2, color.White) // before any run
	}
	doc.WriteString("![](Doc%20one_img/keep.png)\n\n![](Doc%20one_img/bad.png)\n")
	host.att["p/IN/wiki/A/B/Doc one.md"] = []byte(doc.String())
	host.att["p/IN/wiki/A/B/Doc one_img/keep.png"] = pngOf(t, 4, 4, color.Black)
	host.att["p/IN/wiki/A/B/Doc one_img/bad.png"] = pngOf(t, 5, 5, color.Black)
	_, out := startImport(t, e)

	fc.mu.Lock()
	calls := fc.calls
	fc.mu.Unlock()
	if calls != 2 || out["converted"] != 30.0 {
		t.Fatalf("%d calls, output %v", calls, out)
	}
	if errs, _ := out["convert_errors"].([]any); len(errs) != 1 || errs[0].(map[string]any)["src"] != "A/B/Doc one_img/bad.png" {
		t.Errorf("convert_errors %v", out["convert_errors"])
	}
	note := host.note("p/documents/wiki/A/B/Doc one.md")
	if strings.Count(note, ".webp)") != 30 || strings.Count(note, ".png)") != 2 {
		t.Errorf("links:\n%s", note)
	}
	img := host.note("p/documents/wiki/A/B/Doc one_img/003.md")
	mustContain(t, "a converted image's note", img, ".webp\n", "width: 7", "height: 5", "in_sha256: ")
	mustContain(t, "a kept image's note", host.note("p/documents/wiki/A/B/Doc one_img/keep.md"), ".png\n", "width: 4")

	// Described, then converted again with other bytes: the description
	// stays, the media is the new attachment.
	host.mu.Lock()
	host.notes["p/documents/wiki/A/B/Doc one_img/003.md"] = strings.Replace(img, "type: image\n", "type: image\nlabel: A box\n", 1) + "\nA white box.\n"
	host.mu.Unlock()
	fc.mu.Lock()
	fc.version = "v2"
	fc.mu.Unlock()
	startImport(t, e)
	again := host.note("p/documents/wiki/A/B/Doc one_img/003.md")
	mustContain(t, "the note converted again", again, "label: A box", "A white box.")
	if again == img || !strings.Contains(again, "media: p/attachments/") {
		t.Errorf("the media did not change:\n%s", again)
	}
}

// A note of before in_sha256 keeps its description while its sha256 is the
// original's, and gets in_sha256 in its place.
func TestINImport_LegacySHA256(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/pipelines/wiki-md.md"] = pipelineNote(importPipeline)
	data := pngOf(t, 4, 4, color.White)
	host.att["p/IN/wiki/A/B/Doc one.md"] = []byte("# Doc one\n\n![](Doc%20one_img/001.png)\n")
	host.att["p/IN/wiki/A/B/Doc one_img/001.png"] = data
	startImport(t, e)
	p := "p/documents/wiki/A/B/Doc one_img/001.md"
	sum := sha256.Sum256(data)
	host.mu.Lock()
	c := host.notes[p]
	c = strings.Replace(c, "in_sha256: "+hex.EncodeToString(sum[:]), "sha256: "+hex.EncodeToString(sum[:]), 1)
	host.notes[p] = strings.Replace(c, "type: image\n", "type: image\nlabel: Old\n", 1) + "\nDescribed before.\n"
	host.mu.Unlock()
	startImport(t, e)
	c = host.note(p)
	mustContain(t, "the legacy note", c, "label: Old", "Described before.", "in_sha256: "+hex.EncodeToString(sum[:]))
	if strings.Contains(c, "\nsha256:") {
		t.Errorf("sha256 is still there:\n%s", c)
	}
}

// failConvert is a convert node that fails whole, as a plugin down or one
// that converted nothing in a batch answers.
type failConvert struct{}

func (failConvert) Run(context.Context, *Env, map[string]any) (any, error) {
	return nil, errors.New("the plugin is down")
}

// A batch the convert node fails whole: its images are stored as they are,
// each with the reason, and the document is imported.
func TestINImport_ConvertFailingWhole(t *testing.T) {
	e, host := newEngine(t, Config{})
	e.Types().Register("fake.convert", failConvert{})
	host.notes["p/pipelines/wiki-md.md"] = pipelineNote(strings.Replace(importPipeline, "dest: documents/wiki}",
		"dest: documents/wiki, convert: fake.convert}", 1))
	host.att["p/IN/wiki/A/B/Doc one.md"] = []byte("# Doc one\n\n![](Doc%20one_img/a.png)\n\n![](Doc%20one_img/b.png)\n")
	host.att["p/IN/wiki/A/B/Doc one_img/a.png"] = pngOf(t, 4, 4, color.Black)
	host.att["p/IN/wiki/A/B/Doc one_img/b.png"] = pngOf(t, 5, 5, color.White)
	_, out := startImport(t, e)

	if errs, _ := out["convert_errors"].([]any); len(errs) != 2 || !strings.Contains(fmt.Sprint(errs[0]), "the plugin is down") {
		t.Errorf("convert_errors %v", out["convert_errors"])
	}
	if strings.Count(host.note("p/documents/wiki/A/B/Doc one.md"), ".png)") != 2 {
		t.Errorf("links:\n%s", host.note("p/documents/wiki/A/B/Doc one.md"))
	}
	mustContain(t, "an image stored as it is", host.note("p/documents/wiki/A/B/Doc one_img/a.md"), ".png\n", "width: 4")
}
