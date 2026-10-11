package main

import (
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
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/plugins"
	"github.com/gosidian/gosidian/internal/secrets"
)

// The plugins (IMP-168, docs/plugins.md): MCP servers registered in
// [[pipelines.plugins]], whose nodes run once the owner accepted their
// manifest with gosidian plugin accept.

// pluginCheckEvery is how often the server checks its plugins.
const pluginCheckEvery = time.Minute

// newPlugins is the registry of [[pipelines.plugins]].
func newPlugins(pc config.PipelinesConfig, sdir string, sec pipeline.Secrets, logger *slog.Logger) (*plugins.Registry, error) {
	cfgs := make([]plugins.Config, 0, len(pc.Plugins))
	for _, p := range pc.Plugins {
		files := p.FilesURL
		if files == "" {
			files = pc.FilesURL
		}
		cfgs = append(cfgs, plugins.Config{Name: p.Name, URL: p.URL, TokenSecret: p.TokenSecret, Timeout: p.Timeout,
			FilesURL: strings.TrimSuffix(files, "/")})
	}
	// A nil pipeline.Secrets must stay a nil plugins.Secrets.
	var ps plugins.Secrets
	if sec != nil {
		ps = sec
	}
	return plugins.New(cfgs, sdir, ps, version, logger)
}

func runPluginCmd(args []string) {
	if len(args) == 0 {
		pluginUsage()
		os.Exit(2)
	}
	action := args[0]
	if action == "-h" || action == "--help" || action == "help" {
		pluginUsage()
		return
	}
	fs := flag.NewFlagSet("plugin "+action, flag.ExitOnError)
	vaultDir := fs.String("vault", "", "vault directory")
	stateDirFlag := fs.String("state-dir", "", "state dir (default <vault>/.gosidian; env GOSIDIAN_STATE_DIR)")
	pos := parseArgs(fs, args[1:])
	sdir := cliStateDir(*vaultDir, *stateDirFlag)
	cfg, err := config.Load(filepath.Join(sdir, "config.toml"))
	if err == nil {
		err = cfg.ApplyEnv()
	}
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	var sec pipeline.Secrets
	switch s, err := openSecrets(sdir); {
	case err == nil:
		sec = s
	case !errors.Is(err, secrets.ErrNoKey):
		log.Fatalf("secrets: %v", err)
	}
	// The commands print what they found; the registry's log lines are
	// the server's.
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg, err := newPlugins(cfg.Pipelines, sdir, sec, quiet)
	if err != nil {
		log.Fatalf("plugins: %v", err)
	}
	if len(reg.Names()) == 0 {
		fmt.Println("(no plugins in [[pipelines.plugins]] or GOSIDIAN_PIPELINES_PLUGINS)")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	need := func(n int, usage string) {
		if len(pos) != n {
			log.Fatalf("usage: gosidian plugin %s", usage)
		}
	}
	switch action {
	case "list":
		need(0, "list")
		reg.Check(ctx)
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTATUS\tVERSION\tSHA\tNODES\tURL")
		for _, s := range reg.States() {
			sha := "-"
			if len(s.SHA) >= 12 {
				sha = s.SHA[:12]
			}
			nodes := make([]string, 0, len(s.Nodes))
			for _, n := range s.Nodes {
				nodes = append(nodes, n.Type)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, s.Status, orDash(s.Version), sha, orDash(strings.Join(nodes, ",")), s.URL)
		}
		w.Flush()
		for _, s := range reg.States() {
			if s.Error != "" {
				fmt.Printf("\n%s: %s", s.Name, s.Error)
			}
		}
		fmt.Println()
	case "show":
		need(1, "show <name>")
		m, err := reg.Fetch(ctx, pos[0])
		if err != nil {
			log.Fatal(err)
		}
		printManifest(m)
		st, _ := reg.State(pos[0])
		switch sha := m.SHA(); {
		case st.Accepted == nil:
			fmt.Printf("\nNot accepted yet. Its nodes run once accepted:\n  gosidian plugin accept %s %s\n", m.Name, sha[:12])
		case st.Accepted.SHA == sha:
			fmt.Printf("\nAccepted %s by %s.\n", st.Accepted.At.Format("2006-01-02 15:04"), orDash(st.Accepted.By))
		default:
			fmt.Printf("\nThe accepted manifest is another one (version %s, sha %s): the nodes stop until you accept this one:\n  gosidian plugin accept %s %s\n",
				st.Accepted.Manifest.Version, st.Accepted.SHA[:12], m.Name, sha[:12])
		}
	case "accept":
		need(2, "accept <name> <sha>")
		a, err := reg.Accept(ctx, pos[0], pos[1], "cli")
		if err != nil {
			log.Fatal(err)
		}
		nodes := make([]string, 0, len(a.Manifest.Nodes))
		for _, n := range a.Manifest.Nodes {
			nodes = append(nodes, a.Manifest.Name+"."+n.Name)
		}
		fmt.Printf("Accepted %s %s (sha %s). Node types: %s.\n", a.Manifest.Name, a.Manifest.Version, a.SHA[:12], strings.Join(nodes, ", "))
	case "forget":
		need(1, "forget <name>")
		if err := reg.Forget(pos[0]); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("The manifest of %s is no longer accepted: its nodes stop.\n", pos[0])
	default:
		fmt.Fprintf(os.Stderr, "unknown plugin action %q\n\n", action)
		pluginUsage()
		os.Exit(2)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func printManifest(m *plugins.Manifest) {
	fmt.Printf("Plugin:      %s %s\n", m.Name, m.Version)
	if m.Description != "" {
		fmt.Printf("Description: %s\n", m.Description)
	}
	fmt.Printf("SHA:         %s\n", m.SHA())
	files := "no"
	if m.Permissions.Files {
		files = "yes, the files a pipeline hands it"
	}
	network := "none"
	if len(m.Permissions.Network) > 0 {
		network = strings.Join(m.Permissions.Network, ", ")
	}
	fmt.Printf("Reads files: %s\nCalls:       %s (declared: the plugin's container enforces it)\n\nNodes:\n", files, network)
	for _, n := range m.Nodes {
		fmt.Printf("  %s.%s (tool %s)", m.Name, n.Name, n.Tool)
		if len(n.Files) > 0 {
			fmt.Printf(", files in %s", strings.Join(n.Files, ", "))
			if n.FileRefs {
				fmt.Print(", by reference")
			}
		}
		fmt.Println()
		if n.Description != "" {
			fmt.Printf("    %s\n", n.Description)
		}
		if len(n.Input) > 0 {
			fmt.Printf("    settings: %s\n", n.Input)
		}
	}
}

func pluginUsage() {
	fmt.Fprintln(os.Stderr, `Usage: gosidian plugin <action> [options]

Plugins are MCP servers registered in [[pipelines.plugins]] (or
GOSIDIAN_PIPELINES_PLUGINS). Their nodes run once you accepted the
manifest a plugin serves, and stop when it serves another one.

Actions:
  list                 The plugins, their status and node types
  show <name>          The manifest a plugin serves now, and its sha
  accept <name> <sha>  Accept that manifest (the first 12 characters of
                       the sha shown by show)
  forget <name>        Drop the accepted manifest: the nodes stop

Options:
  --vault <dir>      Vault directory (default: $GOSIDIAN_VAULT)
  --state-dir <dir>  State dir (default: <vault>/.gosidian; env GOSIDIAN_STATE_DIR)`)
}
