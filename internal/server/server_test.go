package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/config"
	"github.com/gosidian/gosidian/internal/gitsync"
	"github.com/gosidian/gosidian/internal/i18n"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/vault"
)

func setupServer(t *testing.T) (*Server, string) {
	return setupServerWithConfig(t, "")
}

func setupServerWithConfig(t *testing.T, configPath string) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.md"),
		[]byte("# Hello\n\nLink to [[Other]] and #demo tag.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.md"),
		[]byte("# Other\n\nBody text.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })

	v := vault.New(dir)
	if err := v.ScanInto(idx); err != nil {
		t.Fatal(err)
	}
	if err := idx.ResolveAll(); err != nil {
		t.Fatal(err)
	}
	srv := New(v, idx, nil, configPath, nil)
	if cat, err := i18n.Load("it"); err == nil {
		srv.SetI18n(cat, "it")
	}
	return srv, dir
}

func doReq(t *testing.T, s *Server, method, path string, body string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if htmx {
		r.Header.Set("HX-Request", "true")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestServer_Health(t *testing.T) {
	s, _ := setupServer(t)
	s.SetBuildInfo("0.1.0", true)
	w := doReq(t, s, "GET", "/healthz", "", false)
	if w.Code != 200 {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("missing status ok: %s", body)
	}
	if !strings.Contains(body, `"version":"0.1.0"`) {
		t.Errorf("missing version: %s", body)
	}
	// git_sync is a structured object: {enabled, healthy, ...}.
	if !strings.Contains(body, `"git_sync":{`) {
		t.Errorf("expected structured git_sync object: %s", body)
	}
	if !strings.Contains(body, `"enabled":true`) {
		t.Errorf("expected git_sync.enabled=true: %s", body)
	}
	if !strings.Contains(body, `"healthy":true`) {
		t.Errorf("expected git_sync.healthy=true: %s", body)
	}
	// We seeded two notes in setupServer
	if !strings.Contains(body, `"notes":2`) {
		t.Errorf("notes count wrong: %s", body)
	}
}

// TestServer_Health_GitSyncDegraded wires a Sync whose init was forced to fail
// (by unsetting PATH so `git` is unresolvable), then verifies /healthz reports
// the degraded state without taking down the whole endpoint (IMP-002).
func TestServer_Health_GitSyncDegraded(t *testing.T) {
	s, _ := setupServer(t)
	s.SetBuildInfo("0.1.0", true)

	// Build a syncer that will fail its ensureRepo step.
	vaultDir := t.TempDir()
	t.Setenv("PATH", "")
	syncer := gitsync.New(vaultDir, config.GitConfig{
		Enabled:     true,
		Branch:      "main",
		AuthorName:  "Test Bot",
		AuthorEmail: "bot@example.com",
		Debounce:    50 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := syncer.Start(ctx); err == nil {
		t.Fatal("expected Start to fail under empty PATH")
	}
	s.SetGitSync(syncer)

	w := doReq(t, s, "GET", "/healthz", "", false)
	// Still 200 OK — readiness does not fail on gitsync degradation, but the
	// payload surfaces status=degraded so operators can alert.
	if w.Code != 200 {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"status":"degraded"`) {
		t.Errorf("expected status=degraded, got: %s", body)
	}
	if !strings.Contains(body, `"healthy":false`) {
		t.Errorf("expected git_sync.healthy=false: %s", body)
	}
	if !strings.Contains(body, `"last_error":`) {
		t.Errorf("expected last_error populated: %s", body)
	}
}

func TestServer_Index(t *testing.T) {
	s, _ := setupServer(t)
	w := doReq(t, s, "GET", "/", "", false)
	if w.Code != 200 {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Vault") {
		t.Errorf("missing 'Vault' in body")
	}
	if !strings.Contains(body, "hello.md") {
		t.Errorf("missing hello.md link")
	}
}

func TestServer_NoteView(t *testing.T) {
	s, _ := setupServer(t)
	w := doReq(t, s, "GET", "/notes/hello.md", "", false)
	if w.Code != 200 {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `href="/notes/other.md"`) {
		t.Errorf("wiki-link not resolved to other.md: %s", body)
	}
	if !strings.Contains(body, `class="wikilink"`) {
		t.Errorf("wikilink class missing")
	}
	if !strings.Contains(body, `href="/tags/demo"`) {
		t.Errorf("tag link missing")
	}
}

func TestServer_Backlinks(t *testing.T) {
	s, _ := setupServer(t)
	w := doReq(t, s, "GET", "/notes/other.md", "", false)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "hello.md") {
		t.Errorf("backlinks should include hello.md: %s", w.Body.String())
	}
}

func TestServer_EditAndSave(t *testing.T) {
	s, _ := setupServer(t)
	// edit form
	w := doReq(t, s, "GET", "/notes/hello.md/edit", "", true)
	if w.Code != 200 {
		t.Fatalf("edit status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "<textarea") {
		t.Errorf("edit form missing textarea")
	}

	// save
	body := "content=# Hello 2\n\n[[Other]] updated\n"
	w = doReq(t, s, "POST", "/notes/hello.md", body, true)
	if w.Code != 200 {
		t.Fatalf("save status = %d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Hello 2") {
		t.Errorf("post-save body should include new title: %s", w.Body.String())
	}
}

func TestServer_Search(t *testing.T) {
	s, _ := setupServer(t)
	w := doReq(t, s, "GET", "/search?q=Hello", "", false)
	if w.Code != 200 {
		t.Fatalf("search status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "hello.md") {
		t.Errorf("search should find hello.md: %s", w.Body.String())
	}
}

func TestServer_Tree(t *testing.T) {
	s, _ := setupServer(t)
	w := doReq(t, s, "GET", "/api/tree", "", true)
	if w.Code != 200 {
		t.Fatalf("tree status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/notes/hello.md") || !strings.Contains(body, "/notes/other.md") {
		t.Errorf("tree missing entries: %s", body)
	}
}

func TestServer_Tags(t *testing.T) {
	s, _ := setupServer(t)
	w := doReq(t, s, "GET", "/tags", "", false)
	if w.Code != 200 {
		t.Fatalf("tags status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "demo") {
		t.Errorf("tags page missing 'demo': %s", w.Body.String())
	}

	w = doReq(t, s, "GET", "/tags/demo", "", false)
	if w.Code != 200 {
		t.Fatalf("tag notes status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "hello.md") {
		t.Errorf("tag page missing note: %s", w.Body.String())
	}
}

func TestServer_GraphJSON(t *testing.T) {
	s, _ := setupServer(t)
	w := doReq(t, s, "GET", "/api/graph", "", false)
	if w.Code != 200 {
		t.Fatalf("graph status = %d", w.Code)
	}

	// IMP-001 / BUG-002: parse the payload and assert the Cytoscape
	// element shape instead of substring-matching — a rename of `id` or
	// `target` would then actually fail the test.
	var payload struct {
		Elements []struct {
			Data map[string]any `json:"data"`
		} `json:"elements"`
		Projects  []string `json:"projects"`
		Selected  string   `json:"selected"`
		MaxDegree int      `json:"maxDegree"`
		MaxCount  int      `json:"maxCount"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("parse graph json: %v body=%s", err, w.Body.String())
	}
	if len(payload.Elements) == 0 {
		t.Fatalf("expected >=1 element, got %+v", payload)
	}
	var foundHello, foundEdge bool
	for _, e := range payload.Elements {
		if id, _ := e.Data["id"].(string); id == "hello.md" {
			if _, hasDegree := e.Data["degree"]; !hasDegree {
				t.Errorf("node missing `degree` field: %+v", e.Data)
			}
			if _, hasLabel := e.Data["label"]; !hasLabel {
				t.Errorf("node missing `label` field: %+v", e.Data)
			}
			foundHello = true
		}
		if src, _ := e.Data["source"].(string); src == "hello.md" {
			tgt, _ := e.Data["target"].(string)
			if tgt != "other.md" {
				t.Errorf("unexpected edge target: %+v", e.Data)
			}
			if _, hasCount := e.Data["count"]; !hasCount {
				t.Errorf("edge missing `count` field: %+v", e.Data)
			}
			if id, _ := e.Data["id"].(string); id == "" {
				t.Errorf("edge id should be non-empty: %+v", e.Data)
			}
			foundEdge = true
		}
	}
	if !foundHello {
		t.Errorf("hello.md node not present in %d elements", len(payload.Elements))
	}
	if !foundEdge {
		t.Errorf("hello→other edge not present in %d elements", len(payload.Elements))
	}
	if payload.MaxDegree < 1 {
		t.Errorf("maxDegree should be >=1, got %d", payload.MaxDegree)
	}
}

func TestServer_SettingsGetAndSave(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	s, _ := setupServerWithConfig(t, cfgPath)

	// GET with empty file → form shows defaults
	w := doReq(t, s, "GET", "/settings", "", false)
	if w.Code != 200 {
		t.Fatalf("GET /settings = %d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Git sync") {
		t.Errorf("settings form missing 'Git sync': %s", w.Body.String())
	}

	// POST saving enables git sync with a remote
	form := "git_enabled=on&git_remote=" + url.QueryEscape("https://ex.invalid/v.git") +
		"&git_branch=main&git_author_name=bot&git_author_email=bot@ex" +
		"&git_debounce=45s&git_push=on&git_token_env=TOK"
	w = doReq(t, s, "POST", "/settings", form, false)
	if w.Code != 200 {
		t.Fatalf("POST /settings = %d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "salvate") {
		t.Errorf("missing success message: %s", w.Body.String())
	}

	// config file on disk round-trips
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "https://ex.invalid/v.git") {
		t.Errorf("config not saved: %s", data)
	}
}

func TestServer_SettingsValidationError(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	s, _ := setupServerWithConfig(t, cfgPath)

	// push enabled without remote → error. The _form_version marker is
	// required for checkbox fields (git_enabled, git_push) to be applied:
	// without it the handler treats the POST as partial and preserves the
	// current config values (IMP-027 semantics).
	form := "_form_version=settings_full&git_enabled=on&git_push=on&git_remote=&git_token_env=&git_debounce=30s"
	w := doReq(t, s, "POST", "/settings", form, false)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "remote vuoto") {
		t.Errorf("expected validation error, got: %s", w.Body.String())
	}
}

// TestServer_SettingsPartialPOSTPreservesCheckboxes is the positive
// counterpart of TestServer_SettingsValidationError: a partial POST without
// the _form_version marker must NOT reset the checkbox fields (IMP-027).
func TestServer_SettingsPartialPOSTPreservesCheckboxes(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")

	// Seed a config with git enabled + push on so we can detect a reset.
	seed := config.Default()
	seed.Git.Enabled = true
	seed.Git.Push = true
	seed.Git.Remote = "https://ex.invalid/seed.git"
	seed.Git.TokenEnv = "GIT_TOKEN_SEED"
	if err := config.Save(cfgPath, seed); err != nil {
		t.Fatal(err)
	}

	s, _ := setupServerWithConfig(t, cfgPath)

	// Partial POST — only changes the theme preset. The git checkboxes are
	// not on the wire; the handler must leave them alone.
	form := "theme_preset=light-clean"
	w := doReq(t, s, "POST", "/settings", form, false)
	if w.Code != 200 {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}

	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Git.Enabled {
		t.Errorf("Git.Enabled reset by partial POST (should be preserved)")
	}
	if !loaded.Git.Push {
		t.Errorf("Git.Push reset by partial POST (should be preserved)")
	}
	if loaded.Git.Remote != "https://ex.invalid/seed.git" {
		t.Errorf("Git.Remote mutated by partial POST: %q", loaded.Git.Remote)
	}
	if loaded.Git.TokenEnv != "GIT_TOKEN_SEED" {
		t.Errorf("Git.TokenEnv mutated by partial POST: %q", loaded.Git.TokenEnv)
	}
	if loaded.Theme.Preset != "light-clean" {
		t.Errorf("Theme.Preset not applied: %q", loaded.Theme.Preset)
	}
}

func TestServer_Preview(t *testing.T) {
	s, _ := setupServer(t)
	body := "content=" + url.QueryEscape("# Test\n\n[[Other]] #live")
	w := doReq(t, s, "POST", "/api/preview", body, false)
	if w.Code != 200 {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	out := w.Body.String()
	if !strings.Contains(out, `href="/notes/other.md"`) {
		t.Errorf("wiki-link not resolved: %s", out)
	}
	if !strings.Contains(out, `href="/tags/live"`) {
		t.Errorf("tag not rendered: %s", out)
	}
	if !strings.Contains(out, "<h1") {
		t.Errorf("markdown heading not rendered: %s", out)
	}
}

func TestServer_ProjectsCRUD(t *testing.T) {
	s, dir := setupServer(t)

	// GET /projects (empty)
	w := doReq(t, s, "GET", "/projects", "", false)
	if w.Code != 200 {
		t.Fatalf("projects GET = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Nome progetto") {
		t.Errorf("missing form: %s", w.Body.String())
	}

	// POST create
	w = doReq(t, s, "POST", "/projects", "name=Lavoro", false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create = %d, body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/projects/Lavoro" {
		t.Errorf("redirect = %q", loc)
	}
	if _, err := os.Stat(filepath.Join(dir, "Lavoro")); err != nil {
		t.Errorf("project dir not created: %v", err)
	}

	// GET detail
	w = doReq(t, s, "GET", "/projects/Lavoro", "", false)
	if w.Code != 200 {
		t.Fatalf("detail = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Lavoro") {
		t.Errorf("detail missing name: %s", w.Body.String())
	}

	// POST invalid name
	w = doReq(t, s, "POST", "/projects", "name=../evil", false)
	if w.Code != 200 {
		t.Fatalf("invalid create = %d (expected 200 with error msg)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid project name") {
		t.Errorf("missing error message: %s", w.Body.String())
	}

	// Duplicate
	w = doReq(t, s, "POST", "/projects", "name=Lavoro", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "already exists") {
		t.Errorf("duplicate create should show error, got %d %s", w.Code, w.Body.String())
	}
}

func TestServer_NewNoteInProject(t *testing.T) {
	s, dir := setupServer(t)
	// Create project first
	if _, err := s.vault.CreateProject("Studio"); err != nil {
		t.Fatal(err)
	}

	w := doReq(t, s, "GET", "/notes/new?project=Studio&title=Appunti", "", false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("new note = %d, body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "Studio/Appunti.md") {
		t.Errorf("location = %q", loc)
	}
	if _, err := os.Stat(filepath.Join(dir, "Studio", "Appunti.md")); err != nil {
		t.Errorf("note file not created: %v", err)
	}

	// Project detail should now list the note
	w = doReq(t, s, "GET", "/projects/Studio", "", false)
	if !strings.Contains(w.Body.String(), "Studio/Appunti.md") {
		t.Errorf("project detail missing note: %s", w.Body.String())
	}
}

func TestServer_RenameNote(t *testing.T) {
	s, dir := setupServer(t)

	// hello.md and other.md are seeded by setupServer
	form := "to=" + url.QueryEscape("renamed.md")
	w := doReq(t, s, "POST", "/notes/hello.md/rename", form, false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/notes/renamed.md" {
		t.Errorf("redirect = %q", loc)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.md")); err == nil {
		t.Errorf("old file should be gone")
	}
	if _, err := os.Stat(filepath.Join(dir, "renamed.md")); err != nil {
		t.Errorf("new file missing: %v", err)
	}
}

func TestServer_RenameNote_AddsExt(t *testing.T) {
	s, dir := setupServer(t)
	form := "to=" + url.QueryEscape("howdy")
	w := doReq(t, s, "POST", "/notes/hello.md/rename", form, false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/notes/howdy.md" {
		t.Errorf("redirect = %q", loc)
	}
	if _, err := os.Stat(filepath.Join(dir, "howdy.md")); err != nil {
		t.Errorf("file not created with .md ext: %v", err)
	}
}

func TestServer_RenameProject(t *testing.T) {
	s, dir := setupServer(t)
	if _, err := s.vault.CreateProject("Old"); err != nil {
		t.Fatal(err)
	}
	form := "to=New"
	w := doReq(t, s, "POST", "/projects/Old/rename", form, false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/projects/New" {
		t.Errorf("redirect = %q", loc)
	}
	if _, err := os.Stat(filepath.Join(dir, "Old")); err == nil {
		t.Errorf("old dir should be gone")
	}
	if _, err := os.Stat(filepath.Join(dir, "New")); err != nil {
		t.Errorf("new dir missing: %v", err)
	}
}

func TestServer_DeleteNote(t *testing.T) {
	s, dir := setupServer(t)

	// Delete root-level note → redirect to /
	w := doReq(t, s, "POST", "/notes/hello.md/delete", "", false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("redirect = %q, want /", loc)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.md")); err == nil {
		t.Errorf("file should be gone")
	}
	// Index cleaned
	if n, _ := s.index.Note("hello.md"); n != nil {
		t.Errorf("note still in index")
	}

	// Create a note inside a project and delete it → redirect to project
	_, _ = s.vault.CreateProject("Work")
	if err := os.WriteFile(filepath.Join(dir, "Work", "task.md"), []byte("# task"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = s.index.Upsert(index.NoteDoc{Path: "Work/task.md", Title: "task", Body: "# task", ModTime: 1, Size: 6})
	w = doReq(t, s, "POST", "/notes/Work/task.md/delete", "", false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("scoped delete status = %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/projects/Work" {
		t.Errorf("redirect = %q", loc)
	}
}

func TestServer_DeleteProject(t *testing.T) {
	s, dir := setupServer(t)

	// Create project with two notes via the index + filesystem (mirrors a scan)
	_, _ = s.vault.CreateProject("Zone")
	for _, name := range []string{"a.md", "b.md"} {
		full := filepath.Join(dir, "Zone", name)
		if err := os.WriteFile(full, []byte("# "+name), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = s.index.Upsert(index.NoteDoc{
			Path: "Zone/" + name, Title: name, Body: "# " + name, ModTime: 1, Size: 6,
		})
	}

	w := doReq(t, s, "POST", "/projects/Zone/delete", "", false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/projects" {
		t.Errorf("redirect = %q", loc)
	}
	if _, err := os.Stat(filepath.Join(dir, "Zone")); err == nil {
		t.Errorf("project dir should be gone")
	}
	// Index cleaned for both notes
	for _, p := range []string{"Zone/a.md", "Zone/b.md"} {
		if n, _ := s.index.Note(p); n != nil {
			t.Errorf("%s still in index", p)
		}
	}

	// Delete non-existent → 400
	w = doReq(t, s, "POST", "/projects/Ghost/delete", "", false)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing project delete should be 400, got %d", w.Code)
	}
}

func TestServer_NewNote(t *testing.T) {
	s, dir := setupServer(t)
	w := doReq(t, s, "GET", "/notes/new?title=Brand+New", "", false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("new note status = %d, body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc == "" || !strings.Contains(loc, "Brand") {
		t.Errorf("location = %q", loc)
	}
	// file should exist
	if _, err := os.Stat(filepath.Join(dir, "Brand New.md")); err != nil {
		t.Errorf("file not created: %v", err)
	}
}
