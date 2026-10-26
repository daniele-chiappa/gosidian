package infolder

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/unicode/norm"
)

// zipEntry is a file a zip unpacked into an upload's folder.
type zipEntry struct {
	Rel      string // its path in the zip, cleaned
	Staged   string // where it is, relative to the upload's folder
	Size     int64
	SHA256   string
	Modified time.Time
}

// zipCaps cap a zip the server unpacks ([in] zip_*); 0 is no cap.
type zipCaps struct {
	MaxEntries int
	MaxBytes   int64
	MaxRatio   int
	// Extensions, when set, are the kinds of file taken out of it.
	Extensions []string
	// Room says whether the disk can take need bytes more, the reserve
	// kept (ErrNoSpace when not).
	Room func(need int64) error
}

// ratioFloor is the size under which a zip's ratio is not looked at: a
// small zip of text files can be 200 times smaller than its files, and
// does no harm.
const ratioFloor = 100 << 20

// maxRefused is how many of the files a zip left out an answer names.
const maxRefused = 20

// zipName reads the name of a zip's file as a path: UTF-8 (a name that is
// not, from an old Windows, is read as code page 850), in NFC, with / for
// \. ok is false for a name gosidian does not unpack: a folder, a hidden
// file or folder (macOS's __MACOSX/ too); err says why a name is refused.
func zipName(f *zip.File) (name string, ok bool, err error) {
	name = f.Name
	if f.NonUTF8 && !utf8.ValidString(name) {
		if dec, derr := charmap.CodePage850.NewDecoder().String(name); derr == nil {
			name = dec
		}
	}
	name = norm.NFC.String(strings.ReplaceAll(name, `\`, "/"))
	if strings.HasSuffix(name, "/") || f.FileInfo().IsDir() {
		return "", false, nil
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "__MACOSX" || (strings.HasPrefix(seg, ".") && seg != "." && seg != "..") {
			return "", false, nil
		}
	}
	if f.Mode()&os.ModeType != 0 {
		return "", false, nil // a link, a device: not a file
	}
	clean, err := cleanUploadPath(strings.TrimPrefix(name, "./"))
	if err != nil {
		return "", false, err
	}
	return clean, true, nil
}

// unpack unpacks the zip at zipAbs into the folder outAbs, which is outRel
// of the upload's folder, hashing each file as it is written. The caps are
// checked on what the zip says of itself before a byte is written, and on
// what it gives as it is read: a file longer than the zip said stops it.
// It returns the files, the names it left out on purpose (ignored: folders,
// hidden files, links) and those it refused, "name: why".
func unpack(zipAbs, outAbs, outRel string, caps zipCaps) (entries []zipEntry, ignored int, refused []string, err error) {
	// A name that would leave the folder is refused below, one by one.
	zr, err := zip.OpenReader(zipAbs)
	if errors.Is(err, zip.ErrInsecurePath) {
		err = nil
	}
	if err != nil {
		return nil, 0, nil, fmt.Errorf("not a zip that can be read: %w", err)
	}
	defer zr.Close()
	st, err := os.Stat(zipAbs)
	if err != nil {
		return nil, 0, nil, err
	}
	type todo struct {
		f    *zip.File
		name string
	}
	var list []todo
	var total uint64
	seen := map[string]bool{}
	allowed := func(name string) bool {
		if len(caps.Extensions) == 0 {
			return true
		}
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
		for _, e := range caps.Extensions {
			if e == ext {
				return true
			}
		}
		return false
	}
	for _, f := range zr.File {
		name, ok, nerr := zipName(f)
		switch {
		case nerr != nil:
			refused = appendRefused(refused, f.Name, nerr.Error())
			continue
		case !ok:
			ignored++
			continue
		case !allowed(name):
			refused = appendRefused(refused, name, "a kind of file this server does not take")
			continue
		case seen[name]:
			refused = appendRefused(refused, name, "twice in the zip")
			continue
		}
		seen[name] = true
		list = append(list, todo{f, name})
		total += f.UncompressedSize64
	}
	if caps.MaxEntries > 0 && len(list) > caps.MaxEntries {
		return nil, ignored, refused, fmt.Errorf("%w: the zip holds %d files, at most %d", ErrTooLarge, len(list), caps.MaxEntries)
	}
	if total > uint64(1<<62) {
		return nil, ignored, refused, fmt.Errorf("%w: the zip says its files are more than 4 EiB", ErrTooLarge)
	}
	if caps.MaxBytes > 0 && int64(total) > caps.MaxBytes {
		return nil, ignored, refused, fmt.Errorf("%w: the zip's files are %d bytes, at most %d", ErrTooLarge, total, caps.MaxBytes)
	}
	if caps.MaxRatio > 0 && total > ratioFloor && total > uint64(caps.MaxRatio)*uint64(max(st.Size(), 1)) {
		return nil, ignored, refused, fmt.Errorf("%w: the zip's files are %d times its size, at most %d", ErrTooLarge, total/uint64(max(st.Size(), 1)), caps.MaxRatio)
	}
	if caps.Room != nil {
		if err := caps.Room(int64(total)); err != nil {
			return nil, ignored, refused, err
		}
	}
	if err := os.RemoveAll(outAbs); err != nil {
		return nil, ignored, refused, err
	}
	written := int64(0)
	nextCheck := int64(roomEvery)
	for _, t := range list {
		e, err := unpackFile(t.f, t.name, outAbs, outRel)
		if err != nil {
			if errors.Is(err, errZipLies) {
				return nil, ignored, refused, err
			}
			refused = appendRefused(refused, t.name, err.Error())
			continue
		}
		entries = append(entries, e)
		written += e.Size
		if caps.Room != nil && written >= nextCheck {
			nextCheck = written + roomEvery
			if err := caps.Room(int64(total) - written); err != nil {
				return nil, ignored, refused, err
			}
		}
	}
	return entries, ignored, refused, nil
}

// roomEvery is how often, in bytes written, unpack checks the reserve.
const roomEvery = 64 << 20

// errZipLies stops a zip whose file is longer than the zip says: a zip
// made to fill the disk.
var errZipLies = errors.New("a file of the zip is longer than the zip says: the zip is refused")

func appendRefused(list []string, name, why string) []string {
	if len(list) >= maxRefused {
		return list
	}
	return append(list, name+": "+why)
}

// unpackFile writes a zip's file into outAbs at its name, hashed.
func unpackFile(f *zip.File, name, outAbs, outRel string) (zipEntry, error) {
	rc, err := f.Open()
	if err != nil {
		return zipEntry{}, err
	}
	defer rc.Close()
	abs := filepath.Join(outAbs, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return zipEntry{}, err
	}
	out, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return zipEntry{}, err
	}
	h := sha256.New()
	limit := int64(f.UncompressedSize64)
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(rc, limit+1))
	if err == nil && n > limit {
		err = errZipLies
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(abs)
		return zipEntry{}, err
	}
	mod := f.Modified
	if !mod.IsZero() {
		_ = os.Chtimes(abs, mod, mod)
	}
	return zipEntry{Rel: name, Staged: outRel + "/" + name, Size: n, SHA256: hex.EncodeToString(h.Sum(nil)), Modified: mod}, nil
}

// zipFolder is the folder of the IN folder a zip's files go to, under
// base: the zip's own folder when all its files are in one (a zip made of
// a folder), else a folder named after the zip. It returns the folder and
// the part of the files' paths it takes.
func zipFolder(base, zipName string, entries []zipEntry) (folder, strip string) {
	common := ""
	for i, e := range entries {
		first, _, nested := strings.Cut(e.Rel, "/")
		if !nested || (i > 0 && first != common) {
			common = ""
			break
		}
		common = first
	}
	if common != "" {
		return path.Join(base, common), common + "/"
	}
	stem := strings.TrimSuffix(zipName, path.Ext(zipName))
	if stem == "" {
		stem = zipName
	}
	return path.Join(base, stem), ""
}
