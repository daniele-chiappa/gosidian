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
	for k := range sessions {
		if q, ok := byQ[sessions[k].ID]; ok && sessions[k].Error == "" {
			sessions[k].Correct, _ = score(q, sessions[k].Answer)
		}
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
