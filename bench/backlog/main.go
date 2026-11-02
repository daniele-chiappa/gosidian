// Command backlog builds the backlog benchmark of IMP-212 (phase C): one
// project, harbor, whose improvements backlog of 180 entries is written in
// the two shapes a gosidian team keeps one in, and the status questions
// about it, with the answers computed from the same entries.
//
//   - sections: docs/improvements.md, one "## IMP-001 — title" section per
//     entry with its fields as a list, the backlog of a project before
//     database notes (v2.42.0 and earlier).
//   - database: docs/improvements.md as a database note (type: database)
//     and one note per entry in docs/improvements/, its fields in the
//     frontmatter, as "Moving an existing backlog" in
//     docs/vault/databases.md does it.
//
// Both shapes hold the same facts and the same other notes, so a level A
// run on each, on one version of the server, measures the shape alone, and
// one shape on two versions measures the version. The vault lives outside
// bench/vault, so the numbers of that vault stay comparable with the runs
// of 2026-09-25.
//
// The entries are deterministic for the seed, and
// bench/backlog/questions.jsonl is this command's output: a test checks
// that it still is.
//
//	go run ./bench/backlog -out /tmp/bench-backlog    # sections/ and database/
//	go run ./bench/backlog -questions bench/backlog/questions.jsonl
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	project = "harbor"
	entries = 180
	seed    = 212
)

// today is the vault's today: hot.md and the questions are written on it.
var today = day("2026-10-26")

var (
	priorities = []string{"low", "medium", "high", "critical"}
	areas      = []string{"api", "web", "billing", "search", "infra", "mobile"}
	owners     = []string{"Ana", "Bruno", "Chen", "Dara", "Emil", "Femi"}
	verbs      = []string{"Improve", "Fix", "Add", "Speed up", "Simplify", "Document", "Rework", "Test", "Audit", "Monitor"}
	subjects   = map[string][]string{
		"api":     {"rate limits on the public API", "pagination of the orders endpoint", "API key rotation", "webhook retries", "the OpenAPI schema", "API error codes", "idempotency keys", "the bulk export endpoint", "request tracing", "the API version header", "CORS rules", "the batch endpoint"},
		"web":     {"the checkout page", "dark mode", "the search results page", "account settings", "accessibility of the forms", "lazy loading of images", "the cookie banner", "the onboarding tour", "the order history page", "the error pages", "keyboard shortcuts", "the language switcher"},
		"billing": {"invoice PDFs", "VAT rounding", "the refund flow", "proration on plan changes", "failed payment emails", "coupon codes", "currency conversion", "tax IDs on invoices", "payout reports", "the dunning schedule", "card payment receipts", "credit notes"},
		"search":  {"typo tolerance", "the synonyms list", "facet counts", "the index rebuild time", "the ranking of recent items", "autocomplete", "Italian stemming", "search analytics", "result highlighting", "suggestions for empty results", "date filters", "query logging"},
		"infra":   {"the Postgres upgrade", "the backup restore drill", "TLS certificate renewal", "the staging environment", "the CI cache", "the container image size", "log retention", "alerting thresholds", "DNS failover", "secret rotation", "disk usage on the workers", "deploy rollbacks"},
		"mobile":  {"push notifications", "offline mode", "app start time", "biometric login", "deep links", "crash reporting", "in-app purchases", "the tablet layout", "background sync", "the app size", "the dark theme on Android", "the QR code scanner"},
	}
	reasons = []string{
		"Raised in the {month} review of the {area} work.",
		"Support counted {n} tickets about it in {month}.",
		"Needed before the 2.0 release.",
		"Found on call: the current behaviour pages someone about once a week.",
		"A customer asked for it during their onboarding in {month}.",
		"Measured on staging: {n}% slower than the target.",
		"The last incident review listed it as a follow-up.",
		"It keeps coming up in the {area} retrospectives.",
	}
	nexts      = []string{"a short write-up for the weekly", "a prototype on staging", "an estimate from the {area} side", "a test plan", "a design review", "measurements with production traffic"}
	rejections = []string{"the benefit does not justify the maintenance", "a larger rework replaces it", "nobody could reproduce the problem", "the vendor fixed it on their side"}
)

// entry is one item of the backlog.
type entry struct {
	ID, Title, Status, Priority, Area, Owner string
	Created, Due, Closed                     time.Time // zero when unset
	BlockedBy                                string
	Body                                     string
}

func (e entry) closed() bool { return e.Status == "done" || e.Status == "rejected" }

// generate makes the entries: older ones are more often done, about a third
// have a deadline, and five are blocked, two of them by the same entry.
func generate() []entry {
	r := rand.New(rand.NewPCG(seed, seed))
	es := make([]entry, entries)
	created := day("2026-01-12")
	used := map[string]bool{}
	for i := range es {
		e := &es[i]
		e.ID = fmt.Sprintf("IMP-%03d", i+1)
		e.Area = areas[r.IntN(len(areas))]
		for e.Title == "" || used[e.Title] {
			e.Title = verbs[r.IntN(len(verbs))] + " " + subjects[e.Area][r.IntN(len(subjects[e.Area]))]
		}
		used[e.Title] = true
		e.Owner = owners[r.IntN(len(owners))]
		e.Priority = pick(r, priorities, 25, 40, 25, 10)
		e.Created = created
		created = created.AddDate(0, 0, r.IntN(4))
		age := float64(entries-i) / entries // 1 for the first entry, about 0 for the last
		e.Status = pick(r, []string{"open", "in-progress", "deferred", "done", "rejected"},
			int(15+30*(1-age)), int(8+12*(1-age)), 8, int(10+55*age), 6)
		if r.IntN(10) < 3 {
			e.Due = e.Created.AddDate(0, 0, 21+r.IntN(120))
		}
		if e.closed() {
			e.Closed = e.Created.AddDate(0, 0, 2+r.IntN(74))
			if last := today.AddDate(0, 0, -1); e.Closed.After(last) {
				e.Closed = e.Created.AddDate(0, 0, r.IntN(int(last.Sub(e.Created).Hours()/24)+1))
			}
		}
		e.Body = body(r, *e)
	}
	var live []int
	for i, e := range es {
		if e.Status == "open" || e.Status == "in-progress" {
			live = append(live, i)
		}
	}
	r.Shuffle(len(live), func(i, j int) { live[i], live[j] = live[j], live[i] })
	blocked, blockers := live[:5], live[5:9]
	for k, i := range blocked {
		es[i].Status = "blocked"
		es[i].BlockedBy = es[blockers[max(0, k-1)]].ID
	}
	return es
}

func body(r *rand.Rand, e entry) string {
	fill := strings.NewReplacer("{month}", e.Created.Format("January"), "{area}", e.Area, "{n}", fmt.Sprint(4+r.IntN(30)))
	text := fill.Replace(reasons[r.IntN(len(reasons))])
	switch e.Status {
	case "done":
		text += " Shipped; the release notes describe the change."
	case "rejected":
		text += " Rejected: " + rejections[r.IntN(len(rejections))] + "."
	case "deferred":
		text += " Deferred until after the 2.0 release."
	default:
		text += " Next step: " + fill.Replace(nexts[r.IntN(len(nexts))]) + "."
	}
	return text
}

func pick(r *rand.Rand, values []string, weights ...int) string {
	total := 0
	for _, w := range weights {
		total += w
	}
	n := r.IntN(total)
	for i, w := range weights {
		if n < w {
			return values[i]
		}
		n -= w
	}
	return values[len(values)-1]
}

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func main() {
	out := flag.String("out", "", "directory that gets the two vaults, sections/ and database/ (must not exist)")
	questionsPath := flag.String("questions", "", "write the questions to this file")
	flag.Parse()
	if *out == "" && *questionsPath == "" {
		fail(errors.New("nothing to do: give -out, -questions or both"))
	}
	es := generate()
	if *out != "" {
		if _, err := os.Stat(*out); err == nil {
			fail(fmt.Errorf("%s already exists", *out))
		}
		for _, form := range []string{"sections", "database"} {
			if err := writeVault(filepath.Join(*out, form), form, es); err != nil {
				fail(err)
			}
		}
	}
	if *questionsPath != "" {
		qs, err := questions(es)
		if err != nil {
			fail(err)
		}
		if err := os.WriteFile(*questionsPath, marshalQuestions(qs), 0o644); err != nil {
			fail(err)
		}
	}
}

// writeVault writes the project in one of the two shapes.
func writeVault(dir, form string, es []entry) error {
	files := map[string]string{
		"README.md": readme,
		"hot.md":    hot,
		"log.md":    logNote,
	}
	if form == "sections" {
		files["docs/improvements.md"] = sectionsNote(es)
	} else {
		files["docs/improvements.md"] = databaseNote
		for _, e := range es {
			files["docs/improvements/"+e.ID+".md"] = rowNote(e)
		}
	}
	for name, text := range files {
		p := filepath.Join(dir, project, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

const readme = `---
title: Harbor
description: Map of the harbor project vault — the booking platform's product team.
tags: [harbor, type:index, pinned]
type: index
updated: 2026-10-26
---

# Harbor

The product team of the booking platform: the public API, the web app,
billing, search, the infrastructure and the mobile apps, six people (Ana,
Bruno, Chen, Dara, Emil and Femi).

| Note | What it holds |
|---|---|
| [[harbor/hot]] | what the team is working on right now |
| [[harbor/docs/improvements]] | the backlog: every improvement and piece of tech debt, with its status, priority, area, owner and dates |
| [[harbor/log]] | releases and reviews, newest last |
`

const hot = `---
title: Harbor Hot State
description: What the team is working on right now. Short, rewritten often.
tags: [harbor, type:hot, pinned]
type: hot
updated: 2026-10-26
---

# Hot state — 2026-10-26

## Current focus

- **Release 1.9** freezes on 2026-11-06: what goes in is decided at the
  backlog review of 2026-10-30.
- **Billing** moves to the new invoice numbering this week.

## Watch out

- The backlog is the only list of what is open: [[harbor/docs/improvements]].
`

const logNote = `---
title: Harbor Log
description: Chronological log of releases and reviews.
tags: [harbor, type:log]
type: log
updated: 2026-10-26
---

# Harbor Log

Append-only, newest last. One line per event: date — kind — what.

- 2026-03-30 — release — 1.6: the new checkout page.
- 2026-05-25 — release — 1.7: search facets and the tablet layout.
- 2026-07-27 — release — 1.8: invoice PDFs and push notifications.
- 2026-09-28 — review — backlog review of the third quarter.
- 2026-10-26 — ops — the 1.9 freeze set for 2026-11-06.
`

func sectionsNote(es []entry) string {
	var b strings.Builder
	b.WriteString(`---
title: Improvements backlog
description: The improvements and tech debt of harbor, one section per entry with its status, priority, area, owner and dates.
tags: [harbor, type:index]
updated: 2026-10-26
---

# Improvements backlog

One section per entry, in the order they were filed. To file one, append a
section with the next number and the status open; to close one, set its
status to done or rejected and add the date it was closed. The statuses
are open, in-progress, blocked (on the entry named under "Blocked by"),
deferred, done and rejected.
`)
	for _, e := range es {
		fmt.Fprintf(&b, "\n## %s — %s\n\n", e.ID, e.Title)
		fmt.Fprintf(&b, "- Status: %s\n- Priority: %s\n- Area: %s\n- Owner: %s\n- Created: %s\n", e.Status, e.Priority, e.Area, e.Owner, iso(e.Created))
		if !e.Due.IsZero() {
			fmt.Fprintf(&b, "- Due: %s\n", iso(e.Due))
		}
		if !e.Closed.IsZero() {
			fmt.Fprintf(&b, "- Closed: %s\n", iso(e.Closed))
		}
		if e.BlockedBy != "" {
			fmt.Fprintf(&b, "- Blocked by: %s\n", e.BlockedBy)
		}
		fmt.Fprintf(&b, "\n%s\n", e.Body)
	}
	return b.String()
}

const databaseNote = `---
title: Improvements backlog
description: The improvements and tech debt of harbor, one note per entry in docs/improvements/ with its status, priority, area, owner and dates in the frontmatter.
tags: [harbor, type:index]
type: database
source: harbor/docs/improvements
fields:
  id: {type: text, required: true}
  title: {type: text, required: true}
  status: {type: select, required: true, options: [open, in-progress, blocked, deferred, done, rejected]}
  priority: {type: select, options: [low, medium, high, critical]}
  area: {type: select, options: [api, web, billing, search, infra, mobile]}
  owner: {type: text}
  created: {type: date}
  due: {type: date}
  closed: {type: date}
  blocked_by: {type: relation}
updated: 2026-10-26
---

# Improvements backlog

One note per entry in ` + "`docs/improvements/`" + `, named by its ID. To file one,
create ` + "`harbor/docs/improvements/IMP-<next number>.md`" + ` with the status open; to
close one, set its status to done or rejected and ` + "`closed`" + ` to the date. The
statuses are open, in-progress, blocked (on the entries in ` + "`blocked_by`" + `),
deferred, done and rejected.

What is not closed yet:

` + "```json" + `
{"project": "harbor", "from": "harbor/docs/improvements",
 "where": [{"field": "status", "op": "in", "value": ["open", "in-progress", "blocked", "deferred"]}],
 "fields": ["id", "title", "status", "priority", "area", "owner", "due"], "limit": 200}
` + "```" + `
`

func rowNote(e entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %q\nid: %s\nstatus: %s\npriority: %s\narea: %s\nowner: %s\ncreated: %q\n",
		e.ID+" — "+e.Title, e.ID, e.Status, e.Priority, e.Area, e.Owner, iso(e.Created))
	if !e.Due.IsZero() {
		fmt.Fprintf(&b, "due: %q\n", iso(e.Due))
	}
	if !e.Closed.IsZero() {
		fmt.Fprintf(&b, "closed: %q\n", iso(e.Closed))
	}
	if e.BlockedBy != "" {
		fmt.Fprintf(&b, "blocked_by: [\"[[%s/docs/improvements/%s]]\"]\n", project, e.BlockedBy)
	}
	fmt.Fprintf(&b, "tags: [%s, type:doc]\n---\n\n# %s — %s\n\n%s\n", project, e.ID, e.Title, e.Body)
	return b.String()
}

func iso(t time.Time) string { return t.Format(time.DateOnly) }

func fail(err error) {
	fmt.Fprintln(os.Stderr, "backlog:", err)
	os.Exit(1)
}

func marshalQuestions(qs []question) []byte {
	var b []byte
	for _, q := range qs {
		line, _ := json.Marshal(q)
		b = append(append(b, line...), '\n')
	}
	return b
}
