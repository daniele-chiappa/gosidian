package mcp

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/clock/clocktest"
	"github.com/gosidian/gosidian/internal/scaffold"
)

// What gosidian writes into notes and their names follows the server's
// zone (IMP-208): these tests set Rome, or a zone on another day than UTC.

var fmLine = regexp.MustCompile(`(?m)^(created|claimed_at|completed_at): (.+)$`)

// fmTimes reads the handoff's times from its frontmatter, as written.
func fmTimes(content string) map[string]string {
	out := map[string]string{}
	for _, m := range fmLine.FindAllStringSubmatch(content, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// A handoff created at 22:04:05 in Rome is named …-220405, and created
// says 22:04:05+02:00; in UTC both are as they were.
func TestHandoffBaseAndBody_NoteZone(t *testing.T) {
	at := time.Date(2026, 10, 20, 20, 4, 5, 0, time.UTC)
	clocktest.Rome(t)
	if got := handoffBase("p", "orchestrator", "go-backend", at); got != "p/handoffs/20261020-220405-orchestrator-to-go-backend" {
		t.Errorf("Rome: base = %s", got)
	}
	if got := fmTimes(renderHandoffBody("a", "b", "x@y", at, "s", nil))["created"]; got != "2026-10-20T22:04:05+02:00" {
		t.Errorf("Rome: created = %s", got)
	}
	clocktest.Zone(t, "UTC")
	if got := handoffBase("p", "orchestrator", "go-backend", at); got != "p/handoffs/20261020-200405-orchestrator-to-go-backend" {
		t.Errorf("UTC: base = %s", got)
	}
	if got := fmTimes(renderHandoffBody("a", "b", "x@y", at, "s", nil))["created"]; got != "2026-10-20T20:04:05Z" {
		t.Errorf("UTC: created = %s", got)
	}
}

// Through the tools, on the real clock: the name is the time of created,
// and created, claimed_at and completed_at carry Rome's offset, in the
// note and in the answers.
func TestMCP_HandoffLifecycle_NoteZone(t *testing.T) {
	rome := clocktest.Rome(t)
	s, v, _ := newTestServer(t)
	ctx := context.Background()
	before := time.Now().Truncate(time.Second)
	path := createTestHandoff(t, s, ctx)
	var claim struct {
		ClaimedAt string `json:"claimed_at"`
	}
	res, _ := s.handleClaimHandoff(ctx, call(map[string]any{"path": path}))
	decodeResult(t, res, &claim)
	var done struct {
		CompletedAt string `json:"completed_at"`
	}
	res, _ = s.handleCompleteHandoff(ctx, call(map[string]any{"path": path, "outcome": "done"}))
	decodeResult(t, res, &done)
	after := time.Now()

	note, err := v.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	times := fmTimes(string(note.Content))
	if times["claimed_at"] != claim.ClaimedAt || times["completed_at"] != done.CompletedAt {
		t.Errorf("answers %q %q, note %v", claim.ClaimedAt, done.CompletedAt, times)
	}
	for _, k := range []string{"created", "claimed_at", "completed_at"} {
		at, err := time.Parse(time.RFC3339, times[k])
		if err != nil || at.Before(before) || at.After(after) {
			t.Fatalf("%s = %q (%v), want a time from %v to %v", k, times[k], err, before, after)
		}
		_, off := at.Zone()
		if _, want := at.In(rome).Zone(); off != want || strings.HasSuffix(times[k], "Z") {
			t.Errorf("%s = %s, without Rome's offset", k, times[k])
		}
		if k == "created" && !strings.HasPrefix(path, "p/handoffs/"+at.In(rome).Format("20060102-150405")+"-") {
			t.Errorf("path %s is not named after created %s", path, times[k])
		}
	}
}

// The automation's handoffs are named and dated the same way.
func TestAutomations_Handoff_NoteZone(t *testing.T) {
	clocktest.Rome(t)
	s, e, now, _ := automationServer(t)
	*now = time.Date(2026, 10, 5, 20, 4, 5, 0, time.UTC)
	runs := e.Tick("p")
	if len(runs) != 1 || runs[0].Path != "p/handoffs/20261005-220405-automation-to-p-dev.md" {
		t.Fatalf("runs = %+v", runs)
	}
	h, err := s.vault.Load(runs[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmTimes(string(h.Content))["created"]; got != "2026-10-05T22:04:05+02:00" {
		t.Errorf("created = %s", got)
	}
}

// memory_ask after midnight in Rome dates the question, and a new file's
// updated, with Rome's day, not UTC's day before.
func TestAppendOQBlock_NoteZone(t *testing.T) {
	at := time.Date(2026, 10, 20, 22, 30, 0, 0, time.UTC) // 00:30 on the 21st in Rome
	clocktest.Rome(t)
	got := string(appendOQBlock(nil, "p", 1, "Which zone?", "low", "", at))
	if !strings.Contains(got, "\nupdated: 2026-10-21\n") || !strings.Contains(got, "- **Date**: 2026-10-21\n") {
		t.Errorf("Rome:\n%s", got)
	}
	clocktest.Zone(t, "UTC")
	got = string(appendOQBlock(nil, "p", 1, "Which zone?", "low", "", at))
	if !strings.Contains(got, "\nupdated: 2026-10-20\n") || !strings.Contains(got, "- **Date**: 2026-10-20\n") {
		t.Errorf("UTC:\n%s", got)
	}
}

// A scaffold's {{TODAY}} and auto dates are the day of the server's zone.
func TestScaffoldToday_NoteZone(t *testing.T) {
	loc := clocktest.OtherDay(t)
	before := time.Now()
	vars, err := resolveVariables(scaffold.Meta{Variables: []scaffold.Variable{{Name: "STARTED", Auto: "date"}}}, "p", call(map[string]any{}))
	after := time.Now()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"TODAY", "STARTED"} {
		if !clocktest.Day(vars[k], loc, before, after) {
			t.Errorf("%s = %s, want the day in %s (UTC: %s)", k, vars[k], loc, after.UTC().Format(time.DateOnly))
		}
	}
}
