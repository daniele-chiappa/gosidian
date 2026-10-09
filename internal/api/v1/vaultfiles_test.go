package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/webauth"
)

// ADR-022 / BUG-031: the /vault-files/ gate resolves the same principals as
// the notes API (Bearer, cookie, open-mode guest) and applies canSee.

func vfReq(rel, bearer, cookie string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/vault-files/"+rel, nil)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: filesCookieName, Value: cookie})
	}
	return r
}

func TestVaultFiles_Authorizer_Principals(t *testing.T) {
	f := newNotesFixture(t)
	fn := f.router.VaultFileAuthorizer()
	const rel = "scratch/attachments/a.png"
	const other = "other/attachments/b.png"

	if got := fn(vfReq(rel, "", ""), rel); got != http.StatusUnauthorized {
		t.Errorf("anonymous: got %d, want 401", got)
	}
	if got := fn(vfReq(rel, f.bearer, ""), rel); got != 0 {
		t.Errorf("owner bearer: got %d, want allow", got)
	}
	bob, err := f.webauth.AddUser("bob", "bob-Pass123!", webauth.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	// Accounts created from v2.32 on are restricted (grants only); these
	// tests exercise visibility, so lift it.
	if err := f.webauth.SetRestricted(bob.ID, false); err != nil {
		t.Fatal(err)
	}
	bobTok, _, err := f.spaTokens.Create(bob.ID, "bob-agent")
	if err != nil {
		t.Fatal(err)
	}
	// An internal project is readable by every member account.
	if err := f.projects.Set("scratch", projects.Flags{Visibility: projects.VisibilityInternal}); err != nil {
		t.Fatal(err)
	}
	if got := fn(vfReq(rel, "", bobTok), rel); got != 0 {
		t.Errorf("member cookie (internal project): got %d, want allow", got)
	}
	if got := fn(vfReq(rel, "", "gsp_bogus"), rel); got != http.StatusUnauthorized {
		t.Errorf("garbage cookie: got %d, want 401", got)
	}
	// A private project (the default) needs a grant.
	if got := fn(vfReq(other, "", bobTok), other); got != http.StatusNotFound {
		t.Errorf("member outside a private project: got %d, want 404", got)
	}
	if err := f.projects.Set("scratch", projects.Flags{Visibility: projects.VisibilityPrivate}); err != nil {
		t.Fatal(err)
	}
	if got := fn(vfReq(rel, "", bobTok), rel); got != http.StatusNotFound {
		t.Errorf("member on a project made private: got %d, want 404", got)
	}
	if err := f.projects.SetMember("scratch", bob.ID, projects.LevelRead); err != nil {
		t.Fatal(err)
	}
	if got := fn(vfReq(rel, "", bobTok), rel); got != 0 {
		t.Errorf("member with a read grant: got %d, want allow", got)
	}
	if err := f.spaTokens.Revoke(bobTok); err != nil {
		t.Fatal(err)
	}
	if got := fn(vfReq(rel, "", bobTok), rel); got != http.StatusUnauthorized {
		t.Errorf("revoked cookie: got %d, want 401", got)
	}
}

func TestVaultFiles_Authorizer_OpenModeAndMCPTokens(t *testing.T) {
	f := newNotesFixture(t)
	fn := f.router.VaultFileAuthorizer()
	const rel = "scratch/attachments/a.png"
	const other = "other/attachments/b.png"

	f.router.deps.Auth.OpenMode = true
	if got := fn(vfReq(rel, "", ""), rel); got != http.StatusNotFound {
		t.Errorf("open-mode guest on private project: got %d, want 404", got)
	}
	if err := f.projects.Set("scratch", projects.Flags{Visibility: projects.VisibilityPublic}); err != nil {
		t.Fatal(err)
	}
	if got := fn(vfReq(rel, "", ""), rel); got != 0 {
		t.Errorf("open-mode guest on public project: got %d, want allow", got)
	}
	f.router.deps.Auth.OpenMode = false
	if got := fn(vfReq(rel, "", ""), rel); got != http.StatusUnauthorized {
		t.Errorf("open-mode off, anonymous: got %d, want 401", got)
	}

	mcp, err := auth.Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	f.router.deps.Auth.MCPTokens = mcp
	reader, _, err := mcp.Create("agent", []string{"scratch"}, []string{auth.ScopeRead}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := fn(vfReq(rel, reader, ""), rel); got != 0 {
		t.Errorf("MCP read token in scope: got %d, want allow", got)
	}
	if got := fn(vfReq(other, reader, ""), other); got != http.StatusNotFound {
		t.Errorf("MCP read token out of scope: got %d, want 404", got)
	}
	writer, _, err := mcp.Create("writer", []string{"scratch"}, []string{auth.ScopeWrite}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := fn(vfReq(rel, writer, ""), rel); got != http.StatusUnauthorized {
		t.Errorf("MCP token without read scope: got %d, want 401", got)
	}
}

// An MCP token reaches the attachments its owner may read now, not every
// path its declared scope allows: a token with no project list, which any
// account may create, used to read every project, and a token kept working
// after a revoked grant or a disabled owner (BUG-100).
func TestVaultFiles_MCPTokenFollowsItsOwner(t *testing.T) {
	f := newNotesFixture(t)
	fn := f.router.VaultFileAuthorizer()
	const rel = "scratch/attachments/a.png"
	const other = "other/attachments/b.png"
	mcp, err := auth.Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	f.router.deps.Auth.MCPTokens = mcp
	bob, err := f.webauth.AddUser("bob", "bob-Pass123!", webauth.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.projects.SetMember("scratch", bob.ID, projects.LevelRead); err != nil {
		t.Fatal(err)
	}
	inherit, _, err := mcp.Create("bob-inherit", nil, []string{auth.ScopeRead}, 0, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	scoped, _, err := mcp.Create("bob-scoped", []string{"scratch"}, []string{auth.ScopeRead}, 0, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := fn(vfReq(rel, inherit, ""), rel); got != 0 {
		t.Errorf("inherit token on a granted project: got %d, want allow", got)
	}
	if got := fn(vfReq(other, inherit, ""), other); got != http.StatusNotFound {
		t.Errorf("inherit token on a private project: got %d, want 404", got)
	}

	if err := f.projects.Set("scratch", projects.Flags{Visibility: projects.VisibilityPrivate, HiddenFromMCP: true}); err != nil {
		t.Fatal(err)
	}
	if got := fn(vfReq(rel, scoped, ""), rel); got != http.StatusNotFound {
		t.Errorf("project hidden from MCP: got %d, want 404", got)
	}
	if err := f.projects.Set("scratch", projects.Flags{Visibility: projects.VisibilityPrivate}); err != nil {
		t.Fatal(err)
	}
	if err := f.projects.RemoveMember("scratch", bob.ID); err != nil {
		t.Fatal(err)
	}
	if got := fn(vfReq(rel, scoped, ""), rel); got != http.StatusNotFound {
		t.Errorf("scoped token after the grant went: got %d, want 404", got)
	}
	if err := f.projects.SetMember("scratch", bob.ID, projects.LevelRead); err != nil {
		t.Fatal(err)
	}
	if err := f.webauth.DisableUser(bob.ID); err != nil {
		t.Fatal(err)
	}
	if got := fn(vfReq(rel, scoped, ""), rel); got != http.StatusUnauthorized {
		t.Errorf("token of a disabled owner: got %d, want 401", got)
	}
}

func TestVaultFiles_CookieLifecycle(t *testing.T) {
	f := newNotesFixture(t)
	fn := f.router.VaultFileAuthorizer()
	const rel = "scratch/attachments/a.png"

	w := f.request(http.MethodPost, "/api/v1/login",
		fmt.Sprintf(`{"username":%q,"password":%q}`, f.username, f.password), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("login: status=%d body=%s", w.Code, w.Body.String())
	}
	var login struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	var files *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == filesCookieName {
			files = c
		}
	}
	if files == nil {
		t.Fatalf("login did not set %s: %v", filesCookieName, w.Header().Values("Set-Cookie"))
	}
	if files.Value != login.Token || !files.HttpOnly || files.Path != "/vault-files/" || files.SameSite != http.SameSiteLaxMode || files.Expires.IsZero() {
		t.Errorf("cookie attributes: %+v", files)
	}
	if files.Secure {
		t.Errorf("plain-HTTP test request must not mark the cookie Secure")
	}
	if got := fn(vfReq(rel, "", files.Value), rel); got != 0 {
		t.Errorf("login cookie should authorize: got %d", got)
	}

	me := f.request(http.MethodGet, "/api/v1/me", "", map[string]string{"Authorization": "Bearer " + login.Token})
	if me.Code != http.StatusOK || !strings.Contains(strings.Join(me.Header().Values("Set-Cookie"), ";"), filesCookieName+"=") {
		t.Errorf("/me should re-issue the cookie: status=%d set-cookie=%v", me.Code, me.Header().Values("Set-Cookie"))
	}

	lo := f.request(http.MethodPost, "/api/v1/logout", "", map[string]string{"Authorization": "Bearer " + login.Token})
	if lo.Code != http.StatusNoContent {
		t.Fatalf("logout: status=%d", lo.Code)
	}
	cleared := false
	for _, c := range lo.Result().Cookies() {
		if c.Name == filesCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("logout should clear the cookie: %v", lo.Header().Values("Set-Cookie"))
	}
	if got := fn(vfReq(rel, "", files.Value), rel); got != http.StatusUnauthorized {
		t.Errorf("revoked session cookie: got %d, want 401", got)
	}
}

// /healthz shows its details (vault path, note count, git's last error) to
// the owner's session and to an MCP token of the owner or of no account,
// to no one else (IMP-160, S2-10).
func TestHealthDetailsAuthorizer(t *testing.T) {
	f := newNotesFixture(t)
	fn := f.router.HealthDetailsAuthorizer()
	req := func(bearer string) *http.Request { return vfReq("", bearer, "") }

	if fn(req("")) {
		t.Error("anonymous sees the details")
	}
	if !fn(req(f.bearer)) {
		t.Error("the owner's session does not see the details")
	}
	bob, err := f.webauth.AddUser("bob", "bob-Pass123!", webauth.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	bobTok, _, err := f.spaTokens.Create(bob.ID, "bob-agent")
	if err != nil {
		t.Fatal(err)
	}
	if fn(req(bobTok)) {
		t.Error("a member's session sees the details")
	}

	mcp, err := auth.Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	f.router.deps.Auth.MCPTokens = mcp
	cli, _, err := mcp.Create("cli", nil, []string{auth.ScopeRead}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	mine, _, err := mcp.Create("mine", nil, []string{auth.ScopeRead}, 0, f.webauth.FirstOwner().ID)
	if err != nil {
		t.Fatal(err)
	}
	bobs, _, err := mcp.Create("bobs", nil, []string{auth.ScopeRead}, 0, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !fn(req(cli)) || !fn(req(mine)) {
		t.Error("an MCP token of no account or of the owner does not see the details")
	}
	scoped, _, err := mcp.Create("scoped", []string{"scratch"}, []string{auth.ScopeRead}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if fn(req(bobs)) || fn(req("gsm_bogus")) || fn(req(scoped)) {
		t.Error("a member's MCP token, a bogus one or one limited to a project sees the details")
	}
}
