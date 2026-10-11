package pipeline

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// Files by reference (IMP-172): a plugin node whose manifest says
// file_refs gets, for each file it names, a URL to fetch it from instead
// of its content in base64. The URL holds a ticket bound to the run and to
// the file, valid while the step may run, for as many requests (ranges,
// retries) as the plugin makes.

// FileStat describes a file of the project a node names: an attachment, or
// a file of the IN folder.
type FileStat struct {
	Path    string
	Name    string
	Size    int64
	ModTime time.Time
	SHA256  string
}

// FileTicketPath is where a ticket's file is served, before its id.
const FileTicketPath = "/api/v1/run-files/"

type fileTicket struct {
	run, project, path string
	expires            time.Time
}

type fileTickets struct {
	mu sync.Mutex
	m  map[string]fileTicket
}

func (t *fileTickets) add(id string, ft fileTicket) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[string]fileTicket{}
	}
	now := time.Now()
	for k, v := range t.m {
		if now.After(v.expires) {
			delete(t.m, k)
		}
	}
	t.m[id] = ft
}

func (t *fileTickets) get(id string) (fileTicket, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ft, ok := t.m[id]
	if ok && time.Now().After(ft.expires) {
		delete(t.m, id)
		return fileTicket{}, false
	}
	return ft, ok
}

func (t *fileTickets) dropRun(run string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, v := range t.m {
		if v.run == run {
			delete(t.m, k)
		}
	}
}

func newTicketID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// mintFileTicket makes a ticket for a file of a run's project.
func (e *Engine) mintFileTicket(run jobs.Run, rel string) string {
	id := newTicketID()
	e.tickets.add(id, fileTicket{run: run.ID, project: run.Project, path: rel, expires: time.Now().Add(e.cfg.FileTicketTTL)})
	return id
}

// ErrNoTicket is returned for a file ticket unknown, expired, or of a run
// that ended.
var ErrNoTicket = errors.New("no such file ticket")

// OpenTicketFile opens the file a ticket names, for the plugin that holds
// it: the reader, what the file is, and its MIME type.
func (e *Engine) OpenTicketFile(id string) (io.ReadSeekCloser, FileStat, string, error) {
	ft, ok := e.tickets.get(id)
	if !ok {
		return nil, FileStat{}, "", ErrNoTicket
	}
	f, st, err := e.host.OpenFile(ft.project, ft.path)
	if err != nil {
		return nil, FileStat{}, "", err
	}
	return f, st, MIMEOf(st.Name), nil
}

// MIMEOf is the MIME type of a file by its name; application/octet-stream
// when the extension says nothing.
func MIMEOf(name string) string {
	if t := mime.TypeByExtension(strings.ToLower(path.Ext(name))); t != "" {
		return t
	}
	return "application/octet-stream"
}

// FileRef is a file a node names, inside the run's project (an attachment,
// or a file of the IN folder), as a plugin takes it by reference: name,
// MIME type, size, SHA-256 and the URL under base it is fetched from while
// the step may run.
func (env *Env) FileRef(p, base string) (map[string]any, error) {
	rel, err := env.projectPath(p)
	if err != nil {
		return nil, err
	}
	st, err := env.Engine.host.StatFile(env.Project, rel)
	if err != nil {
		return nil, err
	}
	id := env.Engine.mintFileTicket(env.Run, st.Path)
	return map[string]any{
		"name": st.Name, "mime": MIMEOf(st.Name), "size": st.Size, "sha256": st.SHA256,
		"url": strings.TrimRight(base, "/") + FileTicketPath + id,
	}, nil
}
