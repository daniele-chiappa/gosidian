// Command scale builds a larger copy of the benchmark vault for the scale
// measurements of IMP-102: it copies bench/vault and adds noise notes to the
// tidewater project — plans, meetings, support articles and research notes
// on topics the questions never touch — so that reading files costs what it
// costs on a large project while every answer stays where it was.
//
// The noise is deterministic for a given seed and never introduces an ADR,
// bug, improvement, incident, number or date the questions depend on.
//
//	go run ./bench/scale -out /tmp/bench-vault-large -notes 420
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	people     = []string{"Marta", "Ivo", "Sara", "Luca", "Nadia", "Omar", "Elena"}
	components = []string{"tide-api", "tide-web", "tide-worker", "Gatekeeper", "the organizer dashboard", "the admin console", "the seat map editor", "the event page", "the PDF renderer", "the search index", "the notification service", "the reporting export"}
	infra      = []string{"Redis", "Postgres", "the job streams", "HAProxy", "MinIO", "the build runner", "the staging copy", "the read replica", "the CDN edge", "the metrics stack"}
	topics     = []string{"accessibility audit", "error budgets", "dependency upgrades", "log retention", "dashboard performance", "CSV exports", "translations", "analytics events", "feature flag cleanup", "image resizing", "admin roles", "venue onboarding checklist", "seat map import", "load test harness", "alert tuning", "SQL query review", "cache warmup", "print-at-home layout", "API pagination", "audit trail", "timezone display", "event duplication", "bulk messaging", "accessibility of the purchase path", "session analytics", "organizer permissions", "image moderation", "venue floor plans", "waitlist", "promo codes", "gift cards", "sponsor pages"}
	findings   = []string{
		"most of the time goes into serializing large responses",
		"the slowest pages are the ones with many small images",
		"two queries run once per row instead of once per page",
		"the logs are too verbose to be searched quickly",
		"error messages still leak internal names",
		"the layout breaks on narrow phones",
		"the same data is fetched twice on the first visit",
		"the retry logic hides real failures from the dashboards",
		"organizers use the export mostly to reconcile with their own books",
		"the documentation is out of date since the last redesign",
		"half of the alerts fire outside office hours and nobody acts on them",
		"the settings page is used by very few organizers",
		"screen readers skip the section headings",
		"the cache is invalidated far more often than needed",
		"a handful of venues produce most of the support requests",
	}
	decisions = []string{
		"keep the current approach and revisit after the summer",
		"move the work behind a feature flag and roll it out gradually",
		"split the change into two smaller releases",
		"write the test plan first and estimate afterwards",
		"ask the venues for feedback before building anything",
		"drop the idea: the benefit does not justify the maintenance",
		"prototype it on staging for a week",
		"pair on it so that two people know the code",
		"document the current behaviour before changing it",
		"measure again with production traffic before deciding",
	}
	nexts = []string{
		"a short write-up for the weekly",
		"a prototype on staging",
		"a checklist for support",
		"tickets for the next sprint",
		"a review with the organizer panel",
		"an update of the runbook",
		"a comparison table of the options",
		"a note in the log once it ships",
	}
	itSentences = []string{
		"Abbiamo ripreso il tema con calma e senza fretta di chiudere.",
		"La proposta piace, ma servono dati più solidi prima di investire tempo.",
		"Per ora teniamo tutto com'è e ne riparliamo dopo il prossimo rilascio.",
		"Il supporto segnala richieste simili da più organizzatori.",
		"Serve una prova su staging prima di coinvolgere i teatri.",
		"La documentazione interna va aggiornata, è rimasta indietro.",
		"Chiediamo un parere anche a chi lavora ai varchi durante gli eventi.",
	}
	kbVerbs = []string{"Export", "Duplicate", "Translate", "Archive", "Share", "Customize", "Preview", "Schedule", "Review", "Tag"}
	kbObjs  = []string{"an event", "the attendee list", "a seat map", "the event page", "a promo code", "a sponsor page", "the venue profile", "a waitlist", "a report", "the email footer"}
)

func main() {
	base := flag.String("base", "bench/vault", "benchmark vault to copy")
	out := flag.String("out", "", "directory to create (must not exist)")
	notes := flag.Int("notes", 420, "noise notes to add to the tidewater project")
	seed := flag.Int64("seed", 7, "random seed")
	flag.Parse()
	if *out == "" {
		fail(fmt.Errorf("-out is required"))
	}
	if _, err := os.Stat(*out); err == nil {
		fail(fmt.Errorf("%s already exists", *out))
	}
	if err := copyTree(*base, *out); err != nil {
		fail(err)
	}
	r := rand.New(rand.NewSource(*seed))
	g := gen{r: r, used: map[string]bool{}}
	kinds := []func() (string, string){g.plan, g.meeting, g.kb, g.research}
	for i := 0; i < *notes; i++ {
		rel, body := kinds[i%len(kinds)]()
		p := filepath.Join(*out, "tidewater", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			fail(err)
		}
	}
	fmt.Printf("%s: %d noise notes added to tidewater\n", *out, *notes)
}

type gen struct {
	r    *rand.Rand
	used map[string]bool
}

// reserved dates belong to the questions' notes: no noise note lands there.
var reserved = map[string]bool{
	"2026-01-30": true, "2026-02-10": true, "2026-02-27": true, "2026-03-17": true,
	"2026-04-02": true, "2026-05-05": true, "2026-05-20": true, "2026-05-29": true, "2026-06-09": true,
}

func (g gen) pick(list []string) string { return list[g.r.Intn(len(list))] }

func (g gen) date() string {
	for {
		d := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC).AddDate(0, 0, g.r.Intn(170))
		s := d.Format("2006-01-02")
		if !reserved[s] {
			return s
		}
	}
}

func slug(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer(" ", "-", "'", "", ",", "").Replace(s)
	return strings.Trim(s, "-")
}

// unique makes a file name unique within the run.
func (g gen) unique(rel string) string {
	base := strings.TrimSuffix(rel, ".md")
	for i := 2; g.used[rel]; i++ {
		rel = fmt.Sprintf("%s-%s.md", base, strings.Repeat("b", i-1))
	}
	g.used[rel] = true
	return rel
}

func (g gen) paragraph(n int) string {
	var s []string
	for i := 0; i < n; i++ {
		switch g.r.Intn(4) {
		case 0:
			s = append(s, fmt.Sprintf("%s looked at %s and found that %s.", g.pick(people), g.pick(components), g.pick(findings)))
		case 1:
			s = append(s, fmt.Sprintf("On %s the main question is whether %s can stay as it is.", g.pick(infra), g.pick(components)))
		case 2:
			s = append(s, fmt.Sprintf("The team agreed to %s.", g.pick(decisions)))
		default:
			s = append(s, fmt.Sprintf("Next step: %s, owned by %s.", g.pick(nexts), g.pick(people)))
		}
	}
	return strings.Join(s, " ")
}

func (g gen) italian(n int) string {
	var s []string
	for i := 0; i < n; i++ {
		s = append(s, g.pick(itSentences))
	}
	return strings.Join(s, " ")
}

func (g gen) plan() (string, string) {
	topic := g.pick(topics)
	d := g.date()
	status := []string{"done", "done", "done", "draft"}[g.r.Intn(4)]
	rel := g.unique(fmt.Sprintf("plans/%s-%s.md", d, slug(topic)))
	body := fmt.Sprintf(`---
title: %s
description: Plan about the %s of tidewater.
tags: [tidewater, type:plan, status:%s, topic:%s]
type: plan
status: %s
author: %s
created: %s
---

# %s

## Context
%s

## Solution
%s

## Outcome
%s
`, strings.ToUpper(topic[:1])+topic[1:], topic, status, slug(topic), status, g.pick(people), d, strings.ToUpper(topic[:1])+topic[1:],
		g.paragraph(4), g.paragraph(5), g.paragraph(3))
	return rel, body
}

func (g gen) meeting() (string, string) {
	d := g.date()
	kind := []string{"design-review", "planning", "support-sync", "venue-call"}[g.r.Intn(4)]
	rel := g.unique(fmt.Sprintf("docs/meetings/%s-%s.md", d, kind))
	title := strings.ReplaceAll(kind, "-", " ")
	var items []string
	for i := 0; i < 4+g.r.Intn(4); i++ {
		items = append(items, "- "+g.paragraph(1+g.r.Intn(2)))
	}
	extra := ""
	if g.r.Intn(6) == 0 {
		extra = "\n\n## Note in italiano\n\n" + g.italian(3)
	}
	body := fmt.Sprintf(`---
title: %s %s
description: Notes of the %s of %s about %s.
tags: [tidewater, type:doc, topic:meetings]
type: doc
created: %s
---

# %s — %s

Present: %s, %s, %s.

%s%s
`, strings.ToUpper(title[:1])+title[1:], d, title, d, g.pick(topics), d, strings.ToUpper(title[:1])+title[1:], d,
		g.pick(people), g.pick(people), g.pick(people), strings.Join(items, "\n"), extra)
	return rel, body
}

func (g gen) kb() (string, string) {
	verb, obj := g.pick(kbVerbs), g.pick(kbObjs)
	title := fmt.Sprintf("%s %s", verb, obj)
	rel := g.unique(fmt.Sprintf("docs/kb/%s.md", slug(title)))
	var steps []string
	for i := 0; i < 4+g.r.Intn(4); i++ {
		steps = append(steps, fmt.Sprintf("%d. %s", i+1, g.paragraph(1)))
	}
	body := fmt.Sprintf(`---
title: %s
description: Support article for organizers — how to %s %s from the dashboard.
tags: [tidewater, type:doc, topic:support]
type: doc
updated: %s
---

# %s

This article explains how organizers %s %s in the organizer dashboard.

%s

If something does not work as described, support collects the details and opens a ticket for the team.
`, title, strings.ToLower(verb), obj, g.date(), title, strings.ToLower(verb), obj, strings.Join(steps, "\n"))
	return rel, body
}

func (g gen) research() (string, string) {
	topic := g.pick(topics)
	rel := g.unique(fmt.Sprintf("docs/research/%s.md", slug(topic)))
	lang := ""
	if g.r.Intn(7) == 0 {
		lang = "\n\n" + g.italian(4)
	}
	body := fmt.Sprintf(`---
title: Research — %s
description: What we learned about %s and the options we looked at.
tags: [tidewater, type:doc, topic:research]
type: doc
updated: %s
---

# Research — %s

## What we looked at
%s

## Options
%s

## Recommendation
%s%s
`, topic, topic, g.date(), topic, g.paragraph(5), g.paragraph(4), g.paragraph(2), lang)
	return rel, body
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
	fmt.Fprintln(os.Stderr, "scale:", err)
	os.Exit(1)
}
