package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/gosidian/gosidian/internal/config"
	"github.com/gosidian/gosidian/internal/infolder"
	"github.com/gosidian/gosidian/internal/jobs"
	mcpsrv "github.com/gosidian/gosidian/internal/mcp"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/secrets"
)

// The pipelines (IMP-166, IMP-167, ADR-035): off unless [pipelines]
// enabled (GOSIDIAN_PIPELINES_ENABLED), and then only for the projects
// with use_pipelines.

// openSecrets opens <state>/secrets.json under GOSIDIAN_SECRETS_KEY; nil
// without a key.
func openSecrets(sdir string) (*secrets.Store, error) {
	raw := os.Getenv(secrets.EnvVar)
	if raw == "" {
		return nil, secrets.ErrNoKey
	}
	key, err := secrets.ParseKey(raw)
	if err != nil {
		return nil, err
	}
	return secrets.Open(filepath.Join(sdir, secrets.FileName), key)
}

// startPipelines opens jobs.db, builds the engine on the MCP server and
// starts the runner and the schedules. The returned stop waits for the
// runner to put down its steps (ctx must be done) and closes jobs.db.
func startPipelines(ctx context.Context, cfg *config.Config, sdir string, mcpServer *mcpsrv.Server, in *infolder.Service) (stop func(time.Duration)) {
	pc := cfg.Pipelines
	loc := time.Local
	tz := pc.Timezone
	if tz == "" {
		tz = cfg.Automations.Timezone
	}
	if tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			log.Fatalf("pipelines.timezone: %v", err)
		}
		loc = l
	}
	dbPath := filepath.Join(sdir, "jobs.db")
	store, err := jobs.Open(dbPath)
	if err != nil {
		log.Fatalf("pipelines: %v", err)
	}
	// A nil *secrets.Store must not become a non-nil interface.
	var sec pipeline.Secrets
	switch s, err := openSecrets(sdir); {
	case err == nil:
		sec = s
	case errors.Is(err, secrets.ErrNoKey):
		log.Printf("pipelines: no %s, the nodes that need a secret fail", secrets.EnvVar)
	default:
		log.Fatalf("secrets: %v", err)
	}
	providers := make([]pipeline.Provider, 0, len(pc.LLM))
	for _, p := range pc.LLM {
		providers = append(providers, pipeline.Provider{Name: p.Name, BaseURL: p.BaseURL, APIKeySecret: p.APIKeySecret, Model: p.Model})
	}
	publicURL := pc.PublicURL
	if publicURL == "" {
		publicURL = cfg.OAuth.Issuer
	}
	runner := jobs.NewRunner(store, jobs.Options{
		Workers:   pc.Workers,
		Lease:     pc.StepTimeout,
		Retention: pc.Retention,
		Logger:    slog.Default(),
	})
	mcpServer.SetPipelinesSwitch(true)
	engine := pipeline.New(mcpServer.PipelineHost(), runner, pipeline.Config{
		MaxInputBytes:  pc.MaxInputBytes,
		MaxOutputBytes: pc.MaxOutputBytes,
		MaxLLMTokens:   pc.MaxLLMTokens,
		MaxWait:        pc.MaxWait,
		HTTPAllowlist:  pc.HTTPAllowlist,
		PublicURL:      strings.TrimSuffix(publicURL, "/"),
		Providers:      providers,
		Location:       loc,
		FileTicketTTL:  pc.StepTimeout,
	}, sec, slog.Default())
	reg, err := newPlugins(pc, sdir, sec, slog.Default())
	if err != nil {
		log.Fatalf("plugins: %v", err)
	}
	engine.Types().Extend(reg)
	// The IN folders (IMP-172): their batches start the in pipelines, the
	// in.* nodes read and move through them.
	engine.SetIN(in)
	in.SetSink(engine)
	mcpServer.SetPipelines(engine)
	mcpServer.SetPlugins(reg)
	if len(reg.Names()) > 0 {
		go reg.Start(ctx, pluginCheckEvery)
	}
	done := make(chan struct{})
	go func() { runner.Start(ctx); close(done) }()
	go engine.StartSchedules(ctx)
	names := make([]string, 0, len(providers))
	for _, p := range providers {
		names = append(names, p.Name)
	}
	log.Printf("pipelines: enabled (%d workers, step timeout %s, retention %s, time zone %s, llm %v, plugins %v, http allowlist %d, state %s)",
		max(pc.Workers, 1), pc.StepTimeout, pc.Retention, loc, names, reg.Names(), len(pc.HTTPAllowlist), dbPath)
	return func(d time.Duration) {
		select {
		case <-done:
		case <-time.After(d):
			log.Printf("pipelines: steps still running at shutdown, the next start takes them back")
		}
		_ = store.Close()
	}
}

// --- gosidian secret ---

func runSecretCmd(args []string) {
	if len(args) == 0 {
		secretUsage()
		os.Exit(2)
	}
	fs := flag.NewFlagSet("secret "+args[0], flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	stateDirFlag := fs.String("state-dir", "", "state dir (default <vault>/.gosidian; env GOSIDIAN_STATE_DIR)")
	action := args[0]
	if action == "-h" || action == "--help" || action == "help" {
		secretUsage()
		return
	}
	pos := parseArgs(fs, args[1:])
	store, err := openSecrets(cliStateDir(*vaultDir, *stateDirFlag))
	if err != nil {
		log.Fatalf("secrets: %v", err)
	}
	switch action {
	case "set":
		if len(pos) != 1 {
			log.Fatal("usage: gosidian secret set <name>   (the value on standard input)")
		}
		value, err := readSecretValue(os.Stdin)
		if err != nil {
			log.Fatalf("secret value: %v", err)
		}
		if err := store.Set(pos[0], value); err != nil {
			log.Fatalf("set: %v", err)
		}
		fmt.Printf("Secret %s saved.\n", pos[0])
	case "list":
		list, err := store.List()
		if err != nil {
			log.Fatalf("list: %v", err)
		}
		if len(list) == 0 {
			fmt.Println("(no secrets)")
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tUPDATED")
		for _, e := range list {
			fmt.Fprintf(w, "%s\t%s\n", e.Name, e.Updated.UTC().Format("2006-01-02 15:04"))
		}
		_ = w.Flush()
	case "delete":
		if len(pos) != 1 {
			log.Fatal("usage: gosidian secret delete <name>")
		}
		if err := store.Delete(pos[0]); err != nil {
			log.Fatalf("delete: %v", err)
		}
		fmt.Printf("Secret %s deleted.\n", pos[0])
	default:
		fmt.Fprintf(os.Stderr, "unknown secret action %q\n\n", action)
		secretUsage()
		os.Exit(2)
	}
}

// parseArgs parses fs from args and returns the positional arguments,
// wherever they sit among the flags: the flag package stops at the first
// one (secret set name --vault /vault), like tokenID does for an id.
func parseArgs(fs *flag.FlagSet, args []string) []string {
	_ = fs.Parse(args)
	var pos []string
	for fs.NArg() > 0 {
		pos = append(pos, fs.Arg(0))
		_ = fs.Parse(fs.Args()[1:])
	}
	return pos
}

// readSecretValue reads a value from r: its first line, so a value never
// sits in the shell history or the process list.
func readSecretValue(r io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(r, 64<<10)).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", errors.New("empty: pipe the value on standard input")
	}
	return line, nil
}

func secretUsage() {
	fmt.Fprintln(os.Stderr, `Usage: gosidian secret <action> [options]

The secrets the pipelines use (API keys of the language models,
headers of http.request), sealed with AES-GCM under GOSIDIAN_SECRETS_KEY
(32 bytes in base64: openssl rand -base64 32) in <state-dir>/secrets.json.

Actions:
  set <name>      Save a secret; the value is read from standard input
                  (echo "$KEY" | gosidian secret set openrouter)
  list            List the secrets (names only, never values)
  delete <name>   Delete a secret

Options:
  --vault <dir>      Vault directory (default: $GOSIDIAN_VAULT)
  --state-dir <dir>  State dir (default: <vault>/.gosidian; env GOSIDIAN_STATE_DIR)`)
}

// --- gosidian pipeline token ---

func runPipelineCmd(args []string) {
	if len(args) < 2 || args[0] != "token" {
		pipelineUsage()
		os.Exit(2)
	}
	action := args[1]
	fs := flag.NewFlagSet("pipeline token "+action, flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	stateDirFlag := fs.String("state-dir", "", "state dir (default <vault>/.gosidian; env GOSIDIAN_STATE_DIR)")
	ref := fs.String("pipeline", "", "the pipeline note, vault-relative (gosidian/pipelines/arrivals.md)")
	name := fs.String("name", "", "who uses the token (the service)")
	pos := parseArgs(fs, args[2:])
	vault := cliVaultDir(*vaultDir)
	store, err := jobs.Open(filepath.Join(cliStateDir(vault, *stateDirFlag), "jobs.db"))
	if err != nil {
		log.Fatalf("jobs: %v", err)
	}
	defer store.Close()
	switch action {
	case "create":
		p := strings.TrimPrefix(strings.TrimSpace(*ref), "/")
		if p == "" || strings.TrimSpace(*name) == "" {
			log.Fatal("usage: gosidian pipeline token create --pipeline <path.md> --name <service>")
		}
		if !strings.HasSuffix(p, ".md") {
			p += ".md"
		}
		if filepath.Clean(p) != p || strings.Contains(p, "..") {
			log.Fatalf("--pipeline %q: a plain vault-relative path", p)
		}
		if st, err := os.Stat(filepath.Join(vault, p)); err != nil || !st.Mode().IsRegular() {
			log.Fatalf("--pipeline %s: no such note in the vault", p)
		}
		tok, secret, err := store.CreateToken(p, strings.TrimSpace(*name), "cli")
		if err != nil {
			log.Fatalf("create: %v", err)
		}
		fmt.Printf("Run token created.\n\n")
		fmt.Printf("  id:       %s\n", tok.ID)
		fmt.Printf("  pipeline: %s\n", tok.Ref)
		fmt.Printf("  name:     %s\n", tok.Name)
		fmt.Printf("  token:    %s\n\n", secret)
		fmt.Println("It starts this pipeline and nothing else (POST /api/v1/pipelines/<path>/runs).")
		fmt.Println("Store it now: it is not shown again.")
	case "list":
		list, err := store.Tokens(strings.TrimSpace(*ref))
		if err != nil {
			log.Fatalf("list: %v", err)
		}
		if len(list) == 0 {
			fmt.Println("(no run tokens)")
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tPIPELINE\tNAME\tCREATED\tLAST USED")
		for _, t := range list {
			used := "-"
			if !t.LastUsed.IsZero() {
				used = t.LastUsed.UTC().Format("2006-01-02 15:04")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.Ref, t.Name, t.Created.UTC().Format("2006-01-02"), used)
		}
		_ = w.Flush()
	case "revoke":
		if len(pos) != 1 {
			log.Fatal("usage: gosidian pipeline token revoke <id>")
		}
		if err := store.RevokeToken(pos[0]); err != nil {
			log.Fatalf("revoke: %v", err)
		}
		fmt.Printf("Run token %s revoked.\n", pos[0])
	default:
		fmt.Fprintf(os.Stderr, "unknown pipeline token action %q\n\n", action)
		pipelineUsage()
		os.Exit(2)
	}
}

func pipelineUsage() {
	fmt.Fprintln(os.Stderr, `Usage: gosidian pipeline token <action> [options]

Run tokens let a service without an account start one pipeline (trigger
on: external or manual) through POST /api/v1/pipelines/<path>/runs, and
do nothing else.

Actions:
  create --pipeline <path.md> --name <service>   Mint a token (shown once)
  list [--pipeline <path.md>]                    List the tokens (no secrets)
  revoke <id>                                    Revoke a token

Options:
  --vault <dir>      Vault directory (default: $GOSIDIAN_VAULT)
  --state-dir <dir>  State dir (default: <vault>/.gosidian; env GOSIDIAN_STATE_DIR)`)
}
