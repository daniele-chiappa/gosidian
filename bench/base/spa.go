package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

// The browser's visits to the first page: spaVisits in a row, each in a
// new context, so with an empty cache. The first also warms the browser
// up and is left out.
const spaVisits = 7

// spaScript runs in Playwright's image, with its client library from
// web/node_modules, and prints the visits' times as JSON.
const spaScript = `
const { chromium } = require('playwright-core');
(async () => {
  const browser = await chromium.launch();
  const out = [];
  for (let i = 0; i < +process.env.VISITS; i++) {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await page.goto(process.env.URL, { waitUntil: 'networkidle' });
    out.push(await page.evaluate(() => {
      const nav = performance.getEntriesByType('navigation')[0];
      const fcp = performance.getEntriesByName('first-contentful-paint')[0];
      return { fcp: fcp ? fcp.startTime : 0, load: nav.loadEventEnd };
    }));
    await ctx.close();
  }
  console.log(JSON.stringify(out));
  await browser.close();
})().catch(e => { console.error(String(e)); process.exit(1); });
`

var playwrightDep = regexp.MustCompile(`"@playwright/test":\s*"[\^~]?([0-9][0-9.]*)"`)

// browserFor returns the browser's image and the absolute web directory
// its client library is in, or why the load cannot be timed here: the
// library is not installed, or the image is not on this host (the bench
// pulls nothing).
func browserFor(webDir, image string) (string, string, error) {
	abs, err := filepath.Abs(webDir)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(filepath.Join(abs, "node_modules", "playwright-core", "package.json")); err != nil {
		return "", "", fmt.Errorf("no playwright-core in %s/node_modules", webDir)
	}
	if image == "" {
		pkg, err := os.ReadFile(filepath.Join(abs, "package.json"))
		if err != nil {
			return "", "", err
		}
		m := playwrightDep.FindSubmatch(pkg)
		if m == nil {
			return "", "", fmt.Errorf("no @playwright/test in %s/package.json", webDir)
		}
		image = "mcr.microsoft.com/playwright:v" + string(m[1]) + "-noble"
	}
	if _, err := docker("image", "inspect", "--format", "{{.Id}}", image); err != nil {
		return "", "", fmt.Errorf("the image %s is not on this host", image)
	}
	return image, abs, nil
}

// spaLoad times the web UI's first load in a headless browser inside the
// server's network, with the server's limits: the medians, in
// milliseconds, of the first paint with content and of the load event.
func spaLoad(s *server, image, webDir string) (fcp, load float64, err error) {
	out, err := exec.Command("docker", "run", "--rm", "--cpus="+serverCPUs, "--memory="+serverMemory, "--ipc=host",
		"--network", "container:"+s.name, "-e", "URL=http://127.0.0.1:8080/", "-e", fmt.Sprint("VISITS=", spaVisits),
		"-e", "NODE_PATH=/web/node_modules", "-v", filepath.Join(webDir, "node_modules")+":/web/node_modules:ro",
		image, "node", "-e", spaScript).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return 0, 0, fmt.Errorf("%w: %.300s", err, ee.Stderr)
		}
		return 0, 0, err
	}
	return parseSPALoad(out)
}

// parseSPALoad takes the visits the script printed, leaves the first out
// and returns the medians of the rest.
func parseSPALoad(out []byte) (fcp, load float64, err error) {
	var visits []struct {
		FCP  float64 `json:"fcp"`
		Load float64 `json:"load"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &visits); err != nil {
		return 0, 0, fmt.Errorf("the browser printed %.200q: %w", out, err)
	}
	if len(visits) < 2 {
		return 0, 0, fmt.Errorf("%d visits, want at least 2", len(visits))
	}
	var fcps, loads []float64
	for _, v := range visits[1:] {
		if v.FCP <= 0 || v.Load <= 0 {
			return 0, 0, fmt.Errorf("a visit without its paint or load time: %+v", v)
		}
		fcps, loads = append(fcps, v.FCP), append(loads, v.Load)
	}
	return round(median(fcps), 1), round(median(loads), 1), nil
}
