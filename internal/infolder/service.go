package infolder

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/vault"
)

// Sink is told when a batch closes: the pipelines' engine, which starts
// the runs of the pipelines that take it.
type Sink interface {
	INBatch(b Batch, files []File)
}

// Options tune a Service.
type Options struct {
	// Every is the time between two scans; default 20 s.
	Every time.Duration
	// Quiet closes a batch when nothing changed in its folder for that
	// long; default 60 s.
	Quiet time.Duration
	// Marker is the name of the file that closes the batch of its folder
	// at once; default ".done".
	Marker string
	Log    *slog.Logger
	Now    func() time.Time
	// FreeBytes reads the space left on the disk of a folder, for the
	// uploads' reserve; default vault.FreeBytes.
	FreeBytes func(dir string) (int64, error)
}

// Service keeps the state of the IN folders: a scan of each, every Every,
// brings in.db in line with the disk, hashes what arrived or changed, and
// closes the batches. The moves and removals made through it update the
// state at once.
type Service struct {
	store    *Store
	v        *vault.Vault
	projects func() []string
	opts     Options

	mu       sync.Mutex
	sink     Sink
	onChange func(project string)
	markers  map[string]bool // the markers seen, by absolute path
	locks    map[string]*sync.Mutex
	wake     chan string

	// The sources (sources.go).
	src           Sources
	wakeSrc       chan string
	infos         map[string]SourceInfo      // by <project>/<folder>, from the last session
	firstTry      map[string]time.Time       // the last check of a binding not listed yet
	firstErr      map[string]string          // why that check failed
	remoteMarkers map[string]map[string]bool // the markers a source holds, by project and batch folder
	client        *http.Client

	// The uploads (uploads.go).
	limits    UploadLimits
	discard   func(rel string) error
	onUpload  func(Upload)
	upLocks   map[string]*sync.Mutex // a file's pieces, one at a time, by <id>/<n>
	finishing map[string]bool        // the completions running
}

// Open opens the service on the database at dbPath, for the IN folders of
// the projects that projects lists.
func Open(dbPath string, v *vault.Vault, projects func() []string, opts Options) (*Service, error) {
	if opts.Every <= 0 {
		opts.Every = 20 * time.Second
	}
	if opts.Quiet <= 0 {
		opts.Quiet = 60 * time.Second
	}
	if opts.Marker == "" {
		opts.Marker = ".done"
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.FreeBytes == nil {
		opts.FreeBytes = vault.FreeBytes
	}
	st, err := OpenStore(dbPath)
	if err != nil {
		return nil, err
	}
	return &Service{store: st, v: v, projects: projects, opts: opts,
		markers: map[string]bool{}, locks: map[string]*sync.Mutex{}, wake: make(chan string, 16),
		wakeSrc: make(chan string, 16), infos: map[string]SourceInfo{}, firstTry: map[string]time.Time{},
		firstErr: map[string]string{}, remoteMarkers: map[string]map[string]bool{}, client: remoteClient()}, nil
}

// Store is the service's database.
func (s *Service) Store() *Store { return s.store }

// Vault is the vault the IN folders are in.
func (s *Service) Vault() *vault.Vault { return s.v }

// Close closes the database.
func (s *Service) Close() error { return s.store.Close() }

// SetOnChange sets who is told that the state of a project's IN folder
// changed: the web UI's live updates.
func (s *Service) SetOnChange(f func(project string)) {
	s.mu.Lock()
	s.onChange = f
	s.mu.Unlock()
}

func (s *Service) changed(project string) {
	s.mu.Lock()
	f := s.onChange
	s.mu.Unlock()
	if f != nil {
		f(project)
	}
}

// SetSink sets who is told of the batches that close; nil: nobody.
func (s *Service) SetSink(k Sink) {
	s.mu.Lock()
	s.sink = k
	s.mu.Unlock()
}

func (s *Service) lock(project string) func() {
	s.mu.Lock()
	l := s.locks[project]
	if l == nil {
		l = &sync.Mutex{}
		s.locks[project] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// Run scans the IN folders at once, then every Every, and a project when
// ScanSoon asks, until ctx ends; the sources, on their own clock. It
// resumes the uploads' completions a restart cut short, and drops the
// old uploads, at once and every hour.
func (s *Service) Run(ctx context.Context) {
	go s.runSources(ctx)
	t := time.NewTicker(s.opts.Every)
	defer t.Stop()
	gc := time.NewTicker(time.Hour)
	defer gc.Stop()
	s.resumeUploads()
	s.gcUploads()
	s.scanAll()
	for {
		select {
		case <-ctx.Done():
			return
		case <-gc.C:
			s.gcUploads()
		case <-t.C:
			s.scanAll()
		case p := <-s.wake:
			if err := s.Scan(p); err != nil {
				s.opts.Log.Warn("IN: scan", "project", p, "err", err)
			}
		}
	}
}

// ScanSoon asks Run for a scan of a project (its use_in changed).
func (s *Service) ScanSoon(project string) {
	select {
	case s.wake <- project:
	default:
	}
}

func (s *Service) scanAll() {
	for _, p := range s.projects() {
		if err := s.Scan(p); err != nil {
			s.opts.Log.Warn("IN: scan", "project", p, "err", err)
		}
	}
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Scan brings the state of a project's IN folder in line with the disk: a
// file there for the first time, or changed, is new, in the open batch of
// its first-level folder (a new one if none is open); a file gone is
// forgotten. Then it hashes the files without a hash and closes the
// batches whose folder was quiet long enough, or whose marker appeared.
func (s *Service) Scan(project string) error {
	changed, err := s.reconcile(project)
	if err != nil {
		return err
	}
	if err := s.hash(project); err != nil {
		return err
	}
	closed, err := s.closeBatches(project)
	if changed || closed {
		s.changed(project)
	}
	return err
}

func (s *Service) reconcile(project string) (bool, error) {
	unlock := s.lock(project)
	defer unlock()
	root := vault.INRoot(project)
	remote, err := s.remoteFolders(project)
	if err != nil {
		return false, err
	}
	// A source's folder is the source's: what the disk has there is not
	// looked at, nor are the rows of the source's files.
	inRemote := func(p string) bool {
		first, _, _ := strings.Cut(strings.TrimPrefix(p, root+"/"), "/")
		_, ok := remote[first]
		return ok
	}
	disk := map[string]vault.INEntry{}
	err = s.v.INWalk(root, func(e vault.INEntry) error {
		if !inRemote(e.Path) {
			disk[e.Path] = e
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, vault.ErrNotIN) {
		return false, err
	}
	rows, err := s.store.projectFiles(project)
	if err != nil {
		return false, err
	}
	for p, r := range rows {
		if r.Remote() || inRemote(p) {
			delete(rows, p)
		}
	}
	open, err := s.store.openBatches(project)
	if err != nil {
		return false, err
	}
	changed := false
	now := s.opts.Now()
	touched := map[string]bool{} // the groups whose batch changed

	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	tx, err := s.store.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	batchOf := func(group string) (string, error) {
		b, ok := open[group]
		if !ok {
			b = Batch{ID: newID(), Project: project, Dir: group, Opened: now}
			if _, err := tx.Exec(`INSERT INTO batches (id, project, dir, opened, last_change) VALUES (?, ?, ?, ?, ?)`,
				b.ID, project, group, nanos(now), nanos(now)); err != nil {
				return "", err
			}
			open[group] = b
		}
		touched[group] = true
		return b.ID, nil
	}
	for p, e := range disk {
		r, known := rows[p]
		if known && r.Size == e.Size && r.Modified.Equal(e.ModTime.UTC()) {
			continue
		}
		changed = true
		dir := dirOf(root+"/", p)
		id, err := batchOf(groupOf(dir))
		if err != nil {
			return false, err
		}
		if known {
			_, err = tx.Exec(`UPDATE files SET size = ?, mtime = ?, sha256 = '', status = ?, run = '', batch = ?, updated = ? WHERE project = ? AND path = ?`,
				e.Size, nanos(e.ModTime), StatusNew, id, nanos(now), project, p)
		} else {
			_, err = tx.Exec(`INSERT INTO files (`+fileCols+`) VALUES (?, ?, ?, ?, ?, '', ?, '', ?, ?, ?, ?, '')`,
				project, p, dir, e.Size, nanos(e.ModTime), StatusNew, SourceLocal, id, nanos(now), nanos(now))
		}
		if err != nil {
			return false, err
		}
	}
	for p := range rows {
		if _, ok := disk[p]; !ok {
			changed = true
			if _, err := tx.Exec(`DELETE FROM files WHERE project = ? AND path = ?`, project, p); err != nil {
				return false, err
			}
		}
	}
	for g := range touched {
		if _, err := tx.Exec(`UPDATE batches SET last_change = ? WHERE id = ?`, nanos(now), open[g].ID); err != nil {
			return false, err
		}
	}
	return changed, tx.Commit()
}

// hash computes the SHA-256 of the files of a project that have none. A
// file that changed while it was read keeps none, for the next scan.
func (s *Service) hash(project string) error {
	rows, err := s.store.db.Query(`SELECT path, size, mtime FROM files WHERE project = ? AND sha256 = '' AND source = ?`, project, SourceLocal)
	if err != nil {
		return err
	}
	type todo struct {
		path        string
		size, mtime int64
	}
	var list []todo
	for rows.Next() {
		var t todo
		if err := rows.Scan(&t.path, &t.size, &t.mtime); err != nil {
			rows.Close()
			return err
		}
		list = append(list, t)
	}
	rows.Close()
	for _, t := range list {
		sum, e, err := s.sum(t.path)
		if err != nil {
			continue // gone or unreadable: the next scan sees it
		}
		if e.Size != t.size || nanos(e.ModTime) != t.mtime {
			continue
		}
		s.store.mu.Lock()
		_, err = s.store.db.Exec(`UPDATE files SET sha256 = ? WHERE project = ? AND path = ? AND size = ? AND mtime = ?`,
			sum, project, t.path, t.size, t.mtime)
		s.store.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

// sum is the SHA-256 of a file of an IN folder, and the file as it was
// after the read.
func (s *Service) sum(p string) (string, vault.INEntry, error) {
	f, _, err := s.v.INOpen(p)
	if err != nil {
		return "", vault.INEntry{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", vault.INEntry{}, err
	}
	e, err := s.v.INStat(p)
	if err != nil {
		return "", vault.INEntry{}, err
	}
	return hex.EncodeToString(h.Sum(nil)), e, nil
}

// closeBatches closes the open batches of a project whose folder was quiet
// for Quiet, or whose marker appeared, once all their files have a hash,
// and tells the sink.
func (s *Service) closeBatches(project string) (bool, error) {
	// The disk's scan and a source's check both close batches: under the
	// project's lock, none closes a batch a file is joining.
	unlock := s.lock(project)
	defer unlock()
	open, err := s.store.openBatches(project)
	if err != nil {
		return false, err
	}
	now := s.opts.Now()
	closedAny := false
	remote, err := s.remoteFolders(project)
	if err != nil {
		return false, err
	}
	for _, b := range open {
		first, _, _ := strings.Cut(b.Dir, "/")
		_, isRemote := remote[first]
		var marker string
		var fresh bool
		if isRemote {
			fresh = s.remoteMarkerAppeared(project, b.Dir)
		} else {
			marker = filepath.Join(s.v.Root, filepath.FromSlash(vault.INRoot(project)), filepath.FromSlash(b.Dir), s.opts.Marker)
			fresh = s.markerAppeared(marker)
		}
		if !fresh && now.Sub(b.LastChange) < s.opts.Quiet {
			continue
		}
		// A local file waits for its hash; a remote one has its version.
		var unhashed int
		if err := s.store.db.QueryRow(`SELECT count(*) FROM files WHERE batch = ? AND sha256 = '' AND source = ?`, b.ID, SourceLocal).Scan(&unhashed); err != nil {
			return closedAny, err
		}
		if unhashed > 0 {
			continue
		}
		var files int
		var bytes int64
		if err := s.store.db.QueryRow(`SELECT count(*), coalesce(sum(max(size, 0)), 0) FROM files WHERE batch = ?`, b.ID).Scan(&files, &bytes); err != nil {
			return closedAny, err
		}
		s.store.mu.Lock()
		res, err := s.store.db.Exec(`UPDATE batches SET closed = ?, files = ?, bytes = ? WHERE id = ? AND closed = 0`, nanos(now), files, bytes, b.ID)
		s.store.mu.Unlock()
		if err != nil {
			return closedAny, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue // closed meanwhile, by the local scan or a source's check
		}
		if fresh && marker != "" {
			_ = os.Remove(marker) // a read-only folder keeps it: it was seen
		}
		closedAny = true
		b.Closed, b.Files, b.Bytes = now, files, bytes
		if files == 0 {
			continue // all gone before it closed
		}
		s.mu.Lock()
		sink := s.sink
		s.mu.Unlock()
		if sink == nil {
			continue
		}
		list, err := s.batchFiles(b.ID, project)
		if err != nil {
			return closedAny, err
		}
		sink.INBatch(b, list)
	}
	return closedAny, nil
}

// markerAppeared reports whether the marker at abs is there and was not
// the last time: one that stays (a folder the server cannot write) counts
// once.
func (s *Service) markerAppeared(abs string) bool {
	st, err := os.Lstat(abs)
	there := err == nil && st.Mode().IsRegular()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !there {
		delete(s.markers, abs)
		return false
	}
	if s.markers[abs] {
		return false
	}
	s.markers[abs] = true
	return true
}

func (s *Service) batchFiles(id, project string) ([]File, error) {
	var out []File
	q := Query{Batch: id, Limit: MaxLimit}
	for {
		page, err := s.store.List(project, q)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Files...)
		if page.Next == "" {
			return out, nil
		}
		q.Cursor = page.Next
	}
}

// Batch returns a batch with its files.
func (s *Service) Batch(id string) (Batch, []File, error) {
	b, err := s.store.Batch(id)
	if err != nil {
		return Batch{}, nil, err
	}
	files, err := s.batchFiles(id, b.Project)
	return b, files, err
}

// Move moves a file or a folder of a project's IN folder (vault.INMove)
// and its state with it. In a source's folder it moves inside that
// source, through its plugin, when the binding lets it write and the
// source can move; never between the source and the disk. A source's
// file is moved to a target outside its source inside it: done/ is the
// source's done/, so a pipeline moves a local file and a source's alike.
// A file moved where it is stays, and that is no error.
func (s *Service) Move(project, from, to string) (vault.INEntry, error) {
	ff, frel, fromRemote := s.remoteOf(project, from)
	tf, trel, toRemote := s.remoteOf(project, to)
	root := vault.INRoot(project) + "/"
	if fromRemote && !toRemote && strings.HasPrefix(to, root) {
		trel = strings.TrimPrefix(to, root)
		tf, to, toRemote = ff, INPrefix(project, ff)+trel, true
	}
	if from == to {
		// Already there (done/ of a file in done/): nothing to move (BUG-120).
		return s.Stat(project, from)
	}
	switch {
	case fromRemote && toRemote && ff == tf:
		return s.moveRemote(project, ff, from, to, frel, trel)
	case fromRemote || toRemote:
		return vault.INEntry{}, fmt.Errorf("%w moves inside its source only: %s to %s", ErrRemote, from, to)
	}
	unlock := s.lock(project)
	defer unlock()
	e, err := s.v.INMove(from, to)
	if err != nil {
		return vault.INEntry{}, err
	}
	if err := s.store.Moved(project, from, to, s.opts.Now()); err != nil {
		return e, fmt.Errorf("moved, but its state: %w", err)
	}
	s.changed(project)
	return e, nil
}

// Remove removes a file or a folder of a project's IN folder, with discard
// when given (the trash), else for good, and forgets its state. A source's
// files are not removed from here.
func (s *Service) Remove(project, p string, discard func(rel string, dir bool) error) error {
	if folder, _, ok := s.remoteOf(project, p); ok {
		return fmt.Errorf("%w is not removed from gosidian: move it, or set it aside (%s is the source %s)", ErrRemote, p, folder)
	}
	unlock := s.lock(project)
	defer unlock()
	e, err := s.v.INStat(p)
	if err != nil {
		return err
	}
	if e.Path == vault.INRoot(project) {
		return fmt.Errorf("%w: the IN folder itself is not removed", vault.ErrNotIN)
	}
	if discard != nil {
		err = discard(e.Path, e.Dir)
	} else {
		err = s.v.INRemove(e.Path)
	}
	if err != nil {
		return err
	}
	if err := s.store.Removed(project, e.Path); err != nil {
		return err
	}
	s.changed(project)
	return nil
}

// SetStatus sets the status of files of a project; run, when given, is
// the run that took them.
func (s *Service) SetStatus(project string, paths []string, status, run string) error {
	if !ValidStatus(status) {
		return fmt.Errorf("status %q: new, working, done, error or ignored", status)
	}
	if err := s.store.SetStatus(project, paths, status, run, s.opts.Now()); err != nil {
		return err
	}
	s.changed(project)
	return nil
}

// RunEnded sets done, or error, on the files a run took and left working.
func (s *Service) RunEnded(run string, ok bool) error {
	projects, err := s.store.RunEnded(run, ok, s.opts.Now())
	for _, p := range projects {
		s.changed(p)
	}
	return err
}

// moveRemote moves a file or a folder inside a source's folder.
func (s *Service) moveRemote(project, folder, from, to, frel, trel string) (vault.INEntry, error) {
	b, ok := s.binding(project, folder)
	if !ok {
		return vault.INEntry{}, fmt.Errorf("%w: the source of %s is not bound now, or the pipelines are off in the server", ErrRemote, folder)
	}
	if !b.Write {
		return vault.INEntry{}, fmt.Errorf("%w: %s is bound read-only (bind it with --write to move its files)", ErrRemote, b.ID())
	}
	if frel == "" || trel == "" {
		return vault.INEntry{}, fmt.Errorf("%w: the source's folder itself does not move", ErrRemote)
	}
	if _, _, ok := s.cleanRemote(trel); !ok {
		return vault.INEntry{}, fmt.Errorf("%w: %s is no plain path", vault.ErrNotIN, to)
	}
	if path.Base(trel) == s.opts.Marker {
		return vault.INEntry{}, fmt.Errorf("%w: %s is the name of the marker", vault.ErrNotIN, to)
	}
	src := s.sourcesOf()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sess, err := src.Session(ctx, b)
	if err != nil {
		return vault.INEntry{}, err
	}
	defer sess.Close()
	if !sess.Info().Move {
		return vault.INEntry{}, fmt.Errorf("%w: the source %s cannot move its files", ErrRemote, b.ID())
	}
	unlock := s.lock(project)
	defer unlock()
	if _, err := s.store.File(project, to); err == nil {
		return vault.INEntry{}, fmt.Errorf("%s is there already", to)
	}
	got, err := sess.Move(ctx, frel, trel)
	if err != nil {
		return vault.INEntry{}, err
	}
	if err := s.store.Moved(project, from, to, s.opts.Now()); err != nil {
		return vault.INEntry{}, fmt.Errorf("moved, but its state: %w", err)
	}
	e := vault.INEntry{Path: to}
	if row, err := s.store.File(project, to); err == nil {
		// The source may give the moved file another etag: the same file.
		if got.ETag != "" && got.ETag != row.ETag {
			s.store.mu.Lock()
			_, _ = s.store.db.Exec(`UPDATE files SET etag = ? WHERE project = ? AND path = ?`, got.ETag, project, to)
			s.store.mu.Unlock()
		}
		e.Size, e.ModTime = row.Size, row.Modified
	} else {
		e.Dir = true
	}
	s.changed(project)
	return e, nil
}

// Stat describes a file or a folder of a project's IN folder: on the disk,
// or, in a source's folder, as in.db holds it.
func (s *Service) Stat(project, p string) (vault.INEntry, error) {
	folder, rel, ok := s.remoteOf(project, p)
	if !ok {
		return s.v.INStat(p)
	}
	if rel == "" {
		return vault.INEntry{Path: p, Dir: true}, nil
	}
	if f, err := s.store.File(project, p); err == nil {
		return vault.INEntry{Path: p, Size: f.Size, ModTime: f.Modified}, nil
	}
	if under, err := s.store.hasUnder(project, p); err == nil && under {
		return vault.INEntry{Path: p, Dir: true}, nil
	}
	return vault.INEntry{}, fmt.Errorf("%s in the source %s: %w", p, folder, fs.ErrNotExist)
}

// Open opens a file of an IN folder for reading: on the disk, or, in a
// source's folder, from the source, read in ranges as it is read.
func (s *Service) Open(ctx context.Context, p string) (io.ReadSeekCloser, vault.INEntry, error) {
	project, _, _ := strings.Cut(p, "/")
	folder, rel, ok := s.remoteOf(project, p)
	if !ok {
		f, e, err := s.v.INOpen(p)
		if err != nil {
			return nil, vault.INEntry{}, err
		}
		return f, e, nil
	}
	row, err := s.store.File(project, p)
	if err != nil {
		return nil, vault.INEntry{}, fmt.Errorf("%s: %w", p, fs.ErrNotExist)
	}
	b, bound := s.binding(project, folder)
	if !bound {
		return nil, vault.INEntry{}, fmt.Errorf("%w: the source of %s is not bound now, or the pipelines are off in the server", ErrRemote, folder)
	}
	src := s.sourcesOf()
	open := func(ctx context.Context) (RemoteURL, error) {
		sess, err := src.Session(ctx, b)
		if err != nil {
			return RemoteURL{}, err
		}
		defer sess.Close()
		return sess.Open(ctx, rel)
	}
	e := vault.INEntry{Path: p, Size: row.Size, ModTime: row.Modified}
	return newRemoteFile(ctx, s.client, open, p, row.Size), e, nil
}

// Read reads a file of an IN folder whole, up to max bytes.
func (s *Service) Read(ctx context.Context, p string, max int64) ([]byte, error) {
	f, e, err := s.Open(ctx, p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// A size not known yet (-1) is checked as the file is read.
	if e.Size > max {
		return nil, fmt.Errorf("%s is %d bytes, more than %d", p, e.Size, max)
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s is more than %d bytes", p, max)
	}
	return data, nil
}

// remoteClient reads the files of the sources: no redirect (a URL goes to
// the hosts the plugin may point at, and no further), and an answer within
// a minute.
func remoteClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = time.Minute
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
