package main

import (
	"bytes"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/webauth"
)

// token list: an empty project list reads "(admin)" only for a CLI token or
// one owned by the owner account; any other account's token inherits
// (BUG-062). The owner column tells CLI, live, disabled and missing apart.
func TestTokenListLabels(t *testing.T) {
	disabled := time.Now()
	users := map[string]webauth.User{
		"o": {ID: "o", Username: "admin", Role: webauth.RoleOwner},
		"m": {ID: "m", Username: "alice", Role: webauth.RoleMember},
		"d": {ID: "d", Username: "bob", Role: webauth.RoleMember, DisabledAt: &disabled},
	}
	cases := []struct {
		name         string
		tok          auth.Token
		owner, scope string
	}{
		{"cli admin", auth.Token{}, "-", "(admin)"},
		{"owner unscoped", auth.Token{OwnerUserID: "o"}, "admin", "(admin)"},
		{"member inherit", auth.Token{OwnerUserID: "m"}, "alice", "(inherit)"},
		{"member custom", auth.Token{OwnerUserID: "m", Projects: []string{"a", "b"}}, "alice", "a,b"},
		{"disabled owner", auth.Token{OwnerUserID: "d"}, "bob (disabled)", "(inherit)"},
		{"missing owner", auth.Token{OwnerUserID: "x"}, "x (missing)", "(inherit)"},
	}
	for _, c := range cases {
		u, found := users[c.tok.OwnerUserID]
		if got := ownerLabel(c.tok, u, found); got != c.owner {
			t.Errorf("%s: owner = %q, want %q", c.name, got, c.owner)
		}
		if got := storedScopeLabel(c.tok, u, found); got != c.scope {
			t.Errorf("%s: scope = %q, want %q", c.name, got, c.scope)
		}
	}
}

// The boot warning names the tokens whose owner is gone or disabled, and
// only those.
func TestWarnDanglingTokenOwners(t *testing.T) {
	dir := t.TempDir()
	tokens, err := auth.Open(filepath.Join(dir, "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := webauth.Open(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.Setup("admin", "adminpassword", false, "test"); err != nil {
		t.Fatal(err)
	}
	owner := accounts.FirstOwner()
	_, cli, _ := tokens.Create("cli", nil, []string{auth.ScopeRead}, 0, "")
	_, live, _ := tokens.Create("live", nil, []string{auth.ScopeRead}, 0, owner.ID)
	_, gone, _ := tokens.Create("gone", nil, []string{auth.ScopeRead}, 0, "0123456789abcdef")

	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	warnDanglingTokenOwners(tokens, accounts)
	out := buf.String()
	if !strings.Contains(out, "1 MCP token(s)") || !strings.Contains(out, gone.ID) {
		t.Errorf("warning should name the token of the missing owner: %q", out)
	}
	if strings.Contains(out, cli.ID) || strings.Contains(out, live.ID) {
		t.Errorf("warning names a token whose owner resolves: %q", out)
	}
}

// Resetting the owner ends its web sessions and OAuth grants — the id is
// kept, so nothing else would — but leaves static MCP tokens and other
// accounts' sessions alone.
func TestEndOwnerSessions(t *testing.T) {
	dir := t.TempDir()
	accounts, err := webauth.Open(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.Setup("admin", "adminpassword", false, "test"); err != nil {
		t.Fatal(err)
	}
	owner := accounts.FirstOwner()
	spa, _ := auth.OpenSpaTokens(filepath.Join(dir, "spa_tokens.json"))
	_, _, _ = spa.Create(owner.ID, "browser")
	_, _, _ = spa.Create("someone-else", "browser")
	tokens, _ := auth.Open(filepath.Join(dir, "tokens.json"))
	_, _, _ = tokens.Create("integration", nil, []string{auth.ScopeRead}, 0, owner.ID)
	_, _ = tokens.CreateGrant("claude.ai", nil, []string{auth.ScopeRead}, 0, owner.ID, "client-1")

	sessions, grants := endOwnerSessions(dir, accounts, "admin")
	if sessions != 1 || grants != 1 {
		t.Errorf("ended sessions=%d grants=%d, want 1 and 1", sessions, grants)
	}
	left, _ := auth.Open(filepath.Join(dir, "tokens.json"))
	if l := left.List(); len(l) != 1 || l[0].Name != "integration" {
		t.Errorf("static token must survive: %+v", l)
	}
}

// The token id is taken from --id or from the one positional argument,
// wherever it sits among the flags (IMP-112).
func TestTokenID(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--id", "abc"}, "abc"},
		{[]string{"abc"}, "abc"},
		{[]string{"abc", "--vault", "/v"}, "abc"},
		{[]string{"--vault", "/v", "abc"}, "abc"},
		{[]string{"--vault", "/v"}, ""},
	} {
		fs := flag.NewFlagSet("token revoke", flag.ContinueOnError)
		vault := fs.String("vault", "", "")
		id := fs.String("id", "", "")
		tokenID(fs, c.args, id)
		if *id != c.want {
			t.Errorf("%q: id %q, want %q", c.args, *id, c.want)
		}
		if len(c.args) > 1 && c.args[0] != "--id" && *vault != "/v" {
			t.Errorf("%q: --vault lost after the positional id: %q", c.args, *vault)
		}
	}
}

// Without --vault the CLI takes GOSIDIAN_VAULT, as serve does (IMP-112).
func TestCLIVaultDirFromEnv(t *testing.T) {
	t.Setenv("GOSIDIAN_VAULT", "/from/env")
	if got := cliVaultDir(""); got != "/from/env" {
		t.Errorf("cliVaultDir(\"\") = %q", got)
	}
	if got := cliVaultDir("/flag"); got != "/flag" {
		t.Errorf("the flag must win over the env: %q", got)
	}
}
