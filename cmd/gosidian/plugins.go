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
	"github.com/gosidian/gosidian/internal/infolder"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/plugins"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/secrets"
	"github.com/gosidian/gosidian/internal/vault"
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
	as := fs.String("as", "", "bind: the folder of the IN folder (default: the source's name)")
	write := fs.Bool("write", false, "bind: gosidian may move files inside the source")
	every := fs.Duration("every", 0, "bind: the time between two checks of the source (default 1m, at least 20s)")
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
		printBindings(reg)
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
			fmt.Printf("\nNot accepted yet. Its nodes run, and its sources may be bound, once accepted:\n  gosidian plugin accept %s %s\n", m.Name, sha[:12])
		case st.Accepted.SHA == sha:
			fmt.Printf("\nAccepted %s by %s.\n", st.Accepted.At.Format("2006-01-02 15:04"), orDash(st.Accepted.By))
		default:
			fmt.Printf("\nThe accepted manifest is another one (version %s, sha %s): the nodes and the sources stop until you accept this one:\n  gosidian plugin accept %s %s\n",
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
		fmt.Printf("Accepted %s %s (sha %s). Node types: %s.\n", a.Manifest.Name, a.Manifest.Version, a.SHA[:12], orDash(strings.Join(nodes, ", ")))
		if len(a.Manifest.Sources) > 0 {
			names := make([]string, 0, len(a.Manifest.Sources))
			for _, src := range a.Manifest.Sources {
				names = append(names, src.Name)
			}
			fmt.Printf("Sources: %s (gosidian plugin bind %s <source> <project>).\n", strings.Join(names, ", "), a.Manifest.Name)
		}
	case "bind":
		need(3, "bind <plugin> <source> <project> [--as <folder>] [--write] [--every 1m]")
		project := pos[2]
		ps, err := projects.Open(filepath.Join(sdir, "projects.json"))
		if err != nil {
			log.Fatal(err)
		}
		// A project without its IN folder yet may be bound: the source
		// shows once use_in is on.
		noIN := !ps.UsesIN(project)
		folder := *as
		if folder == "" {
			folder = pos[1]
		}
		// A local folder of that name with files stays local.
		if local, err := os.ReadDir(filepath.Join(cliVaultDir(*vaultDir), project, vault.INFolder, folder)); err == nil && len(local) > 0 {
			log.Fatalf("the IN folder of %s has a local folder %s with files: bind the source under another name (--as), or move them", project, folder)
		}
		if *every != 0 && *every < infolder.MinSourceEvery {
			log.Fatalf("--every: at least %s", infolder.MinSourceEvery)
		}
		b, err := reg.Bind(infolder.Binding{Project: project, Folder: folder, Plugin: pos[0], Source: pos[1], Write: *write, Every: int(every.Seconds())}, "cli")
		if err != nil {
			log.Fatal(err)
		}
		mode := "read-only"
		if b.Write {
			mode = "files may be moved inside it"
		}
		fmt.Printf("Bound %s to %s as %s/IN/%s/ (%s). The server reads it at its next check.\n", b.ID(), project, project, b.Folder, mode)
		if noIN {
			fmt.Printf("The project has no IN folder yet: the source shows once its use_in is on (the chip IN in the Projects window).\n")
		}
	case "unbind":
		need(2, "unbind <project> <folder>")
		b, err := reg.Unbind(pos[0], pos[1])
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Unbound %s from %s/IN/%s/: the server forgets its files at its next check; the source keeps them.\n", b.ID(), b.Project, b.Folder)
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
	fmt.Printf("Reads files: %s\nCalls:       %s (declared: the plugin's container enforces it)\n", files, network)
	if len(m.Sources) > 0 {
		hosts := "the plugin's own host only"
		if len(m.Permissions.SourceHosts) > 0 {
			hosts = "the plugin's host, " + strings.Join(m.Permissions.SourceHosts, ", ")
		}
		fmt.Printf("Files from:  %s (gosidian reads no source's file from another)\n\nSources:\n", hosts)
		for _, src := range m.Sources {
			var can []string
			if src.Kind != "" {
				can = append(can, src.Kind)
			}
			if src.Changes {
				can = append(can, "changes")
			}
			if src.Move {
				can = append(can, "moves files")
			}
			fmt.Printf("  %s", src.Name)
			if len(can) > 0 {
				fmt.Printf(" (%s)", strings.Join(can, ", "))
			}
			fmt.Println()
			if src.Description != "" {
				fmt.Printf("    %s\n", src.Description)
			}
		}
	}
	if len(m.Nodes) == 0 {
		return
	}
	fmt.Println("\nNodes:")
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
  bind <plugin> <source> <project>
                       Show a source of the plugin in the project's IN
                       folder, as IN/<source>/ (--as another name)
  unbind <project> <folder>
                       Drop that: the source keeps its files

Options:
  --vault <dir>      Vault directory (default: $GOSIDIAN_VAULT)
  --state-dir <dir>  State dir (default: <vault>/.gosidian; env GOSIDIAN_STATE_DIR)
  --as <folder>      bind: the folder of the IN folder
  --write            bind: gosidian may move files inside the source
  --every <d>        bind: the time between two checks (default 1m, at least 20s)`)
}

// printBindings lists the sources bound to IN folders.
func printBindings(reg *plugins.Registry) {
	list, err := reg.Bound()
	if err != nil {
		fmt.Printf("\nThe bound sources: %v\n", err)
		return
	}
	if len(list) == 0 {
		return
	}
	fmt.Println("\nBound sources:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tFOLDER\tSOURCE\tWRITE\tEVERY\tBOUND")
	for _, b := range list {
		mode := "no"
		if b.Write {
			mode = "yes"
		}
		fmt.Fprintf(w, "%s\tIN/%s\t%s\t%s\t%s\t%s %s\n", b.Project, b.Folder, b.ID(), mode, b.Interval(), b.At.Format("2006-01-02 15:04"), orDash(b.By))
	}
	w.Flush()
}
