// Command base measures gosidian's base (the roadmap's milestones M1 and M2,
// IMP-212 and IMP-213): an image of the server, any version, started in a
// throwaway container on a copy of the benchmark vault and measured from
// outside, the way an agent uses it. Results (level R through
// memory_search), what the base hands an agent (tools and bootstrap),
// latency, start, memory, CPU and sizes; the code of the version from git.
//
//	go run ./bench/base run -image IMAGE [-runs 3] [-large DIR] [-out FILE]
//	go run ./bench/base compare bench/results/base-*.json
//	go run ./bench/base check [-ref bench/base/reference.json] RESULTS
//	go run ./bench/base reference -why WHY RESULTS...
//
// check is the guard (the roadmap's M1.4): it fails when a number is past
// the limit bench/base/reference.json gives it; reference moves that file
// to the medians of new results, in a commit that says why.
//
// It needs the docker CLI and git, and talks to the server on loopback.
// bench/base/run.sh builds it and the large vault in containers and runs it.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = runCmd(os.Args[2:])
	case "compare":
		err = compareCmd(os.Args[2:])
	case "check":
		err = checkCmd(os.Args[2:])
	case "reference":
		err = referenceCmd(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "base:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: base run -image IMAGE [flags] | base compare FILE... | base check [-ref FILE] FILE | base reference -why WHY FILE...")
	os.Exit(2)
}

var tagRef = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	image := fs.String("image", "", "the gosidian image to measure (required)")
	label := fs.String("label", "", "a name for the results (default: the server's version)")
	runs := fs.Int("runs", 3, "containers to measure, each from scratch")
	vaultDir := fs.String("vault", "bench/vault", "the benchmark vault")
	largeDir := fs.String("large", "", "a larger vault (bench/scale) for the start and search at scale; empty: skip")
	qPath := fs.String("questions", "bench/questions.jsonl", "level R questions")
	yPath := fs.String("queries", "bench/queries.jsonl", "level R queries")
	repo := fs.String("repo", ".", "the gosidian repository, for the code at the version")
	ref := fs.String("ref", "", "the git ref of the image's code (default: the version, when it is a tag)")
	out := fs.String("out", "", "results file (default bench/results/base-<date>-<label>.json)")
	var env envList
	fs.Var(&env, "env", "KEY=VALUE for the server, repeatable: a setting to measure against the default")
	fs.Parse(args)
	if *image == "" {
		return fmt.Errorf("-image is required")
	}

	qs, err := readJSONL[question](*qPath)
	if err != nil {
		return err
	}
	ys, err := readJSONL[query](*yPath)
	if err != nil {
		return err
	}
	byID := make(map[string]query, len(ys))
	for _, y := range ys {
		byID[y.ID] = y
	}
	absVault, err := filepath.Abs(*vaultDir)
	if err != nil {
		return err
	}
	absLarge := ""
	if *largeDir != "" {
		if absLarge, err = filepath.Abs(*largeDir); err != nil {
			return err
		}
	}
	n, size := vaultStats(absVault)
	imageID, err := docker("image", "inspect", "--format", "{{.Id}}", *image)
	if err != nil {
		return err
	}
	res := &result{
		Schema: 1, Date: time.Now().Format("2006-01-02"), Image: shortImage(*image), ImageID: imageID, Runs: *runs,
		Machine: machineInfo(),
		Limits:  map[string]string{"cpus": serverCPUs, "memory": serverMemory},
		Vault:   vaultInfo{Notes: n, Bytes: size},
		Env:     env,
	}
	logf := func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }
	b := &bench{image: *image, vaultDir: absVault, largeDir: absLarge, runs: *runs, env: env, questions: qs, queries: byID, res: res, log: logf}
	if err := b.run(); err != nil {
		return err
	}
	res.Machine.LoadAfter = loadAvg()

	res.Label = *label
	if res.Label == "" {
		res.Label = res.Version
	}
	res.Ref = *ref
	if res.Ref == "" && tagRef.MatchString(res.Version) {
		res.Ref = res.Version
	}
	if res.Ref != "" {
		if cs, err := codeAt(*repo, res.Ref); err != nil {
			b.fail("code: %v", err)
		} else {
			res.Code = cs
		}
	}

	if *out == "" {
		*out = filepath.Join("bench", "results", fmt.Sprintf("base-%s-%s.json", res.Date, res.Label))
	}
	data, err := json.MarshalIndent(res, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
		return err
	}
	logf("written %s", *out)
	return printTable(os.Stdout, []*result{res})
}

func compareCmd(files []string) error {
	if len(files) == 0 {
		return fmt.Errorf("compare: no files")
	}
	var rs []*result
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		r := &result{}
		if err := json.Unmarshal(data, r); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		rs = append(rs, r)
	}
	return printTable(os.Stdout, rs)
}

func healthVersion(base string) string {
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(base + "/healthz")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var h struct {
		Version string `json:"version"`
	}
	json.NewDecoder(resp.Body).Decode(&h)
	return h.Version
}

func machineInfo() machine {
	m := machine{CPUs: runtime.NumCPU(), LoadBefore: loadAvg()}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		m.Kernel = strings.TrimSpace(string(b))
	}
	if f, err := os.Open("/proc/cpuinfo"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if k, v, ok := strings.Cut(sc.Text(), ":"); ok && strings.TrimSpace(k) == "model name" {
				m.CPU = strings.TrimSpace(v)
				break
			}
		}
	}
	return m
}

// loadAvg is the host's one-minute load: the measures mean little above a
// few, and the results say what it was.
func loadAvg() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

// shortImage drops the registry and owner from an image reference: the
// results go into the public repository, and the image id says which build.
func shortImage(ref string) string {
	return ref[strings.LastIndex(ref, "/")+1:]
}

// envList collects the repeated -env flags.
type envList []string

func (e *envList) String() string { return strings.Join(*e, ",") }

func (e *envList) Set(v string) error {
	if !strings.Contains(v, "=") {
		return fmt.Errorf("-env %q: want KEY=VALUE", v)
	}
	*e = append(*e, v)
	return nil
}

func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, v)
	}
	return out, sc.Err()
}
