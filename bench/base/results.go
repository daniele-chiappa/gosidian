package main

import (
	"math"
	"sort"
	"time"
)

// result is one file of bench/results/base-*.json: one image measured
// runs times on the same machine. Lists hold one value per run; the
// compare table takes their median.
type result struct {
	Schema    int                `json:"schema"`
	Date      string             `json:"date"`
	Label     string             `json:"label"`
	Image     string             `json:"image"`    // the image's name without its registry
	ImageID   string             `json:"image_id"` // its id: which build it was, wherever it came from
	Version   string             `json:"version"`
	Ref       string             `json:"ref,omitempty"`
	Runs      int                `json:"runs"`
	Env       []string           `json:"env,omitempty"` // settings given to the server, beyond its defaults
	Machine   machine            `json:"machine"`
	Limits    map[string]string  `json:"limits"`
	Vault     vaultInfo          `json:"vault"`
	Start     startTimes         `json:"start"`
	Retrieval map[string]metrics `json:"retrieval,omitempty"`
	Surface   surface            `json:"surface"`
	Latency   map[string]latency `json:"latency"`
	Resources resources          `json:"resources"`
	Sizes     sizes              `json:"sizes"`
	SPA       *spaSize           `json:"spa,omitempty"`
	Large     *large             `json:"large,omitempty"`
	Code      *codeSize          `json:"code,omitempty"`
	Errors    []string           `json:"errors,omitempty"`
}

type machine struct {
	CPU        string  `json:"cpu"`
	CPUs       int     `json:"cpus"`
	Kernel     string  `json:"kernel"`
	LoadBefore float64 `json:"load_before"`
	LoadAfter  float64 `json:"load_after"`
}

type vaultInfo struct {
	Notes int   `json:"notes"`
	Bytes int64 `json:"bytes"`
}

// startTimes are milliseconds from `docker start` to the first /healthz 200.
type startTimes struct {
	ColdMS []float64 `json:"cold_ms"` // empty state: the index from scratch
	WarmMS []float64 `json:"warm_ms"` // a restart on the same state
}

// metrics are the retrieval figures of bench/retrieval, computed the same
// way from memory_search's hits.
type metrics struct {
	N          int                `json:"n"`
	R1         float64            `json:"r_at_1"`
	R5         float64            `json:"r_at_5"`
	MRR        float64            `json:"mrr"`
	Miss       int                `json:"miss"`
	ByCategory map[string]metrics `json:"by_category,omitempty"`
	Ranks      map[string]int     `json:"ranks,omitempty"`
}

// surface is what the base hands an agent before any work: the tools and
// their schemas, and the bootstrap. Bytes, not tokens: a tokenizer is not
// at hand, and bytes compare across versions all the same.
type surface struct {
	ToolsFull          int `json:"tools_full"`
	ToolsCore          int `json:"tools_core"`
	ToolsListBytesFull int `json:"tools_list_bytes_full"`
	ToolsListBytesCore int `json:"tools_list_bytes_core"`
	BootstrapBytes     int `json:"bootstrap_bytes"`       // what the agent reads of memory_bootstrap
	BootstrapReply     int `json:"bootstrap_reply_bytes"` // the whole reply on the wire
	SearchBytes        int `json:"search_bytes"`          // what the agent reads of a search, the mean
}

// latency is one tool's wall time from the client: each run's
// percentiles, and their median across the runs, so that one run the host
// slowed does not move them (until 2026-10-29 they were over all the runs'
// calls together; a file of one run reads the same either way).
type latency struct {
	N      int     `json:"n"`
	P50    float64 `json:"p50_ms"`
	P95    float64 `json:"p95_ms"`
	P99    float64 `json:"p99_ms"`
	Mean   float64 `json:"mean_ms"`
	Errors int     `json:"errors,omitempty"`
}

// resources are the container's cgroup counters, one value per run.
type resources struct {
	IdleAnon     []int64   `json:"idle_anon"`       // after the start, 5 s idle
	IdleFile     []int64   `json:"idle_file"`       // page cache at the same moment
	IdleCurrent  []int64   `json:"idle_current"`    // memory.current at the same moment
	CPUIdlePct   []float64 `json:"cpu_idle_pct"`    // CPU over 10 s of nothing, % of one core
	LoadAnon     []int64   `json:"load_anon"`       // after the latency calls
	Peak         []int64   `json:"peak"`            // memory.peak after them
	CPUPerCallUs []float64 `json:"cpu_per_call_us"` // server CPU during the calls, per call
}

type sizes struct {
	Image   int64 `json:"image"`
	Binary  int64 `json:"binary"`
	State   int64 `json:"state"`    // the state dir after the cold start
	IndexDB int64 `json:"index_db"` // its index.db, when the version has one
}

type spaSize struct {
	Files     int   `json:"files"`
	Bytes     int64 `json:"bytes"`      // as the server sends them, gzipped when it does
	GzipBytes int64 `json:"gzip_bytes"` // the same, gzipped here: what a compressing proxy sends
	Served    bool  `json:"served_gzip"`
}

// large is the same image on a vault grown with bench/scale.
type large struct {
	Notes     int       `json:"notes"`
	ColdMS    []float64 `json:"cold_ms"`
	WarmMS    []float64 `json:"warm_ms"`
	IdleAnon  []int64   `json:"idle_anon"`
	IndexDB   int64     `json:"index_db"`
	State     int64     `json:"state"`
	SearchLat latency   `json:"search"`
}

type codeSize struct {
	Ref         string         `json:"ref"`
	GoLines     int            `json:"go_lines"`
	GoTestLines int            `json:"go_test_lines"`
	BaseLines   int            `json:"base_lines"`   // internal/ and cmd/, without the branches' packages
	BranchLines int            `json:"branch_lines"` // the branches' own packages (see branchPackages)
	Packages    map[string]int `json:"packages"`
	WebLines    int            `json:"web_lines"`
	GoDirect    int            `json:"go_direct"`
	GoIndirect  int            `json:"go_indirect"`
	NPM         int            `json:"npm"`
	NPMDev      int            `json:"npm_dev"`
}

// summarize turns a tool's samples into its percentiles.
func summarize(samples []time.Duration, errors int) latency {
	l := latency{N: len(samples), Errors: errors}
	if len(samples) == 0 {
		return l
	}
	ms := make([]float64, len(samples))
	var sum float64
	for i, d := range samples {
		ms[i] = float64(d.Microseconds()) / 1000
		sum += ms[i]
	}
	sort.Float64s(ms)
	l.P50, l.P95, l.P99 = percentile(ms, 50), percentile(ms, 95), percentile(ms, 99)
	l.Mean = round(sum/float64(len(ms)), 3)
	return l
}

// summarizeRuns is summarize of each run's samples, and the median of
// each figure across the runs; N and Errors count all of them.
func summarizeRuns(runs [][]time.Duration, errors int) latency {
	var p50, p95, p99, mean []float64
	n := 0
	for _, s := range runs {
		if len(s) == 0 {
			continue
		}
		l := summarize(s, 0)
		p50, p95, p99, mean = append(p50, l.P50), append(p95, l.P95), append(p99, l.P99), append(mean, l.Mean)
		n += len(s)
	}
	if n == 0 {
		return latency{Errors: errors}
	}
	return latency{N: n, P50: round(median(p50), 3), P95: round(median(p95), 3), P99: round(median(p99), 3), Mean: round(median(mean), 3), Errors: errors}
}

// percentile of sorted values, nearest rank.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	return round(sorted[max(0, min(i, len(sorted)-1))], 3)
}

func median[T int64 | float64](vs []T) T {
	if len(vs) == 0 {
		return 0
	}
	s := append([]T(nil), vs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

func round(v float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(v*p) / p
}

// aggregate computes the retrieval metrics of ranks (question id → 1-based
// rank of the first gold note, 0 when it is not in the hits).
func aggregate(ranks map[string]int, ids []string) metrics {
	m := metrics{N: len(ids)}
	if m.N == 0 {
		return m
	}
	var r1, r5, rr float64
	for _, id := range ids {
		r := ranks[id]
		switch {
		case r == 0:
			m.Miss++
		case r == 1:
			r1++
			r5++
			rr++
		case r <= 5:
			r5++
			rr += 1 / float64(r)
		default:
			rr += 1 / float64(r)
		}
	}
	n := float64(m.N)
	m.R1, m.R5, m.MRR = round(r1/n, 3), round(r5/n, 3), round(rr/n, 3)
	return m
}
