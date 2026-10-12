package infolder

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/vault"
)

// The sources of the IN folders (IMP-172 phase 2): a plugin offers remote
// archives, the owner binds one to a project as a folder at the root of
// its IN folder, and its files are files of the IN folder like the local
// ones: in in.db, in batches, taken by the in pipelines, handed to the
// plugins by reference. The bytes stay in the source: gosidian reads them
// through a URL the plugin gives, when a file is opened.

// Binding binds a plugin's source to a project's IN folder, as the folder
// IN/<Folder>/.
type Binding struct {
	Project string `json:"project"`
	Folder  string `json:"folder"`
	Plugin  string `json:"plugin"`
	Source  string `json:"source"`
	// Write lets gosidian move files inside the source.
	Write bool `json:"write,omitempty"`
	// Every is the seconds between two checks of the source; 0 is
	// DefaultSourceEvery.
	Every int `json:"every,omitempty"`
}

// ID is the binding's source, as the files' source column holds it.
func (b Binding) ID() string { return b.Plugin + "/" + b.Source }

// Interval is the time between two checks of the source.
func (b Binding) Interval() time.Duration {
	d := time.Duration(b.Every) * time.Second
	switch {
	case d <= 0:
		return DefaultSourceEvery
	case d < MinSourceEvery:
		return MinSourceEvery
	}
	return d
}

// Checks of a source.
const (
	DefaultSourceEvery = time.Minute
	MinSourceEvery     = 20 * time.Second
	// fullListEvery is how often a source with changes is listed in
	// full all the same: a change it missed is caught there.
	fullListEvery = time.Hour
	// sourceTimeout caps a check.
	sourceTimeout = 10 * time.Minute
	// MaxSourceFiles caps the files of one source.
	MaxSourceFiles = 100000
	// listPage is the page asked of a source's list.
	listPage = 1000
)

// ValidFolder reports whether name may name a source's folder: one plain,
// visible name.
func ValidFolder(name string) bool {
	return name != "" && len(name) <= 64 && !strings.ContainsAny(name, `/\`) && !strings.HasPrefix(name, ".") &&
		name != vault.INFolder && strings.TrimSpace(name) == name
}

// Sources are the bound sources and the sessions to read them: the
// plugins' registry.
type Sources interface {
	// Bindings are the bindings of every project.
	Bindings() []Binding
	// Session opens a session with a binding's source, once its plugin's
	// accepted manifest offers it.
	Session(ctx context.Context, b Binding) (SourceSession, error)
}

// SourceSession talks with a source. Paths are relative to its root.
type SourceSession interface {
	Info() SourceInfo
	List(ctx context.Context, cursor string, limit int) (RemotePage, error)
	// Changes are the changes since cursor; with "" it gives the cursor
	// of now. Only when Info().Changes.
	Changes(ctx context.Context, cursor string) (RemoteChanges, error)
	// Open gives a URL to read a file from, checked against the hosts
	// the plugin may point at.
	Open(ctx context.Context, p string) (RemoteURL, error)
	// Move moves a file or a folder. Only when Info().Move.
	Move(ctx context.Context, from, to string) (RemoteFile, error)
	Close() error
}

// SourceInfo is what a source says of itself in its plugin's manifest.
type SourceInfo struct {
	Kind        string `json:"kind,omitempty"`
	Description string `json:"description,omitempty"`
	Changes     bool   `json:"changes"`
	Move        bool   `json:"move"`
}

// RemoteFile is a file of a source.
type RemoteFile struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	ETag     string    `json:"etag"`
	SHA256   string    `json:"sha256,omitempty"`
	// Deleted marks a change that removed the file.
	Deleted bool `json:"deleted,omitempty"`
}

// RemotePage is a page of a source's list.
type RemotePage struct {
	Files []RemoteFile `json:"files"`
	Next  string       `json:"next"`
}

// RemoteChanges are a source's changes since a cursor.
type RemoteChanges struct {
	Changes []RemoteFile `json:"changes"`
	Cursor  string       `json:"cursor"`
	// More: more changes follow, from Cursor.
	More bool `json:"more,omitempty"`
	// Reset: the cursor is no longer good, list the source in full.
	Reset bool `json:"reset,omitempty"`
}

// RemoteURL is where a file of a source is read from.
type RemoteURL struct {
	URL  string `json:"url"`
	Size int64  `json:"size"`
	ETag string `json:"etag,omitempty"`
}

// ErrRemote is returned for what a file of a source does not allow.
var ErrRemote = errors.New("a file of a source")

// SetSources sets the bound sources: the plugins' registry, when the
// pipelines are on. Without, the source folders keep what they held and
// are not checked.
func (s *Service) SetSources(src Sources) {
	s.mu.Lock()
	s.src = src
	s.mu.Unlock()
}

func (s *Service) sourcesOf() Sources {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.src
}

// binding returns the binding of a project's folder.
func (s *Service) binding(project, folder string) (Binding, bool) {
	src := s.sourcesOf()
	if src == nil {
		return Binding{}, false
	}
	for _, b := range src.Bindings() {
		if b.Project == project && b.Folder == folder {
			return b, true
		}
	}
	return Binding{}, false
}

// remoteFolders are the folders of a project's IN folder that a source
// holds: those in.db keeps a source row for. A local scan leaves them out.
func (s *Service) remoteFolders(project string) (map[string]SourceRow, error) {
	rows, err := s.store.sourceRows(project)
	if err != nil {
		return nil, err
	}
	out := make(map[string]SourceRow, len(rows))
	for _, r := range rows {
		out[r.Folder] = r
	}
	return out, nil
}

// remoteOf says whether the vault path p is in a source's folder of its
// project, and which, with the path relative to the source's root.
func (s *Service) remoteOf(project, p string) (folder, rel string, ok bool) {
	root := vault.INRoot(project) + "/"
	if !strings.HasPrefix(p, root) {
		return "", "", false
	}
	first, rest, _ := strings.Cut(strings.TrimPrefix(p, root), "/")
	folders, err := s.remoteFolders(project)
	if err != nil {
		return "", "", false
	}
	if _, ok := folders[first]; !ok {
		return "", "", false
	}
	return first, rest, true
}

// SourceState is what the IN window shows of a source folder.
type SourceState struct {
	Folder string `json:"folder"`
	Plugin string `json:"plugin"`
	Source string `json:"source"`
	SourceInfo
	Write bool `json:"write"`
	// Status: ok, checking (not checked yet), error, or off (not bound,
	// or the pipelines are off in the server).
	Status  string     `json:"status"`
	Error   string     `json:"error,omitempty"`
	Checked *time.Time `json:"checked,omitempty"`
	Every   int        `json:"every"`
}

// SourceStates lists the source folders of a project, bound or known to
// in.db, by folder.
func (s *Service) SourceStates(project string) ([]SourceState, error) {
	rows, err := s.remoteFolders(project)
	if err != nil {
		return nil, err
	}
	byFolder := map[string]*SourceState{}
	if src := s.sourcesOf(); src != nil {
		for _, b := range src.Bindings() {
			if b.Project != project {
				continue
			}
			st := &SourceState{Folder: b.Folder, Plugin: b.Plugin, Source: b.Source, Write: b.Write,
				Status: "checking", Every: int(b.Interval() / time.Second)}
			s.mu.Lock()
			info, known := s.infos[b.Project+"/"+b.Folder]
			s.mu.Unlock()
			if known {
				st.SourceInfo = info
			}
			byFolder[b.Folder] = st
		}
	}
	for folder, r := range rows {
		st, bound := byFolder[folder]
		if !bound {
			plugin, source, _ := strings.Cut(r.Source, "/")
			st = &SourceState{Folder: folder, Plugin: plugin, Source: source, Status: "off",
				Error: "the source is not bound now, or the pipelines are off in the server"}
			byFolder[folder] = st
		} else if r.Error != "" {
			st.Status, st.Error = "error", r.Error
		} else if !r.Checked.IsZero() {
			st.Status = "ok"
		}
		if !r.Checked.IsZero() {
			c := r.Checked
			st.Checked = &c
		}
	}
	// A binding whose first check failed has its error in the row of the
	// check, kept apart since it holds no files yet.
	s.mu.Lock()
	for folder, st := range byFolder {
		if e, ok := s.firstErr[project+"/"+folder]; ok && st.Status == "checking" {
			st.Status, st.Error = "error", e
		}
	}
	s.mu.Unlock()
	out := make([]SourceState, 0, len(byFolder))
	for _, st := range byFolder {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Folder < out[j].Folder })
	return out, nil
}

// ScanSourceSoon asks for a check of a project's source folders now (one
// folder, or all of them for "").
func (s *Service) ScanSourceSoon(project, folder string) {
	select {
	case s.wakeSrc <- project + "/" + folder:
	default:
	}
}

// runSources checks the bound sources whose time has come, every Every,
// and those ScanSourceSoon asks for, until ctx ends.
func (s *Service) runSources(ctx context.Context) {
	t := time.NewTicker(s.opts.Every)
	defer t.Stop()
	s.checkSources(ctx, "")
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.checkSources(ctx, "")
		case key := <-s.wakeSrc:
			s.checkSources(ctx, key)
		}
	}
}

// checkSources checks the sources due, or, for key "<project>/<folder>"
// ("<project>/" for all its folders), those at once; then forgets the
// folders no longer bound.
func (s *Service) checkSources(ctx context.Context, key string) {
	src := s.sourcesOf()
	if src == nil {
		return
	}
	withIN := map[string]bool{}
	for _, p := range s.projects() {
		withIN[p] = true
	}
	rows, err := s.store.sourceRows("")
	if err != nil {
		s.opts.Log.Warn("IN: sources", "err", err)
		return
	}
	bound := map[string]bool{}
	now := s.opts.Now()
	for _, b := range src.Bindings() {
		k := b.Project + "/" + b.Folder
		bound[k] = true
		if !withIN[b.Project] || !ValidFolder(b.Folder) {
			continue
		}
		asked := key != "" && (key == k || key == b.Project+"/")
		r, known := rows[k]
		if !asked && known && now.Sub(r.Checked) < b.Interval() {
			continue
		}
		if !asked && !known {
			s.mu.Lock()
			last := s.firstTry[k]
			s.mu.Unlock()
			if !last.IsZero() && now.Sub(last) < b.Interval() {
				continue
			}
		}
		if ctx.Err() != nil {
			return
		}
		s.checkSource(ctx, src, b)
	}
	for k, r := range rows {
		if bound[k] {
			continue
		}
		if err := s.store.dropSource(r.Project, r.Folder); err != nil {
			s.opts.Log.Warn("IN: forgetting a source no longer bound", "project", r.Project, "folder", r.Folder, "err", err)
			continue
		}
		s.opts.Log.Info("IN: a source no longer bound, forgotten", "project", r.Project, "folder", r.Folder, "source", r.Source)
		s.changed(r.Project)
	}
}

// checkSource reads a source's changes, or lists it in full, into in.db.
func (s *Service) checkSource(ctx context.Context, src Sources, b Binding) {
	k := b.Project + "/" + b.Folder
	now := s.opts.Now()
	s.mu.Lock()
	s.firstTry[k] = now
	s.mu.Unlock()
	rows, err := s.store.sourceRows(b.Project)
	if err != nil {
		s.opts.Log.Warn("IN: sources", "err", err)
		return
	}
	row, known := rows[k]
	fail := func(err error) {
		msg := err.Error()
		if known {
			row.Checked, row.Error = now, msg
			if werr := s.store.setSourceRow(row); werr != nil {
				s.opts.Log.Warn("IN: a source's state", "err", werr)
			}
		} else {
			s.mu.Lock()
			s.firstErr[k] = msg
			s.mu.Unlock()
		}
		s.opts.Log.Warn("IN: a source", "project", b.Project, "folder", b.Folder, "source", b.ID(), "err", err)
		s.changed(b.Project)
	}
	// A local folder of that name with files stays local: the source is
	// not shown over it.
	if !known {
		if local, err := s.v.INReadDir(vault.INRoot(b.Project) + "/" + b.Folder); err == nil && len(local) > 0 {
			fail(fmt.Errorf("the IN folder has a local folder %s with files: bind the source under another name (--as), or move them", b.Folder))
			return
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			fail(err)
			return
		}
	}
	cctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()
	sess, err := src.Session(cctx, b)
	if err != nil {
		fail(err)
		return
	}
	defer sess.Close()
	info := sess.Info()
	s.mu.Lock()
	s.infos[k] = info
	s.mu.Unlock()
	if known && row.Source != b.ID() {
		// The folder is bound to another source now: what the other held goes.
		if err := s.store.dropSource(b.Project, b.Folder); err != nil {
			fail(err)
			return
		}
		known = false
	}
	if !known {
		row = SourceRow{Project: b.Project, Folder: b.Folder, Source: b.ID()}
	}
	var changed bool
	if info.Changes && row.Cursor != "" && now.Sub(row.Listed) < fullListEvery {
		changed, err = s.readChanges(cctx, sess, b, &row)
		if errors.Is(err, errReset) {
			row.Cursor = ""
			changed, err = s.listSource(cctx, sess, b, &row, info.Changes)
		}
	} else {
		changed, err = s.listSource(cctx, sess, b, &row, info.Changes)
	}
	if err != nil {
		fail(err)
		return
	}
	row.Checked, row.Error = now, ""
	if err := s.store.setSourceRow(row); err != nil {
		s.opts.Log.Warn("IN: a source's state", "err", err)
		return
	}
	s.mu.Lock()
	delete(s.firstErr, k)
	s.mu.Unlock()
	closed, err := s.closeBatches(b.Project)
	if err != nil {
		s.opts.Log.Warn("IN: closing the batches of a source", "project", b.Project, "err", err)
	}
	if changed || closed || !known {
		s.changed(b.Project)
	}
}

var errReset = errors.New("the source's changes start over")

// listSource lists a source in full and brings in.db in line. With
// changes, the cursor is taken before the list: what changes during it
// comes again with the next changes, and changes nothing twice.
func (s *Service) listSource(ctx context.Context, sess SourceSession, b Binding, row *SourceRow, changes bool) (bool, error) {
	cursor := ""
	if changes {
		ch, err := sess.Changes(ctx, "")
		if err != nil {
			return false, err
		}
		cursor = ch.Cursor
	}
	var all []RemoteFile
	next := ""
	for {
		page, err := sess.List(ctx, next, listPage)
		if err != nil {
			return false, err
		}
		all = append(all, page.Files...)
		if len(all) > MaxSourceFiles {
			return false, fmt.Errorf("the source has more than %d files: bind a folder of it", MaxSourceFiles)
		}
		if page.Next == "" || page.Next == next {
			break
		}
		next = page.Next
	}
	changed, err := s.applyRemote(b, all, true)
	if err != nil {
		return false, err
	}
	row.Cursor, row.Listed = cursor, s.opts.Now()
	return changed, nil
}

// readChanges reads a source's changes from the row's cursor.
func (s *Service) readChanges(ctx context.Context, sess SourceSession, b Binding, row *SourceRow) (bool, error) {
	changed := false
	for i := 0; i < 1000; i++ {
		ch, err := sess.Changes(ctx, row.Cursor)
		if err != nil {
			return changed, err
		}
		if ch.Reset {
			return changed, errReset
		}
		c, err := s.applyRemote(b, ch.Changes, false)
		if err != nil {
			return changed, err
		}
		changed = changed || c
		if ch.Cursor != "" {
			row.Cursor = ch.Cursor
		}
		if !ch.More || len(ch.Changes) == 0 {
			break
		}
	}
	return changed, nil
}

// cleanRemote checks a path a source gives: relative to its root, plain,
// without hidden segments. A marker is a marker: its folder, and marker
// true.
func (s *Service) cleanRemote(p string) (clean string, marker bool, ok bool) {
	p = strings.TrimPrefix(p, "/")
	if p == "" || len(p) > 1024 || strings.ContainsAny(p, "\\\x00") || path.Clean(p) != p || p == "." {
		return "", false, false
	}
	segs := strings.Split(p, "/")
	last := segs[len(segs)-1]
	if last == s.opts.Marker {
		segs = segs[:len(segs)-1]
		marker = true
	}
	for _, seg := range segs {
		if seg == "" || seg == ".." || strings.HasPrefix(seg, ".") {
			return "", false, false
		}
	}
	if marker {
		return strings.Join(segs, "/"), true, true
	}
	return p, false, true
}

// remoteGroup is the batch of a file of a source folder: the first folder
// inside the source, or the source folder itself for the files at its
// root.
func remoteGroup(folder, rel string) string {
	d := path.Dir(rel)
	if d == "." || d == "" {
		return folder
	}
	first, _, _ := strings.Cut(d, "/")
	return folder + "/" + first
}

// applyRemote brings in.db in line with what a source gave: in full (the
// files it does not list are gone), or as changes. A file is new when
// its content changed: another SHA-256 when both have one, else another
// etag or size.
func (s *Service) applyRemote(b Binding, files []RemoteFile, full bool) (bool, error) {
	unlock := s.lock(b.Project)
	defer unlock()
	rows, err := s.store.folderFiles(b.Project, b.Folder)
	if err != nil {
		return false, err
	}
	open, err := s.store.openBatches(b.Project)
	if err != nil {
		return false, err
	}
	root := vault.INRoot(b.Project) + "/"
	prefix := INPrefix(b.Project, b.Folder)
	now := s.opts.Now()
	changed := false
	touched := map[string]bool{}
	seen := map[string]bool{}
	markers := map[string]bool{}

	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	tx, err := s.store.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	batchOf := func(group string) (string, error) {
		bt, ok := open[group]
		if !ok {
			bt = Batch{ID: newID(), Project: b.Project, Dir: group, Opened: now}
			if _, err := tx.Exec(`INSERT INTO batches (id, project, dir, opened, last_change) VALUES (?, ?, ?, ?, ?)`,
				bt.ID, b.Project, group, nanos(now), nanos(now)); err != nil {
				return "", err
			}
			open[group] = bt
		}
		touched[group] = true
		return bt.ID, nil
	}
	for _, f := range files {
		rel, isMarker, ok := s.cleanRemote(f.Path)
		if !ok {
			continue
		}
		if isMarker {
			if strings.Contains(rel, "/") {
				continue // deeper than a batch's folder
			}
			group := b.Folder
			if rel != "" {
				group += "/" + rel
			}
			markers[group] = !f.Deleted
			continue
		}
		p := prefix + rel
		seen[p] = true
		r, known := rows[p]
		if f.Deleted {
			if known {
				changed = true
				if _, err := tx.Exec(`DELETE FROM files WHERE project = ? AND path = ?`, b.Project, p); err != nil {
					return false, err
				}
			}
			continue
		}
		mtime := f.Modified.UTC()
		if known && sameContent(r, f) {
			if r.ETag != f.ETag || (f.SHA256 != "" && r.SHA256 != f.SHA256) || !r.Modified.Equal(mtime) {
				if _, err := tx.Exec(`UPDATE files SET etag = ?, sha256 = ?, mtime = ? WHERE project = ? AND path = ?`,
					f.ETag, firstOf(f.SHA256, r.SHA256), nanos(mtime), b.Project, p); err != nil {
					return false, err
				}
			}
			continue
		}
		changed = true
		dir := dirOf(root, p)
		id, err := batchOf(remoteGroup(b.Folder, rel))
		if err != nil {
			return false, err
		}
		if known {
			_, err = tx.Exec(`UPDATE files SET size = ?, mtime = ?, sha256 = ?, etag = ?, status = ?, run = '', batch = ?, source = ?, updated = ? WHERE project = ? AND path = ?`,
				f.Size, nanos(mtime), f.SHA256, f.ETag, StatusNew, id, b.ID(), nanos(now), b.Project, p)
		} else {
			_, err = tx.Exec(`INSERT INTO files (`+fileCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, ?)`,
				b.Project, p, dir, f.Size, nanos(mtime), f.SHA256, StatusNew, b.ID(), id, nanos(now), nanos(now), f.ETag)
		}
		if err != nil {
			return false, err
		}
	}
	if full {
		for p := range rows {
			if !seen[p] {
				changed = true
				if _, err := tx.Exec(`DELETE FROM files WHERE project = ? AND path = ?`, b.Project, p); err != nil {
					return false, err
				}
			}
		}
	}
	for g := range touched {
		if _, err := tx.Exec(`UPDATE batches SET last_change = ? WHERE id = ?`, nanos(now), open[g].ID); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	s.mu.Lock()
	m := s.remoteMarkers[b.Project]
	if m == nil {
		m = map[string]bool{}
		s.remoteMarkers[b.Project] = m
	}
	if full {
		for g := range m {
			if (g == b.Folder || strings.HasPrefix(g, b.Folder+"/")) && !markers[g] {
				delete(m, g)
			}
		}
	}
	for g, there := range markers {
		if there {
			m[g] = true
		} else {
			delete(m, g)
		}
	}
	s.mu.Unlock()
	return changed, nil
}

// sameContent says whether a remote file is the one a row holds.
func sameContent(r File, f RemoteFile) bool {
	if r.Size != f.Size {
		return false
	}
	if r.SHA256 != "" && f.SHA256 != "" {
		return strings.EqualFold(r.SHA256, f.SHA256)
	}
	if r.ETag == "" && f.ETag == "" {
		return r.Modified.Equal(f.Modified.UTC())
	}
	return r.ETag == f.ETag
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// remoteMarkerAppeared reports whether a source's marker is in the batch
// folder group and was not the last time it was asked: a marker a source
// keeps (it cannot be removed from here) counts once.
func (s *Service) remoteMarkerAppeared(project, group string) bool {
	key := "src:" + project + "/" + group
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.remoteMarkers[project][group] {
		delete(s.markers, key)
		return false
	}
	if s.markers[key] {
		return false
	}
	s.markers[key] = true
	return true
}
