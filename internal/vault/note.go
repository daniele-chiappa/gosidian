package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Note struct {
	Path    string // relative to vault root, forward-slashed, e.g. "folder/note.md"
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
	return fmt.Sprintf("%d-%d", n.ModTime.UnixNano(), n.Size)
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
