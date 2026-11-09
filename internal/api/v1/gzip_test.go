package v1

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serveGzip runs h behind gzipJSON for a GET with the Accept-Encoding given.
func serveGzip(h http.HandlerFunc, accept string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	if accept != "" {
		req.Header.Set("Accept-Encoding", accept)
	}
	w := httptest.NewRecorder()
	jsonHeaders(gzipJSON(h)).ServeHTTP(w, req)
	return w
}

func jsonOf(n int) string { return `{"text":"` + strings.Repeat("abc ", n/4) + `"}` }

// A large JSON answer goes compressed to a browser that takes gzip
// (IMP-225): the tree was 287 KB, 26 in gzip.
func TestGzipJSON_CompressesALargeAnswer(t *testing.T) {
	body := strings.Repeat("abc ", gzipMinBytes)
	w := serveGzip(func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"text": body})
	}, "gzip, deflate, br")
	if w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(w.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("code %d, headers %v", w.Code, w.Header())
	}
	if w.Header().Get("Content-Length") != "" {
		t.Errorf("a Content-Length of the plain body: %q", w.Header().Get("Content-Length"))
	}
	sent := w.Body.Len()
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)
	if !bytes.Contains(plain, []byte(body[:40])) || sent >= len(plain)/4 {
		t.Errorf("plain %d bytes, sent %d", len(plain), sent)
	}
}

func TestGzipJSON_LeavesTheRestAsItIs(t *testing.T) {
	large := func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"text": jsonOf(gzipMinBytes * 4)})
	}
	cases := map[string]*httptest.ResponseRecorder{
		"a small answer": serveGzip(func(w http.ResponseWriter, r *http.Request) {
			WriteJSON(w, http.StatusOK, map[string]string{"ok": "yes"})
		}, "gzip"),
		"a browser without gzip": serveGzip(large, ""),
		"gzip refused":           serveGzip(large, "gzip;q=0, identity"),
		"a file": serveGzip(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(bytes.Repeat([]byte{1}, gzipMinBytes*4))
		}, "gzip"),
		"a 304": serveGzip(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotModified)
		}, "gzip"),
	}
	for name, w := range cases {
		if enc := w.Header().Get("Content-Encoding"); enc != "" {
			t.Errorf("%s: Content-Encoding %q", name, enc)
		}
	}
	if b := cases["a small answer"].Body.String(); b != "{\"ok\":\"yes\"}\n" {
		t.Errorf("small answer: %q", b)
	}
	if n := cases["a file"].Body.Len(); n != gzipMinBytes*4 {
		t.Errorf("file: %d bytes", n)
	}
	if c := cases["a 304"].Code; c != http.StatusNotModified || cases["a 304"].Body.Len() != 0 {
		t.Errorf("304: %d, %d bytes", c, cases["a 304"].Body.Len())
	}
}

// The events stream goes out as it is written: a flush reaches the client.
func TestGzipJSON_LetsTheEventsFlow(t *testing.T) {
	w := serveGzip(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: one\n\n"))
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no Flusher")
		}
		f.Flush()
	}, "gzip")
	if !w.Flushed || w.Header().Get("Content-Encoding") != "" || w.Body.String() != "data: one\n\n" {
		t.Errorf("flushed %v, encoding %q, body %q", w.Flushed, w.Header().Get("Content-Encoding"), w.Body.String())
	}
}

// Through the router: the tree of a vault with many notes goes in gzip.
func TestGzipJSON_TheTree(t *testing.T) {
	f := newNotesFixture(t)
	for i := range 200 {
		f.seedNote(t, "alpha/notes/a-rather-long-note-name-"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+".md", "# n")
		if i%26 == 25 {
			f.seedNote(t, "alpha/more/"+strings.Repeat("y", i%11)+"-note.md", "# m")
		}
	}
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/tree", "", map[string]string{"Accept-Encoding": "gzip"})
	if w.code != http.StatusOK || w.headers.Get("Content-Encoding") != "gzip" {
		t.Errorf("tree: %d, encoding %q, %d bytes", w.code, w.headers.Get("Content-Encoding"), len(w.body))
	}
}
