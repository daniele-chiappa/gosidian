package v1

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/config"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/trash"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/gosidian/gosidian/internal/webauth"
)

// adminFixture wires Trash + ConfigPath alongside the standard notes
// fixture. The settings + trash + extras tests need them, so we pay
// the setup cost once per test.
type adminFixture struct {
	*notesFixture
	configPath string
	trashBin   *trash.Bin
	mcpStore   *auth.Store
}

func newAdminFixture(t *testing.T) *adminFixture {
	t.Helper()
	dir := t.TempDir()

	wa, _ := webauth.Open(filepath.Join(dir, "auth.json"))
	if _, err := wa.Setup("owner", "supersecret", false, "test"); err != nil {
		t.Fatal(err)
	}
	owner := wa.FirstOwner()
	spa, _ := auth.OpenSpaTokens(filepath.Join(dir, "spa.json"))
	mcpStore, _ := auth.Open(filepath.Join(dir, "tokens.json"))
	al, _ := audit.Open(filepath.Join(dir, "audit.jsonl"))

	vaultRoot := filepath.Join(dir, "vault")
	if err := makeDir(vaultRoot); err != nil {
		t.Fatal(err)
	}
	v := vault.New(vaultRoot)
	idx, _ := index.Open(filepath.Join(dir, "index.db"))
	t.Cleanup(func() { idx.Close() })
	hub := events.New(events.HubOptions{Logger: slog.Default()})
	pstore, _ := projects.Open(filepath.Join(dir, "projects.json"))

	cfgPath := filepath.Join(dir, "config.toml")
	if err := config.Save(cfgPath, config.Default()); err != nil {
		t.Fatal(err)
	}
	bin := trash.New(vaultRoot, 0)

	router := NewRouter(&Deps{
		Auth: &AuthDeps{
			WebAuth:   wa,
			SpaAuth:   spa,
			MCPTokens: mcpStore,
			Logger:    slog.Default(),
		},
		Audit:      al,
		Vault:      v,
		Events:     hub,
		Index:      idx,
		Renderer:   parser.NewRenderer(),
		Projects:   pstore,
		Trash:      bin,
		ConfigPath: cfgPath,
	})

	bearer, _, _ := spa.Create(owner.ID, "test")
	af := &authFixture{
		t:         t,
		router:    router,
		webauth:   wa,
		spaTokens: spa,
		auditLog:  al,
		username:  "owner",
		password:  "supersecret",
		owner:     owner,
	}
	nf := &notesFixture{authFixture: af, vaultRoot: vaultRoot, idx: idx, projects: pstore, bearer: bearer}
	return &adminFixture{notesFixture: nf, configPath: cfgPath, trashBin: bin, mcpStore: mcpStore}
}

// memberToken adds a member account and returns a bearer token for it.
// Used to confirm owner-only routes really reject non-owners.
func (f *adminFixture) memberToken(t *testing.T) string {
	t.Helper()
	if _, err := f.webauth.AddUser("alice", "alice-pass-123", webauth.RoleMember); err != nil {
		t.Fatal(err)
	}
	var memberID string
	for _, u := range f.webauth.ListUsers() {
		if u.Role == webauth.RoleMember {
			memberID = u.ID
			break
		}
	}
	if memberID == "" {
		t.Fatal("member user missing")
	}
	tok, _, err := f.spaTokens.Create(memberID, "member-test")
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// helper because os.MkdirAll needs an explicit import dance otherwise.
func makeDir(p string) error { return mkdirAll(p, 0o755) }

// mkdirAll is split out so the test file doesn't import "os" directly
// (other tests in the package do).
func mkdirAll(p string, mode int) error {
	return mkdirAllImpl(p, mode)
}

// ---- /api/v1/settings ----

func TestSettings_GetReturnsConfig(t *testing.T) {
	f := newAdminFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/settings", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	for _, want := range []string{`"git"`, `"trash"`, `"i18n"`, `"mcp"`, `"branch"`} {
		if !strings.Contains(w.body, want) {
			t.Errorf("missing %q: %s", want, w.body)
		}
	}
}

func TestSettings_GetNeverLeaksToken(t *testing.T) {
	f := newAdminFixture(t)
	// Inject a fake config with a token_env value pointed at a real
	// var to make sure only the var name is exposed.
	cfg := config.Default()
	cfg.Git.TokenEnv = "GITEA_PAT"
	_ = config.Save(f.configPath, cfg)

	w := f.doAuthRecorder(http.MethodGet, "/api/v1/settings", "", nil)
	if w.code != http.StatusOK {
		t.Fatal(w.body)
	}
	if !strings.Contains(w.body, `"token_env":"GITEA_PAT"`) {
		t.Errorf("token_env not echoed: %s", w.body)
	}
	// No token plaintext field in the JSON.
	if strings.Contains(w.body, `"token"`) {
		t.Errorf("token leaked: %s", w.body)
	}
}

func TestSettings_PutOwnerUpdatesConfig(t *testing.T) {
	f := newAdminFixture(t)
	body := `{"git":{"enabled":true,"branch":"trunk","debounce_ms":5000}}`
	w := f.doAuthRecorder(http.MethodPut, "/api/v1/settings", body, nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	// On-disk verification — load the config fresh and assert the
	// fields persisted.
	cfg, _ := config.Load(f.configPath)
	if !cfg.Git.Enabled || cfg.Git.Branch != "trunk" || cfg.Git.Debounce.Milliseconds() != 5000 {
		t.Errorf("config not persisted: %+v", cfg.Git)
	}
}

func TestSettings_PutMemberForbidden(t *testing.T) {
	f := newAdminFixture(t)
	memberTok := f.memberToken(t)
	body := `{"git":{"enabled":true}}`
	w := f.request(http.MethodPut, "/api/v1/settings", body, map[string]string{
		"Authorization": "Bearer " + memberTok,
	})
	if w.Code != http.StatusForbidden {
		t.Errorf("status=%d, want 403", w.Code)
	}
}

func TestSettings_PutInvalidDebounce(t *testing.T) {
	f := newAdminFixture(t)
	body := `{"git":{"debounce_ms":50}}`
	w := f.doAuthRecorder(http.MethodPut, "/api/v1/settings", body, nil)
	if w.code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 body=%s", w.code, w.body)
	}
}

func TestSettings_PutPushWithoutRemote(t *testing.T) {
	f := newAdminFixture(t)
	body := `{"git":{"enabled":true,"push":true}}`
	w := f.doAuthRecorder(http.MethodPut, "/api/v1/settings", body, nil)
	if w.code != http.StatusBadRequest {
		t.Errorf("expected validation error: status=%d body=%s", w.code, w.body)
	}
}

// ---- /api/v1/trash ----

func TestTrash_ListEmpty(t *testing.T) {
	f := newAdminFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/trash", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d", w.code)
	}
	if !strings.Contains(w.body, `"total":0`) {
		t.Errorf("expected empty trash: %s", w.body)
	}
}

func TestTrash_DeletePopulatesTrash(t *testing.T) {
	f := newAdminFixture(t)
	f.seedNote(t, "doomed.md", "x")
	w := f.doAuthRecorder(http.MethodDelete, "/api/v1/notes/doomed.md", "", nil)
	if w.code != http.StatusNoContent {
		t.Fatalf("delete failed: %s", w.body)
	}
	w2 := f.doAuthRecorder(http.MethodGet, "/api/v1/trash", "", nil)
	if !strings.Contains(w2.body, `"doomed.md"`) {
		t.Errorf("trash list missing entry: %s", w2.body)
	}
}

func TestTrash_RestoreReindexes(t *testing.T) {
	f := newAdminFixture(t)
	f.seedNote(t, "victim.md", "content")
	_ = f.doAuthRecorder(http.MethodDelete, "/api/v1/notes/victim.md", "", nil)
	list := f.doAuthRecorder(http.MethodGet, "/api/v1/trash", "", nil)
	var parsed struct {
		Items []trashView `json:"items"`
	}
	if err := json.Unmarshal([]byte(list.body), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Items) == 0 {
		t.Fatal("no trash entries to restore")
	}
	id := parsed.Items[0].ID
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/trash/"+id+"/restore", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("restore failed: %d %s", w.code, w.body)
	}
	// The note should be reachable again.
	w2 := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/victim.md", "", nil)
	if w2.code != http.StatusOK {
		t.Errorf("restored note not reachable: %d", w2.code)
	}
}

func TestTrash_PurgeRemoves(t *testing.T) {
	f := newAdminFixture(t)
	f.seedNote(t, "kill.md", "x")
	_ = f.doAuthRecorder(http.MethodDelete, "/api/v1/notes/kill.md", "", nil)
	list := f.doAuthRecorder(http.MethodGet, "/api/v1/trash", "", nil)
	var parsed struct {
		Items []trashView `json:"items"`
	}
	_ = json.Unmarshal([]byte(list.body), &parsed)
	if len(parsed.Items) == 0 {
		t.Fatal("no entry to purge")
	}
	id := parsed.Items[0].ID
	w := f.doAuthRecorder(http.MethodDelete, "/api/v1/trash/"+id, "", nil)
	if w.code != http.StatusNoContent {
		t.Fatalf("purge failed: %d %s", w.code, w.body)
	}
	list2 := f.doAuthRecorder(http.MethodGet, "/api/v1/trash", "", nil)
	if !strings.Contains(list2.body, `"total":0`) {
		t.Errorf("trash not empty after purge: %s", list2.body)
	}
}

func TestTrash_RestoreUnknownID(t *testing.T) {
	f := newAdminFixture(t)
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/trash/unknown/restore", "", nil)
	if w.code != http.StatusNotFound {
		t.Errorf("status=%d, want 404 body=%s", w.code, w.body)
	}
}

// ---- /api/v1/notes/{path}/backlinks + /excerpt ----

func TestBacklinks_Returns(t *testing.T) {
	f := newAdminFixture(t)
	f.seedNote(t, "target.md", "# target")
	f.seedNote(t, "src.md", "links to [[target]]")
	if err := f.idx.ResolveAll(); err != nil {
		t.Fatal(err)
	}
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/target.md/backlinks", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `"src.md"`) {
		t.Errorf("missing backlink: %s", w.body)
	}
}

func TestBacklinks_NoteNotFound(t *testing.T) {
	f := newAdminFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/ghost.md/backlinks", "", nil)
	if w.code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.code)
	}
}

func TestExcerpt_DefaultLines(t *testing.T) {
	f := newAdminFixture(t)
	f.seedNote(t, "long.md", "line1\nline2\nline3\nline4\nline5\nline6\nline7\n")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/long.md/excerpt", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `line5`) {
		t.Errorf("expected line5 in default excerpt: %s", w.body)
	}
	if strings.Contains(w.body, `line6`) {
		t.Errorf("excerpt overran default 5 lines: %s", w.body)
	}
}

func TestExcerpt_CustomLines(t *testing.T) {
	f := newAdminFixture(t)
	f.seedNote(t, "long.md", "a\nb\nc\nd\ne\nf\ng\n")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/long.md/excerpt?lines=2", "", nil)
	if w.code != http.StatusOK {
		t.Fatal(w.body)
	}
	if !strings.Contains(w.body, `"lines":2`) {
		t.Errorf("lines param not echoed: %s", w.body)
	}
}

func TestExcerpt_StripsFrontmatter(t *testing.T) {
	f := newAdminFixture(t)
	f.seedNote(t, "fm.md", "---\ntitle: hello\ntags: [a]\n---\n# heading\n\nbody line\n")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/fm.md/excerpt", "", nil)
	if w.code != http.StatusOK {
		t.Fatal(w.body)
	}
	if strings.Contains(w.body, "title: hello") {
		t.Errorf("frontmatter leaked into excerpt: %s", w.body)
	}
	if !strings.Contains(w.body, "heading") {
		t.Errorf("body line missing: %s", w.body)
	}
}

// GET returns the effective settings (config.toml + GOSIDIAN_* env, as the
// server runs) and lists the env-set fields; PUT accepts the effective value
// echoed back but refuses a different one for an env-set field.
func TestSettings_EffectiveWithEnvOverrides(t *testing.T) {
	f := newAdminFixture(t)
	t.Setenv("GOSIDIAN_ANCHORS_ENABLED", "true")
	t.Setenv("GOSIDIAN_GIT_AUTHOR_EMAIL", "bot@example.org")

	w := f.doAuthRecorder(http.MethodGet, "/api/v1/settings", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	var got struct {
		Git struct {
			AuthorEmail string `json:"author_email"`
		} `json:"git"`
		AnchorsEnabled bool     `json:"anchors_enabled"`
		EnvOverrides   []string `json:"env_overrides"`
	}
	if err := json.Unmarshal([]byte(w.body), &got); err != nil {
		t.Fatal(err)
	}
	if !got.AnchorsEnabled || got.Git.AuthorEmail != "bot@example.org" {
		t.Errorf("not the effective values: %s", w.body)
	}
	if strings.Join(got.EnvOverrides, ",") != "anchors_enabled,git.author_email" {
		t.Errorf("env_overrides = %v", got.EnvOverrides)
	}

	// Echoing the effective value is fine; changing an env-set field is not.
	echo := `{"git":{"author_email":"bot@example.org","branch":"trunk"}}`
	if w := f.doAuthRecorder(http.MethodPut, "/api/v1/settings", echo, nil); w.code != http.StatusOK {
		t.Errorf("echo of the effective value refused: %d %s", w.code, w.body)
	}
	change := `{"git":{"author_email":"someone@else.org"}}`
	w = f.doAuthRecorder(http.MethodPut, "/api/v1/settings", change, nil)
	if w.code != http.StatusBadRequest || !strings.Contains(w.body, "git.author_email") {
		t.Errorf("change of an env-set field: %d %s", w.code, w.body)
	}
}

// A save that does not set totp_mode leaves the live policy alone (the SPA
// leaves an env-set policy out of its body), and members read the git
// remote without the credentials a URL may carry; the owner sees it whole.
func TestSettings_TOTPUntouchedAndRemoteRedacted(t *testing.T) {
	f := newAdminFixture(t)
	wa := f.router.deps.Auth.WebAuth
	wa.SetTOTPMode("optional")
	if w := f.doAuthRecorder(http.MethodPut, "/api/v1/settings", `{"trash":{"enabled":true}}`, nil); w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if got := wa.TOTPMode(); got != "optional" {
		t.Errorf("unrelated save changed the live TOTP mode to %q", got)
	}

	cfg, _ := config.Load(f.configPath)
	cfg.Git.Remote = "https://bot:s3cr3t@git.example.com/r.git"
	if err := config.Save(f.configPath, cfg); err != nil {
		t.Fatal(err)
	}
	w := f.request(http.MethodGet, "/api/v1/settings", "", map[string]string{
		"Authorization": "Bearer " + f.memberToken(t),
	})
	if body := w.Body.String(); w.Code != http.StatusOK || strings.Contains(body, "s3cr3t") || !strings.Contains(body, "redacted@git.example.com") {
		t.Errorf("member view: %d %s", w.Code, body)
	}
	if o := f.doAuthRecorder(http.MethodGet, "/api/v1/settings", "", nil); !strings.Contains(o.body, "s3cr3t") {
		t.Errorf("owner should see the remote whole: %s", o.body)
	}
}
