package main

import (
	"fmt"
	"io"
	"strings"
)

// latencyTools fixes the order of the latency rows.
var latencyTools = []string{"memory_bootstrap", "memory_search", "memory_get", "memory_list_notes", "memory_query", "memory_create", "memory_edit", "memory_append"}

// printTable writes one markdown table with a column per result: the
// medians of the runs, the latency percentiles among them.
func printTable(w io.Writer, rs []*result) error {
	var b strings.Builder
	head := "| |"
	sep := "|---|"
	for _, r := range rs {
		head += " " + r.Label + " |"
		sep += "---|"
	}
	b.WriteString(head + "\n" + sep + "\n")
	row := func(name string, f func(r *result) string) {
		b.WriteString("| " + name + " |")
		for _, r := range rs {
			b.WriteString(" " + f(r) + " |")
		}
		b.WriteString("\n")
	}
	section := func(name string) { row("**"+name+"**", func(*result) string { return "" }) }

	row("Date, runs, load", func(r *result) string {
		return fmt.Sprintf("%s, %d, %.1f→%.1f", r.Date, r.Runs, r.Machine.LoadBefore, r.Machine.LoadAfter)
	})
	for _, r := range rs {
		if len(r.Env) > 0 {
			row("Settings beyond the defaults", func(r *result) string {
				if len(r.Env) == 0 {
					return "—"
				}
				return "`" + strings.Join(r.Env, "` `") + "`"
			})
			break
		}
	}
	section("Results")
	for _, mode := range []string{"query", "any_of"} {
		name := map[string]string{"query": "Search, one query: R@1 · R@5 · MRR · misses", "any_of": "Search with any_of: R@1 · R@5 · MRR · misses"}[mode]
		row(name, func(r *result) string {
			m, ok := r.Retrieval[mode]
			if !ok {
				return "—"
			}
			return fmt.Sprintf("%.2f · %.2f · %.2f · %d", m.R1, m.R5, m.MRR, m.Miss)
		})
	}
	section("What an agent receives")
	row("Tools, full · core", func(r *result) string { return fmt.Sprintf("%d · %d", r.Surface.ToolsFull, r.Surface.ToolsCore) })
	row("tools/list, full · core", func(r *result) string {
		return kb(int64(r.Surface.ToolsListBytesFull)) + " · " + kb(int64(r.Surface.ToolsListBytesCore))
	})
	row("Bootstrap of the project", func(r *result) string { return kb(int64(r.Surface.BootstrapBytes)) })
	row("A search's result, mean", func(r *result) string { return kb(int64(r.Surface.SearchBytes)) })
	section("Speed")
	row("Start, cold · warm", func(r *result) string {
		return fmt.Sprintf("%s · %s", msStr(median(r.Start.ColdMS)), msStr(median(r.Start.WarmMS)))
	})
	for _, t := range latencyTools {
		row(strings.TrimPrefix(t, "memory_")+", p50 · p95", func(r *result) string {
			l, ok := r.Latency[t]
			if !ok || l.N == 0 {
				return "—"
			}
			s := fmt.Sprintf("%s · %s", msStr(l.P50), msStr(l.P95))
			if l.Errors > 0 {
				s += fmt.Sprintf(" (%d errors)", l.Errors)
			}
			return s
		})
	}
	row("Large vault: notes", func(r *result) string {
		if r.Large == nil {
			return "—"
		}
		return fmt.Sprint(r.Large.Notes)
	})
	row("Large vault: start, cold · warm", func(r *result) string {
		if r.Large == nil {
			return "—"
		}
		return fmt.Sprintf("%s · %s", msStr(median(r.Large.ColdMS)), msStr(median(r.Large.WarmMS)))
	})
	row("Large vault: search, p50 · p95", func(r *result) string {
		if r.Large == nil || r.Large.SearchLat.N == 0 {
			return "—"
		}
		return fmt.Sprintf("%s · %s", msStr(r.Large.SearchLat.P50), msStr(r.Large.SearchLat.P95))
	})
	section("Resources")
	row("Memory at rest: own · page cache", func(r *result) string {
		return mib(median(r.Resources.IdleAnon)) + " · " + mib(median(r.Resources.IdleFile))
	})
	row("Memory after the calls: own · peak", func(r *result) string {
		return mib(median(r.Resources.LoadAnon)) + " · " + mib(median(r.Resources.Peak))
	})
	row("Large vault: memory at rest, own", func(r *result) string {
		if r.Large == nil {
			return "—"
		}
		return mib(median(r.Large.IdleAnon))
	})
	row("CPU at rest, % of a core", func(r *result) string { return fmt.Sprintf("%.2f", median(r.Resources.CPUIdlePct)) })
	row("CPU per call", func(r *result) string { return fmt.Sprintf("%.0f µs", median(r.Resources.CPUPerCallUs)) })
	row("Image · binary", func(r *result) string { return mb(r.Sizes.Image) + " · " + mb(r.Sizes.Binary) })
	row("State after the start · index", func(r *result) string { return kb(r.Sizes.State) + " · " + kb(r.Sizes.IndexDB) })
	row("Large vault: index", func(r *result) string {
		if r.Large == nil {
			return "—"
		}
		return mb(r.Large.IndexDB)
	})
	row("Web UI, first load: files · sent · gzipped", func(r *result) string {
		if r.SPA == nil {
			return "—"
		}
		s := fmt.Sprintf("%d · %s · %s", r.SPA.Files, kb(r.SPA.Bytes), kb(r.SPA.GzipBytes))
		if !r.SPA.Served {
			s += " (not compressed by the server)"
		}
		return s
	})
	section("Code")
	row("Go lines: base · branches · tests", func(r *result) string {
		if r.Code == nil {
			return "—"
		}
		return fmt.Sprintf("%s · %s · %s", thousands(r.Code.BaseLines), thousands(r.Code.BranchLines), thousands(r.Code.GoTestLines))
	})
	row("Web UI lines", func(r *result) string {
		if r.Code == nil {
			return "—"
		}
		return thousands(r.Code.WebLines)
	})
	row("Dependencies: Go direct + indirect · npm + dev", func(r *result) string {
		if r.Code == nil {
			return "—"
		}
		return fmt.Sprintf("%d + %d · %d + %d", r.Code.GoDirect, r.Code.GoIndirect, r.Code.NPM, r.Code.NPMDev)
	})
	if errs := countErrors(rs); errs != "" {
		row("Errors", func(r *result) string { return fmt.Sprint(len(r.Errors)) })
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func countErrors(rs []*result) string {
	for _, r := range rs {
		if len(r.Errors) > 0 {
			return "yes"
		}
	}
	return ""
}

func kb(n int64) string { return fmt.Sprintf("%.1f KB", float64(n)/1000) }
func mb(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/1e6) }

func mib(n int64) string { return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20)) }

func msStr(v float64) string {
	if v >= 100 {
		return fmt.Sprintf("%.0f ms", v)
	}
	return fmt.Sprintf("%.2f ms", v)
}

func thousands(n int) string {
	s := fmt.Sprint(n)
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}
