package main

// Expected-off times: when a target is off on purpose, like a gaming PC at
// night. OmniPing keeps pinging it, but sends no alerts for it then, leaves
// it out of "together" rules, and the report doesn't count that time.

import (
	"errors"
	"time"
)

// OffTime is one block of time a target is expected to be off: From to To
// ("15:04", the server's clock) on Days: "every", "weekdays" or "weekends".
// A To before From runs past midnight; From equal to To is a whole day.
type OffTime struct {
	Days string `json:"days"`
	From string `json:"from"`
	To   string `json:"to"`
}

// maxOffTimes keeps the list short enough to show and check quickly.
const maxOffTimes = 8

// onDay tells whether a block that starts on day d counts.
func (o OffTime) onDay(d time.Weekday) bool {
	switch o.Days {
	case "weekdays":
		return d >= time.Monday && d <= time.Friday
	case "weekends":
		return d == time.Saturday || d == time.Sunday
	}
	return true
}

func minuteOfDay(hm string) (int, bool) {
	t, err := time.Parse("15:04", hm)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// covers tells whether t, on the server's clock, is in this block.
func (o OffTime) covers(t time.Time) bool {
	from, ok1 := minuteOfDay(o.From)
	to, ok2 := minuteOfDay(o.To)
	if !ok1 || !ok2 {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	if from < to {
		return o.onDay(t.Weekday()) && m >= from && m < to
	}
	// Past midnight: from From on a matching day, or before To on the day
	// after one.
	return o.onDay(t.Weekday()) && m >= from || o.onDay((t.Weekday()+6)%7) && m < to
}

// offAt tells whether the target is expected to be off at ms.
func (t *Target) offAt(ms int64) bool {
	if len(t.Off) == 0 {
		return false
	}
	now := time.UnixMilli(ms).In(time.Local)
	for _, o := range t.Off {
		if o.covers(now) {
			return true
		}
	}
	return false
}

// checkOffTimes checks a list from the page.
func checkOffTimes(list []OffTime) error {
	if len(list) > maxOffTimes {
		return errors.New("that is more than 8 expected-off times")
	}
	for _, o := range list {
		if o.Days != "every" && o.Days != "weekdays" && o.Days != "weekends" {
			return errors.New(`days must be "every", "weekdays" or "weekends"`)
		}
		if _, ok := minuteOfDay(o.From); !ok {
			return errors.New("a start time must look like 01:00")
		}
		if _, ok := minuteOfDay(o.To); !ok {
			return errors.New("an end time must look like 08:00")
		}
	}
	return nil
}
