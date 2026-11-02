package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// question is a line of questions.jsonl, in the format of bench/agents:
// every accept pattern must match the answer (case-insensitive regular
// expressions), and an answer that a reject pattern matches is checked by
// hand.
type question struct {
	ID       string   `json:"id"`
	Category string   `json:"category"`
	Question string   `json:"question"`
	Answer   string   `json:"answer"`
	Accept   []string `json:"accept"`
	Reject   []string `json:"reject,omitempty"`
	Review   bool     `json:"review,omitempty"`
}

// questions asks what a team asks of a backlog: one entry, how many, which
// ones, which is the most, and two whose answer is that there is none. The
// filters of the lists are chosen so that each answer names two to six
// entries, and a list's reject patterns are the entries that miss one
// condition only.
func questions(es []entry) ([]question, error) {
	byID := map[string]entry{}
	for _, e := range es {
		byID[e.ID] = e
	}
	var qs []question
	var errs []string
	add := func(q question) { qs = append(qs, q) }
	need := func(ok bool, what string) bool {
		if !ok {
			errs = append(errs, what)
		}
		return ok
	}

	// One entry.
	if e, ok := first(es[115:], func(e entry) bool { return e.Status == "in-progress" }); need(ok, "HL1") {
		add(question{ID: "HL1", Category: "lookup",
			Question: fmt.Sprintf("What is the status of %s and who owns it?", e.ID),
			Answer:   e.Status + ", owned by " + e.Owner,
			Accept:   []string{statusPat(e.Status), `\b` + e.Owner + `\b`}})
	}
	if e, ok := first(es[60:], func(e entry) bool { return !e.Due.IsZero() && !e.closed() }); need(ok, "HL2") {
		add(question{ID: "HL2", Category: "lookup",
			Question: fmt.Sprintf("When is %s due, and what is its priority?", e.ID),
			Answer:   iso(e.Due) + ", priority " + e.Priority,
			Accept:   []string{datePat(e.Due), `\b` + e.Priority + `\b`}})
	}
	blocked := filter(es, func(e entry) bool { return e.Status == "blocked" })
	if need(len(blocked) > 0, "HL3") {
		e := blocked[len(blocked)-1]
		b := byID[e.BlockedBy]
		add(question{ID: "HL3", Category: "lookup",
			Question: fmt.Sprintf("What is %s waiting for: which entry blocks it, and what is the status of that entry?", e.ID),
			Answer:   b.ID + ", which is " + b.Status,
			Accept:   []string{idPat(b.ID), statusPat(b.Status)}})
	}

	// How many.
	open := func(e entry) bool { return e.Status == "open" }
	urgent := func(e entry) bool { return e.Priority == "high" || e.Priority == "critical" }
	add(count("HC1", "How many entries of the backlog have the status open?", filter(es, open)))
	if a, n, ok := choose(areas, 3, 8, func(a string) []entry {
		return filter(es, func(e entry) bool { return e.Area == a && e.Status == "in-progress" })
	}); need(ok, "HC2") {
		add(count("HC2", fmt.Sprintf("How many entries in the area %s are in progress?", a), n))
	}
	add(count("HC3", "How many entries with priority critical are not closed yet, that is neither done nor rejected?",
		filter(es, func(e entry) bool { return e.Priority == "critical" && !e.closed() })))
	add(count("HC4", "How many entries were closed as done in September 2026?",
		filter(es, func(e entry) bool { return e.Status == "done" && e.Closed.Format("2006-01") == "2026-09" })))
	if o, n, ok := choose(owners, 4, 15, func(o string) []entry {
		return filter(es, func(e entry) bool { return e.Owner == o && !e.closed() })
	}); need(ok, "HC5") {
		add(count("HC5", fmt.Sprintf("How many entries owned by %s are not closed yet, that is neither done nor rejected?", o), n))
	}

	// Which ones.
	add(list("HF1", "Which entries are blocked?", blocked, nil))
	if a, n, ok := choose(areas, 2, 5, func(a string) []entry {
		return filter(es, func(e entry) bool { return e.Area == a && urgent(e) && open(e) })
	}); need(ok, "HF2") {
		add(list("HF2", fmt.Sprintf("Which entries in the area %s have the status open and priority high or critical?", a), n,
			filter(es, func(e entry) bool { return e.Area == a && urgent(e) && !open(e) && !e.closed() })))
	}
	if o, n, ok := choose(owners, 2, 6, func(o string) []entry {
		return filter(es, func(e entry) bool { return e.Owner == o && e.Status == "in-progress" })
	}); need(ok, "HF3") {
		add(list("HF3", fmt.Sprintf("Which entries owned by %s are in progress?", o), n,
			filter(es, func(e entry) bool { return e.Owner == o && (open(e) || e.Status == "blocked") })))
	}
	dueIn := func(e entry, from, to time.Time) bool {
		return !e.Due.IsZero() && !e.Due.Before(from) && !e.Due.After(to)
	}
	var windows []string
	for from := day("2026-10-27"); from.Before(day("2027-01-01")); from = from.AddDate(0, 0, 7) {
		windows = append(windows, iso(from))
	}
	if w, n, ok := choose(windows, 2, 6, func(w string) []entry {
		return filter(es, func(e entry) bool { return !e.closed() && dueIn(e, day(w), day(w).AddDate(0, 0, 13)) })
	}); need(ok, "HF4") {
		from, to := day(w), day(w).AddDate(0, 0, 13)
		add(list("HF4", fmt.Sprintf("Which entries that are not closed yet (neither done nor rejected) are due between %s and %s?", iso(from), iso(to)), n,
			filter(es, func(e entry) bool {
				return e.closed() && dueIn(e, from, to) || !e.closed() && (dueIn(e, from.AddDate(0, 0, -7), from.AddDate(0, 0, -1)) || dueIn(e, to.AddDate(0, 0, 1), to.AddDate(0, 0, 7)))
			})))
	}
	overdue := func(e entry) bool { return !e.closed() && !e.Due.IsZero() && e.Due.Before(today) }
	if p, n, ok := choose([]string{"", "high or critical", "critical"}, 2, 6, func(p string) []entry {
		return filter(es, func(e entry) bool { return overdue(e) && (p == "" || strings.Contains(p, e.Priority)) })
	}); need(ok, "HF5") {
		text := "Which entries that are not closed yet (neither done nor rejected) were past their due date on " + iso(today) + "?"
		if p != "" {
			text = "Which entries with priority " + p + " that are not closed yet (neither done nor rejected) were past their due date on " + iso(today) + "?"
		}
		add(list("HF5", text, n, filter(es, func(e entry) bool {
			return (p == "" || strings.Contains(p, e.Priority)) && !e.Due.IsZero() && e.Due.Before(today) && e.closed()
		})))
	}
	if need(len(blocked) > 1, "HF6") {
		w := blocked[0].BlockedBy
		add(list("HF6", fmt.Sprintf("Which entries are blocked by %s?", w),
			filter(blocked, func(e entry) bool { return e.BlockedBy == w }),
			filter(blocked, func(e entry) bool { return e.BlockedBy != w })))
	}
	if a, n, ok := choose(areas, 2, 4, func(a string) []entry {
		return filter(es, func(e entry) bool { return e.Area == a && e.Status == "rejected" })
	}); need(ok, "HF7") {
		add(list("HF7", fmt.Sprintf("Which entries in the area %s were rejected?", a), n,
			filter(es, func(e entry) bool { return e.Area == a && e.Status == "deferred" })))
	}
	if p, n, ok := choose([]string{"high", "critical", "medium"}, 2, 6, func(p string) []entry {
		return filter(es, func(e entry) bool { return e.Status == "deferred" && e.Priority == p })
	}); need(ok, "HF8") {
		add(list("HF8", fmt.Sprintf("Which deferred entries have priority %s?", p), n,
			filter(es, func(e entry) bool { return e.Status == "deferred" && e.Priority == lower(priorities, p) })))
	}

	// Which is the most.
	if a, n, ok := most(areas, func(a string) int {
		return len(filter(es, func(e entry) bool { return e.Area == a && open(e) }))
	}); need(ok, "HG1") {
		add(question{ID: "HG1", Category: "aggregate",
			Question: "Which area has the most entries with the status open, and how many does it have?",
			Answer:   fmt.Sprintf("%s, with %d", a, n),
			Accept:   []string{`\b` + a + `\b`, countPat(n)}})
	}
	if o, n, ok := most(owners, func(o string) int {
		return len(filter(es, func(e entry) bool { return e.Owner == o && e.Status == "in-progress" }))
	}); need(ok, "HG2") {
		add(question{ID: "HG2", Category: "aggregate",
			Question: "Who owns the most entries in progress, and how many?",
			Answer:   fmt.Sprintf("%s, with %d", o, n),
			Accept:   []string{`\b` + o + `\b`, countPat(n)}})
	}
	if e, ok := extreme(filter(es, func(e entry) bool { return !e.closed() && !e.Due.IsZero() }), func(e entry) time.Time { return e.Due }, false); need(ok, "HG3") {
		add(question{ID: "HG3", Category: "aggregate",
			Question: "Of the entries not closed yet (neither done nor rejected), which one has the earliest due date, and what is that date?",
			Answer:   e.ID + ", due " + iso(e.Due),
			Accept:   []string{idPat(e.ID), datePat(e.Due)}})
	}
	if e, ok := extreme(filter(es, entry.closed), func(e entry) time.Time { return e.Closed }, true); need(ok, "HG4") {
		add(question{ID: "HG4", Category: "aggregate",
			Question: "Which entry was closed most recently, and on what date?",
			Answer:   e.ID + ", closed " + iso(e.Closed),
			Accept:   []string{idPat(e.ID), datePat(e.Closed)}})
	}

	// None.
	add(question{ID: "HN1", Category: "negative",
		Question: fmt.Sprintf("What is the status of IMP-%03d?", entries+25),
		Answer:   "There is no such entry: the backlog ends at " + es[len(es)-1].ID + ".",
		Accept:   []string{`\bno (entry|such|IMP-)|not (in|found|exist|contain|appear|listed|present)|(does|do)(n't| not) (exist|contain|appear|have|include)|there is no|only (goes|runs) (up )?to|ends at`},
		Review:   true})
	blockers := map[string]bool{}
	for _, e := range blocked {
		blockers[e.BlockedBy] = true
	}
	if e, ok := first(es[139:], func(e entry) bool { return open(e) && !blockers[e.ID] }); need(ok, "HN2") {
		q := list("HN2", fmt.Sprintf("Which entries are blocked by %s?", e.ID), nil, blocked)
		q.Category, q.Answer, q.Review = "negative", "None: no entry is blocked by "+e.ID+".", true
		q.Accept = []string{`\bnone\b|\bno (entry|entries|other|item|items|one)\b|\bnot (blocking|block)|nothing|\bno\b.{0,40}\bblock`}
		add(q)
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("no filter gives a fitting answer for %s: change the seed", strings.Join(errs, ", "))
	}
	return qs, nil
}

func count(id, text string, es []entry) question {
	return question{ID: id, Category: "count", Question: text, Answer: fmt.Sprint(len(es)), Accept: []string{countPat(len(es))}}
}

// list asks for the entries of want, every one required, with the near
// misses (at most four) as reject patterns.
func list(id, text string, want, near []entry) question {
	q := question{ID: id, Category: "list", Question: text}
	var names []string
	for _, e := range want {
		names = append(names, e.ID)
		q.Accept = append(q.Accept, idPat(e.ID))
	}
	q.Answer = strings.Join(names, ", ")
	for i, e := range near {
		if i == 4 {
			break
		}
		q.Reject = append(q.Reject, idPat(e.ID))
	}
	return q
}

func filter(es []entry, keep func(entry) bool) []entry {
	var out []entry
	for _, e := range es {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

func first(es []entry, keep func(entry) bool) (entry, bool) {
	for _, e := range es {
		if keep(e) {
			return e, true
		}
	}
	return entry{}, false
}

// choose returns the first option whose entries number lo to hi.
func choose(options []string, lo, hi int, of func(string) []entry) (string, []entry, bool) {
	for _, o := range options {
		if es := of(o); len(es) >= lo && len(es) <= hi {
			return o, es, true
		}
	}
	return "", nil, false
}

// most returns the option with the highest count, if no other ties it.
func most(options []string, count func(string) int) (string, int, bool) {
	best, n, tie := "", -1, false
	for _, o := range options {
		switch c := count(o); {
		case c > n:
			best, n, tie = o, c, false
		case c == n:
			tie = true
		}
	}
	return best, n, !tie && n > 0
}

// extreme returns the entry with the latest (or earliest) date, if no other
// shares it.
func extreme(es []entry, date func(entry) time.Time, latest bool) (entry, bool) {
	if len(es) == 0 {
		return entry{}, false
	}
	slices.SortStableFunc(es, func(a, b entry) int {
		if latest {
			return date(b).Compare(date(a))
		}
		return date(a).Compare(date(b))
	})
	return es[0], len(es) == 1 || !date(es[1]).Equal(date(es[0]))
}

// lower returns the level below v, or the one above the lowest.
func lower(values []string, v string) string {
	i := slices.Index(values, v)
	if i == 0 {
		return values[1]
	}
	return values[i-1]
}

func idPat(id string) string {
	return `IMP-0*` + strings.TrimLeft(strings.TrimPrefix(id, "IMP-"), "0") + `\b`
}

var numberWords = []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"}

// countPat matches the number alone, not inside an ID, a date or a larger
// number.
func countPat(n int) string {
	p := fmt.Sprintf(`(^|[^\w-])%d([^\w-]|$)`, n)
	if n < len(numberWords) {
		p += `|\b` + numberWords[n] + `\b`
	}
	return p
}

func datePat(t time.Time) string {
	d, m := fmt.Sprint(t.Day()), strings.ToLower(t.Format("Jan"))
	return fmt.Sprintf(`%s|%s[a-z]*\.? 0?%s\b|\b0?%s %s`, iso(t), m, d, d, m)
}

func statusPat(s string) string {
	return `\b` + strings.ReplaceAll(s, "-", "[- ]") + `\b`
}
