// Package layout reads and writes the page layouts of the notes (IMP-226).
// A layout is made in designcraft's editor, which saves a package
// (.designcraft): a zip of its document.json, a preview and the files placed
// in its pages. gosidian takes the package apart into attachments, the
// document as a .json the note names and the files each asset links, and
// puts it back together for the editor. The note's text stays as it is.
package layout

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
)

// MIME is a designcraft package's type, the content of its mimetype entry.
const MIME = "application/vnd.designcraft+zip"

// Limits cap a package as it is unpacked: on the bytes read, not on the
// sizes the zip declares.
type Limits struct {
	// MaxBytes caps all the entries unpacked.
	MaxBytes int64
	// MaxDocument caps document.json.
	MaxDocument int64
	// MaxEntries caps the entries.
	MaxEntries int
}

// maxMeta caps meta.json and mimetype, a few dozen bytes.
const maxMeta = 64 << 10

// Package is a designcraft package taken apart.
type Package struct {
	// Meta is meta.json: the format's version and the generator.
	Meta []byte
	// Document is document.json, the layout without the files' bytes.
	Document []byte
	// Preview is preview.png, a picture of the first page; nil without.
	Preview []byte
	// Assets are the placed files, assets/<id>.<ext>, by id.
	Assets map[uint64]File
}

// File is a placed file: its extension in the package and its bytes.
type File struct {
	Ext  string
	Data []byte
}

var assetEntry = regexp.MustCompile(`^assets/([0-9]{1,19})\.([a-z0-9]{1,8})$`)

// Unpack takes a package apart. It refuses an entry it does not know (a
// path out of the package among them), an entry twice, and a package past
// lim.
func Unpack(b []byte, lim Limits) (*Package, error) {
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, fmt.Errorf("not a designcraft package: %w", err)
	}
	if len(z.File) > lim.MaxEntries {
		return nil, fmt.Errorf("the package has %d entries, at most %d", len(z.File), lim.MaxEntries)
	}
	p := &Package{Assets: map[uint64]File{}}
	left := lim.MaxBytes
	seen := map[string]bool{}
	var mime []byte
	for _, f := range z.File {
		if seen[f.Name] {
			return nil, fmt.Errorf("the package has %s twice", f.Name)
		}
		seen[f.Name] = true
		limit := left
		var id uint64
		var ext string
		switch f.Name {
		case "mimetype", "meta.json":
			limit = min(limit, maxMeta)
		case "document.json":
			limit = min(limit, lim.MaxDocument)
		case "preview.png":
		default:
			m := assetEntry.FindStringSubmatch(f.Name)
			if m == nil {
				return nil, fmt.Errorf("the package has %q, which is no entry of a designcraft package", f.Name)
			}
			if id, err = strconv.ParseUint(m[1], 10, 64); err != nil {
				return nil, fmt.Errorf("the package has %q, which is no entry of a designcraft package", f.Name)
			}
			ext = m[2]
		}
		data, err := read(f, limit)
		if err != nil {
			return nil, err
		}
		left -= int64(len(data))
		switch f.Name {
		case "mimetype":
			mime = data
		case "meta.json":
			p.Meta = data
		case "document.json":
			p.Document = data
		case "preview.png":
			p.Preview = data
		default:
			p.Assets[id] = File{Ext: ext, Data: data}
		}
	}
	switch {
	case mime == nil:
		return nil, errors.New("not a designcraft package: it has no mimetype")
	case string(mime) != MIME:
		return nil, fmt.Errorf("not a designcraft package: its mimetype is %q", mime)
	case p.Document == nil:
		return nil, errors.New("the package has no document.json")
	}
	return p, nil
}

// read reads an entry, at most limit bytes.
func read(f *zip.File, limit int64) ([]byte, error) {
	if f.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("%s is larger than the %d bytes left", f.Name, limit)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Name, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than the %d bytes left", f.Name, limit)
	}
	return data, nil
}

// stored are the extensions of the files already compressed, which a
// package stores as they are.
var stored = map[string]bool{"png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true}

// Pack puts a package together the way designcraft writes one: mimetype
// first and stored, then meta.json, the preview, document.json and the
// assets by id.
func Pack(p *Package) ([]byte, error) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	put := func(name string, method uint16, data []byte) error {
		w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	if err := put("mimetype", zip.Store, []byte(MIME)); err != nil {
		return nil, err
	}
	if p.Meta != nil {
		if err := put("meta.json", zip.Deflate, p.Meta); err != nil {
			return nil, err
		}
	}
	if p.Preview != nil {
		if err := put("preview.png", zip.Store, p.Preview); err != nil {
			return nil, err
		}
	}
	if err := put("document.json", zip.Deflate, p.Document); err != nil {
		return nil, err
	}
	ids := make([]uint64, 0, len(p.Assets))
	for id := range p.Assets {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		a := p.Assets[id]
		method := zip.Deflate
		if stored[a.Ext] {
			method = zip.Store
		}
		if err := put(fmt.Sprintf("assets/%d.%s", id, a.Ext), method, a.Data); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DocAsset is an asset as document.json says it: link is the vault path of
// its bytes, once gosidian saved it.
type DocAsset struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
	Mime string `json:"mime"`
	Link string `json:"link,omitempty"`
}

// DocumentAssets reads the assets of a document.json, by id.
func DocumentAssets(doc []byte) (map[uint64]DocAsset, error) {
	var d struct {
		Assets map[string]DocAsset `json:"assets"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		return nil, fmt.Errorf("document.json: %w", err)
	}
	out := make(map[uint64]DocAsset, len(d.Assets))
	for k, a := range d.Assets {
		id, err := strconv.ParseUint(k, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("document.json: asset %q has no number for an id", k)
		}
		out[id] = a
	}
	return out, nil
}

// WithLinks is doc with the link of each asset of links set, the rest as
// it was: numbers as written, and nothing escaped for HTML. The keys come
// out in order, two spaces an indent.
func WithLinks(doc []byte, links map[uint64]string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("document.json: %w", err)
	}
	if len(links) > 0 {
		assets, ok := v["assets"].(map[string]any)
		if !ok {
			return nil, errors.New("document.json has no assets to link")
		}
		for id, link := range links {
			a, ok := assets[strconv.FormatUint(id, 10)].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("document.json has no asset %d", id)
			}
			a["link"] = link
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
