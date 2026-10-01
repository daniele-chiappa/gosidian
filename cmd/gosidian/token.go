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
	"github.com/gosidian/gosidian/internal/statedir"
	"github.com/gosidian/gosidian/internal/webauth"
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
	case "opt-in":
		tokenOptIn(rest)
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
  revoke   Delete a token by id: token revoke <id>, or --id <id>
  opt-in   Toggle self-improve opt-in on existing token(s)

Common options:
  --vault <dir>   Vault directory (default: $GOSIDIAN_VAULT)

Create options:
  --name <s>              Human label (required)
  --project <s>           Restrict the token to one or more top-level projects,
                          comma-separated, e.g. "gosidian" or "agent-a,agent-b"
                          (empty = admin). A multi-project token suits an
                          orchestrator spanning several agent projects.
  --scopes read,write     Comma-separated scopes (default: read,write)
  --ttl <duration>        Expiration, e.g. 720h. 0 = no expiry (default)
  --self-improve          Opt this token in to the self-improvement insight loop
  --tool-profile <s>      MCP tool surface: "full" (default, whole catalogue) or
                          "core" (worker subset — read/write/search/upload/handoff;
                          cuts the per-session schema cost for sub-agents)

Opt-in options:
  --id <s>                Token id from 'token list', also accepted as the
                          argument (mutually exclusive with --all)
  --all                   Apply to every token
  --off                   Withdraw the opt-in instead of granting it`)
}

func openStore(vaultDir, stateDirFlag string) *auth.Store {
	abs, err := filepath.Abs(cliVaultDir(vaultDir))
	if err != nil {
		log.Fatalf("vault: %v", err)
	}
	// Same resolution as `serve` (ADR-023): --state-dir > GOSIDIAN_STATE_DIR > <vault>/.gosidian.
	sdir, _, err := statedir.Resolve(abs, stateDirFlag, os.Getenv(statedir.EnvVar))
	if err != nil {
		log.Fatalf("state dir: %v", err)
	}
	path := filepath.Join(sdir, "tokens.json")
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
	stateDirFlag := fs.String("state-dir", "", "state dir (default <vault>/.gosidian; env GOSIDIAN_STATE_DIR)")
	name := fs.String("name", "", "token name")
	project := fs.String("project", "", "comma-separated project list (empty = admin)")
	scopesCSV := fs.String("scopes", "read,write", "comma-separated scopes")
	ttl := fs.Duration("ttl", 0, "expiration (0 = no expiry)")
	selfImprove := fs.Bool("self-improve", false, "opt this token in to the self-improvement insight loop")
	toolProfile := fs.String("tool-profile", "", "MCP tool surface: full (default) or core")
	_ = fs.Parse(args)

	if !auth.ValidToolProfile(*toolProfile) {
		log.Fatalf("invalid --tool-profile %q (expected core or full)", *toolProfile)
	}

	store := openStore(*vaultDir, *stateDirFlag)
	scopes := splitCSV(*scopesCSV)

	plaintext, tok, err := store.Create(*name, splitCSV(*project), scopes, *ttl, "")
	if err != nil {
		log.Fatalf("create: %v", err)
	}
	if *selfImprove {
		if err := store.SetSelfImproveOptIn(tok.ID, true); err != nil {
			log.Fatalf("set self-improve opt-in: %v", err)
		}
		tok.SelfImproveOptIn = true
	}
	if *toolProfile != "" {
		if err := store.SetToolProfile(tok.ID, *toolProfile); err != nil {
			log.Fatalf("set tool profile: %v", err)
		}
		tok.ToolProfile = *toolProfile
	}

	fmt.Printf("Token created.\n\n")
	fmt.Printf("  id:      %s\n", tok.ID)
	fmt.Printf("  name:    %s\n", tok.Name)
	fmt.Printf("  project: %s\n", tok.ScopeLabel())
	fmt.Printf("  scopes:  %s\n", strings.Join(tok.Scopes, ","))
	if tok.SelfImproveOptIn {
		fmt.Printf("  self-improve: opt-in\n")
	}
	if tok.ToolProfile != "" {
		fmt.Printf("  tool-profile: %s\n", tok.ToolProfile)
	}
	if !tok.ExpiresAt.IsZero() {
		fmt.Printf("  expires: %s\n", tok.ExpiresAt.Format(time.RFC3339))
	}
	fmt.Printf("\n  Plaintext (shown only once — save it now):\n\n    %s\n\n", plaintext)
	fmt.Printf("Configure Claude Code with:\n    claude mcp add gosidian http://127.0.0.1:8080/mcp --transport http --header \"Authorization: Bearer %s\"\n", plaintext)
}

func tokenList(args []string) {
	fs := flag.NewFlagSet("token list", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	stateDirFlag := fs.String("state-dir", "", "state dir (default <vault>/.gosidian; env GOSIDIAN_STATE_DIR)")
	_ = fs.Parse(args)

	store := openStore(*vaultDir, *stateDirFlag)
	tokens := store.List()
	if len(tokens) == 0 {
		fmt.Println("(no tokens — auth disabled)")
		return
	}
	users := map[string]webauth.User{}
	for _, u := range openWebauth(*vaultDir, *stateDirFlag).ListUsers() {
		users[u.ID] = u
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tOWNER\tPROJECT\tSCOPES\tCREATED\tEXPIRES\tSELF-IMPROVE\tPROFILE")
	for _, t := range tokens {
		exp := "-"
		if !t.ExpiresAt.IsZero() {
			exp = t.ExpiresAt.Format("2006-01-02")
		}
		si := "-"
		if t.SelfImproveOptIn {
			si = "opt-in"
		}
		profile := t.ToolProfile
		if profile == "" {
			profile = "full"
		}
		u, found := users[t.OwnerUserID]
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			t.ID, t.Name, ownerLabel(t, u, found), storedScopeLabel(t, u, found),
			strings.Join(t.Scopes, ","),
			t.CreatedAt.Format("2006-01-02"),
			exp, si, profile)
	}
	_ = w.Flush()
}

// ownerLabel names the account a token belongs to: "-" for a CLI token,
// flagged when the account is gone or disabled (the server then refuses it).
func ownerLabel(t auth.Token, u webauth.User, found bool) string {
	switch {
	case t.OwnerUserID == "":
		return "-"
	case !found:
		return t.OwnerUserID + " (missing)"
	case !u.Enabled():
		return u.Username + " (disabled)"
	}
	return u.Username
}

// storedScopeLabel is ScopeLabel for a token as stored rather than as the
// server narrows it per request: an empty project list is admin only for a
// CLI token or one owned by the owner account; any other account's token
// inherits that account's live access (BUG-062).
func storedScopeLabel(t auth.Token, u webauth.User, found bool) string {
	if len(t.ProjectList()) > 0 || t.OwnerUserID == "" || (found && u.Role == webauth.RoleOwner) {
		return t.ScopeLabel()
	}
	return "(inherit)"
}

func tokenRevoke(args []string) {
	fs := flag.NewFlagSet("token revoke", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	stateDirFlag := fs.String("state-dir", "", "state dir (default <vault>/.gosidian; env GOSIDIAN_STATE_DIR)")
	id := fs.String("id", "", "token id (from `token list`)")
	tokenID(fs, args, id)
	if *id == "" {
		log.Fatal("a token id is required: token revoke <id>, or --id <id>")
	}

	store := openStore(*vaultDir, *stateDirFlag)
	if err := store.Revoke(*id); err != nil {
		log.Fatalf("revoke: %v", err)
	}
	fmt.Printf("revoked token %s\n", *id)
}

// tokenID parses fs from args and takes a single positional argument as the
// token id when --id is not given, wherever it sits among the flags (the
// flag package stops at the first positional one). revoke used to ignore a
// positional id and answer token "" not found (IMP-112).
func tokenID(fs *flag.FlagSet, args []string, id *string) {
	_ = fs.Parse(args)
	var positional []string
	for fs.NArg() > 0 {
		positional = append(positional, fs.Arg(0))
		_ = fs.Parse(fs.Args()[1:])
	}
	switch {
	case len(positional) > 1 || (len(positional) == 1 && *id != ""):
		log.Fatalf("unexpected arguments %q: pass one token id, as <id> or --id <id>", positional)
	case len(positional) == 1:
		*id = positional[0]
	}
}

// tokenOptIn enrols or withdraws an existing token (or all of them) from the
// self-improvement insight loop. It reuses Store.SetSelfImproveOptIn, so the
// operator no longer has to hand-edit tokens.json to flip the flag on a token
// that already exists (IMP-051). --id and --all are mutually exclusive.
func tokenOptIn(args []string) {
	fs := flag.NewFlagSet("token opt-in", flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	stateDirFlag := fs.String("state-dir", "", "state dir (default <vault>/.gosidian; env GOSIDIAN_STATE_DIR)")
	id := fs.String("id", "", "token id (from `token list`)")
	all := fs.Bool("all", false, "apply to every token")
	off := fs.Bool("off", false, "withdraw the opt-in instead of granting it")
	tokenID(fs, args, id)

	if (*id == "" && !*all) || (*id != "" && *all) {
		log.Fatal("exactly one of --id or --all is required")
	}

	store := openStore(*vaultDir, *stateDirFlag)
	optIn := !*off

	if *all {
		tokens := store.List()
		if len(tokens) == 0 {
			fmt.Println("(no tokens — auth disabled)")
			return
		}
		for i := range tokens {
			if err := store.SetSelfImproveOptIn(tokens[i].ID, optIn); err != nil {
				log.Fatalf("set self-improve opt-in on %s: %v", tokens[i].ID, err)
			}
		}
		fmt.Printf("self-improve opt-in %s on %d token(s)\n", optInWord(optIn), len(tokens))
		return
	}

	if err := store.SetSelfImproveOptIn(*id, optIn); err != nil {
		log.Fatalf("set self-improve opt-in: %v", err)
	}
	fmt.Printf("self-improve opt-in %s on token %s\n", optInWord(optIn), *id)
}

func optInWord(optIn bool) string {
	if optIn {
		return "granted"
	}
	return "withdrawn"
}

func splitCSV(csv string) []string {
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

// warnDanglingTokenOwners logs, at boot, the tokens whose owning account no
// longer resolves or is disabled. The server refuses them on every request
// (fail closed), so without this line a batch of valid-looking tokens would
// start failing with nothing in the log saying why — typically after the
// owner account was recreated with a new id.
func warnDanglingTokenOwners(tokens *auth.Store, accounts *webauth.Store) {
	users := map[string]webauth.User{}
	for _, u := range accounts.ListUsers() {
		users[u.ID] = u
	}
	var ids []string
	for _, t := range tokens.List() {
		if t.OwnerUserID == "" {
			continue
		}
		if u, ok := users[t.OwnerUserID]; !ok || !u.Enabled() {
			ids = append(ids, t.ID)
		}
	}
	if len(ids) > 0 {
		log.Printf("auth: WARNING %d MCP token(s) belong to a missing or disabled account and are refused: %s (see `gosidian token list`, OWNER column)",
			len(ids), strings.Join(ids, ","))
	}
}
