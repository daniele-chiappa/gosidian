package infolder

import (
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gosidian/gosidian/internal/vault"
	"golang.org/x/text/unicode/norm"
)

// The upload into an IN folder (IMP-172 phase 5): a session lists the
// files, each file arrives in pieces at the offset received so far, into
// IN/.uploads/<id>/<n>.part, hashed as it arrives; the state of its hash
// is saved with each piece, so that an upload resumes after a restart of
// the server as after a lost connection. Its completion moves each file to
// its place with a rename (a zip is unpacked first), writes their rows in
// in.db with their hash, and closes their batch at once: an upload is one
// batch of each first-level folder it fills.

// Upload states.
const (
	UploadOpen       = "open"
	UploadCompleting = "completing"
	UploadDone       = "done"
)

// What an upload does with a file whose place is taken, chosen once for
// the upload.
const (
	// PolicyReplace: the file there goes to the trash (for good without
	// it), the new one takes its place; a folder is not replaced.
	PolicyReplace = "replace"
	// PolicyKeep: the new file takes a free name, "name (2).ext".
	PolicyKeep = "keep"
	// PolicySkip: the new file is left out.
	PolicySkip = "skip"
)

// ValidPolicy reports whether p is a policy.
func ValidPolicy(p string) bool {
	return p == PolicyReplace || p == PolicyKeep || p == PolicySkip
}

// Results of an upload's file.
const (
	ResultNew      = "new"      // placed where nothing was
	ResultReplaced = "replaced" // placed over the file there
	ResultRenamed  = "renamed"  // placed under a free name: both kept
	ResultSkipped  = "skipped"  // left out: the place was taken
	ResultSame     = "same"     // left out: the file there is the same
	ResultUnzipped = "unzipped" // a zip, unpacked
	ResultDropped  = "dropped"  // left out by the client
	ResultError    = "error"
)

// The pieces of a file.
const (
	// ChunkSize is the piece a client starts with.
	ChunkSize = 8 << 20
	// MaxChunk is the largest piece taken: under the 100 MB a request may
	// carry through Cloudflare, and the disk's reserve is checked for one.
	MaxChunk = 64 << 20
	// MaxUploadFiles caps the files of one upload.
	MaxUploadFiles = 100000
)

// Errors of the uploads.
var (
	ErrUploadNotFound = errors.New("no such upload")
	// ErrUploadState: the upload is completing, or done.
	ErrUploadState = errors.New("the upload is not open")
	// ErrNoSpace: the disk would go under its reserve.
	ErrNoSpace = errors.New("not enough free disk")
	// ErrTooLarge: over a cap of [in].
	ErrTooLarge = errors.New("over a cap of the server")
	// ErrBadUpload: a file named or sized as no file can be.
	ErrBadUpload = errors.New("invalid upload")
)

// OffsetError is a piece sent at another offset than the bytes received.
type OffsetError struct{ Received int64 }

func (e *OffsetError) Error() string {
	return fmt.Sprintf("the file has %d bytes: send the piece at that offset", e.Received)
}

// IncompleteError is a completion asked before its files arrived whole.
type IncompleteError struct{ Files []int }

func (e *IncompleteError) Error() string {
	return fmt.Sprintf("%d files have not arrived whole: send them, or drop them", len(e.Files))
}

// UploadLimits cap the uploads ([in] of the configuration); 0 is no cap.
type UploadLimits struct {
	Reserve       int64
	MaxIN         int64
	TTL           time.Duration
	ZipMaxEntries int
	ZipMaxBytes   int64
	ZipMaxRatio   int
	// Channels caps each way in: web (the web UI and the REST API).
	Channels map[string]ChannelLimits
}

// ChannelLimits cap the uploads of one way in.
type ChannelLimits struct {
	MaxFile    int64
	MaxUpload  int64
	Extensions []string
}

// Upload is an upload into a project's IN folder.
type Upload struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	// Dir is the folder the files go to, relative to the IN folder.
	Dir     string    `json:"dir"`
	Policy  string    `json:"policy"`
	Owner   string    `json:"owner"`
	OwnerID string    `json:"-"`
	Channel string    `json:"channel"`
	State   string    `json:"state"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	Expires time.Time `json:"expires"`
	// Error says why a completion stopped.
	Error     string `json:"error,omitempty"`
	FileCount int    `json:"file_count"`
	Bytes     int64  `json:"bytes"`
	Received  int64  `json:"received"`
	ChunkSize int64  `json:"chunk_size"`
	MaxChunk  int64  `json:"max_chunk"`
	// In a listing: its files are in folders, or all zips to unpack (to
	// choose them again the way they were).
	Folders bool `json:"folders,omitempty"`
	Zips    bool `json:"zips,omitempty"`
	// Batches are the batches its completion closed.
	Batches []string     `json:"batches,omitempty"`
	Files   []UploadFile `json:"files,omitempty"`

	fingerprint string
	batches     map[string]string // by group
}

// UploadFile is a file of an upload.
type UploadFile struct {
	N    int    `json:"n"`
	Path string `json:"path"` // relative to the upload's folder
	Size int64  `json:"size"`
	// Unzip: a zip to unpack, into a folder of its own.
	Unzip bool `json:"unzip,omitempty"`
	// Modified, in Unix milliseconds, is the time the file keeps; 0 is the
	// time it arrived.
	Modified int64  `json:"modified,omitempty"`
	Received int64  `json:"received"`
	SHA256   string `json:"sha256,omitempty"`
	Dropped  bool   `json:"dropped,omitempty"`
	// Exists, when the upload opens: its place is taken.
	Exists *bool       `json:"exists,omitempty"`
	Result *FileResult `json:"result,omitempty"`
}

// FileResult is what the completion did with a file.
type FileResult struct {
	Result string `json:"result"`
	// Path is the vault path it went to: the file, or a zip's folder.
	Path  string `json:"path,omitempty"`
	Error string `json:"error,omitempty"`
	// A zip's: its files by result, how many it left out on purpose
	// (folders, hidden files, links), the first it refused, with why.
	Counts  map[string]int `json:"counts,omitempty"`
	Ignored int            `json:"ignored,omitempty"`
	Refused []string       `json:"refused,omitempty"`
}

// UploadRequest opens an upload.
type UploadRequest struct {
	Project, Dir, Policy    string
	Owner, OwnerID, Channel string
	Files                   []UploadFile
}

// SetUploadLimits sets the caps of the uploads.
func (s *Service) SetUploadLimits(l UploadLimits) {
	if l.TTL <= 0 {
		l.TTL = 24 * time.Hour
	}
	s.mu.Lock()
	s.limits = l
	s.mu.Unlock()
}

func (s *Service) uploadLimits() UploadLimits {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.limits.TTL <= 0 {
		s.limits.TTL = 24 * time.Hour
	}
	return s.limits
}

// SetDiscard sets how a file an upload replaces goes: the trash; nil, the
// default, replaces it for good.
func (s *Service) SetDiscard(f func(rel string) error) {
	s.mu.Lock()
	s.discard = f
	s.mu.Unlock()
}

// SetOnUpload sets who is told of an upload whose completion ended: the
// audit log.
func (s *Service) SetOnUpload(f func(Upload)) {
	s.mu.Lock()
	s.onUpload = f
	s.mu.Unlock()
}

// cleanUploadPath checks the path of a file an upload names, relative to
// its folder, and returns it in NFC: no empty, ".", ".." or hidden segment,
// no \ or control character, at most 255 bytes a segment and 1024 in all.
func cleanUploadPath(p string) (string, error) {
	if !utf8.ValidString(p) {
		return "", fmt.Errorf("%w: %q is not UTF-8", ErrBadUpload, p)
	}
	p = norm.NFC.String(p)
	if p == "" || len(p) > 1024 {
		return "", fmt.Errorf("%w: a path of 1 to 1024 bytes", ErrBadUpload)
	}
	if strings.ContainsRune(p, '\\') || strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%w: %q has a \\ or a control character", ErrBadUpload, p)
	}
	for _, seg := range strings.Split(p, "/") {
		switch {
		case seg == "" || seg == "." || seg == "..":
			return "", fmt.Errorf("%w: %q is no plain relative path", ErrBadUpload, p)
		case strings.HasPrefix(seg, "."):
			return "", fmt.Errorf("%w: %q has a hidden name, which IN does not show", ErrBadUpload, p)
		case len(seg) > 255:
			return "", fmt.Errorf("%w: a name of %q is over 255 bytes", ErrBadUpload, p)
		case strings.TrimSpace(seg) != seg:
			return "", fmt.Errorf("%w: a name of %q starts or ends with a space", ErrBadUpload, p)
		}
	}
	return p, nil
}

// cleanUploadDir checks the folder of an upload, relative to the IN folder
// ("" its root).
func cleanUploadDir(d string) (string, error) {
	d = strings.Trim(d, "/")
	if d == "" {
		return "", nil
	}
	return cleanUploadPath(d)
}

// uploadTarget is the vault path of an upload's file.
func uploadTarget(u Upload, p string) string {
	return path.Join(vault.INRoot(u.Project), u.Dir, p)
}

// partName is the name of a file arriving, in its upload's folder.
func partName(n int) string { return fmt.Sprintf("%d.part", n) }

func fingerprintOf(dir string, files []UploadFile) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n", dir)
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%d\x00%t\n", f.Path, f.Size, f.Unzip)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// room says whether the disk of a project's IN folder can take need bytes
// more and keep the reserve.
func (s *Service) room(project string, need int64) error {
	l := s.uploadLimits()
	if l.Reserve <= 0 {
		return nil
	}
	free, err := s.opts.FreeBytes(filepath.Join(s.v.Root, filepath.FromSlash(vault.INRoot(project))))
	if err != nil || free < 0 {
		return nil // not known: the reserve is not checked
	}
	if free-need < l.Reserve {
		return fmt.Errorf("%w: %d bytes left, %d more asked, %d kept free", ErrNoSpace, free, need, l.Reserve)
	}
	return nil
}

// OpenUpload opens an upload, or returns the open upload of the same
// account, project, folder and files, to resume. Policy "" is keep.
func (s *Service) OpenUpload(r UploadRequest) (Upload, error) {
	if r.Policy == "" {
		r.Policy = PolicyKeep
	}
	if !ValidPolicy(r.Policy) {
		return Upload{}, fmt.Errorf("%w: policy %q: replace, keep or skip", ErrBadUpload, r.Policy)
	}
	dir, err := cleanUploadDir(r.Dir)
	if err != nil {
		return Upload{}, err
	}
	if len(r.Files) == 0 || len(r.Files) > MaxUploadFiles {
		return Upload{}, fmt.Errorf("%w: 1 to %d files", ErrBadUpload, MaxUploadFiles)
	}
	l := s.uploadLimits()
	ch := l.Channels[r.Channel]
	remote, err := s.remoteFolders(r.Project)
	if err != nil {
		return Upload{}, err
	}
	files := make([]UploadFile, len(r.Files))
	seen := map[string]bool{}
	var total int64
	for i, f := range r.Files {
		p, err := cleanUploadPath(f.Path)
		if err != nil {
			return Upload{}, err
		}
		if f.Size < 0 {
			return Upload{}, fmt.Errorf("%w: %s has a size below 0", ErrBadUpload, p)
		}
		full := path.Join(dir, p)
		if first, _, _ := strings.Cut(full, "/"); remote[first].Source != "" {
			return Upload{}, fmt.Errorf("%w: %s is the folder of the source %s: its files are the source's", ErrBadUpload, first, remote[first].Source)
		}
		if seen[full] {
			return Upload{}, fmt.Errorf("%w: %s is named twice", ErrBadUpload, p)
		}
		seen[full] = true
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(p), "."))
		unzip := f.Unzip && ext == "zip"
		if len(ch.Extensions) > 0 && !unzip && !slices.Contains(ch.Extensions, ext) {
			return Upload{}, fmt.Errorf("%w: %s: the server takes only %s", ErrTooLarge, p, strings.Join(ch.Extensions, ", "))
		}
		if ch.MaxFile > 0 && f.Size > ch.MaxFile {
			return Upload{}, fmt.Errorf("%w: %s is %d bytes, at most %d a file", ErrTooLarge, p, f.Size, ch.MaxFile)
		}
		total += f.Size
		files[i] = UploadFile{N: i, Path: p, Size: f.Size, Unzip: unzip, Modified: max(f.Modified, 0)}
	}
	if ch.MaxUpload > 0 && total > ch.MaxUpload {
		return Upload{}, fmt.Errorf("%w: the files are %d bytes, at most %d an upload", ErrTooLarge, total, ch.MaxUpload)
	}
	fp := fingerprintOf(dir, files)
	now := s.opts.Now()
	if u, err := s.store.openUploadLike(r.Project, r.OwnerID, fp); err == nil {
		if err := s.store.touchUpload(u.ID, r.Policy, now); err != nil {
			return Upload{}, err
		}
		return s.uploadView(u.ID, true)
	}
	if l.MaxIN > 0 {
		have, err := s.store.localBytes(r.Project)
		if err != nil {
			return Upload{}, err
		}
		if have+total > l.MaxIN {
			return Upload{}, fmt.Errorf("%w: the IN folder holds %d bytes, with these %d, at most %d", ErrTooLarge, have, total, l.MaxIN)
		}
	}
	if err := s.room(r.Project, total); err != nil {
		return Upload{}, err
	}
	u := Upload{ID: newID() + newID(), Project: r.Project, Dir: dir, Policy: r.Policy, Owner: r.Owner, OwnerID: r.OwnerID,
		Channel: r.Channel, State: UploadOpen, Created: now, Updated: now, Files: files, fingerprint: fp}
	if _, err := s.v.INStaging(r.Project, u.ID, true); err != nil {
		return Upload{}, err
	}
	if err := s.store.insertUpload(u); err != nil {
		_ = s.v.INRemoveStaging(r.Project, u.ID)
		return Upload{}, err
	}
	return s.uploadView(u.ID, true)
}

// uploadView is an upload as a client sees it; with exists, each file
// says whether its place is taken.
func (s *Service) uploadView(id string, exists bool) (Upload, error) {
	u, err := s.store.upload(id, true)
	if err != nil {
		return Upload{}, err
	}
	s.fill(&u)
	if exists && u.State == UploadOpen {
		for i := range u.Files {
			p := uploadTarget(u, u.Files[i].Path)
			if u.Files[i].Unzip {
				p = strings.TrimSuffix(p, path.Ext(p))
			}
			_, err := s.v.INStat(p)
			there := err == nil
			u.Files[i].Exists = &there
		}
	}
	return u, nil
}

// fill fills what an upload's row does not hold.
func (s *Service) fill(u *Upload) {
	u.ChunkSize, u.MaxChunk = ChunkSize, MaxChunk
	u.Expires = u.Updated.Add(s.uploadLimits().TTL)
	u.Batches = nil
	for _, id := range u.batches {
		u.Batches = append(u.Batches, id)
	}
	slices.Sort(u.Batches)
}

// owned returns the upload of an account in a project, without its files.
func (s *Service) owned(project, id, ownerID string) (Upload, error) {
	u, err := s.store.upload(id, false)
	if err != nil {
		return Upload{}, err
	}
	if u.Project != project || u.OwnerID != ownerID {
		return Upload{}, ErrUploadNotFound
	}
	return u, nil
}

// GetUpload returns an upload of an account in a project, with its files
// when files is set.
func (s *Service) GetUpload(project, id, ownerID string, files bool) (Upload, error) {
	u, err := s.owned(project, id, ownerID)
	if err != nil {
		return Upload{}, err
	}
	if !files {
		s.fill(&u)
		return u, nil
	}
	return s.uploadView(id, false)
}

// Uploads lists the uploads of an account in a project, newest first,
// without their files.
func (s *Service) Uploads(project, ownerID string) ([]Upload, error) {
	list, err := s.store.uploadsWhere(`project = ? AND owner_id = ?`, project, ownerID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		s.fill(&list[i])
	}
	if list == nil {
		list = []Upload{}
	}
	return list, nil
}

// CancelUpload drops an open upload, or forgets a done one, with what its
// folder holds.
func (s *Service) CancelUpload(project, id, ownerID string) error {
	u, err := s.owned(project, id, ownerID)
	if err != nil {
		return err
	}
	if u.State == UploadCompleting {
		return fmt.Errorf("%w: it is completing", ErrUploadState)
	}
	return s.dropUpload(u)
}

// dropUpload forgets an upload, and removes its folder: a folder that
// cannot be (its project gone meanwhile) is left to the project.
func (s *Service) dropUpload(u Upload) error {
	if err := s.v.INRemoveStaging(u.Project, u.ID); err != nil {
		s.opts.Log.Warn("IN: an upload's folder", "upload", u.ID, "project", u.Project, "err", err)
	}
	s.forgetLocks(u.ID)
	return s.store.deleteUpload(u.ID)
}

func (s *Service) fileLock(id string, n int) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upLocks == nil {
		s.upLocks = map[string]*sync.Mutex{}
	}
	k := fmt.Sprintf("%s/%d", id, n)
	l := s.upLocks[k]
	if l == nil {
		l = &sync.Mutex{}
		s.upLocks[k] = l
	}
	return l
}

func (s *Service) forgetLocks(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.upLocks {
		if strings.HasPrefix(k, id+"/") {
			delete(s.upLocks, k)
		}
	}
}

// WriteChunk writes a piece of a file of an open upload, at offset: at
// most the bytes received so far, the bytes the server has already being
// skipped, so that a piece sent again after a connection broke in its
// middle goes on where it stopped; past them, an OffsetError says how
// many there are. The piece is at most MaxChunk bytes, and no more than
// the file lacks. What arrived before the connection broke is kept.
func (s *Service) WriteChunk(project, id, ownerID string, n int, offset int64, body io.Reader) (UploadFile, error) {
	u, err := s.owned(project, id, ownerID)
	if err != nil {
		return UploadFile{}, err
	}
	if u.State != UploadOpen {
		return UploadFile{}, ErrUploadState
	}
	l := s.fileLock(id, n)
	l.Lock()
	defer l.Unlock()
	f, state, err := s.store.uploadFile(id, n)
	if err != nil {
		return UploadFile{}, err
	}
	if f.Dropped {
		return UploadFile{}, fmt.Errorf("%w: the file was dropped", ErrUploadState)
	}
	if offset > f.Received {
		return f, &OffsetError{Received: f.Received}
	}
	if skip := f.Received - offset; skip > 0 {
		// Bytes the server has: sent again, they are not written twice.
		if _, err := io.CopyN(io.Discard, body, skip); err != nil {
			return f, nil
		}
	}
	lack := f.Size - f.Received
	if lack == 0 {
		return f, nil
	}
	if err := s.room(project, min(lack, MaxChunk)); err != nil {
		return f, err
	}
	dir, err := s.v.INStaging(project, id, false)
	if err != nil {
		return f, err
	}
	out, err := os.OpenFile(filepath.Join(dir, partName(n)), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return f, err
	}
	defer out.Close()
	st, err := out.Stat()
	if err != nil {
		return f, err
	}
	h := sha256.New()
	switch {
	case st.Size() < f.Received:
		// The bytes the database counts are not all on the disk (a copy
		// of the state without its files): the file starts again.
		if err := out.Truncate(0); err != nil {
			return f, err
		}
		if _, err := s.store.setReceived(id, n, f.Received, 0, nil, "", s.opts.Now()); err != nil {
			return f, err
		}
		f.Received = 0
		return f, &OffsetError{Received: 0}
	case st.Size() > f.Received:
		// A piece written, and not recorded: a crash in between.
		if err := out.Truncate(f.Received); err != nil {
			return f, err
		}
	}
	if f.Received > 0 {
		if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(state); err != nil {
			return f, fmt.Errorf("the hash of the bytes received: %w", err)
		}
	}
	if _, err := out.Seek(f.Received, io.SeekStart); err != nil {
		return f, err
	}
	got, rerr, werr := copyPiece(out, h, body, min(lack, MaxChunk))
	if werr != nil {
		// The disk refused: back to what was recorded.
		_ = out.Truncate(f.Received)
		return f, werr
	}
	if err := out.Sync(); err != nil {
		_ = out.Truncate(f.Received)
		return f, err
	}
	from := f.Received
	f.Received += got
	saved, err := h.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return f, err
	}
	if f.Received == f.Size {
		f.SHA256 = hex.EncodeToString(h.Sum(nil))
	}
	ok, err := s.store.setReceived(id, n, from, f.Received, saved, f.SHA256, s.opts.Now())
	if err != nil {
		return f, err
	}
	if !ok {
		return f, ErrUploadState
	}
	if rerr != nil {
		return f, rerr
	}
	if f.Received == f.Size {
		// No more than the file lacks: a byte more is an error, the bytes
		// before it are kept.
		var one [1]byte
		if k, _ := body.Read(one[:]); k > 0 {
			return f, fmt.Errorf("%w: the piece goes past the file's %d bytes", ErrBadUpload, f.Size)
		}
	}
	return f, nil
}

// copyPiece copies up to limit bytes of r to out and h, and reports the
// bytes written whole: a read error after some keeps them, a write error
// is returned on its own.
func copyPiece(out io.Writer, h io.Writer, r io.Reader, limit int64) (n int64, rerr, werr error) {
	buf := make([]byte, 1<<20)
	for n < limit {
		want := min(int64(len(buf)), limit-n)
		k, err := io.ReadFull(r, buf[:want])
		if k > 0 {
			if _, werr = out.Write(buf[:k]); werr != nil {
				return n, nil, werr
			}
			h.Write(buf[:k])
			n += int64(k)
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return n, nil, nil
		}
		if err != nil {
			return n, err, nil
		}
	}
	return n, nil, nil
}

// CompleteUpload completes an open upload: the files given in drop are
// left out, every other must have arrived whole (an IncompleteError lists
// those that did not). The files take their places in the background; the
// upload says how it went once done. policy "" keeps the upload's.
func (s *Service) CompleteUpload(project, id, ownerID, policy string, drop []int) (Upload, error) {
	u, err := s.owned(project, id, ownerID)
	if err != nil {
		return Upload{}, err
	}
	if u.State != UploadOpen {
		return Upload{}, ErrUploadState
	}
	if policy == "" {
		policy = u.Policy
	}
	if !ValidPolicy(policy) {
		return Upload{}, fmt.Errorf("%w: policy %q: replace, keep or skip", ErrBadUpload, policy)
	}
	full, err := s.store.upload(id, true)
	if err != nil {
		return Upload{}, err
	}
	dropped := map[int]bool{}
	for _, n := range drop {
		dropped[n] = true
	}
	var missing []int
	for _, f := range full.Files {
		if !dropped[f.N] && !f.Dropped && (f.Received < f.Size || f.SHA256 == "" && f.Size > 0) {
			missing = append(missing, f.N)
		}
	}
	if len(missing) > 0 {
		return Upload{}, &IncompleteError{Files: missing}
	}
	ok, err := s.store.startCompletion(id, policy, drop, s.opts.Now())
	if err != nil {
		return Upload{}, err
	}
	if !ok {
		return Upload{}, ErrUploadState
	}
	go s.finish(id)
	return s.uploadView(id, false)
}

// finish places the files of a completing upload, and closes its batches.
// A restart in the middle runs it again: the files placed are not again,
// those of a zip are found the same.
func (s *Service) finish(id string) {
	s.mu.Lock()
	if s.finishing == nil {
		s.finishing = map[string]bool{}
	}
	if s.finishing[id] {
		s.mu.Unlock()
		return
	}
	s.finishing[id] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.finishing, id)
		s.mu.Unlock()
	}()
	u, err := s.store.upload(id, true)
	if err != nil {
		s.opts.Log.Warn("IN: an upload's completion", "upload", id, "err", err)
		return
	}
	if err := s.placeFiles(&u); err != nil {
		s.opts.Log.Warn("IN: an upload's completion", "upload", id, "project", u.Project, "err", err)
		_ = s.store.endUpload(id, UploadOpen, err.Error(), s.opts.Now())
		s.changed(u.Project)
		return
	}
	now := s.opts.Now()
	var closed []Batch
	unlock := s.lock(u.Project)
	for group, bid := range u.batches {
		b, ok, err := s.store.closeUploadBatch(bid, u.Project, group, now)
		if err != nil {
			s.opts.Log.Warn("IN: an upload's batch", "upload", id, "err", err)
			continue
		}
		if ok {
			closed = append(closed, b)
		}
	}
	unlock()
	if err := s.v.INRemoveStaging(u.Project, u.ID); err != nil {
		s.opts.Log.Warn("IN: an upload's folder", "upload", id, "err", err)
	}
	s.forgetLocks(id)
	if err := s.store.endUpload(id, UploadDone, "", now); err != nil {
		s.opts.Log.Warn("IN: an upload's end", "upload", id, "err", err)
	}
	s.changed(u.Project)
	s.mu.Lock()
	sink, onUpload := s.sink, s.onUpload
	s.mu.Unlock()
	for _, b := range closed {
		if sink == nil {
			break
		}
		list, err := s.batchFiles(b.ID, u.Project)
		if err != nil {
			s.opts.Log.Warn("IN: an upload's batch", "upload", id, "err", err)
			continue
		}
		sink.INBatch(b, list)
	}
	if onUpload != nil {
		if done, err := s.uploadView(id, false); err == nil {
			onUpload(done)
		}
	}
}

// placeBatch is how many files take their places under one hold of the
// project's lock, written to in.db in one transaction.
const placeBatch = 500

// item is a file of a completing upload to place.
type item struct {
	staged string // relative to the upload's folder
	target string // vault path
	size   int64
	sum    string
	mod    time.Time
}

// placeFiles places the files of a completing upload that have no result.
func (s *Service) placeFiles(u *Upload) error {
	var plain []UploadFile
	for _, f := range u.Files {
		switch {
		case f.Result != nil:
		case f.Dropped:
			if err := s.store.recordPlaced([]placedRow{{upload: u.ID, n: f.N, result: &FileResult{Result: ResultDropped}}}, s.opts.Now()); err != nil {
				return err
			}
		case f.Unzip:
			if err := s.flush(u, plain); err != nil {
				return err
			}
			plain = nil
			res := s.placeZip(u, f)
			if err := s.store.recordPlaced([]placedRow{{upload: u.ID, n: f.N, result: &res}}, s.opts.Now()); err != nil {
				return err
			}
		default:
			plain = append(plain, f)
			if len(plain) == placeBatch {
				if err := s.flush(u, plain); err != nil {
					return err
				}
				plain = nil
			}
		}
	}
	return s.flush(u, plain)
}

// flush places plain files of an upload, and records them.
func (s *Service) flush(u *Upload, files []UploadFile) error {
	if len(files) == 0 {
		return nil
	}
	items := make([]item, len(files))
	for i, f := range files {
		items[i] = item{staged: partName(f.N), target: uploadTarget(*u, f.Path), size: f.Size, sum: f.SHA256}
		if f.Modified > 0 {
			items[i].mod = time.UnixMilli(f.Modified)
		}
		if f.Size == 0 {
			items[i].sum = emptySHA256
			// A file of no bytes got no piece: its part is made here.
			if dir, err := s.v.INStaging(u.Project, u.ID, false); err == nil {
				if fh, err := os.OpenFile(filepath.Join(dir, partName(f.N)), os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
					fh.Close()
				}
			}
		}
	}
	rows, err := s.place(u, items)
	if err != nil {
		return err
	}
	for i := range rows {
		rows[i].upload, rows[i].n = u.ID, files[i].N
	}
	return s.store.recordPlaced(rows, s.opts.Now())
}

// emptySHA256 is the SHA-256 of no bytes.
const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// place moves files of an upload's folder to their places, as the upload's
// policy says, under the project's lock: the scan does not see a file
// placed before its row is written. It returns a row for each, with its
// result; a row without a path wrote no file.
func (s *Service) place(u *Upload, items []item) ([]placedRow, error) {
	unlock := s.lock(u.Project)
	defer unlock()
	s.mu.Lock()
	discard := s.discard
	s.mu.Unlock()
	rows := make([]placedRow, len(items))
	for i, it := range items {
		res := &FileResult{}
		rows[i].result = res
		target := it.target
		e, err := s.v.INStat(target)
		replace := false
		switch {
		case errors.Is(err, fs.ErrNotExist):
			res.Result = ResultNew
		case err != nil:
			res.Result, res.Error = ResultError, err.Error()
			continue
		case !e.Dir && s.sameFile(u.Project, e, it.size, it.sum):
			res.Result, res.Path = ResultSame, target
			continue
		case u.Policy == PolicySkip:
			res.Result, res.Path = ResultSkipped, target
			continue
		case u.Policy == PolicyKeep:
			if target, err = s.v.INFreeName(target); err != nil {
				res.Result, res.Error = ResultError, err.Error()
				continue
			}
			res.Result = ResultRenamed
		case e.Dir:
			res.Result, res.Error = ResultError, target+" is a folder: it is not replaced by a file"
			continue
		default:
			res.Result, replace = ResultReplaced, true
			if discard != nil {
				if err := discard(target); err != nil {
					res.Result, res.Error = ResultError, "the file there could not go to the trash: "+err.Error()
					continue
				}
				replace = false
			}
		}
		placed, err := s.v.INPlace(u.Project, u.ID, it.staged, target, replace)
		if err != nil {
			res.Result, res.Error = ResultError, err.Error()
			continue
		}
		if !it.mod.IsZero() {
			abs := filepath.Join(s.v.Root, filepath.FromSlash(placed.Path))
			if os.Chtimes(abs, it.mod, it.mod) == nil {
				if again, err := s.v.INStat(placed.Path); err == nil {
					placed = again
				}
			}
		}
		res.Path = placed.Path
		bid, err := s.batchOf(u, groupOfPath(u.Project, placed.Path))
		if err != nil {
			return nil, err
		}
		rows[i].File = File{Project: u.Project, Path: placed.Path, Dir: dirOf(vault.INRoot(u.Project)+"/", placed.Path),
			Size: placed.Size, Modified: placed.ModTime, SHA256: it.sum, Batch: bid}
	}
	return rows, nil
}

// sameFile reports whether the file e of a project's IN folder holds size
// bytes of SHA-256 sum: its row's hash, or the file's, read when the sizes
// match.
func (s *Service) sameFile(project string, e vault.INEntry, size int64, sum string) bool {
	if e.Size != size || sum == "" {
		return false
	}
	if r, err := s.store.File(project, e.Path); err == nil && r.SHA256 != "" && r.Size == e.Size && r.Modified.Equal(e.ModTime.UTC()) {
		return r.SHA256 == sum
	}
	got, _, err := s.sum(e.Path)
	return err == nil && got == sum
}

// batchOf is the batch of an upload's completion in a group, made, and
// recorded, the first time.
func (s *Service) batchOf(u *Upload, group string) (string, error) {
	if u.batches == nil {
		u.batches = map[string]string{}
	}
	if id, ok := u.batches[group]; ok {
		return id, nil
	}
	id := newID()
	u.batches[group] = id
	if err := s.store.setUploadBatches(u.ID, u.batches); err != nil {
		delete(u.batches, group)
		return "", err
	}
	return id, nil
}

// placeZip unpacks a zip of an upload in its folder, then places its files
// as the upload's policy says, in the folder zipFolder names.
func (s *Service) placeZip(u *Upload, f UploadFile) FileResult {
	dir, err := s.v.INStaging(u.Project, u.ID, false)
	if err != nil {
		return FileResult{Result: ResultError, Error: err.Error()}
	}
	l := s.uploadLimits()
	caps := zipCaps{MaxEntries: l.ZipMaxEntries, MaxBytes: l.ZipMaxBytes, MaxRatio: l.ZipMaxRatio,
		Extensions: l.Channels[u.Channel].Extensions, Room: func(need int64) error { return s.room(u.Project, need) }}
	outRel := fmt.Sprintf("%d.x", f.N)
	entries, ignored, refused, err := unpack(filepath.Join(dir, partName(f.N)), filepath.Join(dir, outRel), outRel, caps)
	res := FileResult{Result: ResultUnzipped, Ignored: ignored, Refused: refused, Counts: map[string]int{}}
	if err != nil {
		_ = os.RemoveAll(filepath.Join(dir, outRel))
		res.Result, res.Error = ResultError, err.Error()
		return res
	}
	folder, strip := zipFolder(path.Dir(uploadTarget(*u, f.Path)), path.Base(f.Path), entries)
	res.Path = folder
	for start := 0; start < len(entries); start += placeBatch {
		part := entries[start:min(start+placeBatch, len(entries))]
		items := make([]item, len(part))
		for i, e := range part {
			items[i] = item{staged: e.Staged, target: folder + "/" + strings.TrimPrefix(e.Rel, strip), size: e.Size, sum: e.SHA256, mod: e.Modified}
		}
		rows, err := s.place(u, items)
		if err != nil {
			res.Result, res.Error = ResultError, err.Error()
			return res
		}
		for i := range rows {
			r := rows[i].result
			res.Counts[r.Result]++
			if r.Result == ResultError {
				res.Refused = appendRefused(res.Refused, part[i].Rel, r.Error)
			}
			rows[i].n = -1
		}
		if err := s.store.recordPlaced(rows, s.opts.Now()); err != nil {
			res.Result, res.Error = ResultError, err.Error()
			return res
		}
	}
	_ = os.RemoveAll(filepath.Join(dir, outRel))
	_ = os.Remove(filepath.Join(dir, partName(f.N)))
	return res
}

// resumeUploads runs again the completions a restart cut short.
func (s *Service) resumeUploads() {
	list, err := s.store.uploadsWhere(`state = ?`, UploadCompleting)
	if err != nil {
		s.opts.Log.Warn("IN: the uploads completing", "err", err)
		return
	}
	for _, u := range list {
		go s.finish(u.ID)
	}
}

// gcUploads drops the uploads whose last piece is older than the TTL, with
// their folders, forgets those done as long ago, and removes the upload
// folders of the IN folders that no upload holds, an hour old.
func (s *Service) gcUploads() {
	l := s.uploadLimits()
	now := s.opts.Now()
	cutoff := nanos(now.Add(-l.TTL))
	list, err := s.store.uploadsWhere(`state != ? AND updated < ?`, UploadCompleting, cutoff)
	if err != nil {
		s.opts.Log.Warn("IN: the old uploads", "err", err)
		return
	}
	for _, u := range list {
		if err := s.dropUpload(u); err != nil {
			s.opts.Log.Warn("IN: an old upload", "upload", u.ID, "err", err)
		}
	}
	for _, p := range s.projects() {
		ids, err := s.v.INStagingIDs(p)
		if err != nil {
			continue
		}
		for _, id := range ids {
			if _, err := s.store.upload(id, false); !errors.Is(err, ErrUploadNotFound) {
				continue
			}
			dir, err := s.v.INStaging(p, id, false)
			if err != nil {
				continue
			}
			if st, err := os.Stat(dir); err == nil && time.Since(st.ModTime()) > time.Hour {
				_ = s.v.INRemoveStaging(p, id)
			}
		}
	}
}
