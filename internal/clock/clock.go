// Package clock is the time gosidian writes into notes and their names
// (IMP-208): the server's time zone, time.Local, which the TZ environment
// variable sets (without it the system's, UTC in a container). A time goes
// in as RFC 3339 with its offset (2026-10-20T22:04:05+02:00), a date as the
// day it is there. Internal times stay in UTC and do not come through
// here: the databases, the audit log, tokens, the API's answers about
// system objects, and the keys that make a schedule's slot fire once.
//
// It reads the dates and times of notes back too: SortKey orders them by
// instant, whatever offset each was written with.
package clock

import (
	"os"
	"strings"
	"sync/atomic"
	"time"
	_ "time/tzdata" // TZ names a zone the image may have no zoneinfo for
)

// FromTZ makes time.Local the zone TZ names when Go fell back to UTC for
// it. Go reads TZ the first time anything asks for local time; without
// zoneinfo in the image it needs the embedded database (time/tzdata),
// which a package's init may not have registered by then, and Local stays
// UTC for the whole run (seen in the lab, 2026-10-20). The server calls it
// first thing in main.
func FromTZ() error {
	tz := strings.TrimPrefix(os.Getenv("TZ"), ":")
	if tz == "" || tz == "UTC" || strings.HasPrefix(tz, "/") || time.Local.String() != "UTC" {
		return nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return err
	}
	time.Local = loc
	return nil
}

// override is the zone a test set, nil for time.Local.
var override atomic.Pointer[time.Location]

// Location is the zone of note times: time.Local, unless a test set
// another with SetLocation.
func Location() *time.Location {
	if l := override.Load(); l != nil {
		return l
	}
	return time.Local
}

// SetLocation makes loc the zone of note times until the returned func
// restores the one before. It is for tests, which then must not run in
// parallel with tests that expect another zone; the server takes its zone
// from TZ.
func SetLocation(loc *time.Location) (restore func()) {
	prev := override.Swap(loc)
	return func() { override.Store(prev) }
}

// Now is the current time in Location, for a note's name.
func Now() time.Time { return time.Now().In(Location()) }

// NoteTime is t as a note holds it: RFC 3339 in Location, with its offset
// ("2026-10-20T22:04:05+02:00", "…Z" in UTC).
func NoteTime(t time.Time) string { return t.In(Location()).Format(time.RFC3339) }

// NoteDate is the day of t in Location, "2026-10-20".
func NoteDate(t time.Time) string { return t.In(Location()).Format(time.DateOnly) }

// parse reads a date or a time as notes write them, the shapes the index
// types as dates (see SortKey); a day is its first instant.
func parse(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if len(v) == len(time.DateOnly) {
		t, err := time.ParseInLocation(time.DateOnly, v, Location())
		return t, err == nil
	}
	if len(v) < len("2006-01-02T15:04") || v[10] != 'T' && v[10] != ' ' {
		return time.Time{}, false
	}
	layout := "2006-01-02T15:04"
	if len(v) > 16 && v[16] == ':' {
		// A fraction after the seconds parses without a layout of its own.
		layout += ":05"
	}
	tail := v[16:]
	switch i := strings.IndexAny(tail, "Z+-"); {
	case i < 0:
	case tail[i] == 'Z' || strings.Contains(tail[i:], ":"):
		layout += "Z07:00"
	default:
		layout += "-0700"
	}
	t, err := time.ParseInLocation(layout, v[:10]+"T"+v[11:], Location())
	return t, err == nil
}

// keyLayout is fixed width for the years 0000 to 9999, so that two keys
// compare as text in the order of their instants.
const keyLayout = "2006-01-02T15:04:05.000000000Z"

// SortKey is what a date or a time of a note compares and sorts by: its
// instant in UTC, as text of a fixed width: 22:04+02:00 and 20:30Z order
// by when they happened, not by how they read. It reads a day
// ("2026-10-20"), or a day and a time of day with or without seconds, a
// fraction and an offset ("2026-10-20T22:04", "2026-10-20 22:04:05.5+02:00",
// "…Z", "…+0200"); a day, or a time without an offset, is in Location. A
// value it does not read is its own key, with ok false.
func SortKey(v string) (key string, ok bool) {
	t, ok := parse(v)
	if !ok {
		return v, false
	}
	return t.UTC().Format(keyLayout), true
}
