package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// printSummary prints the level-A tables for one results file: accuracy
// and cost per configuration, accuracy per category, and the questions each
// configuration missed at least once. Answers are re-scored with the current
// accept patterns of the questions file.
func printSummary(resultsPath, questionsPath string) error {
	f, err := os.Open(resultsPath)
	if err != nil {
		return err
	}
	defer f.Close()
	var sessions []session
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		var s session
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			return err
		}
		sessions = append(sessions, s)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	qs, err := readQuestions(questionsPath, "")
	if err != nil {
		return err
	}
	// Re-score every answer with the current accept patterns, so a pattern
	// revised after a manual review applies to every stored run alike.
	byQ := map[string]question{}
	for _, q := range qs {
		byQ[q.ID] = q
	}
	var batches []session
	single := sessions[:0]
	for _, s := range sessions {
		if s.Error != "" {
			single = append(single, s) // counted as errors below
			continue
		}
		if len(s.Batch) > 0 {
			s.CorrectN, s.Missing = 0, nil
			for _, id := range s.Batch {
				ok, missing := score(byQ[id], s.Parts[id])
				if ok {
					s.CorrectN++
				}
				for _, m := range missing {
					s.Missing = append(s.Missing, id+": "+m)
				}
			}
			batches = append(batches, s)
			continue
		}
		if q, ok := byQ[s.ID]; ok {
			s.Correct, _ = score(q, s.Answer)
		}
		single = append(single, s)
	}
	sessions = single
	defer printBatches(batches)
	if len(sessions) == 0 {
		return nil
	}
	order := []string{}
	seen := map[string]bool{}
	for _, s := range sessions {
		if !seen[s.Config] {
			seen[s.Config] = true
			order = append(order, s.Config)
		}
	}

	fmt.Printf("Level A — %s, %d sessions\n\n", resultsPath, len(sessions))
	fmt.Println("| Configuration | Sessions | Correct | Turns | Time | Cached input | Cache writes | Output | API-price estimate |")
	fmt.Println("|---|---|---|---|---|---|---|---|---|")
	for _, c := range order {
		var n, ok, turns, ms, cr, cw, out int
		var cost float64
		for _, s := range sessions {
			if s.Config != c || s.Error != "" {
				continue
			}
			n++
			if s.Correct {
				ok++
			}
			turns += s.Turns
			ms += s.DurationMS
			cr += s.CacheRead
			cw += s.CacheWrite
			out += s.OutputTok
			cost += s.CostUSD
		}
		if n == 0 {
			continue
		}
		fmt.Printf("| %s | %d | %.0f%% | %.1f | %.1f s | %.0fk | %.1fk | %.0f | $%.2f |\n", c, n,
			100*float64(ok)/float64(n), float64(turns)/float64(n), float64(ms)/float64(n)/1000,
			float64(cr)/float64(n)/1000, float64(cw)/float64(n)/1000, float64(out)/float64(n), cost)
	}

	cats := []string{}
	catSeen := map[string]bool{}
	for _, q := range qs {
		if !catSeen[q.Category] {
			catSeen[q.Category] = true
			cats = append(cats, q.Category)
		}
	}
	fmt.Println("\nCorrect answers by category:")
	fmt.Println()
	fmt.Println("| Category | " + strings.Join(order, " | ") + " |")
	fmt.Println("|---|" + strings.Repeat("---|", len(order)))
	for _, cat := range cats {
		row := "| " + cat + " |"
		for _, c := range order {
			var n, ok int
			for _, s := range sessions {
				if s.Config == c && s.Category == cat && s.Error == "" {
					n++
					if s.Correct {
						ok++
					}
				}
			}
			if n == 0 {
				row += " — |"
				continue
			}
			row += fmt.Sprintf(" %.0f%% |", 100*float64(ok)/float64(n))
		}
		fmt.Println(row)
	}

	fmt.Println("\nMissed at least once (correct runs / runs):")
	fmt.Println()
	for _, c := range order {
		byID := map[string][2]int{}
		for _, s := range sessions {
			if s.Config != c || s.Error != "" {
				continue
			}
			v := byID[s.ID]
			v[1]++
			if s.Correct {
				v[0]++
			}
			byID[s.ID] = v
		}
		var miss []string
		for id, v := range byID {
			if v[0] < v[1] {
				miss = append(miss, fmt.Sprintf("%s %d/%d", id, v[0], v[1]))
			}
		}
		sort.Strings(miss)
		fmt.Printf("- %s: %s\n", c, strings.Join(miss, ", "))
	}
	printToolUse(sessions, order)

	var errs int
	for _, s := range sessions {
		if s.Error != "" {
			errs++
		}
	}
	if errs > 0 {
		fmt.Printf("\n%d sessions ended in an error and are excluded above.\n", errs)
	}
	return nil
}

// printToolUse prints how each configuration spent its tool calls, for the
// sessions that recorded them: file tools, memory_bootstrap, other MCP
// tools, ToolSearch.
func printToolUse(sessions []session, order []string) {
	header := false
	for _, c := range order {
		var n, file, boot, booted, mcp, search int
		for _, s := range sessions {
			if s.Config != c || s.Error != "" || s.Tools == nil {
				continue
			}
			n++
			for name, k := range s.Tools {
				switch {
				case name == "Read" || name == "Grep" || name == "Glob":
					file += k
				case strings.HasSuffix(name, "__memory_bootstrap"):
					boot += k
				case strings.HasPrefix(name, "mcp__"):
					mcp += k
				case name == "ToolSearch":
					search += k
				}
			}
			if s.Tools["mcp__gosidian__memory_bootstrap"] > 0 {
				booted++
			}
		}
		if n == 0 {
			continue
		}
		if !header {
			fmt.Println("\nTool calls per session (sessions that recorded them):")
			fmt.Println()
			fmt.Println("| Configuration | Sessions | File tools | memory_bootstrap | Other MCP tools | ToolSearch | Sessions with bootstrap |")
			fmt.Println("|---|---|---|---|---|---|---|")
			header = true
		}
		avg := func(v int) float64 { return float64(v) / float64(n) }
		fmt.Printf("| %s | %d | %.1f | %.1f | %.1f | %.1f | %.0f%% |\n", c, n,
			avg(file), avg(boot), avg(mcp), avg(search), 100*avg(booted))
	}
}

// printBatches prints the sessions that answered several questions each:
// accuracy over all their questions, cost per session and per question,
// how many bootstrapped, and the questions they missed.
func printBatches(batches []session) {
	if len(batches) == 0 {
		return
	}
	order := []string{}
	seen := map[string]bool{}
	for _, s := range batches {
		if !seen[s.Config] {
			seen[s.Config] = true
			order = append(order, s.Config)
		}
	}
	fmt.Printf("\nSessions with several questions each, %d sessions:\n\n", len(batches))
	fmt.Println("| Configuration | Sessions | Questions | Correct | Turns | Time | Cached input | Cache writes | Per session | Per question | File tools | MCP calls | With bootstrap |")
	fmt.Println("|---|---|---|---|---|---|---|---|---|---|---|---|---|")
	for _, c := range order {
		var n, qn, ok, turns, ms, cr, cw, file, mcp, booted int
		var cost float64
		for _, s := range batches {
			if s.Config != c {
				continue
			}
			n++
			qn += len(s.Batch)
			ok += s.CorrectN
			turns += s.Turns
			ms += s.DurationMS
			cr += s.CacheRead
			cw += s.CacheWrite
			cost += s.CostUSD
			for name, k := range s.Tools {
				switch {
				case name == "Read" || name == "Grep" || name == "Glob":
					file += k
				case strings.HasPrefix(name, "mcp__"):
					mcp += k
				}
			}
			if s.Tools["mcp__gosidian__memory_bootstrap"] > 0 {
				booted++
			}
		}
		avg := func(v int) float64 { return float64(v) / float64(n) }
		fmt.Printf("| %s | %d | %d | %.0f%% | %.1f | %.1f s | %.0fk | %.1fk | $%.3f | $%.4f | %.1f | %.1f | %.0f%% |\n",
			c, n, qn, 100*float64(ok)/float64(qn), avg(turns), avg(ms)/1000, avg(cr)/1000, avg(cw)/1000,
			cost/float64(n), cost/float64(qn), avg(file), avg(mcp), 100*avg(booted))
	}
	fmt.Println("\nMissed in the sessions with several questions:")
	fmt.Println()
	for _, c := range order {
		var miss []string
		for _, s := range batches {
			if s.Config != c {
				continue
			}
			for _, m := range s.Missing {
				miss = append(miss, s.ID+" "+m)
			}
		}
		fmt.Printf("- %s: %s\n", c, strings.Join(miss, "; "))
	}
}
