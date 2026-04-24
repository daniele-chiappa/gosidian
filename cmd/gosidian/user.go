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

func openWebauth(vaultDir string) *webauth.Store {
	if vaultDir == "" {
		log.Fatal("--vault is required")
	}
	abs, err := filepath.Abs(vaultDir)
	if err != nil {
		log.Fatalf("vault: %v", err)
	}
	path := filepath.Join(abs, ".gosidian", "auth.json")
	store, err := webauth.Open(path)
	if err != nil {
		log.Fatalf("open web auth: %v", err)
	}
	return store
}

func userSetup(args []string) {
	fs := flag.NewFlagSet("user setup", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	username := fs.String("username", "admin", "account username")
	enableTOTP := fs.Bool("totp", false, "enable TOTP")
	pwStdin := fs.Bool("password-stdin", false, "read password from stdin instead of prompt")
	_ = fs.Parse(args)

	store := openWebauth(*vaultDir)

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
		fmt.Println("TOTP secret generated. Scan this URI with your authenticator")
		fmt.Println("(1Password, Bitwarden, Aegis, Google Authenticator, …):")
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
	_ = fs.Parse(args)

	store := openWebauth(*vaultDir)
	if !store.Enabled() {
		fmt.Println("Auth already disabled.")
		return
	}
	if err := store.Disable(); err != nil {
		log.Fatalf("disable: %v", err)
	}
	fmt.Println("Auth disabled. Web UI is now open.")
}

func userStatus(args []string) {
	fs := flag.NewFlagSet("user status", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	_ = fs.Parse(args)

	store := openWebauth(*vaultDir)
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
