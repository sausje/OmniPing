package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOffTimeCovers(t *testing.T) {
	at := func(day, h, m int) time.Time { return time.Date(2026, 10, day, h, m, 0, 0, time.UTC) } // 19 Oct is a Monday
	night := OffTime{Days: "weekdays", From: "22:00", To: "07:00"}
	day := OffTime{Days: "every", From: "09:00", To: "17:30"}
	whole := OffTime{Days: "weekends", From: "00:00", To: "00:00"}
	for _, c := range []struct {
		o    OffTime
		t    time.Time
		want bool
	}{
		{night, at(19, 23, 0), true}, // Monday night
		{night, at(20, 6, 59), true}, // Tuesday morning, from Monday night
		{night, at(20, 7, 0), false}, // until 07:00
		{night, at(19, 21, 59), false},
		{night, at(24, 6, 0), true},   // Saturday morning, from Friday night
		{night, at(25, 6, 0), false},  // Sunday morning: Saturday night isn't a weekday
		{night, at(19, 6, 0), false},  // Monday morning: Sunday night isn't either
		{night, at(24, 23, 0), false}, // Saturday night
		{day, at(22, 9, 0), true},
		{day, at(22, 17, 29), true},
		{day, at(22, 17, 30), false},
		{whole, at(24, 13, 0), true}, // all of Saturday
		{whole, at(26, 0, 0), false}, // Monday
		{OffTime{Days: "every", From: "bad", To: "07:00"}, at(19, 3, 0), false},
	} {
		if got := c.o.covers(c.t); got != c.want {
			t.Errorf("%+v at %s: %v, want %v", c.o, c.t.Format("Mon 15:04"), got, c.want)
		}
	}
}

func TestCheckOffTimes(t *testing.T) {
	ok := []OffTime{{Days: "every", From: "01:00", To: "08:00"}}
	if err := checkOffTimes(ok); err != nil {
		t.Error(err)
	}
	for _, bad := range [][]OffTime{
		{{Days: "sometimes", From: "01:00", To: "08:00"}},
		{{Days: "every", From: "1 am", To: "08:00"}},
		{{Days: "every", From: "01:00", To: "25:00"}},
		make([]OffTime, 9),
	} {
		if checkOffTimes(bad) == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
}

// offAround is an expected-off block from just before t0 to well after it.
func offAround() []OffTime {
	start := time.UnixMilli(t0).Add(-time.Minute).Format("15:04")
	end := time.UnixMilli(t0).Add(30 * time.Minute).Format("15:04")
	return []OffTime{{Days: "every", From: start, To: end}}
}

// While D (on the LAN) is expected to be off, it can be down without an
// alert; A being off leaves "Internet down" to B and C.
func TestOffTimeAlerts(t *testing.T) {
	app, _ := outageApp(t)
	app.cfg.update(func(c *Config) error {
		for _, x := range c.Targets {
			if x.ID == "d" || x.ID == "a" {
				x.Off = offAround()
			}
		}
		return nil
	})
	run(app, 0, 60, func(i int, id string) bool { return id == "d" || (id == "b" || id == "c") && i >= 20 })
	starts := map[string]int{}
	for _, e := range app.alerts.Events("", 100) {
		if e.Kind == "start" {
			starts[e.Rule+"/"+e.Target]++
		}
	}
	if starts["loss/d"] != 0 {
		t.Errorf("D is expected to be off but got an alert: %v", starts)
	}
	if starts["net/"] != 1 {
		t.Errorf("B and C down while A is off: %v, want one Internet down", starts)
	}
}

// The report leaves expected-off time out: the outage inside it doesn't
// count, and neither do its samples.
func TestOffTimeReport(t *testing.T) {
	app, _ := outageApp(t)
	minute := time.UnixMilli(t0).Format("15:04")
	next := time.UnixMilli(t0).Add(time.Minute).Format("15:04")
	app.cfg.update(func(c *Config) error {
		c.Targets[0].Off = []OffTime{{Days: "every", From: minute, To: next}} // A, the first minute
		return nil
	})
	run(app, 0, 120, func(i int, id string) bool { return id == "a" && i >= 30 && i < 50 })
	w := httptest.NewRecorder()
	app.getReport(w, httptest.NewRequest("GET", fmt.Sprintf("/api/report?from=%d&to=%d&ids=a&tz=UTC", t0-1000, t0+200_000), nil))
	var rep Report
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	a := rep.Targets[0]
	if !a.OffTimes || a.Samples != 60 || a.OutageCount != 0 || a.Lost != 0 {
		t.Errorf("A: off %v, %d samples, %d lost, %d outages; want 60 samples and nothing lost", a.OffTimes, a.Samples, a.Lost, a.OutageCount)
	}
	// The weekly summary uses the same numbers.
	if _, body := app.weeklySummary(context.Background(), time.UnixMilli(t0+200_000)); body == "" {
		t.Error("no summary")
	}
}
