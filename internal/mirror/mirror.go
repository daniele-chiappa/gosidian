// Package mirror keeps a local, read-only copy of one project of a gosidian
// vault (IMP-102). Sync lists the project with GET <base>/manifest, fetches
// the notes whose ETag changed with GET <base>/download, removes the ones
// that disappeared and writes every file read-only with the server's
// modification time. It also writes MIRROR.md at the mirror root and
// <project>/_index.md, the project's notes from the most recent, because a
// copy of files cannot otherwise tell which of two notes is newer.
//
// The mirror keeps vault paths (<dir>/<project>/…), so [[project/…]]
// wikilinks resolve from its root. Nothing is ever written back: agents
// write with the MCP tools and the next sync brings the change here.
package mirror

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/parser"
)

// ErrLocked means another sync of the same project is running.
var ErrLocked = errors.New("another mirror sync of this project is running")

const (
	stateVersion = 1
	// staleLock is when a lock left by a crashed sync stops blocking.
	staleLock = 10 * time.Minute
	// defaultWorkers bounds the parallel downloads of one sync.
	defaultWorkers = 4
	// maxIndexTags caps the tags shown per note in _index.md.
	maxIndexTags = 4
)

// Options configures one sync.
type Options struct {
	BaseURL string // MCP base URL; a trailing /sse is dropped
	Token   string // bearer with read scope
	Project string
	Dir     string // mirror root; the project lives in Dir/<project>
	Client  *http.Client
	Workers int
}

// Result counts what a sync changed.
type Result struct {
	Notes, Added, Updated, Deleted int
	Duration                       time.Duration
}

// State is what the mirror remembers about a project between syncs, in
// Dir/.<project>.state.json.
type State struct {
	Version  int               `json:"version"`
	Project  string            `json:"project"`
	Source   string            `json:"source"`
	SyncedAt string            `json:"synced_at"`
	ETags    map[string]string `json:"etags"` // vault path → ETag
}

type manifestNote struct {
	Path  string `json:"path"`
	ETag  string `json:"etag"`
	Size  int64  `json:"size"`
	MTime string `json:"mtime"`
	Title string `json:"title"`
}

// Sync brings Dir/<project> in line with the server. On a failed download
// it keeps what succeeded, records it, and returns the error: the next sync
// resumes from there.
func Sync(ctx context.Context, o Options) (Result, error) {
	start := time.Now()
	if err := validProject(o.Project); err != nil {
		return Result{}, err
	}
	base := strings.TrimRight(strings.TrimSuffix(strings.TrimRight(o.BaseURL, "/"), "/sse"), "/")
	if base == "" || o.Token == "" {
		return Result{}, errors.New("the MCP base URL and a token are required")
	}
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	workers := o.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	if err := os.MkdirAll(filepath.Join(o.Dir, o.Project), 0o755); err != nil {
		return Result{}, err
	}
	unlock, err := lock(o.Dir, o.Project)
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	notes, err := fetchManifest(ctx, client, base, o.Token, o.Project)
	if err != nil {
		return Result{}, err
	}
	old, _ := ReadState(o.Dir, o.Project)
	next := State{Version: stateVersion, Project: o.Project, Source: base, ETags: map[string]string{}}

	var res Result
	res.Notes = len(notes)
	var todo []manifestNote
	listed := map[string]bool{}
	for _, n := range notes {
		local, err := localPath(o.Dir, o.Project, n.Path)
		if err != nil {
			return Result{}, err
		}
		listed[n.Path] = true
		if old.ETags[n.Path] == n.ETag {
			if _, err := os.Stat(local); err == nil {
				next.ETags[n.Path] = n.ETag
				continue
			}
		}
		todo = append(todo, n)
	}

	// Parallel downloads; the first error stops handing out work.
	var mu sync.Mutex
	var firstErr error
	jobs := make(chan manifestNote)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				err := fetchNote(ctx, client, base, o.Token, o.Dir, o.Project, n)
				mu.Lock()
				switch {
				case err != nil && firstErr == nil:
					firstErr = err
				case err == nil:
					next.ETags[n.Path] = n.ETag
					if _, known := old.ETags[n.Path]; known {
						res.Updated++
					} else {
						res.Added++
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, n := range todo {
		mu.Lock()
		stop := firstErr != nil
		mu.Unlock()
		if stop {
			break
		}
		jobs <- n
	}
	close(jobs)
	wg.Wait()

	if firstErr == nil {
		for p := range old.ETags {
			if listed[p] {
				continue
			}
			if local, err := localPath(o.Dir, o.Project, p); err == nil {
				_ = os.Chmod(local, 0o644)
				if err := os.Remove(local); err == nil || errors.Is(err, os.ErrNotExist) {
					res.Deleted++
					removeEmptyParents(filepath.Dir(local), filepath.Join(o.Dir, o.Project))
				}
			}
		}
	} else {
		// Keep tracking what could not be refreshed, so a later sync still
		// knows about (and can delete) those files.
		for p, e := range old.ETags {
			if _, ok := next.ETags[p]; !ok {
				next.ETags[p] = e
			}
		}
	}

	next.SyncedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeState(o.Dir, next); err != nil && firstErr == nil {
		firstErr = err
	}
	if firstErr == nil {
		if err := writeIndex(o.Dir, o.Project, notes); err != nil {
			firstErr = err
		}
	}
	if err := writeMirrorReadme(o.Dir); err != nil && firstErr == nil {
		firstErr = err
	}
	res.Duration = time.Since(start)
	return res, firstErr
}

// Purge deletes the mirror of one project, or the whole mirror when project
// is empty. It refuses a directory that does not look like a mirror.
func Purge(dir, project string) error {
	if project != "" {
		if err := validProject(project); err != nil {
			return err
		}
		if _, err := ReadState(dir, project); err != nil {
			return fmt.Errorf("%s is not a mirror of %s: %w", dir, project, err)
		}
		if err := removeAll(filepath.Join(dir, project)); err != nil {
			return err
		}
		_ = os.Remove(statePath(dir, project))
		_ = os.Remove(lockPath(dir, project))
		return writeMirrorReadme(dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "MIRROR.md")); err != nil {
		return fmt.Errorf("%s is not a gosidian mirror (no MIRROR.md)", dir)
	}
	return removeAll(dir)
}

// ReadState returns the saved state of a project's mirror.
func ReadState(dir, project string) (State, error) {
	var st State
	buf, err := os.ReadFile(statePath(dir, project))
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(buf, &st); err != nil {
		return st, err
	}
	if st.Project != project {
		return State{}, fmt.Errorf("state file is for project %q", st.Project)
	}
	return st, nil
}

// States lists the projects mirrored in dir.
func States(dir string) []State {
	entries, _ := os.ReadDir(dir)
	var out []State
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".state.json") {
			continue
		}
		project := strings.TrimSuffix(strings.TrimPrefix(name, "."), ".state.json")
		if st, err := ReadState(dir, project); err == nil {
			out = append(out, st)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Project < out[b].Project })
	return out
}

func validProject(p string) error {
	if p == "" || strings.ContainsAny(p, `/\`) || strings.HasPrefix(p, ".") {
		return fmt.Errorf("invalid project %q: a top-level folder name", p)
	}
	return nil
}

// localPath maps a vault path of the project to its file in the mirror,
// refusing anything that would land outside Dir/<project>.
func localPath(dir, project, vaultPath string) (string, error) {
	// After Clean a ".." can only lead the path, which the prefix test
	// rejects: the file always lands inside Dir/<project>.
	clean := filepath.Clean(filepath.FromSlash(vaultPath))
	if !strings.HasPrefix(clean, project+string(filepath.Separator)) {
		return "", fmt.Errorf("server listed %q outside project %s", vaultPath, project)
	}
	return filepath.Join(dir, clean), nil
}

func statePath(dir, project string) string { return filepath.Join(dir, "."+project+".state.json") }
func lockPath(dir, project string) string  { return filepath.Join(dir, "."+project+".lock") }

func lock(dir, project string) (func(), error) {
	p := lockPath(dir, project)
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return func() { os.Remove(p) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if st, serr := os.Stat(p); serr == nil && time.Since(st.ModTime()) > staleLock {
			os.Remove(p)
			continue
		}
		return nil, ErrLocked
	}
	return nil, ErrLocked
}

func fetchManifest(ctx context.Context, client *http.Client, base, token, project string) ([]manifestNote, error) {
	var m struct {
		Notes []manifestNote `json:"notes"`
	}
	body, err := get(ctx, client, base+"/manifest?project="+url.QueryEscape(project), token)
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	return m.Notes, nil
}

func fetchNote(ctx context.Context, client *http.Client, base, token, dir, project string, n manifestNote) error {
	local, err := localPath(dir, project, n.Path)
	if err != nil {
		return err
	}
	body, err := get(ctx, client, base+"/download?path="+url.QueryEscape(n.Path), token)
	if err != nil {
		return fmt.Errorf("download %s: %w", n.Path, err)
	}
	mtime, _ := time.Parse(time.RFC3339, n.MTime)
	return writeReadOnly(local, body, mtime)
}

// get performs an authenticated GET and turns a non-200 answer into an error
// carrying the server's message.
func get(ctx context.Context, client *http.Client, u, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, e.Error)
		}
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// writeReadOnly replaces path atomically with a read-only file carrying
// mtime (zero = now).
func writeReadOnly(path string, body []byte, mtime time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mirror-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o444); err != nil {
		os.Remove(name)
		return err
	}
	if !mtime.IsZero() {
		_ = os.Chtimes(name, mtime, mtime)
	}
	_ = os.Chmod(path, 0o644) // lets the rename replace a read-only file everywhere
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

func writeState(dir string, st State) error {
	buf, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := statePath(dir, st.Project) + ".tmp"
	if err := os.WriteFile(tmp, append(buf, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(dir, st.Project))
}

// writeIndex writes <project>/_index.md: the notes from the most recently
// modified, with title, path and a few tags read from the mirrored files.
func writeIndex(dir, project string, notes []manifestNote) error {
	sorted := append([]manifestNote(nil), notes...)
	sort.SliceStable(sorted, func(a, b int) bool {
		if sorted[a].MTime != sorted[b].MTime {
			return sorted[a].MTime > sorted[b].MTime
		}
		return sorted[a].Path < sorted[b].Path
	})
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — notes from the most recent\n\n", project)
	b.WriteString("Generated by `gosidian mirror sync`; do not edit. Paths are relative to the mirror root. ")
	b.WriteString("When two notes disagree, the more recent one is usually the current state.\n\n")
	for _, n := range sorted {
		title, tags := n.Title, ""
		if local, err := localPath(dir, project, n.Path); err == nil {
			if body, err := os.ReadFile(local); err == nil {
				fm := parser.ParseFrontmatterFields(parser.FrontmatterRawForPath(n.Path, body))
				if t, ok := fm["title"].(string); ok && title == "" {
					title = t
				}
				if list, ok := fm["tags"].([]string); ok && len(list) > 0 {
					if len(list) > maxIndexTags {
						list = list[:maxIndexTags]
					}
					tags = " · " + strings.Join(list, ", ")
				}
			}
		}
		if title == "" {
			title = strings.TrimSuffix(filepath.Base(n.Path), filepath.Ext(n.Path))
		}
		date := n.MTime
		if len(date) >= 10 {
			date = date[:10]
		}
		fmt.Fprintf(&b, "- %s — %s — `%s`%s\n", date, title, n.Path, tags)
	}
	return writeReadOnly(filepath.Join(dir, project, "_index.md"), []byte(b.String()), time.Time{})
}

// writeMirrorReadme writes MIRROR.md at the mirror root: what the folder is,
// how to write, and the projects it holds. Without projects it removes it.
func writeMirrorReadme(dir string) error {
	states := States(dir)
	path := filepath.Join(dir, "MIRROR.md")
	if len(states) == 0 {
		_ = os.Chmod(path, 0o644)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	var b strings.Builder
	b.WriteString("# gosidian mirror — read-only\n\n")
	b.WriteString("This folder is a read-only copy of gosidian projects, kept in sync by `gosidian mirror sync`. ")
	b.WriteString("Do not edit these files: changes are lost at the next sync. Write through the gosidian MCP tools ")
	b.WriteString("(memory_append, memory_edit, memory_create, …); the change comes back here at the next sync.\n\n")
	b.WriteString("| Project | Source | Last sync | Notes |\n|---|---|---|---|\n")
	for _, st := range states {
		fmt.Fprintf(&b, "| %s | %s | %s | %d |\n", st.Project, st.Source, st.SyncedAt, len(st.ETags))
	}
	b.WriteString("\nStart from `<project>/hot.md` (current focus) and `<project>/README.md` (map); `<project>/_index.md` ")
	b.WriteString("lists every note from the most recent. Follow tags and [[wikilinks]] (paths from this folder). ")
	b.WriteString("When a search finds nothing, try synonyms and the other language before concluding there is no answer.\n")
	return writeReadOnly(path, []byte(b.String()), time.Time{})
}

func removeEmptyParents(dir, stop string) {
	for dir != stop && strings.HasPrefix(dir, stop) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// removeAll makes every file writable first: mirrored files are read-only.
func removeAll(dir string) error {
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			_ = os.Chmod(p, 0o644)
		}
		return nil
	})
	return os.RemoveAll(dir)
}
