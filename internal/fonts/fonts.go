// Package fonts keeps the fonts an installation adds for the page-layout
// editor (IMP-226): files in the state directory, out of the vault, so that
// neither the git sync nor an export carries them. A font is named by its
// content, and what is shown of it is what the file says of itself.
package fonts

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// What a font's licence bits allow an embedded copy of it to do (OS/2
// fsType), for the admin to see: gosidian never refuses a font for them.
const (
	EmbedInstallable = "installable"
	EmbedEditable    = "editable"
	EmbedPreview     = "preview"
	EmbedRestricted  = "restricted"
)

// ErrNotAFont is a file that is not a font the editor reads.
var ErrNotAFont = errors.New("not a TrueType or OpenType font (.ttf, .otf, .ttc)")

// Font is a file of the catalog.
type Font struct {
	ID        string
	Family    string
	Style     string
	Format    string // ttf, otf or ttc
	Faces     int
	Embedding string // "" when the file does not say
	Bytes     int64
	AddedAt   time.Time
}

// Inspect reads what a font file says of itself. A collection speaks for its
// first face.
func Inspect(data []byte) (Font, error) {
	f := Font{Faces: 1}
	at := 0
	switch {
	case len(data) < 12:
		return Font{}, ErrNotAFont
	case string(data[:4]) == "\x00\x01\x00\x00", string(data[:4]) == "true":
		f.Format = "ttf"
	case string(data[:4]) == "OTTO":
		f.Format = "otf"
	case string(data[:4]) == "ttcf":
		if len(data) < 16 {
			return Font{}, ErrNotAFont
		}
		f.Format = "ttc"
		f.Faces = int(binary.BigEndian.Uint32(data[8:]))
		at = int(binary.BigEndian.Uint32(data[12:]))
	default:
		return Font{}, ErrNotAFont
	}
	names, ok := table(data, at, "name")
	if !ok {
		return Font{}, ErrNotAFont
	}
	f.Family, f.Style = naming(names)
	if f.Family == "" || f.Faces < 1 {
		return Font{}, ErrNotAFont
	}
	if os2, ok := table(data, at, "OS/2"); ok && len(os2) >= 10 {
		f.Embedding = embedding(binary.BigEndian.Uint16(os2[8:]))
	}
	return f, nil
}

// table is the table of that tag in the font whose offset table is at `at`.
func table(data []byte, at int, tag string) ([]byte, bool) {
	if at < 0 || at > len(data)-12 {
		return nil, false
	}
	n := int(binary.BigEndian.Uint16(data[at+4:]))
	for i := range n {
		rec := at + 12 + 16*i
		if rec > len(data)-16 {
			return nil, false
		}
		if string(data[rec:rec+4]) != tag {
			continue
		}
		off := uint64(binary.BigEndian.Uint32(data[rec+8:]))
		size := uint64(binary.BigEndian.Uint32(data[rec+12:]))
		if off+size > uint64(len(data)) {
			return nil, false
		}
		return data[off : off+size], true
	}
	return nil, false
}

// naming is a font's family and style: the typographic names (16, 17), which
// a family of more than four styles has, or else the legacy ones (1, 2).
func naming(t []byte) (family, style string) {
	if len(t) < 6 {
		return "", ""
	}
	count := int(binary.BigEndian.Uint16(t[2:]))
	base := int(binary.BigEndian.Uint16(t[4:]))
	best := map[uint16]int{}
	found := map[uint16]string{}
	for i := range count {
		rec := 6 + 12*i
		if rec > len(t)-12 {
			break
		}
		platform := binary.BigEndian.Uint16(t[rec:])
		lang := binary.BigEndian.Uint16(t[rec+4:])
		id := binary.BigEndian.Uint16(t[rec+6:])
		size := int(binary.BigEndian.Uint16(t[rec+8:]))
		off := base + int(binary.BigEndian.Uint16(t[rec+10:]))
		score := rank(platform, lang)
		if score <= best[id] || off+size > len(t) {
			continue
		}
		if s := decode(platform, t[off:off+size]); s != "" {
			best[id], found[id] = score, s
		}
	}
	family, style = found[16], found[17]
	if family == "" {
		family, style = found[1], found[2]
	}
	if style == "" {
		style = found[2]
	}
	return family, style
}

// rank orders the records of a name: Windows in US English first.
func rank(platform, lang uint16) int {
	switch {
	case platform == 3 && lang == 0x409:
		return 4
	case platform == 3:
		return 3
	case platform == 0:
		return 2
	case platform == 1 && lang == 0:
		return 1
	}
	return 0
}

// maxName bounds a name read from a file: it goes to a page and to the log.
const maxName = 128

func decode(platform uint16, b []byte) string {
	var s string
	if platform == 1 {
		s = string(b)
	} else {
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = binary.BigEndian.Uint16(b[2*i:])
		}
		s = string(utf16.Decode(u))
	}
	s = strings.TrimSpace(strings.ToValidUTF8(s, ""))
	if r := []rune(s); len(r) > maxName {
		s = string(r[:maxName])
	}
	return s
}

// embedding is what fsType allows. A font with more than one bit set allows
// the least restrictive of them.
func embedding(fsType uint16) string {
	switch {
	case fsType&0x0E == 0:
		return EmbedInstallable
	case fsType&0x08 != 0:
		return EmbedEditable
	case fsType&0x04 != 0:
		return EmbedPreview
	}
	return EmbedRestricted
}

// Store is the catalog: a folder of files named <id>.<format>. A file never
// changes under its name, so what was read of it is kept.
type Store struct {
	dir string

	mu   sync.Mutex
	read map[string]Font
}

// New is the catalog in dir, which it makes at the first font.
func New(dir string) *Store {
	return &Store{dir: dir, read: map[string]Font{}}
}

var formats = []string{"ttf", "otf", "ttc"}

func validID(id string) bool {
	if len(id) != 16 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// locate is the file of a font, and its data when read.
func (s *Store) locate(id string) (string, error) {
	if !validID(id) {
		return "", os.ErrNotExist
	}
	for _, format := range formats {
		p := filepath.Join(s.dir, id+"."+format)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}

// describe is what a file says of itself, read once.
func (s *Store) describe(id, path string, data []byte) (Font, error) {
	s.mu.Lock()
	f, ok := s.read[id]
	s.mu.Unlock()
	if !ok {
		var err error
		if data == nil {
			if data, err = os.ReadFile(path); err != nil {
				return Font{}, err
			}
		}
		if f, err = Inspect(data); err != nil {
			return Font{}, err
		}
		f.ID, f.Bytes = id, int64(len(data))
		s.mu.Lock()
		s.read[id] = f
		s.mu.Unlock()
	}
	if st, err := os.Stat(path); err == nil {
		f.AddedAt = st.ModTime()
	}
	return f, nil
}

// List is the catalog by family and style. A file that does not read as a
// font is left out.
func (s *Store) List() ([]Font, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Font
	for _, e := range entries {
		id, _, ok := strings.Cut(e.Name(), ".")
		if !ok || e.IsDir() || !validID(id) {
			continue
		}
		f, err := s.describe(id, filepath.Join(s.dir, e.Name()), nil)
		if err != nil {
			continue
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Family != b.Family {
			return a.Family < b.Family
		}
		if a.Style != b.Style {
			return a.Style < b.Style
		}
		return a.ID < b.ID
	})
	return out, nil
}

// Add keeps a font; added says whether the file was not there yet.
func (s *Store) Add(data []byte) (f Font, added bool, err error) {
	if f, err = Inspect(data); err != nil {
		return Font{}, false, err
	}
	sum := sha256.Sum256(data)
	id := hex.EncodeToString(sum[:8])
	path := filepath.Join(s.dir, id+"."+f.Format)
	if _, err := os.Stat(path); err == nil {
		f, err := s.describe(id, path, data)
		return f, false, err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return Font{}, false, fmt.Errorf("fonts: %w", err)
	}
	tmp, err := os.CreateTemp(s.dir, "new-*")
	if err != nil {
		return Font{}, false, fmt.Errorf("fonts: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return Font{}, false, fmt.Errorf("fonts: %w", err)
	}
	f, err = s.describe(id, path, data)
	return f, true, err
}

// Read is a font's file and what it says of itself.
func (s *Store) Read(id string) ([]byte, Font, error) {
	path, err := s.locate(id)
	if err != nil {
		return nil, Font{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, Font{}, err
	}
	f, err := s.describe(id, path, data)
	return data, f, err
}

// Delete takes a font out of the catalog. A document set in it falls back,
// as for any font that is missing.
func (s *Store) Delete(id string) error {
	path, err := s.locate(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.read, id)
	s.mu.Unlock()
	return nil
}
