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
	Error      string   `json:"error,omitempty"`
}

const (
	systemCommon = "You are answering a question about a software team's knowledge vault. Use only the vault, never general knowledge. Do not modify anything."
	systemFS     = "The vault is the current working directory: one folder per project (tidewater, atlas, global), markdown notes with YAML frontmatter."
	systemFSMap  = "It is a gosidian memory: in each project, hot.md is the current focus and README.md the map; memory/ holds architecture, conventions, decisions (ADRs), environments and the glossary; plans/ have a status and an Outcome; skills/ are procedures; docs/ holds bugs, improvements, incidents, meetings and partners; log.md is the chronology. Start from the project's hot.md and README.md, then follow tags in the frontmatter and [[wikilinks]]. When a search finds nothing, try synonyms and the other language (notes are in English and Italian) before concluding the vault has no answer."
	systemMCP    = "The vault is served by the gosidian MCP server (tools mcp__gosidian__*); its projects are tidewater, atlas and global. As the project instructions say, start with memory_bootstrap({project: \"tidewater\"}) before searching."
	promptTmpl   = "Question: %s\n\nAnswer in at most two sentences. On a last line starting with \"Sources:\" list the note paths you relied on. If the vault does not contain the answer, say so."
)

func main() {
	config := flag.String("config", "both", "comma-separated configurations (fs, fs-oriented, gosidian, gosidian-core, gosidian-deferred), or both (fs+gosidian) or all (fs+gosidian+gosidian-core)")
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

	failures := 0
	for r := 1; r <= *runs; r++ {
		for _, q := range qs {
			for _, c := range configs {
				s := runSession(q, c, r, *model, *vaultDir, *mcpURL, tokens[c], *timeout)
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
				if s.Review {
					mark += "?"
				}
				fmt.Printf("%-4s %-17s run %d %s turns=%-2d %5.1fs in=%d cache=%d out=%d %s\n",
					q.ID, c, r, mark, s.Turns, float64(s.DurationMS)/1000, s.InputTok, s.CacheRead, s.OutputTok, s.Error)
				if failures >= 3 {
					fail(fmt.Errorf("three sessions in a row failed, stopping (last: %s)", s.Error))
				}
			}
		}
	}
}

// runSession runs one headless session in a fresh working directory.
func runSession(q question, config string, run int, model, vaultDir, mcpURL, token string, timeout time.Duration) session {
	s := session{ID: q.ID, Category: q.Category, Config: config, Run: run, Model: model, Review: q.Review}
	work, err := os.MkdirTemp("", "gosidian-bench-a-")
	if err != nil {
		s.Error = err.Error()
		return s
	}
	defer os.RemoveAll(work)

	args := []string{"-p", fmt.Sprintf(promptTmpl, q.Question),
		"--output-format", "json", "--model", model,
		"--restricted", "--strict-mcp-config", "--no-session-persistence",
		"--permission-mode", "dontAsk"}
	switch config {
	case "fs", "fs-oriented":
		if err := copyTree(vaultDir, work); err != nil {
			s.Error = err.Error()
			return s
		}
		system := systemCommon + " " + systemFS
		if config == "fs-oriented" {
			system += " " + systemFSMap
		}
		args = append(args, "--tools", "Read,Grep,Glob", "--allowedTools", "Read,Grep,Glob",
			"--append-system-prompt", system)
	case "gosidian", "gosidian-core", "gosidian-deferred":
		cfg := map[string]any{"mcpServers": map[string]any{"gosidian": map[string]any{
			"type": "http", "url": mcpURL, "headers": map[string]string{"Authorization": "Bearer " + token},
		}}}
		buf, _ := json.Marshal(cfg)
		cfgPath := filepath.Join(work, ".mcp-bench.json")
		if err := os.WriteFile(cfgPath, buf, 0o600); err != nil {
			s.Error = err.Error()
			return s
		}
		builtin := "" // no built-in tool: every MCP schema is loaded up front
		if config == "gosidian-deferred" {
			builtin = "ToolSearch"
		}
		args = append(args, "--mcp-config", cfgPath, "--tools", builtin, "--allowedTools", "mcp__gosidian,ToolSearch",
			"--append-system-prompt", systemCommon+" "+systemMCP)
	default:
		s.Error = "unknown config " + config
		return s
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = work
	outBuf, err := cmd.Output()
	var res struct {
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
	if jerr := json.Unmarshal(outBuf, &res); jerr != nil {
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
	s.Correct, s.Missing = score(q, res.Result)
	return s
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
		return os.WriteFile(target, buf, 0o644)
	})
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "agents:", err)
	os.Exit(1)
}
