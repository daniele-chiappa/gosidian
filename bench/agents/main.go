// Command agents runs level A of the gosidian benchmark (IMP-096): headless
// Claude Code sessions answer the benchmark questions in one of two
// configurations and every session is recorded with its answer, turns,
// tokens and time.
//
//   - fs: the vault as the working directory, with Read, Grep and Glob only.
//   - gosidian: no file tools, only a gosidian MCP server that serves the
//     same vault (start one, see bench/README.md) and a system prompt that
//     asks for memory_bootstrap first, as the stub in an agent's CLAUDE.md
//     does. Token in GOSIDIAN_BENCH_TOKEN.
//   - gosidian-core: the same with a token created with --tool-profile core
//     (20 tools instead of the full catalogue), in GOSIDIAN_BENCH_TOKEN_CORE.
//   - gosidian-deferred: the full token with Claude Code's ToolSearch tool
//     available, so the client may load the MCP tool schemas on demand
//     instead of all at once.
//   - fs-oriented: the fs setup plus the orientation a gosidian project
//     gives (start from hot.md and README.md, follow frontmatter and
//     wikilinks): what a local read-only mirror of the vault would offer
//     (IMP-102).
//   - mirror: the local read-only mirror of IMP-102 as it ships. The real
//     `gosidian mirror sync` (-gosidian-bin) copies every project of
//     -mirror-projects into .gosidian/mirror/ of the working directory,
//     with _index.md and MIRROR.md; the orientation is the mirror section
//     the real Claude Code hook (-hook) injects. File tools only.
//   - mirror-mcp: the deployed setup: only the current project mirrored,
//     the gosidian MCP server as well (tools loaded on demand through
//     ToolSearch, as Claude Code does with a large catalogue), the stub's
//     "memory_bootstrap first" and the whole context the hook injects at
//     SessionStart (focus excerpt, mirror section, bootstrap reminder).
//     The benchmark server needs allow_local_mirror on the mirrored
//     projects.
//   - hooks-mcp: mirror-mcp with the mirror off: the same tools, stub and
//     hook context (focus excerpt, bootstrap reminder), no local copy. The
//     difference with mirror-mcp is the mirror alone.
//
// -per-session N asks N questions in one session instead of one, as a
// working session looks things up several times after a single bootstrap:
// question i goes to session i mod (questions/N), so each session mixes
// categories, and each answer is scored on its own section of the reply.
//
// Sessions run with --restricted and --strict-mcp-config, so the operator's
// own settings, hooks, CLAUDE.md and MCP servers never reach them. They cost
// model usage: start with a few ids and one run.
//
//	GOSIDIAN_BENCH_TOKEN=... go run ./bench/agents -config both -ids L2,L9 -runs 1
//
// A run stops after three sessions in a row end in an error (usage limit,
// server down), so a quota problem does not turn into a column of failures.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type question struct {
	ID       string   `json:"id"`
	Category string   `json:"category"`
	Question string   `json:"question"`
	Answer   string   `json:"answer"`
	Accept   []string `json:"accept"`
	Review   bool     `json:"review"`
}

// session is one line of the results file.
type session struct {
	ID         string   `json:"id"`
	Category   string   `json:"category"`
	Config     string   `json:"config"`
	Run        int      `json:"run"`
	Model      string   `json:"model"`
	Answer     string   `json:"answer"`
	Correct    bool     `json:"correct"`
	Review     bool     `json:"review,omitempty"` // negative question: check the answer by hand
	Missing    []string `json:"missing,omitempty"`
	Turns      int      `json:"turns"`
	DurationMS int      `json:"duration_ms"`
	InputTok   int      `json:"input_tokens"`
	CacheRead  int      `json:"cache_read_tokens"`
	CacheWrite int      `json:"cache_write_tokens"`
	OutputTok  int      `json:"output_tokens"`
	CostUSD    float64  `json:"cost_usd"` // API-price estimate; on a subscription it is usage, not money
	// Tools counts the tool calls of the session by name (runs from
	// 2026-09-26 on).
	Tools map[string]int `json:"tools,omitempty"`
	// Batch lists the questions of a session run with -per-session N > 1,
	// Parts the section of the reply that answers each one and CorrectN
	// how many were right; ID is then B01, B02…
	Batch    []string          `json:"batch,omitempty"`
	Parts    map[string]string `json:"parts,omitempty"`
	CorrectN int               `json:"correct_n,omitempty"`
	Error    string            `json:"error,omitempty"`
}

const (
	systemCommon = "You are answering a question about a software team's knowledge vault. Use only the vault, never general knowledge. Do not modify anything."
	systemFS     = "The vault is the current working directory: one folder per project (%s), markdown notes with YAML frontmatter."
	systemFSMap  = "It is a gosidian memory: in each project, hot.md is the current focus and README.md the map; memory/ holds architecture, conventions, decisions (ADRs), environments and the glossary; plans/ have a status and an Outcome; skills/ are procedures; docs/ holds bugs, improvements, incidents, meetings and partners; log.md is the chronology. Start from the project's hot.md and README.md, then follow tags in the frontmatter and [[wikilinks]]. When a search finds nothing, try synonyms and the other language (notes are in English and Italian) before concluding the vault has no answer."
	systemMirror = "The vault is mirrored under .gosidian/mirror/ in the current working directory: one folder per project (%s), markdown notes with YAML frontmatter."
	systemMCP    = "The vault is served by the gosidian MCP server (tools mcp__gosidian__*); its projects are %s. As the project instructions say, start with memory_bootstrap({project: \"%s\"}) before searching."
	batchTmpl    = "Answer each of these %d questions.\n\n%s\nFor each question write a line \"### <id>\" (for example \"### %s\"), then the answer in at most two sentences and a line starting with \"Sources:\" that lists the note paths you relied on. If the vault does not contain an answer, say so."
	promptTmpl   = "Question: %s\n\nAnswer in at most two sentences. On a last line starting with \"Sources:\" list the note paths you relied on. If the vault does not contain the answer, say so."
)

// vaultProject and vaultProjects describe the vault in the system prompts;
// the defaults fit bench/vault, a private run on another vault sets them.
var vaultProject, vaultProjects string

func main() {
	config := flag.String("config", "both", "comma-separated configurations (fs, fs-oriented, mirror, mirror-mcp, gosidian, gosidian-core, gosidian-deferred), or both (fs+gosidian) or all (fs+gosidian+gosidian-core)")
	ids := flag.String("ids", "", "comma-separated question ids (default: all)")
	runs := flag.Int("runs", 1, "sessions per question and configuration")
	model := flag.String("model", "sonnet", "model passed to claude --model")
	vaultDir := flag.String("vault", "bench/vault", "benchmark vault")
	questionsPath := flag.String("questions", "bench/questions.jsonl", "questions")
	mcpURL := flag.String("mcp-url", "http://127.0.0.1:8083/mcp", "gosidian MCP endpoint serving the benchmark vault")
	out := flag.String("out", "", "results file (JSONL, appended); default bench/results/agents-<date>.jsonl")
	timeout := flag.Duration("timeout", 6*time.Minute, "limit per session")
	summary := flag.String("summary", "", "print the tables for a results file instead of running sessions")
	label := flag.String("label", "", "suffix for the configuration names in the results, e.g. lean when the server's project has lean_read_bootstrap on")
	flag.StringVar(&vaultProject, "project", "tidewater", "project the gosidian configurations bootstrap first")
	flag.StringVar(&vaultProjects, "projects", "tidewater, atlas and global", "how the prompts list the vault's projects")
	gosidianBin := flag.String("gosidian-bin", "gosidian", "gosidian binary that syncs the mirror configurations")
	hookPath := flag.String("hook", "contrib/claude-code/gosidian-hook.sh", "Claude Code hook whose SessionStart context the mirror configurations get")
	perSession := flag.Int("per-session", 1, "questions asked in one session (the working-session case: one bootstrap, several lookups)")
	mirrorProjects := flag.String("mirror-projects", "tidewater,atlas,global", "projects the mirror configuration syncs (mirror-mcp syncs -project only)")
	flag.Parse()

	if *summary != "" {
		if err := printSummary(*summary, *questionsPath); err != nil {
			fail(err)
		}
		return
	}

	qs, err := readQuestions(*questionsPath, *ids)
	if err != nil {
		fail(err)
	}
	configs := strings.Split(*config, ",")
	switch *config {
	case "both":
		configs = []string{"fs", "gosidian"}
	case "all":
		configs = []string{"fs", "gosidian", "gosidian-core"}
	}
	tokens := map[string]string{
		"mirror":            os.Getenv("GOSIDIAN_BENCH_TOKEN"),
		"mirror-mcp":        os.Getenv("GOSIDIAN_BENCH_TOKEN"),
		"hooks-mcp":         os.Getenv("GOSIDIAN_BENCH_TOKEN"),
		"gosidian":          os.Getenv("GOSIDIAN_BENCH_TOKEN"),
		"gosidian-core":     os.Getenv("GOSIDIAN_BENCH_TOKEN_CORE"),
		"gosidian-deferred": os.Getenv("GOSIDIAN_BENCH_TOKEN"),
	}
	for _, c := range configs {
		if !strings.HasPrefix(c, "fs") && tokens[c] == "" {
			fail(fmt.Errorf("configuration %s needs its token (GOSIDIAN_BENCH_TOKEN, GOSIDIAN_BENCH_TOKEN_CORE)", c))
		}
	}
	if *out == "" {
		*out = filepath.Join("bench", "results", "agents-"+time.Now().UTC().Format("2006-01-02")+".jsonl")
	}
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fail(err)
	}
	defer f.Close()

	// The mirror configurations share one synced copy per configuration,
	// prepared before the first session and copied into each working
	// directory like the vault is for fs.
	mirrors := map[string]*mirrorSetup{}
	for _, c := range configs {
		var projects []string
		switch c {
		case "mirror":
			projects = strings.Split(*mirrorProjects, ",")
		case "mirror-mcp":
			projects = []string{vaultProject}
		case "hooks-mcp": // the hook's context, nothing to sync
		default:
			continue
		}
		m, err := prepareMirror(c, projects, *gosidianBin, *hookPath, *mcpURL, tokens[c])
		if err != nil {
			fail(fmt.Errorf("%s: %w", c, err))
		}
		defer os.RemoveAll(m.dir)
		mirrors[c] = m
		if len(projects) > 0 {
			fmt.Printf("%s: %s synced into %s\n", c, strings.Join(projects, ", "), m.dir)
		}
	}

	// One question per session, or -per-session questions spread so that
	// each session mixes categories.
	groups := make([][]question, 0, len(qs))
	if *perSession <= 1 {
		for _, q := range qs {
			groups = append(groups, []question{q})
		}
	} else {
		n := (len(qs) + *perSession - 1) / *perSession
		groups = make([][]question, n)
		for i, q := range qs {
			groups[i%n] = append(groups[i%n], q)
		}
	}

	failures := 0
	for r := 1; r <= *runs; r++ {
		for g, group := range groups {
			for _, c := range configs {
				s := runSession(group, g, c, r, *model, *vaultDir, *mcpURL, tokens[c], mirrors[c], *timeout)
				if *label != "" {
					s.Config += "-" + *label
				}
				if s.Error != "" {
					failures++
				} else {
					failures = 0
				}
				line, _ := json.Marshal(s)
				f.Write(append(line, '\n'))
				mark := "✗"
				if s.Correct {
					mark = "✓"
				}
				if len(s.Batch) > 0 {
					mark = fmt.Sprintf("%d/%d", s.CorrectN, len(s.Batch))
				}
				if s.Review {
					mark += "?"
				}
				fmt.Printf("%-4s %-17s run %d %s turns=%-2d %5.1fs in=%d cache=%d out=%d %s\n",
					s.ID, c, r, mark, s.Turns, float64(s.DurationMS)/1000, s.InputTok, s.CacheRead, s.OutputTok, s.Error)
				if failures >= 3 {
					fail(fmt.Errorf("three sessions in a row failed, stopping (last: %s)", s.Error))
				}
			}
		}
	}
}

// runSession runs one headless session in a fresh working directory: one
// question, or the group'th group of several.
func runSession(qs []question, group int, config string, run int, model, vaultDir, mcpURL, token string, mirror *mirrorSetup, timeout time.Duration) session {
	q := qs[0]
	s := session{ID: q.ID, Category: q.Category, Config: config, Run: run, Model: model, Review: q.Review}
	prompt := fmt.Sprintf(promptTmpl, q.Question)
	if len(qs) > 1 {
		s.ID, s.Category = fmt.Sprintf("B%02d", group+1), "batch"
		var list strings.Builder
		for _, q := range qs {
			s.Batch = append(s.Batch, q.ID)
			s.Review = s.Review || q.Review
			fmt.Fprintf(&list, "%s: %s\n", q.ID, q.Question)
		}
		prompt = fmt.Sprintf(batchTmpl, len(qs), list.String(), qs[0].ID)
	}
	work, err := os.MkdirTemp("", "gosidian-bench-a-")
	if err != nil {
		s.Error = err.Error()
		return s
	}
	defer os.RemoveAll(work)

	args := []string{"-p", prompt,
		"--output-format", "stream-json", "--verbose", "--model", model,
		"--restricted", "--strict-mcp-config", "--no-session-persistence",
		"--permission-mode", "dontAsk"}
	switch config {
	case "fs", "fs-oriented":
		if err := copyTree(vaultDir, work); err != nil {
			s.Error = err.Error()
			return s
		}
		system := systemCommon + " " + fmt.Sprintf(systemFS, vaultProjects)
		if config == "fs-oriented" {
			system += " " + systemFSMap
		}
		args = append(args, "--tools", "Read,Grep,Glob", "--allowedTools", "Read,Grep,Glob",
			"--append-system-prompt", system)
	case "mirror":
		if err := copyTree(mirror.dir, work); err != nil {
			s.Error = err.Error()
			return s
		}
		args = append(args, "--tools", "Read,Grep,Glob", "--allowedTools", "Read,Grep,Glob",
			"--append-system-prompt", systemCommon+" "+fmt.Sprintf(systemMirror, vaultProjects)+"\n\n"+mirror.context)
	case "mirror-mcp", "hooks-mcp":
		if err := copyTree(mirror.dir, work); err != nil {
			s.Error = err.Error()
			return s
		}
		cfgPath, err := writeMCPConfig(work, mcpURL, token)
		if err != nil {
			s.Error = err.Error()
			return s
		}
		args = append(args, "--mcp-config", cfgPath,
			"--tools", "Read,Grep,Glob,ToolSearch", "--allowedTools", "Read,Grep,Glob,ToolSearch,mcp__gosidian",
			"--append-system-prompt", systemCommon+" "+fmt.Sprintf(systemMCP, vaultProjects, vaultProject)+"\n\n"+mirror.context)
	case "gosidian", "gosidian-core", "gosidian-deferred":
		cfgPath, err := writeMCPConfig(work, mcpURL, token)
		if err != nil {
			s.Error = err.Error()
			return s
		}
		builtin := "" // no built-in tool: every MCP schema is loaded up front
		if config == "gosidian-deferred" {
			builtin = "ToolSearch"
		}
		args = append(args, "--mcp-config", cfgPath, "--tools", builtin, "--allowedTools", "mcp__gosidian,ToolSearch",
			"--append-system-prompt", systemCommon+" "+fmt.Sprintf(systemMCP, vaultProjects, vaultProject))
	default:
		s.Error = "unknown config " + config
		return s
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = work
	outBuf, err := cmd.Output()
	// stream-json: one event per line; the tool calls are in the assistant
	// messages and the totals in the final result event.
	var res struct {
		Type         string  `json:"type"`
		Result       string  `json:"result"`
		IsError      bool    `json:"is_error"`
		NumTurns     int     `json:"num_turns"`
		DurationMS   int     `json:"duration_ms"`
		TotalCostUSD float64 `json:"total_cost_usd"`
		Usage        struct {
			Input      int `json:"input_tokens"`
			Output     int `json:"output_tokens"`
			CacheRead  int `json:"cache_read_input_tokens"`
			CacheWrite int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	found := false
	for _, line := range strings.Split(string(outBuf), "\n") {
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Name string `json:"name"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "assistant":
			for _, c := range ev.Message.Content {
				if c.Type == "tool_use" {
					if s.Tools == nil {
						s.Tools = map[string]int{}
					}
					s.Tools[c.Name]++
				}
			}
		case "result":
			found = json.Unmarshal([]byte(line), &res) == nil
		}
	}
	if !found {
		s.Error = fmt.Sprintf("claude: %v; output: %.200s", err, outBuf)
		return s
	}
	s.Answer = res.Result
	s.Turns, s.DurationMS, s.CostUSD = res.NumTurns, res.DurationMS, res.TotalCostUSD
	s.InputTok, s.OutputTok = res.Usage.Input, res.Usage.Output
	s.CacheRead, s.CacheWrite = res.Usage.CacheRead, res.Usage.CacheWrite
	if res.IsError {
		s.Error = "session ended with an error"
	}
	if len(qs) == 1 {
		s.Correct, s.Missing = score(q, res.Result)
		return s
	}
	s.Parts = splitAnswers(res.Result, s.Batch)
	for _, q := range qs {
		ok, missing := score(q, s.Parts[q.ID])
		if ok {
			s.CorrectN++
		}
		for _, m := range missing {
			s.Missing = append(s.Missing, q.ID+": "+m)
		}
	}
	s.Correct = s.CorrectN == len(qs)
	return s
}

// batchHeader finds the "### <id>" lines of a reply to several questions,
// tolerating bold and other heading levels.
var batchHeader = regexp.MustCompile(`(?m)^[ \t]*#{1,4}[ \t]*\**[ \t]*([A-Z]+[0-9]+)\b`)

// splitAnswers cuts a reply into the sections that answer each question
// id; a question without its header gets no section and scores wrong.
func splitAnswers(reply string, ids []string) map[string]string {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	parts := map[string]string{}
	locs := batchHeader.FindAllStringSubmatchIndex(reply, -1)
	for i, loc := range locs {
		id := reply[loc[2]:loc[3]]
		if !want[id] {
			continue
		}
		end := len(reply)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		parts[id] += strings.TrimSpace(reply[loc[1]:end])
	}
	return parts
}

// writeMCPConfig writes the --mcp-config file that points a session at the
// benchmark server.
func writeMCPConfig(work, mcpURL, token string) (string, error) {
	cfg := map[string]any{"mcpServers": map[string]any{"gosidian": map[string]any{
		"type": "http", "url": mcpURL, "headers": map[string]string{"Authorization": "Bearer " + token},
	}}}
	buf, _ := json.Marshal(cfg)
	cfgPath := filepath.Join(work, ".mcp-bench.json")
	return cfgPath, os.WriteFile(cfgPath, buf, 0o600)
}

// mirrorSetup is a synced mirror ready to be copied into a working
// directory, with the context the hook gives a session that sees it.
type mirrorSetup struct {
	dir     string // holds .gosidian/mirror/<project>/...
	context string
}

// prepareMirror syncs the projects with the real binary into a fresh
// directory, then runs the real hook's SessionStart there, as Claude Code
// would in a checkout with GOSIDIAN_MIRROR=1. mirror keeps only the
// hook's mirror section (its sessions have no MCP server); mirror-mcp gets
// the whole context; hooks-mcp syncs nothing and runs the hook with the
// mirror off.
func prepareMirror(config string, projects []string, bin, hook, mcpURL, token string) (*mirrorSetup, error) {
	if p, err := exec.LookPath(bin); err == nil {
		bin, _ = filepath.Abs(p)
	}
	hook, _ = filepath.Abs(hook) // the hook runs from the mirror's directory
	dir, err := os.MkdirTemp("", "gosidian-bench-mirror-")
	if err != nil {
		return nil, err
	}
	root := filepath.Join(dir, ".gosidian", "mirror")
	for _, p := range projects {
		cmd := exec.Command(bin, "mirror", "sync", "--url", mcpURL, "--project", strings.TrimSpace(p), "--dir", root)
		cmd.Env = append(os.Environ(), "GOSIDIAN_TOKEN="+token)
		if out, err := cmd.CombinedOutput(); err != nil {
			os.RemoveAll(dir)
			return nil, fmt.Errorf("mirror sync %s: %v: %s", p, err, out)
		}
	}

	mirrorOn := "1"
	if config == "hooks-mcp" {
		mirrorOn = "0"
	}
	// HOME points at the empty directory so the operator's
	// ~/.config/gosidian/hook.env never reaches the hook.
	cmd := exec.Command("bash", hook)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+dir, "CLAUDE_PROJECT_DIR="+dir,
		"GOSIDIAN_URL="+mcpURL, "GOSIDIAN_TOKEN="+token, "GOSIDIAN_PROJECT="+vaultProject,
		"GOSIDIAN_MIRROR="+mirrorOn, "GOSIDIAN_BIN="+bin)
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"SessionStart","source":"startup","session_id":"bench"}`)
	out, err := cmd.Output()
	if err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("hook: %v", err)
	}
	var res struct {
		Output struct {
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out, &res); err != nil || (mirrorOn == "1") != strings.Contains(res.Output.Context, "## Local read-only mirror") {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("hook context does not match GOSIDIAN_MIRROR=%s: %.300s", mirrorOn, out)
	}
	// The hook started a background sync (nothing to fetch); let it
	// release its lock before the directory is copied.
	lock := filepath.Join(root, "."+vaultProject+".lock")
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(lock); os.IsNotExist(err) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	ctx := res.Output.Context
	if config == "mirror" {
		start := strings.Index(ctx, "## Local read-only mirror")
		ctx = ctx[start:]
		if end := strings.Index(ctx[len("## "):], "\n\n## "); end >= 0 {
			ctx = ctx[:len("## ")+end]
		}
		if end := strings.Index(ctx, "\n\n_This excerpt"); end >= 0 {
			ctx = ctx[:end]
		}
	}
	return &mirrorSetup{dir: dir, context: strings.TrimSpace(ctx)}, nil
}

// score checks the answer against every accept pattern (case-insensitive
// regular expressions, all required) and returns the ones that failed.
func score(q question, answer string) (bool, []string) {
	var missing []string
	for _, p := range q.Accept {
		re, err := regexp.Compile("(?i)" + p)
		if err != nil || !re.MatchString(answer) {
			missing = append(missing, p)
		}
	}
	return len(missing) == 0 && answer != "", missing
}

func readQuestions(path, ids string) ([]question, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	want := map[string]bool{}
	for _, id := range strings.Split(ids, ",") {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	var out []question
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var q question
		if err := json.Unmarshal(sc.Bytes(), &q); err != nil {
			return nil, err
		}
		if len(want) == 0 || want[q.ID] {
			out = append(out, q)
		}
	}
	return out, sc.Err()
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		buf, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, buf, info.Mode().Perm()) // a mirror stays read-only
	})
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "agents:", err)
	os.Exit(1)
}
