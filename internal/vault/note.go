package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/parser"
)

type Note struct {
	Path string // relative to vault root, forward-slashed, e.g. "folder/note.md"
	// Title is the file name without its extension: the index keys a link
	// by it (DisplayTitle gives the frontmatter's title).
	Title   string
	Content []byte
	ModTime time.Time
	Size    int64
}

// ETag returns an opaque version stamp for the note. Format is
// "<mtime_ns>-<size>". Clients receive this in memory_get responses and may
// pass it back as if_match on writes to get optimistic-locking safety
// (request rejected if the note changed since they last read it).
func (n *Note) ETag() string {
	return etagOf(n.ModTime, n.Size)
}

// DisplayTitle is the title a reader of the note is shown: its
// frontmatter's, else its file name, as the index stores it (IMP-219).
func (n *Note) DisplayTitle() string {
	if t := parser.TitleForPath(n.Path, n.Content); t != "" {
		return t
	}
	return n.Title
}

// NoteStat describes a note file without reading it.
type NoteStat struct {
	Path    string
	Size    int64
	ModTime time.Time
}

// ETag is the stamp Note.ETag returns for the same file.
func (n NoteStat) ETag() string { return etagOf(n.ModTime, n.Size) }

func etagOf(mod time.Time, size int64) string {
	return fmt.Sprintf("%d-%d", mod.UnixNano(), size)
}

func titleFromPath(rel string) string {
	base := filepath.Base(rel)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func loadNote(root, rel string) (*Note, error) {
	full := filepath.Join(root, rel)
	st, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	return &Note{
		Path:    filepath.ToSlash(rel),
		Title:   titleFromPath(rel),
		Content: data,
		ModTime: st.ModTime(),
		Size:    st.Size(),
	}, nil
}
