// Package clocktest sets the zone of note times (package clock) for a
// test, and puts the one before back when it ends. A test that sets it
// must not call t.Parallel.
package clocktest

import (
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/clock"
)

// Zone makes the IANA zone name the zone of note times for t.
func Zone(t testing.TB, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(clock.SetLocation(loc))
	return loc
}

// Rome makes Europe/Rome the zone of note times for t.
func Rome(t testing.TB) *time.Location { return Zone(t, "Europe/Rome") }

// OtherDay makes the zone of note times one whose day is not UTC's at
// this moment, for code that reads the real clock: fourteen hours ahead
// from noon UTC (already tomorrow there), twelve behind before it (still
// yesterday). A test reads the day it expects in the zone before and after
// the call it checks: midnight may come in between.
func OtherDay(t testing.TB) *time.Location {
	t.Helper()
	if time.Now().UTC().Hour() >= 12 {
		return Zone(t, "Etc/GMT-14")
	}
	return Zone(t, "Etc/GMT+12")
}

// Day says whether day is the day of before or of after in loc.
func Day(day string, loc *time.Location, before, after time.Time) bool {
	return day == before.In(loc).Format(time.DateOnly) || day == after.In(loc).Format(time.DateOnly)
}
