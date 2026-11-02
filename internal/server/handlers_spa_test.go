package server

import (
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/server/web"
)

// TestHandleSPA_NonceInjected locks the BUG-019 contract: the shell ships a
// per-request CSP nonce both in the script-src header and in a <meta> the SPA
// reads, the two values match, and the nonce rotates per request.
func TestHandleSPA_NonceInjected(t *testing.T) {
	s := newTestServer(t)

	get := func() (csp, body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		s.handleSPA(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		return rec.Header().Get("Content-Security-Policy"), rec.Body.String()
	}

	csp, body := get()

	const marker = "script-src 'self' 'nonce-"
	i := strings.Index(csp, marker)
	if i < 0 {
		t.Fatalf("CSP missing nonce'd script-src: %q", csp)
	}
	rest := csp[i+len(marker):]
	end := strings.IndexByte(rest, '\'')
	if end <= 0 {
		t.Fatalf("malformed nonce in CSP: %q", csp)
	}
	nonce := rest[:end]

	// The same nonce must reach the SPA via <meta> so it can stamp note scripts.
	wantMeta := `<meta name="csp-nonce" content="` + nonce + `">`
	if !strings.Contains(body, wantMeta) {
		t.Errorf("meta nonce missing/mismatched; want %q in shell body", wantMeta)
	}

	// Per-request rotation: a static nonce would defeat the purpose.
	if csp2, _ := get(); csp2 == csp {
		t.Errorf("nonce did not rotate across requests: %q", csp2)
	}
}

// TestHandleSPA_WellKnownNotFound: discovery paths never get the HTML shell.
// With OAuth off the oauth-* documents are not mounted and must 404, not
// 200 text/html; SPA routes keep the shell.
func TestHandleSPA_WellKnownNotFound(t *testing.T) {
	s := newTestServer(t)
	cases := []struct {
		path string
		want int
	}{
		{"/.well-known/oauth-authorization-server", http.StatusNotFound},
		{"/.well-known/oauth-protected-resource", http.StatusNotFound},
		{"/.well-known/oauth-protected-resource/mcp", http.StatusNotFound},
		{"/.well-known/security.txt", http.StatusNotFound},
		{"/oauth/consent", http.StatusOK},
		{"/notes/foo", http.StatusOK},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s: status=%d, want %d", c.path, rec.Code, c.want)
		}
		if c.want == http.StatusNotFound && strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s: got HTML shell on a discovery path", c.path)
		}
	}
}

// firstAsset is the name under dist/ of the first embedded file with ext.
func firstAsset(t *testing.T, ext string) string {
	t.Helper()
	var found string
	fs.WalkDir(web.DistFS, "dist", func(p string, d fs.DirEntry, err error) error {
		if err == nil && found == "" && !d.IsDir() && path.Ext(p) == ext {
			found = strings.TrimPrefix(p, "dist/")
		}
		return nil
	})
	if found == "" {
		t.Skipf("no embedded %s file", ext)
	}
	return found
}

// TestHandleSpaStatic_ServesTheEmbeddedFile: an asset comes back whole,
// with its type and the immutable cache of a fingerprinted name; HEAD has
// no body; a name not in the build is a 404.
func TestHandleSpaStatic_ServesTheEmbeddedFile(t *testing.T) {
	s := newTestServer(t)
	name := firstAsset(t, ".js")
	want, err := fs.ReadFile(web.DistFS, "dist/"+name)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/dist/"+name, nil))
	if rec.Code != http.StatusOK || rec.Body.String() != string(want) {
		t.Fatalf("GET: status %d, %d bytes, want 200 and %d", rec.Code, rec.Body.Len(), len(want))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", cc)
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/static/dist/"+name, nil))
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("HEAD: status %d, %d bytes of body", rec.Code, rec.Body.Len())
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/dist/assets/missing-0000.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("a missing asset: status %d, want 404", rec.Code)
	}
}

// TestHandleSpaStatic_GzipToWhoTakesIt: a text asset goes gzipped to a
// client that accepts it, and whole to one that does not or refuses it
// with q=0; both are the same file, with the same type, and say Vary.
func TestHandleSpaStatic_GzipToWhoTakesIt(t *testing.T) {
	s := newTestServer(t)
	for _, ext := range []string{".js", ".map"} {
		name := firstAsset(t, ext)
		want, _ := fs.ReadFile(web.DistFS, "dist/"+name)
		get := func(method, accept string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, "/static/dist/"+name, nil)
			if accept != "" {
				req.Header.Set("Accept-Encoding", accept)
			}
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s %s %q: status %d", method, name, accept, rec.Code)
			}
			if v := rec.Header().Get("Vary"); v != "Accept-Encoding" {
				t.Errorf("%s %q: Vary = %q", name, accept, v)
			}
			return rec
		}
		plain := get(http.MethodGet, "")
		gz := get(http.MethodGet, "br, gzip, deflate")
		if plain.Header().Get("Content-Encoding") != "" || plain.Body.String() != string(want) {
			t.Errorf("%s without Accept-Encoding: encoded %q, %d bytes", name, plain.Header().Get("Content-Encoding"), plain.Body.Len())
		}
		if gz.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("%s: Content-Encoding %q, want gzip", name, gz.Header().Get("Content-Encoding"))
		}
		if gz.Body.Len() >= len(want) || gz.Header().Get("Content-Length") != fmt.Sprint(gz.Body.Len()) {
			t.Errorf("%s: %d gzipped bytes of %d, Content-Length %q", name, gz.Body.Len(), len(want), gz.Header().Get("Content-Length"))
		}
		zr, err := gzip.NewReader(gz.Body)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := io.ReadAll(zr); string(got) != string(want) {
			t.Errorf("%s: the gzipped body is not the file", name)
		}
		if a, b := plain.Header().Get("Content-Type"), gz.Header().Get("Content-Type"); a != b || a == "" {
			t.Errorf("%s: Content-Type %q plain, %q gzipped", name, a, b)
		}
		if r := get(http.MethodGet, "gzip;q=0"); r.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s: gzipped for gzip;q=0", name)
		}
		if r := get(http.MethodHead, "gzip"); r.Body.Len() != 0 || r.Header().Get("Content-Encoding") != "gzip" {
			t.Errorf("%s HEAD: %d bytes, encoding %q", name, r.Body.Len(), r.Header().Get("Content-Encoding"))
		}
	}
}

func TestAcceptsGzip(t *testing.T) {
	for accept, want := range map[string]bool{
		"": false, "gzip": true, "GZIP": true, "br, gzip;q=0.8": true, "gzip;q=0": false, "gzip; q=0.0": false,
		"*": true, "*;q=0": false, "deflate, br": false, "identity": false, "x-gzip": false,
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", accept)
		if got := acceptsGzip(req); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", accept, got, want)
		}
	}
}
