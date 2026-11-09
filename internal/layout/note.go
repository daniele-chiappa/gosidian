package layout

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/frontmatter"
	"github.com/gosidian/gosidian/internal/parser"
)

// A note's design as a package: Open puts it back together, Take checks a
// package saved and Store takes it apart into the attachments and the
// frontmatter of the note. The REST API and the MCP tools do the same with
// their own rights: they say how to read and store a file, and hold the
// note's lock.

// NoDesign is the etag of a note without a design.
const NoDesign = "none"

// ETag is the etag of a note's design, whose file is named by a hash of its
// bytes. It is not the note's: its text is written while the design is open,
// and only another version of the design is a conflict.
func ETag(file string) string {
	if file == "" {
		return NoDesign
	}
	return path.Base(file)
}

// DesignFile is the attachment a note's content names as its design, "" for
// none.
func DesignFile(content []byte) string {
	f, _ := parser.ParseFrontmatterFields(parser.ExtractFrontmatterRaw(content))["layout_file"].(string)
	return f
}

// Start is what a note without a design says its design starts from (IMP-232):
// a page, by the editor's name for it (A5) or as 148x210mm, and another layout
// note, whose styles, parents and page size it takes. Both go at the first save.
func Start(content []byte) (page, template string) {
	fields := parser.ParseFrontmatterFields(parser.ExtractFrontmatterRaw(content))
	page, _ = fields["layout_page"].(string)
	template, _ = fields["layout_template"].(string)
	return strings.TrimSpace(page), strings.TrimSpace(template)
}

// DraftPDF labels the PDF of a design as it is, beside those of its versions.
const DraftPDF = "draft"

// PDF is a PDF of a note's design kept beside the note (IMP-229): of a
// version, by its label (V3), or of the draft.
type PDF struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// PDFs are the PDFs a note's content names in layout_pdfs, whose items say
// "label: path"; the layout_pdf of before is the draft's.
func PDFs(content []byte) []PDF {
	fields := parser.ParseFrontmatterFields(parser.ExtractFrontmatterRaw(content))
	var items []string
	switch list := fields["layout_pdfs"].(type) {
	case []string:
		items = list
	case []any:
		for _, it := range list {
			if s, ok := it.(string); ok {
				items = append(items, s)
			}
		}
	}
	var out []PDF
	for _, it := range items {
		label, file, ok := strings.Cut(it, ": ")
		if ok && label != "" && file != "" {
			out = append(out, PDF{Label: label, Path: file})
		}
	}
	if old, _ := fields["layout_pdf"].(string); old != "" && !slices.ContainsFunc(out, func(p PDF) bool { return p.Label == DraftPDF }) {
		out = append(out, PDF{Label: DraftPDF, Path: old})
	}
	return out
}

// WithPDF is list with file as the PDF of label, in place of the one it
// had, as layout_pdfs writes it: in their order, a new label last.
func WithPDF(list []PDF, label, file string) []string {
	out := make([]string, 0, len(list)+1)
	placed := false
	for _, p := range list {
		if p.Label == label {
			p.Path, placed = file, true
		}
		out = append(out, p.Label+": "+p.Path)
	}
	if !placed {
		out = append(out, label+": "+file)
	}
	return out
}

// ReadLink reads the attachment a design links, at most max bytes, when the
// reader may read it: its bytes and its extension.
type ReadLink func(link string, max int64) ([]byte, string, bool)

// Opened is a note's design put back together.
type Opened struct {
	// File is the design's attachment; "" and no Package for a note with
	// no design yet, which the editor starts anew.
	File    string
	Package []byte
	// Missing are the assets whose link the reader may not follow, or that
	// is gone: they open without their bytes.
	Missing []string
}

// Unreadable is a design that does not read. It is an error and not a new
// design: the editor would save over it.
type Unreadable struct{ Msg string }

func (e *Unreadable) Error() string { return "the note's layout does not read: " + e.Msg }

// Open puts the design of a note back together, each asset with the bytes
// of its link when the reader may read it.
func Open(content []byte, lim Limits, read ReadLink) (*Opened, error) {
	fields := parser.ParseFrontmatterFields(parser.ExtractFrontmatterRaw(content))
	file, _ := fields["layout_file"].(string)
	out := &Opened{File: file}
	if file == "" {
		return out, nil
	}
	doc, _, ok := read(file, lim.MaxDocument)
	if !ok {
		return nil, &Unreadable{Msg: file}
	}
	assets, err := DocumentAssets(doc)
	if err != nil {
		return nil, &Unreadable{Msg: err.Error()}
	}
	pkg := &Package{Document: doc, Assets: map[uint64]File{}}
	if v, ok := version(fields["layout_format"]); ok {
		gen, _ := fields["layout_generator"].(string)
		pkg.Meta, _ = json.Marshal(map[string]any{"format": "designcraft", "version": v, "generator": gen})
	}
	left := lim.MaxBytes
	if prev, _ := fields["layout_preview"].(string); prev != "" {
		if data, _, ok := read(prev, left); ok {
			pkg.Preview = data
			left -= int64(len(data))
		}
	}
	for _, id := range sortedIDs(assets) {
		link := assets[id].Link
		if link == "" {
			continue
		}
		data, ext, ok := read(path.Join(path.Dir(file), link), left)
		if !ok {
			out.Missing = append(out.Missing, strconv.FormatUint(id, 10))
			continue
		}
		pkg.Assets[id] = File{Ext: ext, Data: data}
		left -= int64(len(data))
	}
	if out.Package, err = Pack(pkg); err != nil {
		return nil, fmt.Errorf("pack: %w", err)
	}
	return out, nil
}

func version(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case string:
		i, err := strconv.Atoi(n)
		return i, err == nil
	}
	return 0, false
}

func sortedIDs[V any](m map[uint64]V) []uint64 {
	ids := make([]uint64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Problem is why a package is refused.
type Problem struct {
	Msg string
	// Malformed says the package is wrong as a package; otherwise a file in
	// it is not one the vault takes.
	Malformed bool
	// Asset is the asset it is about, if any.
	Asset *uint64
}

func (p *Problem) Error() string { return p.Msg }

func malformed(msg string) *Problem { return &Problem{Msg: msg, Malformed: true} }

// Taken is a package saved, taken apart and checked: nothing is written yet.
type Taken struct {
	pkg    *Package
	assets map[uint64]DocAsset
	meta   struct {
		Version   int    `json:"version"`
		Generator string `json:"generator"`
	}
}

// Take reads a package and checks every file with check, before anything is
// written.
func Take(raw []byte, lim Limits, check func(data []byte, ext string) error) (*Taken, *Problem) {
	pkg, err := Unpack(raw, lim)
	if err != nil {
		return nil, malformed(err.Error())
	}
	assets, err := DocumentAssets(pkg.Document)
	if err != nil {
		return nil, malformed(err.Error())
	}
	t := &Taken{pkg: pkg, assets: assets}
	if pkg.Meta != nil {
		if err := json.Unmarshal(pkg.Meta, &t.meta); err != nil {
			return nil, malformed("meta.json: " + err.Error())
		}
	}
	if err := check(pkg.Document, ".json"); err != nil {
		return nil, &Problem{Msg: "document.json: " + err.Error()}
	}
	for _, id := range sortedIDs(pkg.Assets) {
		f := pkg.Assets[id]
		if err := check(f.Data, "."+f.Ext); err != nil {
			return nil, &Problem{Msg: fmt.Sprintf("asset %d (%s): %v", id, assets[id].Name, err), Asset: &id}
		}
	}
	if pkg.Preview != nil {
		if err := check(pkg.Preview, ".png"); err != nil {
			return nil, &Problem{Msg: "preview.png: " + err.Error()}
		}
	}
	return t, nil
}

// Saved is what a save answers: the design's new etag, its file and the
// vault path of each asset's.
type Saved struct {
	Path    string            `json:"path"`
	ETag    string            `json:"etag"`
	File    string            `json:"file"`
	Assets  map[string]string `json:"assets"`
	Preview string            `json:"preview,omitempty"`
}

// Store writes the package's files with store, which answers with the vault
// path of each, and gives the note's content naming them. The same bytes are
// the same attachment, so saving again adds no file. A new version is a new
// file: the one before stays for the note's history, until the attachments'
// GC.
func (t *Taken) Store(rel string, content []byte, store func(data []byte, name string) (string, error)) ([]byte, *Saved, *Problem) {
	links := map[uint64]string{}
	out := &Saved{Path: rel, Assets: map[string]string{}}
	for _, id := range sortedIDs(t.pkg.Assets) {
		if _, ok := t.assets[id]; !ok {
			continue // a file the document does not place
		}
		f := t.pkg.Assets[id]
		p, err := store(f.Data, "asset."+f.Ext)
		if err != nil {
			return nil, nil, &Problem{Msg: fmt.Sprintf("asset %d: %v", id, err)}
		}
		links[id] = path.Base(p)
		out.Assets[strconv.FormatUint(id, 10)] = p
	}
	if t.pkg.Preview != nil {
		p, err := store(t.pkg.Preview, "preview.png")
		if err != nil {
			return nil, nil, &Problem{Msg: "preview.png: " + err.Error()}
		}
		out.Preview = p
	}
	doc, err := WithLinks(t.pkg.Document, links)
	if err != nil {
		return nil, nil, malformed(err.Error())
	}
	if out.File, err = store(doc, "layout.json"); err != nil {
		return nil, nil, &Problem{Msg: "document.json: " + err.Error()}
	}
	out.ETag = ETag(out.File)
	set := []frontmatter.Field{{Key: "layout_file", Value: out.File}, {Key: "layout_format", Value: t.meta.Version}, {Key: "layout_generator", Value: t.meta.Generator}}
	var unset []string
	if out.Preview != "" {
		set = append(set, frontmatter.Field{Key: "layout_preview", Value: out.Preview})
	} else {
		unset = append(unset, "layout_preview")
	}
	// What the design started from goes once it is saved (IMP-232).
	unset = append(unset, "layout_page", "layout_template")
	next, err := frontmatter.SetKeys(content, set, unset)
	if err != nil {
		return nil, nil, malformed("frontmatter: " + err.Error() + ": edit the note as text")
	}
	return next, out, nil
}

// ImageTarget is where an image of the note at rel points: a link
// /vault-files/…, a path beside the note, or a bare name, as in ![[name]],
// which bare resolves. "" for an address elsewhere. Whether it is an
// attachment the reader may read is for the caller to say.
func ImageTarget(rel, src string, bare func(name string) string) string {
	if strings.Contains(src, "://") {
		return ""
	}
	if s, err := url.PathUnescape(src); err == nil {
		src = s
	}
	if vault, ok := strings.CutPrefix(src, "/vault-files/"); ok {
		return vault
	}
	if strings.Contains(src, "/") {
		return path.Join(path.Dir(rel), src)
	}
	return bare(src)
}
