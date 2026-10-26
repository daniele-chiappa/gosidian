package jobs

import (
	"bytes"
	"encoding/json"
	"math"
	"time"
)

// Spend is what the steps of a team's runs spent (IMP-195): the cost in
// USD, the tokens, and how many steps spent something.
type Spend struct {
	CostUSD float64 `json:"cost_usd"`
	Tokens  int64   `json:"tokens"`
	Steps   int     `json:"steps"`
}

// TeamSpend sums what the steps of the runs of team in project spent,
// those that finished at since or later (IMP-195): the start of the day,
// for a daily budget. A run that ended before since has no such step.
func (s *Store) TeamSpend(project, team string, since time.Time) (Spend, error) {
	var sp Spend
	if team == "" {
		return sp, nil
	}
	// team != '' lets the query read the partial index runs_team.
	err := s.db.QueryRow(`SELECT COALESCE(SUM(s.cost_usd), 0), COALESCE(SUM(s.tokens), 0), COUNT(*)
		FROM runs r JOIN steps s ON s.run_id = r.id
		WHERE r.project = ? AND r.team = ? AND r.team != '' AND (r.finished = 0 OR r.finished >= ?)
		AND s.finished >= ? AND (s.cost_usd > 0 OR s.tokens > 0)`,
		project, team, ms(since), ms(since)).Scan(&sp.CostUSD, &sp.Tokens, &sp.Steps)
	return sp, err
}

// CostOf reads what a step spent from its output (IMP-195): the cost in
// USD and the usage.total_tokens of a JSON object, as the agent-run/1
// contract writes them. Anything else reads 0: another output, a value
// missing, not a number, or below 0.
func CostOf(output []byte) (costUSD float64, tokens int64) {
	output = bytes.TrimLeft(output, " \t\r\n")
	if len(output) == 0 || output[0] != '{' ||
		!bytes.Contains(output, []byte(`"cost"`)) && !bytes.Contains(output, []byte(`"total_tokens"`)) {
		return 0, 0
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(output, &top) != nil {
		return 0, 0
	}
	costUSD, _ = amount(top["cost"])
	var usage map[string]json.RawMessage
	if u := top["usage"]; len(u) > 0 && u[0] == '{' && json.Unmarshal(u, &usage) == nil {
		if n, ok := amount(usage["total_tokens"]); ok && n < math.MaxInt64 {
			tokens = int64(n)
		}
	}
	return costUSD, tokens
}

// amount reads a JSON number, finite and at least 0.
func amount(raw json.RawMessage) (float64, bool) {
	var f float64
	if len(raw) == 0 || json.Unmarshal(raw, &f) != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0, false
	}
	return f, true
}
