package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	apiv1 "github.com/gosidian/gosidian/internal/api/v1"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/qrsvg"
	"github.com/gosidian/gosidian/internal/statedir"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/gosidian/gosidian/internal/webauth"
	"golang.org/x/term"
)

// runUserCmd implements `gosidian user <action>`: setup / add / disable /
// list / status / totp-reset.
func runUserCmd(args []string) {
	if len(args) == 0 {
		userUsage()
		os.Exit(2)
	}
	action, rest := args[0], args[1:]
	switch action {
	case "setup":
		userSetup(rest)
	case "add":
		userAdd(rest)
	case "disable":
		userDisable(rest)
	case "list":
		userList(rest)
	case "status":
		userStatus(rest)
	case "totp-reset":
		userTOTPReset(rest)
	case "-h", "--help", "help":
		userUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown user action %q\n\n", action)
		userUsage()
		os.Exit(2)
	}
}

func userUsage() {
	fmt.Fprintln(os.Stderr, `Usage: gosidian user <action> [options]

Actions:
  setup       Create the owner account, or reset its password in place
              (same --username; --totp re-enrolls two-factor). --replace
              wipes every account and starts over.
  add         Add a member or guest account, leaving the others alone; it
              must change its password at its first sign-in
  disable     Disable one account (--username): its web sessions end and
              its MCP tokens are revoked. --all removes EVERY account and
              turns the login off.
  list        List the accounts
  status      Show whether the login is on, and the owner
  totp-reset  Clear a user's two-factor secret and recovery codes
              (lost authenticator; works while the server is running)

The commands work with the server running: it re-reads the accounts on
its next request. A fresh install gets its owner "admin" at the first
start, with the password in the log and in <state-dir>/initial-admin-password
(GOSIDIAN_AUTO_OWNER=false turns that off).

Common options:
  --vault <dir>      Vault directory (default: $GOSIDIAN_VAULT)
  --state-dir <dir>  State dir (default <vault>/.gosidian; env GOSIDIAN_STATE_DIR)

Setup options:
  --username <s>     Account username (default: admin)
  --totp             Enable TOTP (prints the QR and the recovery codes)
  --password-stdin   Read password from stdin instead of prompt

Add options:
  --username <s>     Account username (required)
  --role <r>         member (default) or guest
  --password-stdin   Read the first password from stdin instead of prompt

Disable options:
  --username <s>     Account to disable (the owner cannot be)
  --all              Remove every account instead

totp-reset options:
  --username <s>     Account to reset (required)`)
}

// cliStateDir resolves the state dir like `serve` does (ADR-023):
// --state-dir > GOSIDIAN_STATE_DIR > <vault>/.gosidian.
func cliStateDir(vaultDir, stateDirFlag string) string {
	abs, err := filepath.Abs(cliVaultDir(vaultDir))
	if err != nil {
		log.Fatalf("vault: %v", err)
	}
	sdir, _, err := statedir.Resolve(abs, stateDirFlag, os.Getenv(statedir.EnvVar))
	if err != nil {
		log.Fatalf("state dir: %v", err)
	}
	return sdir
}

func openWebauth(vaultDir, stateDirFlag string) *webauth.Store {
	path := filepath.Join(cliStateDir(vaultDir, stateDirFlag), "auth.json")
	store, err := webauth.Open(path)
	if err != nil {
		log.Fatalf("open web auth: %v", err)
	}
	return store
}

func userSetup(args []string) {
	fs := flag.NewFlagSet("user setup", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	stateDirFlag := fs.String("state-dir", "", "state dir (default <vault>/.gosidian; env GOSIDIAN_STATE_DIR)")
	username := fs.String("username", "admin", "account username")
	enableTOTP := fs.Bool("totp", false, "enable TOTP")
	pwStdin := fs.Bool("password-stdin", false, "read password from stdin instead of prompt")
	replace := fs.Bool("replace", false, "wipe EVERY account and start over with this owner (tokens and grants of the removed accounts stop working)")
	_ = fs.Parse(args)

	store := openWebauth(*vaultDir, *stateDirFlag)
	// Refuse before prompting for a password that would be thrown away.
	if !*replace {
		if u, ok := store.UserByUsername(*username); ok && u.Role != webauth.RoleOwner {
			log.Fatalf("setup: account %q exists with role %s, not owner", *username, u.Role)
		}
		if _, ok := store.UserByUsername(*username); !ok && len(store.ListUsers()) > 0 {
			owner := "<owner>"
			if o := store.FirstOwner(); o != nil {
				owner = o.Username
			}
			log.Fatalf("setup: accounts already exist and %q is not one of them.\n"+
				"  To reset the owner's password, keeping every account and token: gosidian user setup --username %s\n"+
				"  To wipe every account and start over: add --replace", *username, owner)
		}
	}

	password, err := readPassword(*pwStdin)
	if err != nil {
		log.Fatalf("read password: %v", err)
	}

	_, existed := store.UserByUsername(*username)
	setup := store.Setup
	if *replace {
		setup = store.Replace
	}
	uri, err := setup(*username, password, *enableTOTP, "Gosidian")
	if err != nil {
		log.Fatalf("setup: %v", err)
	}

	if *enableTOTP {
		fmt.Println()
		fmt.Println("TOTP secret generated. Scan the QR below with your authenticator")
		fmt.Println("(1Password, Bitwarden, Aegis, Google Authenticator, …):")
		fmt.Println()
		if qrStr, qerr := qrsvg.Terminal(uri); qerr == nil {
			fmt.Print(qrStr)
			fmt.Println()
		}
		fmt.Println("Or enter this URI manually:")
		fmt.Println()
		fmt.Println("  " + uri)
		fmt.Println()
		fmt.Println("After adding it, test the first code on the web UI login page.")
		// The owner is the one account nobody else can rescue, so it gets
		// its recovery codes at provisioning time, not only from Settings.
		if owner := store.FirstOwner(); owner != nil {
			codes, cerr := store.GenerateRecoveryCodes(owner.ID)
			if cerr != nil {
				log.Fatalf("recovery codes: %v", cerr)
			}
			printRecoveryCodes(codes)
		}
	}

	if existed && !*replace {
		what := "new password"
		if *enableTOTP {
			what = "new password and TOTP"
		}
		sessions, grants := endOwnerSessions(cliStateDir(*vaultDir, *stateDirFlag), store, *username)
		fmt.Printf("\nOwner %q reset in place: %s; %d web session(s) and %d OAuth grant(s) ended.\n"+
			"Account id, other accounts and MCP tokens unchanged.\n", *username, what, sessions, grants)
		return
	}
	fmt.Printf("\nAccount %q provisioned. Web UI now requires login at /login.\n", *username)
}

// endOwnerSessions revokes the web sessions and OAuth grants of the account
// just reset. The id is kept on purpose (tokens and grants stay valid), so
// nothing else would end a session opened with the old password — the case a
// reset after a leak is for. The running server re-reads both files on their
// next change. Static MCP bearers survive: they are the integrations.
func endOwnerSessions(stateDir string, store *webauth.Store, username string) (sessions, grants int) {
	u, ok := store.UserByUsername(username)
	if !ok {
		return 0, 0
	}
	if spa, err := auth.OpenSpaTokens(filepath.Join(stateDir, "spa_tokens.json")); err != nil {
		log.Printf("warning: web sessions not revoked: %v", err)
	} else {
		sessions = spa.RevokeByUser(u.ID)
	}
	if tokens, err := auth.Open(filepath.Join(stateDir, "tokens.json")); err != nil {
		log.Printf("warning: OAuth grants not revoked: %v", err)
	} else {
		grants = tokens.RevokeOAuthByOwner(u.ID)
	}
	return sessions, grants
}

// printRecoveryCodes shows a freshly minted set once; only hashes are stored.
func printRecoveryCodes(codes []string) {
	fmt.Println()
	fmt.Println("Recovery codes (single-use, shown once — store them somewhere safe;")
	fmt.Println("each one signs you in once if the authenticator is lost):")
	fmt.Println()
	for _, c := range codes {
		fmt.Println("  " + c)
	}
}

// userTOTPReset implements `gosidian user totp-reset --username <u>`: clears
// the account's TOTP secret and recovery codes. Offline escape hatch for a
// lost authenticator — the web UI has the same for the owner, but nobody can
// reset the owner from there. Safe with the server running: it re-reads the
// accounts file on its next request (mtime check).
func userTOTPReset(args []string) {
	fs := flag.NewFlagSet("user totp-reset", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	stateDirFlag := fs.String("state-dir", "", "state dir (default <vault>/.gosidian; env GOSIDIAN_STATE_DIR)")
	username := fs.String("username", "", "account to reset (required)")
	_ = fs.Parse(args)
	if *username == "" {
		log.Fatal("--username is required")
	}

	store := openWebauth(*vaultDir, *stateDirFlag)
	if !store.Enabled() {
		log.Fatal("auth disabled: no accounts provisioned")
	}
	u, ok := store.UserByUsername(*username)
	if !ok {
		log.Fatalf("user %q not found", *username)
	}
	if u.TOTPSec == "" {
		fmt.Printf("User %q has no two-factor secret enrolled; nothing to reset.\n", *username)
		return
	}
	if err := store.ResetTOTP(u.ID); err != nil {
		log.Fatalf("totp-reset: %v", err)
	}
	fmt.Printf("Two-factor reset for %q: secret and recovery codes removed.\n", *username)
	fmt.Println("If the account's policy still requires two-factor, the next login")
	fmt.Println("goes through enrolment again (new QR, new recovery codes).")
}

// userAdd implements `gosidian user add`: a member or guest account, added
// to the others (IMP-089). Like an account the owner creates from the web
// UI, it must change its password at its first sign-in, and a member gets
// its personal project when the server gives them.
func userAdd(args []string) {
	fs := flag.NewFlagSet("user add", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	stateDirFlag := fs.String("state-dir", "", "state dir (default <vault>/.gosidian; env GOSIDIAN_STATE_DIR)")
	username := fs.String("username", "", "account username (required)")
	roleFlag := fs.String("role", string(webauth.RoleMember), "member or guest")
	pwStdin := fs.Bool("password-stdin", false, "read the first password from stdin instead of prompt")
	_ = fs.Parse(args)
	if *username == "" {
		log.Fatal("--username is required")
	}
	role := webauth.Role(*roleFlag)
	switch role {
	case webauth.RoleMember, webauth.RoleGuest:
	case webauth.RoleOwner:
		log.Fatal("there is one owner: `gosidian user setup` creates it or resets its password")
	default:
		log.Fatalf("unknown role %q (member or guest)", *roleFlag)
	}

	stateDir := cliStateDir(*vaultDir, *stateDirFlag)
	store := openWebauth(*vaultDir, *stateDirFlag)
	// Refuse before prompting for a password that would be thrown away.
	if u, ok := store.UserByUsername(*username); ok {
		if u.Enabled() {
			log.Fatalf("add: username %q already exists", *username)
		}
		log.Fatalf("add: username %q belongs to a disabled account; reusing it is done from Admin → Users, which archives the old one", *username)
	}
	password, err := readPassword(*pwStdin)
	if err != nil {
		log.Fatalf("read password: %v", err)
	}
	u, err := store.AddUser(*username, password, role)
	if err != nil {
		log.Fatalf("add: %v", err)
	}
	if err := store.SetMustChangePassword(u.ID, true); err != nil {
		log.Fatalf("add: %v", err)
	}
	fmt.Printf("Account %q (%s) added. It must choose its own password at its first sign-in.\n", u.Username, u.Role)

	if role != webauth.RoleMember {
		return
	}
	ps, err := projects.Open(filepath.Join(stateDir, "projects.json"))
	if err != nil {
		log.Printf("warning: personal project not provisioned: %v", err)
		return
	}
	tokens, err := auth.Open(filepath.Join(stateDir, "tokens.json"))
	if err != nil {
		log.Printf("warning: personal project not provisioned: %v", err)
		return
	}
	abs, err := filepath.Abs(cliVaultDir(*vaultDir))
	if err != nil {
		log.Fatalf("vault: %v", err)
	}
	v := vault.New(abs)
	v.SetStateDir(stateDir)
	switch name, err := apiv1.ProvisionPersonalProject(v, ps, tokens, nil, *u, false); {
	case err != nil:
		log.Printf("warning: personal project not provisioned: %v", err)
	case name != "":
		fmt.Printf("Personal project %q created.\n", name)
	}
}

// userDisable implements `gosidian user disable`. With --username it
// disables that account as Admin → Users does (IMP-089): its web sessions
// end and its MCP tokens are revoked; the owner cannot be disabled. With
// --all it removes every account, which turns the login off. Without
// either it refuses: the bare command used to remove every account.
func userDisable(args []string) {
	fs := flag.NewFlagSet("user disable", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	stateDirFlag := fs.String("state-dir", "", "state dir (default <vault>/.gosidian; env GOSIDIAN_STATE_DIR)")
	username := fs.String("username", "", "account to disable")
	all := fs.Bool("all", false, "remove EVERY account and turn the login off")
	_ = fs.Parse(args)
	switch {
	case *all && *username != "":
		log.Fatal("disable: --username or --all, not both")
	case !*all && *username == "":
		log.Fatal("disable: say which account with --username <name>, or remove every account with --all")
	case *all:
		userDisableAll(*vaultDir, *stateDirFlag)
		return
	}

	store := openWebauth(*vaultDir, *stateDirFlag)
	u, ok := store.UserByUsername(*username)
	if !ok {
		log.Fatalf("user %q not found", *username)
	}
	if u.Role == webauth.RoleOwner {
		log.Fatal("disable: the owner cannot be disabled (to remove every account: --all)")
	}
	if !u.Enabled() {
		fmt.Printf("Account %q is already disabled.\n", *username)
		return
	}
	if err := store.DisableUser(u.ID); err != nil {
		log.Fatalf("disable: %v", err)
	}
	stateDir := cliStateDir(*vaultDir, *stateDirFlag)
	sessions, revoked := 0, 0
	if spa, err := auth.OpenSpaTokens(filepath.Join(stateDir, "spa_tokens.json")); err != nil {
		log.Printf("warning: web sessions not revoked: %v", err)
	} else {
		sessions = spa.RevokeByUser(u.ID)
	}
	if tokens, err := auth.Open(filepath.Join(stateDir, "tokens.json")); err != nil {
		log.Printf("warning: MCP tokens not revoked: %v", err)
	} else {
		revoked = tokens.RevokeByOwner(u.ID)
	}
	fmt.Printf("Account %q disabled: %d web session(s) ended, %d MCP token(s) revoked.\n", *username, sessions, revoked)
}

// userDisableAll is the old `user disable`: every account goes, and with it
// the login.
func userDisableAll(vaultDir, stateDirFlag string) {
	store := openWebauth(vaultDir, stateDirFlag)
	if !store.Enabled() {
		fmt.Println("Auth already disabled.")
		return
	}
	if err := store.Disable(); err != nil {
		log.Fatalf("disable: %v", err)
	}
	fmt.Println("Auth disabled (no users provisioned).")
	fmt.Println("With the default config the web UI is now unusable — every data route")
	fmt.Println("requires a token. Run `gosidian user setup` to provision an owner, or set")
	fmt.Println("GOSIDIAN_OPEN_MODE=readonly to serve an anonymous read-only view of public")
	fmt.Println("projects. At the next start the server creates the owner \"admin\" itself,")
	fmt.Println("unless GOSIDIAN_AUTO_OWNER=false.")
}

// userList implements `gosidian user list`: one line per account.
func userList(args []string) {
	fs := flag.NewFlagSet("user list", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	stateDirFlag := fs.String("state-dir", "", "state dir (default <vault>/.gosidian; env GOSIDIAN_STATE_DIR)")
	_ = fs.Parse(args)

	store := openWebauth(*vaultDir, *stateDirFlag)
	users := store.ListUsers()
	if len(users) == 0 {
		fmt.Println("No accounts.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "USERNAME\tROLE\tSOURCE\tTOTP\tSTATE")
	for _, u := range users {
		source := "local"
		if u.AuthSource == "ldap" {
			source = "ldap"
		}
		totp := "no"
		if u.TOTPSec != "" {
			totp = "yes"
		}
		var state []string
		if !u.Enabled() {
			state = append(state, "disabled")
		}
		if u.MustChangePassword {
			state = append(state, "must change password")
		}
		if len(state) == 0 {
			state = append(state, "active")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", u.Username, u.Role, source, totp, strings.Join(state, ", "))
	}
	_ = w.Flush()
}

func userStatus(args []string) {
	fs := flag.NewFlagSet("user status", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	stateDirFlag := fs.String("state-dir", "", "state dir (default <vault>/.gosidian; env GOSIDIAN_STATE_DIR)")
	_ = fs.Parse(args)

	store := openWebauth(*vaultDir, *stateDirFlag)
	if !store.Enabled() {
		fmt.Println("disabled")
		return
	}
	fmt.Printf("enabled  username=%s  totp=%v\n", store.Username(), store.TOTPEnabled())
}

func readPassword(fromStdin bool) (string, error) {
	if fromStdin {
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			return "", errors.New("no password on stdin")
		}
		return strings.TrimRight(scanner.Text(), "\r\n"), nil
	}

	fmt.Fprint(os.Stderr, "Password: ")
	pw1, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "Confirm:  ")
	pw2, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(pw1) != string(pw2) {
		return "", errors.New("passwords do not match")
	}
	return string(pw1), nil
}
