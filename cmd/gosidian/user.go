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

	"github.com/gosidian/gosidian/internal/qrsvg"
	"github.com/gosidian/gosidian/internal/statedir"
	"github.com/gosidian/gosidian/internal/webauth"
	"golang.org/x/term"
)

// runUserCmd implements `gosidian user <action>`: setup / disable / status.
func runUserCmd(args []string) {
	if len(args) == 0 {
		userUsage()
		os.Exit(2)
	}
	action, rest := args[0], args[1:]
	switch action {
	case "setup":
		userSetup(rest)
	case "disable":
		userDisable(rest)
	case "status":
		userStatus(rest)
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
  setup    Create or replace the web UI account
  disable  Remove the account (web UI becomes open again)
  status   Show current account state

Common options:
  --vault <dir>  Vault directory (required)

Setup options:
  --username <s>     Account username (default: admin)
  --totp             Enable TOTP (prompts for code after setup)
  --password-stdin   Read password from stdin instead of prompt`)
}

func openWebauth(vaultDir, stateDirFlag string) *webauth.Store {
	if vaultDir == "" {
		log.Fatal("--vault is required")
	}
	abs, err := filepath.Abs(vaultDir)
	if err != nil {
		log.Fatalf("vault: %v", err)
	}
	// Same resolution as `serve` (ADR-023): --state-dir > GOSIDIAN_STATE_DIR > <vault>/.gosidian.
	sdir, _, err := statedir.Resolve(abs, stateDirFlag, os.Getenv(statedir.EnvVar))
	if err != nil {
		log.Fatalf("state dir: %v", err)
	}
	path := filepath.Join(sdir, "auth.json")
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
	_ = fs.Parse(args)

	store := openWebauth(*vaultDir, *stateDirFlag)

	password, err := readPassword(*pwStdin)
	if err != nil {
		log.Fatalf("read password: %v", err)
	}

	uri, err := store.Setup(*username, password, *enableTOTP, "Gosidian")
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
	}

	fmt.Printf("\nAccount %q provisioned. Web UI now requires login at /login.\n", *username)
}

func userDisable(args []string) {
	fs := flag.NewFlagSet("user disable", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	stateDirFlag := fs.String("state-dir", "", "state dir (default <vault>/.gosidian; env GOSIDIAN_STATE_DIR)")
	_ = fs.Parse(args)

	store := openWebauth(*vaultDir, *stateDirFlag)
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
	fmt.Println("projects.")
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
