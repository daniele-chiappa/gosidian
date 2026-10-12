package infolder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// remoteFile reads a file of a source as an io.ReadSeekCloser: a GET of
// the URL its source gives, from the offset reached (Range), started at
// the first read after a seek. Seeking costs nothing until then, so
// http.ServeContent learns the size and serves ranges without a request
// it does not need. A URL that expired is asked again, once per read. A
// file whose size the source's list did not know (-1: a native Google
// document) takes the one its URL comes with, asked for at the first
// seek to the end or read.
type remoteFile struct {
	ctx    context.Context
	client *http.Client
	open   func(context.Context) (RemoteURL, error)
	name   string
	size   int64

	url  string
	off  int64
	body io.ReadCloser
}

func newRemoteFile(ctx context.Context, client *http.Client, open func(context.Context) (RemoteURL, error), name string, size int64) *remoteFile {
	if ctx == nil {
		ctx = context.Background()
	}
	return &remoteFile{ctx: ctx, client: client, open: open, name: name, size: size}
}

// errChanged is returned when the source holds another file than in.db.
var errChanged = errors.New("the file changed in its source since it was last checked: try again after the next check")

// sized learns the size of a file the list did not know, from its URL.
func (r *remoteFile) sized() error {
	if r.size >= 0 {
		return nil
	}
	u, err := r.open(r.ctx)
	if err != nil {
		return err
	}
	if u.Size < 0 {
		return fmt.Errorf("%s: its source gives no size", r.name)
	}
	r.url, r.size = u.URL, u.Size
	return nil
}

func (r *remoteFile) Read(p []byte) (int, error) {
	if err := r.sized(); err != nil {
		return 0, err
	}
	if r.off >= r.size {
		return 0, io.EOF
	}
	if r.body == nil {
		if err := r.start(); err != nil {
			return 0, err
		}
	}
	n, err := r.body.Read(p)
	r.off += int64(n)
	if errors.Is(err, io.EOF) && r.off < r.size {
		err = fmt.Errorf("%s: %w", r.name, io.ErrUnexpectedEOF)
	}
	return n, err
}

// start sends the GET from the offset, asking the URL again when the one
// it has answers as expired.
func (r *remoteFile) start() error {
	fresh := false
	for {
		if r.url == "" {
			u, err := r.open(r.ctx)
			if err != nil {
				return err
			}
			if u.Size != 0 && u.Size != r.size {
				return fmt.Errorf("%s: %w", r.name, errChanged)
			}
			r.url, fresh = u.URL, true
		}
		resp, err := r.get()
		if err != nil {
			return err
		}
		switch resp.StatusCode {
		case http.StatusOK, http.StatusPartialContent:
			if err := r.check(resp); err != nil {
				resp.Body.Close()
				return err
			}
			r.body = &limitedBody{r: io.LimitReader(resp.Body, r.size-r.off), c: resp.Body}
			return nil
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
			resp.Body.Close()
			if !fresh {
				r.url = "" // expired: ask the source for another
				continue
			}
		default:
			resp.Body.Close()
		}
		return fmt.Errorf("%s: the source answered %s", r.name, resp.Status)
	}
}

func (r *remoteFile) get() (*http.Response, error) {
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.name, err)
	}
	if r.off > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(r.off, 10)+"-")
	}
	// The bytes as they are: a size is a size.
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: reading it from its source: %w", r.name, err)
	}
	return resp, nil
}

// check matches an answer with the file: a range from the offset, of a
// file of its size. A source that ignores the range gives the whole file,
// and the bytes before the offset are skipped.
func (r *remoteFile) check(resp *http.Response) error {
	if resp.StatusCode == http.StatusPartialContent {
		start, total, ok := contentRange(resp.Header.Get("Content-Range"))
		if !ok || start != r.off {
			return fmt.Errorf("%s: the source answered another range (%q)", r.name, resp.Header.Get("Content-Range"))
		}
		if total >= 0 && total != r.size {
			return fmt.Errorf("%s: %w", r.name, errChanged)
		}
		return nil
	}
	if resp.ContentLength >= 0 && resp.ContentLength != r.size {
		return fmt.Errorf("%s: %w", r.name, errChanged)
	}
	if r.off > 0 {
		if _, err := io.CopyN(io.Discard, resp.Body, r.off); err != nil {
			return fmt.Errorf("%s: %w", r.name, err)
		}
	}
	return nil
}

// contentRange reads "bytes <start>-<end>/<total>"; total is -1 for "*".
func contentRange(v string) (start, total int64, ok bool) {
	v, found := strings.CutPrefix(strings.TrimSpace(v), "bytes ")
	if !found {
		return 0, 0, false
	}
	span, size, found := strings.Cut(v, "/")
	if !found {
		return 0, 0, false
	}
	first, _, found := strings.Cut(span, "-")
	if !found {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(first, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	if size == "*" {
		return start, -1, true
	}
	total, err = strconv.ParseInt(size, 10, 64)
	return start, total, err == nil
}

func (r *remoteFile) Seek(offset int64, whence int) (int64, error) {
	var to int64
	switch whence {
	case io.SeekStart:
		to = offset
	case io.SeekCurrent:
		to = r.off + offset
	case io.SeekEnd:
		if err := r.sized(); err != nil {
			return 0, err
		}
		to = r.size + offset
	default:
		return 0, errors.New("seek: bad whence")
	}
	if to < 0 {
		return 0, errors.New("seek: before the start")
	}
	if to != r.off {
		r.drop()
		r.off = to
	}
	return to, nil
}

func (r *remoteFile) drop() {
	if r.body != nil {
		r.body.Close()
		r.body = nil
	}
}

func (r *remoteFile) Close() error {
	r.drop()
	return nil
}

// limitedBody reads a response's body up to the bytes the file still has.
type limitedBody struct {
	r io.Reader
	c io.Closer
}

func (b *limitedBody) Read(p []byte) (int, error) { return b.r.Read(p) }
func (b *limitedBody) Close() error               { return b.c.Close() }
