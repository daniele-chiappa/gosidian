package clock

import (
	"testing"
	"time"
)

func rome(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(SetLocation(loc))
	return loc
}

// In Rome a time carries its offset and a date is the Italian day; in UTC
// both are what they were before IMP-208.
func TestNoteTimeAndDate(t *testing.T) {
	at := time.Date(2026, 10, 20, 22, 30, 5, 0, time.UTC) // 00:30:05 on the 21st in Rome
	t.Cleanup(SetLocation(time.UTC))
	if got := NoteTime(at); got != "2026-10-20T22:30:05Z" {
		t.Errorf("UTC NoteTime = %s", got)
	}
	if got := NoteDate(at); got != "2026-10-20" {
		t.Errorf("UTC NoteDate = %s", got)
	}
	rome(t)
	if got := NoteTime(at); got != "2026-10-21T00:30:05+02:00" {
		t.Errorf("Rome NoteTime = %s", got)
	}
	if got := NoteDate(at); got != "2026-10-21" {
		t.Errorf("Rome NoteDate = %s", got)
	}
	// Winter time: the offset follows the day.
	if got := NoteTime(time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)); got != "2026-12-01T10:00:00+01:00" {
		t.Errorf("Rome NoteTime in December = %s", got)
	}
	if got := Now().Location(); got.String() != "Europe/Rome" {
		t.Errorf("Now is in %s", got)
	}
}

func TestSetLocationRestores(t *testing.T) {
	restore := SetLocation(time.UTC)
	if Location() != time.UTC {
		t.Fatal("SetLocation did not take")
	}
	restore()
	if Location() != time.Local {
		t.Errorf("after restore the zone is %s, want time.Local", Location())
	}
}

func TestParse(t *testing.T) {
	loc := rome(t)
	for _, c := range []struct {
		in   string
		want time.Time
	}{
		{"2026-10-20", time.Date(2026, 10, 20, 0, 0, 0, 0, loc)},
		{"2026-10-20T22:04", time.Date(2026, 10, 20, 22, 4, 0, 0, loc)},
		{"2026-10-20 22:04:05", time.Date(2026, 10, 20, 22, 4, 5, 0, loc)},
		{"2026-10-20T22:04:05.25", time.Date(2026, 10, 20, 22, 4, 5, 250_000_000, loc)},
		{"2026-10-20T20:04:05Z", time.Date(2026, 10, 20, 20, 4, 5, 0, time.UTC)},
		{"2026-10-20T22:04:05+02:00", time.Date(2026, 10, 20, 20, 4, 5, 0, time.UTC)},
		{"2026-10-20T22:04:05.5+0200", time.Date(2026, 10, 20, 20, 4, 5, 500_000_000, time.UTC)},
		{"2026-10-20T15:04-05:00", time.Date(2026, 10, 20, 20, 4, 0, 0, time.UTC)},
		{" 2026-10-20T20:04Z ", time.Date(2026, 10, 20, 20, 4, 0, 0, time.UTC)},
	} {
		got, ok := parse(c.in)
		if !ok || !got.Equal(c.want) {
			t.Errorf("parse(%q) = %v, %v; want %v", c.in, got, ok, c.want)
		}
	}
	for _, in := range []string{"", "2026", "2026-13-01", "2026-10-20T25:00", "2026-10-20X22:04", "2026-10-20T22:04:05Zjunk", "tomorrow", "2026-10-20T22"} {
		if got, ok := parse(in); ok {
			t.Errorf("parse(%q) = %v, want no time", in, got)
		}
	}
}

// Old notes say Z, new ones +02:00: the keys order them by when they
// happened, and a value without an offset is in the note zone.
func TestSortKeyByInstant(t *testing.T) {
	rome(t)
	key := func(v string) string {
		t.Helper()
		k, ok := SortKey(v)
		if !ok {
			t.Fatalf("SortKey(%q) is no key", v)
		}
		return k
	}
	older, newer := "2026-10-20T22:04:05+02:00", "2026-10-20T20:30:00Z" // 20:04:05Z, 20:30Z
	if older < newer {
		t.Fatal("the test needs values whose text order is wrong")
	}
	if key(older) >= key(newer) {
		t.Errorf("key(%s) = %s, not before key(%s) = %s", older, key(older), newer, key(newer))
	}
	if key("2026-10-20T22:04:05+02:00") != key("2026-10-20T20:04:05Z") || key("2026-10-20T22:04:05") != key("2026-10-20T20:04:05Z") {
		t.Error("one instant, written three ways, has three keys")
	}
	// A fraction no longer sorts before the whole second it follows.
	if key("2026-10-20T20:04:05.5Z") <= key("2026-10-20T20:04:05Z") {
		t.Error("05.5 sorts before 05")
	}
	// The day begins at midnight in Rome, before 00:30 there.
	if key("2026-10-21") >= key("2026-10-20T22:30:00Z") || key("2026-10-21") <= key("2026-10-20T21:30:00Z") {
		t.Errorf("the day 2026-10-21 is not Rome's midnight: %s", key("2026-10-21"))
	}
	if k, ok := SortKey("not a date"); ok || k != "not a date" {
		t.Errorf("text has the key %q, %v; want itself", k, ok)
	}
}

// FromTZ loads the zone TZ names when Go fell back to UTC for it, and
// leaves a Local Go loaded, or no TZ, alone.
func TestFromTZ(t *testing.T) {
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })

	t.Setenv("TZ", "Europe/Rome")
	time.Local = time.UTC // what Go keeps when it could not load TZ at its first use
	if err := FromTZ(); err != nil {
		t.Fatal(err)
	}
	if got := time.Local.String(); got != "Europe/Rome" {
		t.Fatalf("Local is %s, want Europe/Rome", got)
	}
	if got := Now().Format("-07:00"); got != "+02:00" && got != "+01:00" {
		t.Errorf("Now() in Rome has the offset %s", got)
	}

	t.Setenv("TZ", "Nowhere/Atlantis")
	time.Local = time.UTC
	if err := FromTZ(); err == nil {
		t.Error("an unknown zone loaded")
	}
	if time.Local != time.UTC {
		t.Error("a failed FromTZ changed Local")
	}

	t.Setenv("TZ", "")
	if err := FromTZ(); err != nil || time.Local != time.UTC {
		t.Errorf("an empty TZ changed Local or failed: %v", err)
	}
}
