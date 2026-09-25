package mcp

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/gosidian/gosidian/internal/webauth"
)

// accessFixture wires a token store, a projects store and a principal
// resolver around a vault with three projects, so the live narrowing of a
// token to its owner's access (BUG-055) can be exercised end to end.
type accessFixture struct {
	s        *Server
	tokens   *auth.Store
	projects *projects.Store
	roles    map[string]webauth.Role
}

func newAccessFixture(t *testing.T) *accessFixture {
	t.Helper()
	dir := t.TempDir()
	for _, p := range []string{"alpha", "beta", "gamma"} {
		if err := os.MkdirAll(filepath.Join(dir, p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p, "note.md"), []byte("# "+p+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	tokens, err := auth.Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	f := &accessFixture{
		tokens:   tokens,
		projects: pstore,
		roles: map[string]webauth.Role{
			"owner": webauth.RoleOwner,
			"m1":    webauth.RoleMember,
			"g1":    webauth.RoleGuest,
		},
	}
	s := New(vault.New(dir), idx, tokens)
	s.SetProjects(pstore)
	s.SetPrincipalResolver(func(id string) (authz.Principal, bool) {
		role, ok := f.roles[id]
		if !ok {
			return authz.Principal{}, false
		}
		return authz.Principal{UserID: id, Role: role}, true
	})
	f.s = s
	return f
}

// token mints a static token owned by the given account and returns the
// plaintext plus the stored record.
func (f *accessFixture) token(t *testing.T, owner string, projs, scopes []string) (string, *auth.Token) {
	t.Helper()
	plain, tok, err := f.tokens.Create("t-"+owner, projs, scopes, 0, owner)
	if err != nil {
		t.Fatal(err)
	}
	return plain, &tok
}

func (f *accessFixture) visibility(t *testing.T, project, vis string) {
	t.Helper()
	fl := f.projects.Get(project)
	fl.Visibility = vis
	if err := f.projects.Set(project, fl); err != nil {
		t.Fatal(err)
	}
}

func (f *accessFixture) grant(t *testing.T, project, user, level string) {
	t.Helper()
	if err := f.projects.SetMember(project, user, level); err != nil {
		t.Fatal(err)
	}
}

var rw = []string{auth.ScopeRead, auth.ScopeWrite}

func TestEffectiveToken_OwnerAndCLIUnchanged(t *testing.T) {
	f := newAccessFixture(t)

	_, ownerTok := f.token(t, "owner", []string{"alpha"}, rw)
	if eff := f.s.effectiveToken(ownerTok); eff != ownerTok {
		t.Error("owner-owned token must be returned as is")
	}
	_, cli := f.token(t, "", nil, rw)
	if eff := f.s.effectiveToken(cli); eff != cli || !eff.IsAdmin() {
		t.Error("CLI token (no owner) must stay unscoped")
	}
}

// Internal projects are readable by every member; writing takes a grant.
func TestEffectiveToken_InternalReadsWriteViaGrant(t *testing.T) {
	f := newAccessFixture(t)
	f.visibility(t, "alpha", projects.VisibilityInternal)
	f.visibility(t, "beta", projects.VisibilityInternal)
	f.grant(t, "beta", "m1", projects.LevelWrite)
	_, tok := f.token(t, "m1", []string{"alpha", "beta"}, rw)

	eff := f.s.effectiveToken(tok)
	if eff == nil || strings.Join(eff.ProjectList(), ",") != "alpha,beta" {
		t.Fatalf("internal projects must stay readable, got %+v", eff)
	}
	if eff.AllowsWrite("alpha/x.md") {
		t.Error("alpha: internal without a grant is read-only")
	}
	if !eff.AllowsWrite("beta/x.md") {
		t.Error("beta: write grant must allow writes")
	}
}

func TestEffectiveToken_PrivateIntersectsLive(t *testing.T) {
	f := newAccessFixture(t)
	f.grant(t, "alpha", "m1", projects.LevelWrite)
	f.grant(t, "beta", "m1", projects.LevelRead)
	_, tok := f.token(t, "m1", []string{"alpha", "beta", "gamma"}, rw)

	eff := f.s.effectiveToken(tok)
	if eff == nil {
		t.Fatal("expected a narrowed token")
	}
	if got := strings.Join(eff.ProjectList(), ","); got != "alpha,beta" {
		t.Fatalf("projects = %q, want alpha,beta (gamma is private with no grant)", got)
	}
	if !eff.HasScope(auth.ScopeWrite) {
		t.Fatal("write scope must survive: alpha is writable")
	}
	if !eff.AllowsWrite("alpha/x.md") {
		t.Error("alpha: write grant must allow writes")
	}
	if eff.AllowsWrite("beta/x.md") {
		t.Error("beta: read grant must block writes")
	}
	if tok.ProjectList()[2] != "gamma" || !tok.AllowsWrite("beta/x.md") {
		t.Error("the stored record must be left untouched")
	}

	// Through the handlers: a write on beta is refused, on alpha accepted,
	// and the project list shows only the readable ones.
	ctx := context.WithValue(context.Background(), tokenCtxKey, eff)
	res, _ := f.s.handleCreate(ctx, call(map[string]any{"path": "beta/new.md", "content": "# no"}))
	if msg := expectError(t, res); !strings.Contains(msg, "may only read") {
		t.Errorf("beta write error = %q", msg)
	}
	res, _ = f.s.handleCreate(ctx, call(map[string]any{"path": "alpha/new.md", "content": "# yes"}))
	resultText(t, res)
	res, _ = f.s.handleListProjects(ctx, call(nil))
	if out := resultText(t, res); !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") || strings.Contains(out, "gamma") || !strings.Contains(out, `"visibility":"private"`) {
		t.Errorf("list_projects = %s", out)
	}

	// Grant changes apply on the next derivation, no token rewrite.
	if err := f.projects.RemoveMember("beta", "m1"); err != nil {
		t.Fatal(err)
	}
	if eff := f.s.effectiveToken(tok); strings.Join(eff.ProjectList(), ",") != "alpha" {
		t.Errorf("after removing beta: %v", eff.ProjectList())
	}
	if err := f.projects.RemoveMember("alpha", "m1"); err != nil {
		t.Fatal(err)
	}
	if eff := f.s.effectiveToken(tok); eff != nil {
		t.Errorf("no readable project left: expected nil, got %+v", eff)
	}
}

func TestEffectiveToken_ReadOnlyGrantDropsWrite(t *testing.T) {
	f := newAccessFixture(t)
	f.grant(t, "alpha", "m1", projects.LevelRead)
	_, tok := f.token(t, "m1", []string{"alpha"}, rw)

	eff := f.s.effectiveToken(tok)
	if eff == nil || eff.HasScope(auth.ScopeWrite) {
		t.Fatalf("write must be dropped when no project is writable: %+v", eff)
	}
	ctx := context.WithValue(context.Background(), tokenCtxKey, eff)
	res, _ := f.s.handleCreate(ctx, call(map[string]any{"path": "alpha/new.md", "content": "# no"}))
	if msg := expectError(t, res); !strings.Contains(msg, "write scope") {
		t.Errorf("error = %q", msg)
	}
}

func TestEffectiveToken_UnscopedNonOwnerIsNeverAdmin(t *testing.T) {
	f := newAccessFixture(t)
	f.grant(t, "alpha", "m1", projects.LevelWrite)
	_, tok := f.token(t, "m1", nil, rw)

	eff := f.s.effectiveToken(tok)
	if eff == nil || eff.IsAdmin() {
		t.Fatalf("unscoped member token must be materialised to its readable projects: %+v", eff)
	}
	if got := strings.Join(eff.ProjectList(), ","); got != "alpha" {
		t.Errorf("projects = %q", got)
	}
	ctx := context.WithValue(context.Background(), tokenCtxKey, eff)
	res, _ := f.s.handleCreateProject(ctx, call(map[string]any{"name": "delta"}))
	if msg := expectError(t, res); !strings.Contains(msg, "cannot create projects") {
		t.Errorf("error = %q", msg)
	}
}

func TestEffectiveToken_GuestReadsPublicOnly(t *testing.T) {
	f := newAccessFixture(t)
	f.visibility(t, "alpha", projects.VisibilityPublic)
	f.visibility(t, "beta", projects.VisibilityInternal)
	_, tok := f.token(t, "g1", []string{"alpha", "beta"}, rw)

	eff := f.s.effectiveToken(tok)
	if eff == nil || strings.Join(eff.ProjectList(), ",") != "alpha" {
		t.Fatalf("guest sees public projects only: %+v", eff)
	}
	if eff.HasScope(auth.ScopeWrite) {
		t.Error("guest never writes")
	}
}

func TestEffectiveToken_UnknownOwnerFailsClosed(t *testing.T) {
	f := newAccessFixture(t)
	f.visibility(t, "alpha", projects.VisibilityInternal)
	_, tok := f.token(t, "ghost", []string{"alpha"}, rw)
	if eff := f.s.effectiveToken(tok); eff != nil {
		t.Errorf("owner that does not resolve must yield nil, got %+v", eff)
	}
}

func TestAuthenticate_NarrowsBearer(t *testing.T) {
	f := newAccessFixture(t)
	f.grant(t, "alpha", "m1", projects.LevelWrite)
	plain, _ := f.token(t, "m1", []string{"alpha", "beta"}, rw)

	req, _ := http.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	eff := f.s.authenticate(req)
	if eff == nil || strings.Join(eff.ProjectList(), ",") != "alpha" {
		t.Fatalf("authenticate must hand out the narrowed token: %+v", eff)
	}

	// No readable project at all → the bearer is refused outright.
	if err := f.projects.RemoveMember("alpha", "m1"); err != nil {
		t.Fatal(err)
	}
	if eff := f.s.authenticate(req); eff != nil {
		t.Errorf("expected refusal, got %+v", eff)
	}
}

// memory_create_project pins the store default on the new folder.
func TestCreateProject_PinsDefaultVisibility(t *testing.T) {
	f := newAccessFixture(t)
	if err := f.projects.SetDefaultVisibility(projects.VisibilityInternal); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), tokenCtxKey, auth.AdminToken())
	res, _ := f.s.handleCreateProject(ctx, call(map[string]any{"name": "delta"}))
	if out := resultText(t, res); !strings.Contains(out, `"visibility":"internal"`) {
		t.Errorf("create = %s", out)
	}
	if f.projects.Get("delta").Visibility != projects.VisibilityInternal {
		t.Error("visibility must be written to the store")
	}
}
