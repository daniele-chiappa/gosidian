package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// question and query are the lines of bench/questions.jsonl and
// bench/queries.jsonl that level R reads (see bench/retrieval).
type question struct {
	ID       string   `json:"id"`
	Category string   `json:"category"`
	RGold    []string `json:"r_gold"`
}

type query struct {
	ID    string   `json:"id"`
	Query string   `json:"query"`
	AnyOf []string `json:"any_of"`
}

// The calls of the latency phase. The same counts for every version, so a
// tool's percentiles rest on the same number of samples.
const (
	nBootstrap = 30
	nSearch    = 150
	nGet       = 150
	nList      = 50
	nQuery     = 100
	nWrites    = 40 // of each of create, edit, append
	idleWait   = 5 * time.Second
	idleCPU    = 10 * time.Second
	benchProj  = "tidewater" // the benchmark vault's main project
)

// writesEnv lifts the per-session write limit (60 a minute by default) so
// that the write calls measure the server, not the limiter. A version
// without the setting ignores it; its limiter then shows up as errors.
var writesEnv = []string{"GOSIDIAN_MCP_WRITE_PER_MINUTE=100000"}

type bench struct {
	image     string
	vaultDir  string
	largeDir  string
	runs      int
	env       []string // extra settings for the server, from -env
	questions []question
	queries   map[string]query
	res       *result
	log       func(format string, args ...any)
	samples   map[string][][]time.Duration // a tool's call times, one list per run
	errs      map[string]int
	largeLat  [][]time.Duration
	largeErrs int
}

func (b *bench) fail(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	b.res.Errors = append(b.res.Errors, msg)
	b.log("error: %s", msg)
}

// run measures the image b.runs times, each time in a new container on a
// new copy of the vault, and fills b.res.
func (b *bench) run() error {
	b.samples, b.errs = map[string][][]time.Duration{}, map[string]int{}
	b.largeLat = make([][]time.Duration, b.runs)
	for i := 1; i <= b.runs; i++ {
		b.log("run %d of %d", i, b.runs)
		if err := b.one(i); err != nil {
			return fmt.Errorf("run %d: %w", i, err)
		}
		if b.largeDir != "" {
			if err := b.oneLarge(i); err != nil {
				b.fail("large vault, run %d: %v", i, err)
			}
		}
	}
	b.res.Latency = map[string]latency{}
	for name, s := range b.samples {
		b.res.Latency[name] = summarizeRuns(s, b.errs[name])
	}
	if b.res.Large != nil {
		b.res.Large.SearchLat = summarizeRuns(b.largeLat, b.largeErrs)
	}
	return nil
}

func (b *bench) one(i int) error {
	tag := fmt.Sprintf("gbb-%d-%d", os.Getpid(), i)
	vault, err := newVolume(tag+"-vault", b.vaultDir)
	if err != nil {
		return err
	}
	defer removeVolume(vault)
	state, err := newVolume(tag+"-state", "")
	if err != nil {
		return err
	}
	defer removeVolume(state)
	s := &server{name: tag, image: b.image, vault: vault, state: state}
	if err := s.create(append(append([]string(nil), writesEnv...), b.env...)); err != nil {
		return err
	}
	defer s.remove()

	cold, err := s.start()
	if err != nil {
		return err
	}
	b.res.Start.ColdMS = append(b.res.Start.ColdMS, ms(cold))
	b.log("  cold start %.0f ms", ms(cold))

	full, err := s.token("read,write", "full")
	if err != nil {
		return err
	}
	c, err := newMCPClient(s.url(), full)
	if err != nil {
		return err
	}
	tools, listBytes, err := c.listTools()
	if err != nil {
		return err
	}
	if i == 1 {
		b.res.Version = healthVersion(s.url())
		b.res.Surface.ToolsFull, b.res.Surface.ToolsListBytesFull = len(tools), listBytes
		b.surface(s, c)
		b.retrieval(c, tools)
		b.sizes(s)
		b.res.SPA = measureSPA(s.url(), b.fail)
	}

	time.Sleep(idleWait)
	idle, err := s.cgroup()
	if err != nil {
		return err
	}
	time.Sleep(idleCPU)
	idle2, err := s.cgroup()
	if err != nil {
		return err
	}
	r := &b.res.Resources
	r.IdleAnon, r.IdleFile, r.IdleCurrent = append(r.IdleAnon, idle.Anon), append(r.IdleFile, idle.File), append(r.IdleCurrent, idle.Current)
	r.CPUIdlePct = append(r.CPUIdlePct, round(float64(idle2.CPUUsec-idle.CPUUsec)/float64(idleCPU.Microseconds())*100, 3))

	calls := b.latency(c, tools, i)
	after, err := s.cgroup()
	if err != nil {
		return err
	}
	r.LoadAnon, r.Peak = append(r.LoadAnon, after.Anon), append(r.Peak, after.Peak)
	if calls > 0 {
		r.CPUPerCallUs = append(r.CPUPerCallUs, round(float64(after.CPUUsec-idle2.CPUUsec)/float64(calls), 1))
	}

	if err := s.stop(); err != nil {
		return err
	}
	warm, err := s.start()
	if err != nil {
		return err
	}
	b.res.Start.WarmMS = append(b.res.Start.WarmMS, ms(warm))
	b.log("  warm start %.0f ms", ms(warm))
	return nil
}

// surface measures what an agent receives before it works: the core
// profile's tools, and the bootstrap of the benchmark project.
func (b *bench) surface(s *server, full *mcpClient) {
	if tok, err := s.token("read", "core"); err != nil {
		b.fail("core token: %v", err)
	} else if c, err := newMCPClient(s.url(), tok); err != nil {
		b.fail("core session: %v", err)
	} else if tools, size, err := c.listTools(); err != nil {
		b.fail("core tools/list: %v", err)
	} else {
		b.res.Surface.ToolsCore, b.res.Surface.ToolsListBytesCore = len(tools), size
	}
	raw, size, err := full.rpc("tools/call", map[string]any{"name": "memory_bootstrap", "arguments": map[string]any{"project": benchProj}})
	if err != nil {
		b.fail("bootstrap: %v", err)
		return
	}
	var r toolResult
	if err := json.Unmarshal(raw, &r); err != nil {
		b.fail("bootstrap: %v", err)
		return
	}
	b.res.Surface.BootstrapBytes, b.res.Surface.BootstrapReply = r.agentBytes(), size
}

// retrieval runs level R through memory_search, in the two ways the tool
// takes a question: one query, and the query with its any_of phrasings.
func (b *bench) retrieval(c *mcpClient, tools map[string]toolInfo) {
	_, hasAnyOf := tools["memory_search"].InputSchema.Properties["any_of"]
	modes := []string{"query"}
	if hasAnyOf {
		modes = append(modes, "any_of")
	}
	b.res.Retrieval = map[string]metrics{}
	var searchBytes, searches int
	for _, mode := range modes {
		ranks := map[string]int{}
		var ids []string
		for _, q := range b.questions {
			if len(q.RGold) == 0 {
				continue
			}
			qq, ok := b.queries[q.ID]
			if !ok {
				b.fail("no query for %s", q.ID)
				continue
			}
			args := map[string]any{"query": qq.Query, "limit": 10}
			if mode == "any_of" && len(qq.AnyOf) > 0 {
				args["any_of"] = qq.AnyOf
			}
			r, _, err := c.call("memory_search", args)
			if err != nil {
				b.fail("search %s %s: %v", mode, q.ID, err)
				continue
			}
			if mode == "query" {
				searchBytes += r.agentBytes()
				searches++
			}
			ids = append(ids, q.ID)
			ranks[q.ID] = firstGold(hitPaths(r), q.RGold)
		}
		m := aggregate(ranks, ids)
		m.Ranks = ranks
		m.ByCategory = map[string]metrics{}
		for _, cat := range categoriesOf(b.questions) {
			var cids []string
			for _, q := range b.questions {
				if q.Category == cat && containsStr(ids, q.ID) {
					cids = append(cids, q.ID)
				}
			}
			if len(cids) > 0 {
				m.ByCategory[cat] = aggregate(ranks, cids)
			}
		}
		b.res.Retrieval[mode] = m
		b.log("  retrieval %s: R@1 %.2f R@5 %.2f MRR %.2f misses %d", mode, m.R1, m.R5, m.MRR, m.Miss)
	}
	if searches > 0 {
		b.res.Surface.SearchBytes = searchBytes / searches
	}
}

func hitPaths(r toolResult) []string {
	var out struct {
		Hits []struct {
			Path string `json:"path"`
		} `json:"hits"`
	}
	json.Unmarshal(r.payload(), &out)
	paths := make([]string, len(out.Hits))
	for i, h := range out.Hits {
		paths[i] = h.Path
	}
	return paths
}

func firstGold(paths, gold []string) int {
	for i, p := range paths {
		if containsStr(gold, p) {
			return i + 1
		}
	}
	return 0
}

func (b *bench) sizes(s *server) {
	var err error
	if b.res.Sizes.Image, err = imageSize(b.image); err != nil {
		b.fail("image size: %v", err)
	}
	if b.res.Sizes.Binary, err = s.fileSize("/gosidian"); err != nil {
		b.fail("binary size: %v", err)
	}
	if b.res.Sizes.State, err = s.sizeOf("/data"); err != nil {
		b.fail("state size: %v", err)
	}
	b.res.Sizes.IndexDB, _ = s.fileSize("/data/index.db")
}

// latency times the base's everyday tools, reads first and writes last
// (they add notes). It returns the number of calls made.
func (b *bench) latency(c *mcpClient, tools map[string]toolInfo, run int) int {
	notes := listPaths(c)
	calls := 0
	do := func(name string, n int, args func(k int) map[string]any) {
		if _, ok := tools[name]; !ok {
			return
		}
		for k := 0; k < n; k++ {
			_, took, err := c.call(name, args(k))
			calls++
			if err != nil {
				b.errs[name]++
				if b.errs[name] == 1 {
					b.fail("%s: %v", name, err)
				}
				continue
			}
			if b.samples[name] == nil {
				b.samples[name] = make([][]time.Duration, b.runs)
			}
			b.samples[name][run-1] = append(b.samples[name][run-1], took)
		}
	}
	var qs []query
	for _, q := range b.questions {
		if qq, ok := b.queries[q.ID]; ok {
			qs = append(qs, qq)
		}
	}
	do("memory_bootstrap", nBootstrap, func(int) map[string]any { return map[string]any{"project": benchProj} })
	if len(qs) > 0 {
		do("memory_search", nSearch, func(k int) map[string]any {
			return map[string]any{"query": qs[k%len(qs)].Query, "limit": 10}
		})
	}
	if len(notes) > 0 {
		do("memory_get", nGet, func(k int) map[string]any { return map[string]any{"path": notes[k%len(notes)]} })
	}
	do("memory_list_notes", nList, func(int) map[string]any { return map[string]any{"project": benchProj} })
	do("memory_query", nQuery, func(int) map[string]any {
		return map[string]any{"project": benchProj, "where": []map[string]any{{"field": "type", "op": "exists", "value": true}}}
	})
	path := func(k int) string { return fmt.Sprintf("%s/bench-writes/run%d-note%02d.md", benchProj, run, k) }
	do("memory_create", nWrites, func(k int) map[string]any {
		return map[string]any{"path": path(k), "content": fmt.Sprintf("---\ntitle: Bench note %d\n---\n\n# Bench note %d\n\nstatus: first\n", k, k)}
	})
	do("memory_edit", nWrites, func(k int) map[string]any {
		return map[string]any{"path": path(k), "old_string": "status: first", "new_string": "status: second"}
	})
	do("memory_append", nWrites, func(k int) map[string]any {
		return map[string]any{"path": path(k), "content": "- one more line"}
	})
	return calls
}

// listPaths returns the benchmark project's notes, for memory_get.
func listPaths(c *mcpClient) []string {
	r, _, err := c.call("memory_list_notes", map[string]any{"project": benchProj})
	if err != nil {
		return nil
	}
	var out struct {
		Notes []struct {
			Path string `json:"path"`
		} `json:"notes"`
	}
	json.Unmarshal(r.payload(), &out)
	var paths []string
	for _, n := range out.Notes {
		if strings.HasSuffix(n.Path, ".md") {
			paths = append(paths, n.Path)
		}
	}
	return paths
}

// oneLarge measures the start, the memory and the search on the large vault.
func (b *bench) oneLarge(i int) error {
	tag := fmt.Sprintf("gbb-%d-%d-large", os.Getpid(), i)
	vault, err := newVolume(tag+"-vault", b.largeDir)
	if err != nil {
		return err
	}
	defer removeVolume(vault)
	state, err := newVolume(tag+"-state", "")
	if err != nil {
		return err
	}
	defer removeVolume(state)
	s := &server{name: tag, image: b.image, vault: vault, state: state}
	if err := s.create(b.env); err != nil {
		return err
	}
	defer s.remove()
	cold, err := s.start()
	if err != nil {
		return err
	}
	if b.res.Large == nil {
		b.res.Large = &large{Notes: countNotes(b.largeDir)}
	}
	L := b.res.Large
	L.ColdMS = append(L.ColdMS, ms(cold))
	b.log("  large vault: cold start %.0f ms", ms(cold))
	time.Sleep(idleWait)
	if cg, err := s.cgroup(); err == nil {
		L.IdleAnon = append(L.IdleAnon, cg.Anon)
	}
	if i == 1 {
		L.State, _ = s.sizeOf("/data")
		L.IndexDB, _ = s.fileSize("/data/index.db")
	}
	tok, err := s.token("read", "full")
	if err != nil {
		return err
	}
	c, err := newMCPClient(s.url(), tok)
	if err != nil {
		return err
	}
	var qs []query
	for _, q := range b.questions {
		if qq, ok := b.queries[q.ID]; ok {
			qs = append(qs, qq)
		}
	}
	for k := 0; k < nSearch && len(qs) > 0; k++ {
		_, took, err := c.call("memory_search", map[string]any{"query": qs[k%len(qs)].Query, "limit": 10})
		if err != nil {
			b.largeErrs++
			continue
		}
		b.largeLat[i-1] = append(b.largeLat[i-1], took)
	}
	if err := s.stop(); err != nil {
		return err
	}
	warm, err := s.start()
	if err != nil {
		return err
	}
	L.WarmMS = append(L.WarmMS, ms(warm))
	b.log("  large vault: warm start %.0f ms", ms(warm))
	return nil
}

func ms(d time.Duration) float64 { return round(float64(d.Microseconds())/1000, 1) }

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func categoriesOf(qs []question) []string {
	var out []string
	for _, q := range qs {
		if q.Category != "" && !containsStr(out, q.Category) {
			out = append(out, q.Category)
		}
	}
	return out
}

// countNotes counts the .md files of a vault directory, and their bytes.
func countNotes(dir string) int {
	n, _ := vaultStats(dir)
	return n
}

func vaultStats(dir string) (int, int64) {
	var n int
	var size int64
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") && p != dir {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			n++
			if info, err := d.Info(); err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	return n, size
}
