package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/mirror"
)

// defaultMirrorDir is where the mirror lives, relative to the working
// directory: inside it, so an agent's file tools see it without setup, and
// under .gosidian/ so one .gitignore line keeps it out of the repository.
const defaultMirrorDir = ".gosidian/mirror"

// runMirrorCmd implements `gosidian mirror <action>`: a local read-only copy
// of one project, synced over HTTP with an MCP token (IMP-102).
func runMirrorCmd(args []string) {
	if len(args) == 0 {
		mirrorUsage()
		os.Exit(2)
	}
	action, rest := args[0], args[1:]
	switch action {
	case "sync":
		mirrorSync(rest)
	case "purge":
		mirrorPurge(rest)
	case "status":
		mirrorStatus(rest)
	case "-h", "--help", "help":
		mirrorUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown mirror action %q\n\n", action)
		mirrorUsage()
		os.Exit(2)
	}
}

func mirrorUsage() {
	fmt.Fprintln(os.Stderr, `Usage: gosidian mirror <action> [options]

Keeps a read-only copy of one project next to an agent, so it can read and
search files instead of calling MCP. The project admin must enable
"mirror" (allow_local_mirror) on the project. Writes always go through MCP.

Actions:
  sync     Bring the local copy in line with the server
  status   Show the mirrored projects and their last sync
  purge    Delete the copy of a project (or the whole mirror)

Options:
  --url <u>         MCP base URL (env GOSIDIAN_URL), e.g. https://host/mcp
  --project <p>     Project to mirror (env GOSIDIAN_PROJECT)
  --dir <d>         Mirror root (default .gosidian/mirror)
  --env-file <f>    KEY=VALUE file read when the variables are not set
                    (default .claude/gosidian.env, as the Claude Code hooks)

The bearer token comes from GOSIDIAN_TOKEN (read scope). It is never
written to the mirror.`)
}

// mirrorConfig resolves url, project and token: flags, then the environment,
// then the env file.
func mirrorConfig(fs *flag.FlagSet, args []string) (dir, baseURL, project, token string) {
	urlFlag := fs.String("url", "", "MCP base URL")
	projectFlag := fs.String("project", "", "project")
	dirFlag := fs.String("dir", defaultMirrorDir, "mirror root")
	envFile := fs.String("env-file", ".claude/gosidian.env", "KEY=VALUE file")
	_ = fs.Parse(args)
	file := readEnvFile(*envFile)
	pick := func(flagVal, key string) string {
		if flagVal != "" {
			return flagVal
		}
		if v := os.Getenv(key); v != "" {
			return v
		}
		return file[key]
	}
	return *dirFlag, pick(*urlFlag, "GOSIDIAN_URL"), pick(*projectFlag, "GOSIDIAN_PROJECT"), pick("", "GOSIDIAN_TOKEN")
}

func mirrorSync(args []string) {
	fs := flag.NewFlagSet("mirror sync", flag.ExitOnError)
	dir, baseURL, project, token := mirrorConfig(fs, args)
	if baseURL == "" || project == "" || token == "" {
		fmt.Fprintln(os.Stderr, "mirror sync: the MCP URL, the project and GOSIDIAN_TOKEN are required (flags, environment or .claude/gosidian.env)")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	res, err := mirror.Sync(ctx, mirror.Options{BaseURL: baseURL, Token: token, Project: project, Dir: dir})
	if err != nil {
		if errors.Is(err, mirror.ErrLocked) {
			fmt.Fprintln(os.Stderr, "mirror sync:", err)
			os.Exit(0) // a sync is already running: nothing to add
		}
		fmt.Fprintln(os.Stderr, "mirror sync:", err)
		os.Exit(1)
	}
	fmt.Printf("mirror %s: %d notes in %s (%d added, %d updated, %d deleted) in %s\n",
		project, res.Notes, filepath.Join(dir, project), res.Added, res.Updated, res.Deleted, res.Duration.Round(time.Millisecond))
	warnIfTracked(dir)
}

func mirrorStatus(args []string) {
	fs := flag.NewFlagSet("mirror status", flag.ExitOnError)
	dir, _, _, _ := mirrorConfig(fs, args)
	states := mirror.States(dir)
	if len(states) == 0 {
		fmt.Printf("no mirror in %s\n", dir)
		return
	}
	for _, st := range states {
		fmt.Printf("%s\t%d notes\tlast sync %s\tfrom %s\n", st.Project, len(st.ETags), st.SyncedAt, st.Source)
	}
}

func mirrorPurge(args []string) {
	fs := flag.NewFlagSet("mirror purge", flag.ExitOnError)
	dir, _, project, _ := mirrorConfig(fs, args)
	if err := mirror.Purge(dir, project); err != nil {
		fmt.Fprintln(os.Stderr, "mirror purge:", err)
		os.Exit(1)
	}
	if project != "" {
		fmt.Printf("removed the mirror of %s from %s\n", project, dir)
		return
	}
	fmt.Printf("removed %s\n", dir)
}

// readEnvFile parses KEY=VALUE lines (comments and blank lines skipped,
// surrounding quotes dropped); a missing file yields an empty map.
func readEnvFile(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		out[strings.TrimSpace(strings.TrimPrefix(k, "export "))] = strings.Trim(v, `"'`)
	}
	return out
}

// warnIfTracked tells the user when the mirror sits in a git work tree
// without being ignored: the copy must never be committed.
func warnIfTracked(dir string) {
	if _, err := exec.LookPath("git"); err != nil {
		return
	}
	if exec.Command("git", "rev-parse", "--is-inside-work-tree").Run() != nil {
		return // not a repository
	}
	if exec.Command("git", "check-ignore", "-q", dir).Run() != nil {
		fmt.Fprintf(os.Stderr, "warning: %s is not ignored by git — add \".gosidian/\" to .gitignore so the copy is never committed\n", dir)
	}
}
