package automation

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // automations.timezone names a zone the image may lack
)

// Schedule is when an every rule fires: each day, or one weekday, at a
// time of day in the engine's time zone.
type Schedule struct {
	// Weekday is the day of the week, or -1 for every day.
	Weekday time.Weekday `json:"weekday"`
	Hour    int          `json:"hour"`
	Minute  int          `json:"minute"`
}

var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
	"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
	"domenica": time.Sunday, "lunedì": time.Monday, "lunedi": time.Monday, "martedì": time.Tuesday,
	"martedi": time.Tuesday, "mercoledì": time.Wednesday, "mercoledi": time.Wednesday, "giovedì": time.Thursday,
	"giovedi": time.Thursday, "venerdì": time.Friday, "venerdi": time.Friday, "sabato": time.Saturday,
}

// ParseSchedule reads "monday 09:00", "day 18:30" (every day), or a day
// alone, "monday", at 09:00. Italian day names are read too.
func ParseSchedule(s string) (Schedule, error) {
	f := strings.Fields(strings.ToLower(strings.TrimSpace(s)))
	if len(f) == 0 || len(f) > 2 {
		return Schedule{}, fmt.Errorf("every: %q, want a day and a time, \"monday 09:00\" or \"day 09:00\"", s)
	}
	sc := Schedule{Weekday: -1, Hour: 9}
	switch f[0] {
	case "day", "daily", "giorno":
	default:
		d, ok := weekdays[f[0]]
		if !ok {
			return Schedule{}, fmt.Errorf("every: %q is not a day of the week (or day, for every day)", f[0])
		}
		sc.Weekday = d
	}
	if len(f) == 2 {
		h, m, ok := strings.Cut(f[1], ":")
		hour, err1 := strconv.Atoi(h)
		minute, err2 := strconv.Atoi(m)
		if !ok || err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
			return Schedule{}, fmt.Errorf("every: %q is not a time of day, HH:MM", f[1])
		}
		sc.Hour, sc.Minute = hour, minute
	}
	return sc, nil
}

func (s Schedule) String() string {
	day := "day"
	if s.Weekday >= 0 {
		day = strings.ToLower(s.Weekday.String())
	}
	return fmt.Sprintf("%s %02d:%02d", day, s.Hour, s.Minute)
}

// Last is the latest slot of the schedule at or before now, in loc.
func (s Schedule) Last(now time.Time, loc *time.Location) time.Time {
	now = now.In(loc)
	t := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, s.Minute, 0, 0, loc)
	for i := 0; i < 8; i++ {
		if !t.After(now) && (s.Weekday < 0 || t.Weekday() == s.Weekday) {
			return t
		}
		t = time.Date(t.Year(), t.Month(), t.Day()-1, s.Hour, s.Minute, 0, 0, loc)
	}
	return t
}

// Next is the first slot of the schedule after now, in loc.
func (s Schedule) Next(now time.Time, loc *time.Location) time.Time {
	now = now.In(loc)
	t := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, s.Minute, 0, 0, loc)
	for i := 0; i < 9; i++ {
		if t.After(now) && (s.Weekday < 0 || t.Weekday() == s.Weekday) {
			return t
		}
		t = time.Date(t.Year(), t.Month(), t.Day()+1, s.Hour, s.Minute, 0, 0, loc)
	}
	return t
}
