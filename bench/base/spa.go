package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"regexp"
	"time"
)

// assetRef matches the files the web UI's first page loads: its script,
// stylesheets and module preloads. The chunks of later screens are not on
// it, as they are not in a first load.
var assetRef = regexp.MustCompile(`(?:src|href)="(/static/[^"]+\.(?:js|css|svg))"`)

// measureSPA fetches the first page of the web UI and what it references,
// as a browser's first visit does, and counts their bytes as sent and
// gzipped.
func measureSPA(base string, fail func(string, ...any)) *spaSize {
	c := &http.Client{Timeout: 30 * time.Second}
	page, wire, served, err := fetch(c, base+"/")
	if err != nil {
		fail("spa: %v", err)
		return nil
	}
	out := &spaSize{Files: 1, Served: served}
	add := func(b []byte, wire int64) {
		out.Bytes += wire
		out.GzipBytes += gzipLen(b)
	}
	add(page, wire)
	seen := map[string]bool{}
	for _, m := range assetRef.FindAllSubmatch(page, -1) {
		path := string(m[1])
		if seen[path] {
			continue
		}
		seen[path] = true
		b, wire, gz, err := fetch(c, base+path)
		if err != nil {
			fail("spa %s: %v", path, err)
			continue
		}
		out.Files++
		out.Served = out.Served || gz
		add(b, wire)
	}
	return out
}

// fetch returns the body, decompressed, the bytes that came for it, and
// whether the server gzipped it. The Accept-Encoding header is set by
// hand, so the client leaves the body as it came. (Until 2026-10-29 the
// bytes counted were the decompressed ones; the server sent nothing
// gzipped then, so the results before read the same.)
func fetch(c *http.Client, url string) ([]byte, int64, bool, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, false, err
	}
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, false, err
	}
	defer resp.Body.Close()
	body := &counter{r: resp.Body}
	var r io.Reader = body
	gz := resp.Header.Get("Content-Encoding") == "gzip"
	if gz {
		zr, err := gzip.NewReader(body)
		if err != nil {
			return nil, body.n, true, err
		}
		r = zr
	}
	b, err := io.ReadAll(r)
	io.Copy(io.Discard, body)
	return b, body.n, gz, err
}

// counter counts the bytes read through it.
type counter struct {
	r io.Reader
	n int64
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func gzipLen(b []byte) int64 {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(b)
	w.Close()
	return int64(buf.Len())
}
