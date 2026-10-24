package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWeeklyDue(t *testing.T) {
	s := Settings{WeeklyDay: 1, WeeklyTime: "09:00"} // Monday 09:00
	at := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, time.UTC) }
	for _, c := range []struct{ now, want time.Time }{
		{at(19, 10, 0), at(19, 9, 0)},  // Monday after nine: today
		{at(19, 9, 0), at(19, 9, 0)},   // on the minute
		{at(19, 8, 59), at(12, 9, 0)},  // Monday before nine: last week
		{at(18, 23, 0), at(12, 9, 0)},  // Sunday
		{at(21, 12, 30), at(19, 9, 0)}, // Wednesday
	} {
		if got := weeklyDue(s, c.now); !got.Equal(c.want) {
			t.Errorf("due at %v: %v, want %v", c.now, got, c.want)
		}
	}
}

// The internet hosts are down together for 20 s; the summary names the
// together rule's outage, the lowest uptime and the alert.
func TestWeeklySummary(t *testing.T) {
	app, _ := outageApp(t)
	run(app, 0, 120, func(i int, id string) bool { return id != "d" && i >= 30 && i < 50 })
	title, body := app.weeklySummary(context.Background(), time.UnixMilli(t0+200_000))
	t.Logf("%s\n%s", title, body)
	for _, want := range []string{"Internet down: once, 20 s (", "Lowest uptime: ", "Alerts: 1 (Internet down 1)"} {
		if !strings.Contains(body, want) {
			t.Errorf("the summary lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Most packet loss") == false {
		t.Errorf("20 lost pings of 120 should show as packet loss:\n%s", body)
	}

	// A quiet week.
	app2, _ := outageApp(t)
	run(app2, 0, 60, func(int, string) bool { return false })
	_, body = app2.weeklySummary(context.Background(), time.UnixMilli(t0+100_000))
	for _, want := range []string{"Internet down: none", "Every host was up the whole time.", "No alerts."} {
		if !strings.Contains(body, want) {
			t.Errorf("a quiet week lacks %q:\n%s", want, body)
		}
	}
}

// The summary goes out once when it's due, not again after that, and not
// when it's more than a few hours late.
func TestWeeklyCheckSendsOnce(t *testing.T) {
	app, sent := outageApp(t)
	now := time.Now()
	app.cfg.update(func(c *Config) error {
		c.Settings.Weekly = true
		c.Settings.WeeklyDay = int(now.Weekday())
		c.Settings.WeeklyTime = now.Add(-time.Hour).Format("15:04")
		c.Settings.WeeklyChannels = []string{"hook", "gone"}
		return nil
	})
	if now.Hour() == 0 {
		t.Skip("an hour ago was yesterday")
	}
	app.weeklyCheck(context.Background(), now)
	app.weeklyCheck(context.Background(), now.Add(time.Minute))
	got := sent(1)
	if len(got) != 1 || got[0]["event"] != "summary" || !strings.Contains(got[0]["title"].(string), "7 days") {
		t.Fatalf("sent %v, want one summary", got)
	}

	late, sentLate := outageApp(t)
	late.cfg.update(func(c *Config) error {
		c.Settings.Weekly = true
		c.Settings.WeeklyDay = int(now.AddDate(0, 0, -1).Weekday())
		c.Settings.WeeklyTime = "00:00"
		c.Settings.WeeklyChannels = []string{"hook"}
		return nil
	})
	if now.Hour() >= 6 {
		late.weeklyCheck(context.Background(), now)
		if got := sentLate(0); len(got) != 0 {
			t.Errorf("a summary %v late went out: %v", now.Sub(weeklyDue(late.cfg.settings(), now)), got)
		}
	}
}

func TestSummaryAPI(t *testing.T) {
	app, sent := outageApp(t)
	run(app, 0, 60, func(int, string) bool { return false })
	w := httptest.NewRecorder()
	app.getSummary(w, httptest.NewRequest("GET", "/api/summary", nil))
	var m map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || m["title"] == "" || m["body"] == "" {
		t.Fatalf("GET /api/summary: %v %s", err, w.Body)
	}

	w = httptest.NewRecorder()
	app.sendSummaryNow(w, httptest.NewRequest("POST", "/api/summary/send", nil))
	if w.Code != 400 {
		t.Errorf("without channels: %d %s", w.Code, w.Body)
	}
	app.cfg.update(func(c *Config) error { c.Settings.WeeklyChannels = []string{"hook"}; return nil })
	w = httptest.NewRecorder()
	app.sendSummaryNow(w, httptest.NewRequest("POST", "/api/summary/send", nil))
	if w.Code != 200 || len(sent(1)) != 1 {
		t.Errorf("send now: %d %s", w.Code, w.Body)
	}
}
