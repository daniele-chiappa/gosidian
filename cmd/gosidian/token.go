package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
)

// runTokenCmd implements the `gosidian token <action>` subcommand. It opens
// the token store at <vault>/.gosidian/tokens.json and creates / lists /
// revokes tokens. Plaintext tokens are shown only at creation time.
func runTokenCmd(args []string) {
	if len(args) == 0 {
		tokenUsage()
		os.Exit(2)
	}
	action, rest := args[0], args[1:]
	switch action {
	case "create":
		tokenCreate(rest)
	case "list":
		tokenList(rest)
	case "revoke":
		tokenRevoke(rest)
	case "-h", "--help", "help":
		tokenUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown token action %q\n\n", action)
		tokenUsage()
		os.Exit(2)
	}
}

func tokenUsage() {
	fmt.Fprintln(os.Stderr, `Usage: gosidian token <action> [options]

Actions:
  create   Create a new bearer token
  list     List provisioned tokens (no plaintext)
  revoke   Delete a token by id

Common options:
  --vault <dir>   Vault directory (required)

Create options:
  --name <s>              Human label (required)
  --project <s>           Restrict the token to a top-level project (empty = admin)
  --scopes read,write     Comma-separated scopes (default: read,write)
  --ttl <duration>        Expiration, e.g. 720h. 0 = no expiry (default)`)
}

func openStore(vaultDir string) *auth.Store {
	if vaultDir == "" {
		log.Fatal("--vault is required")
	}
	abs, err := filepath.Abs(vaultDir)
	if err != nil {
		log.Fatalf("vault: %v", err)
	}
	path := filepath.Join(abs, ".gosidian", "tokens.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatalf("mkdir: %v", err)
	}
	st, err := auth.Open(path)
	if err != nil {
		log.Fatalf("open token store: %v", err)
	}
	return st
}

func tokenCreate(args []string) {
	fs := flag.NewFlagSet("token create", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	name := fs.String("name", "", "token name")
	project := fs.String("project", "", "restrict to project (empty = admin)")
	scopesCSV := fs.String("scopes", "read,write", "comma-separated scopes")
	ttl := fs.Duration("ttl", 0, "expiration (0 = no expiry)")
	_ = fs.Parse(args)

	store := openStore(*vaultDir)
	scopes := parseScopes(*scopesCSV)

	plaintext, tok, err := store.Create(*name, *project, scopes, *ttl, "")
	if err != nil {
		log.Fatalf("create: %v", err)
	}

	fmt.Printf("Token created.\n\n")
	fmt.Printf("  id:      %s\n", tok.ID)
	fmt.Printf("  name:    %s\n", tok.Name)
	fmt.Printf("  project: %s\n", displayProject(tok.Project))
	fmt.Printf("  scopes:  %s\n", strings.Join(tok.Scopes, ","))
	if !tok.ExpiresAt.IsZero() {
		fmt.Printf("  expires: %s\n", tok.ExpiresAt.Format(time.RFC3339))
	}
	fmt.Printf("\n  Plaintext (shown only once — save it now):\n\n    %s\n\n", plaintext)
	fmt.Printf("Configure Claude Code with:\n    claude mcp add gosidian http://127.0.0.1:8765/sse --transport sse --header \"Authorization: Bearer %s\"\n", plaintext)
}

func tokenList(args []string) {
	fs := flag.NewFlagSet("token list", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	_ = fs.Parse(args)

	store := openStore(*vaultDir)
	tokens := store.List()
	if len(tokens) == 0 {
		fmt.Println("(no tokens — auth disabled)")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tPROJECT\tSCOPES\tCREATED\tEXPIRES")
	for _, t := range tokens {
		exp := "-"
		if !t.ExpiresAt.IsZero() {
			exp = t.ExpiresAt.Format("2006-01-02")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			t.ID, t.Name, displayProject(t.Project),
			strings.Join(t.Scopes, ","),
			t.CreatedAt.Format("2006-01-02"),
			exp)
	}
	_ = w.Flush()
}

func tokenRevoke(args []string) {
	fs := flag.NewFlagSet("token revoke", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	id := fs.String("id", "", "token id (from `token list`)")
	_ = fs.Parse(args)

	store := openStore(*vaultDir)
	if err := store.Revoke(*id); err != nil {
		log.Fatalf("revoke: %v", err)
	}
	fmt.Printf("revoked token %s\n", *id)
}

func parseScopes(csv string) []string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func displayProject(p string) string {
	if p == "" {
		return "(admin)"
	}
	return p
}
