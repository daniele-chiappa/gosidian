package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/webauth"
)

// The CLI's failures end in log.Fatal: they run in a child process, the
// test binary itself started again with the user command to run.
func TestMain(m *testing.M) {
	if args := os.Getenv("GOSIDIAN_TEST_USER_CMD"); args != "" {
		runUserCmd(strings.Split(args, "\x1f"))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runUserChild runs `gosidian user <args>` in a child process and returns
// its output and whether it succeeded.
func runUserChild(t *testing.T, stdin string, args ...string) (string, bool) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "GOSIDIAN_TEST_USER_CMD="+strings.Join(args, "\x1f"))
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// runUser runs `gosidian user <args>` in this process with stdin, and
// returns what it printed.
func runUser(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	inR, inW, _ := os.Pipe()
	_, _ = io.WriteString(inW, stdin)
	_ = inW.Close()
	outR, outW, _ := os.Pipe()
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()
	runUserCmd(args)
	_ = outW.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, outR)
	return buf.String()
}

// cliVault is a vault with its owner, as `user setup` leaves it.
func cliVault(t *testing.T) (vaultDir, stateDir string) {
	t.Helper()
	vaultDir = t.TempDir()
	stateDir = filepath.Join(vaultDir, ".gosidian")
	s, err := webauth.Open(filepath.Join(stateDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Setup("root", "root-pass-1234", false, "test"); err != nil {
		t.Fatal(err)
	}
	return vaultDir, stateDir
}

func openAccounts(t *testing.T, stateDir string) *webauth.Store {
	t.Helper()
	s, err := webauth.Open(filepath.Join(stateDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// user add puts an account beside the others, to change its password at
// the first sign-in, with its personal project; user disable disables that
// one account, ending its sessions and revoking its tokens; user list shows
// both (IMP-089).
func TestUserAddDisableList(t *testing.T) {
	vaultDir, stateDir := cliVault(t)
	out := runUser(t, "ada-pass-1234\n", "add", "--vault", vaultDir, "--username", "ada", "--password-stdin")
	if !strings.Contains(out, `Account "ada" (member) added`) || !strings.Contains(out, `Personal project "ada" created`) {
		t.Fatalf("add printed %q", out)
	}
	s := openAccounts(t, stateDir)
	ada, ok := s.UserByUsername("ada")
	if !ok || ada.Role != webauth.RoleMember || !ada.MustChangePassword {
		t.Fatalf("ada = %+v", ada)
	}
	if _, ok := s.UserByUsername("root"); !ok {
		t.Fatal("add removed the owner")
	}
	if st, err := os.Stat(filepath.Join(vaultDir, "ada")); err != nil || !st.IsDir() {
		t.Errorf("personal project folder: %v", err)
	}
	if out := runUser(t, "guest-pass-1234\n", "add", "--vault", vaultDir, "--username", "gus", "--role", "guest", "--password-stdin"); strings.Contains(out, "Personal project") {
		t.Errorf("a guest got a personal project: %q", out)
	}

	tokens, err := auth.Open(filepath.Join(stateDir, "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := tokens.Create("ada-laptop", nil, []string{auth.ScopeRead}, time.Hour, ada.ID); err != nil {
		t.Fatal(err)
	}
	spa, err := auth.OpenSpaTokens(filepath.Join(stateDir, "spa_tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := spa.Create(ada.ID, "test"); err != nil {
		t.Fatal(err)
	}

	out = runUser(t, "", "disable", "--vault", vaultDir, "--username", "ada")
	if !strings.Contains(out, "1 web session(s) ended, 1 MCP token(s) revoked") {
		t.Errorf("disable printed %q", out)
	}
	s = openAccounts(t, stateDir)
	if a, _ := s.UserByUsername("ada"); a.Enabled() {
		t.Error("ada is still enabled")
	}
	if len(s.ListUsers()) != 3 {
		t.Errorf("accounts after disable = %d, want 3", len(s.ListUsers()))
	}

	out = runUser(t, "", "list", "--vault", vaultDir)
	for _, want := range []string{"root", "owner", "ada", "disabled", "gus", "guest", "must change password"} {
		if !strings.Contains(out, want) {
			t.Errorf("list misses %q:\n%s", want, out)
		}
	}
}

// The refusals: disable without saying what, the owner, a role that is
// not member or guest, a taken username.
func TestUserCLI_Refusals(t *testing.T) {
	vaultDir, stateDir := cliVault(t)
	cases := []struct {
		name  string
		stdin string
		args  []string
		want  string
	}{
		{"bare disable", "", []string{"disable", "--vault", vaultDir}, "--username <name>, or remove every account with --all"},
		{"disable the owner", "", []string{"disable", "--vault", vaultDir, "--username", "root"}, "the owner cannot be disabled"},
		{"both forms", "", []string{"disable", "--vault", vaultDir, "--username", "root", "--all"}, "not both"},
		{"add an owner", "x\n", []string{"add", "--vault", vaultDir, "--username", "boss", "--role", "owner", "--password-stdin"}, "there is one owner"},
		{"add a taken username", "x\n", []string{"add", "--vault", vaultDir, "--username", "root", "--password-stdin"}, "already exists"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, ok := runUserChild(t, c.stdin, c.args...)
			if ok || !strings.Contains(out, c.want) {
				t.Errorf("ok=%v, output %q, want %q", ok, out, c.want)
			}
		})
	}
	if len(openAccounts(t, stateDir).ListUsers()) != 1 {
		t.Error("a refused command changed the accounts")
	}
}

// disable --all is the old disable: every account goes.
func TestUserDisableAll(t *testing.T) {
	vaultDir, stateDir := cliVault(t)
	if out, ok := runUserChild(t, "", "disable", "--vault", vaultDir, "--all"); !ok || !strings.Contains(out, "Auth disabled") {
		t.Fatalf("ok=%v %q", ok, out)
	}
	if openAccounts(t, stateDir).Enabled() {
		t.Error("accounts left after --all")
	}
}
