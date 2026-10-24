package main

// The weekly summary: once a week a short message on how the network did,
// through the channels picked under Settings → Notifications. It uses the
// report's numbers, so the two always agree.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// weeklyFile remembers when the last summary went out, so a restart doesn't
// send it again.
const weeklyFile = "weekly.json"

// weeklyLate is how late a summary may still go out, for when OmniPing was
// off at the time. Later than this it waits for the next week.
const weeklyLate = 6 * time.Hour

// weeklyMinLost is the report's default: an outage is 3 or more lost pings
// in a row.
const weeklyMinLost = 3

// weeklyDue is the most recent moment at or before now that the summary was
// due.
func weeklyDue(s Settings, now time.Time) time.Time {
	hm, err := time.Parse("15:04", s.WeeklyTime)
	if err != nil {
		hm, _ = time.Parse("15:04", defaultSettings().WeeklyTime)
	}
	due := time.Date(now.Year(), now.Month(), now.Day(), hm.Hour(), hm.Minute(), 0, 0, now.Location())
	due = due.AddDate(0, 0, -((int(due.Weekday()) - s.WeeklyDay + 7) % 7))
	if due.After(now) {
		due = due.AddDate(0, 0, -7)
	}
	return due
}

func (a *App) weeklySent() int64 {
	var st struct {
		LastSent int64 `json:"lastSent"`
	}
	if b, err := os.ReadFile(filepath.Join(a.dataDir, weeklyFile)); err == nil {
		json.Unmarshal(b, &st)
	}
	return st.LastSent
}

func (a *App) setWeeklySent(t int64) {
	b, _ := json.Marshal(map[string]int64{"lastSent": t})
	if err := os.WriteFile(filepath.Join(a.dataDir, weeklyFile), b, 0o644); err != nil {
		log.Printf("writing %s: %v", weeklyFile, err)
	}
}

// weeklyCheck sends the summary when it is due and hasn't gone out yet.
func (a *App) weeklyCheck(ctx context.Context, now time.Time) {
	s := a.cfg.settings()
	if !s.Weekly || len(s.WeeklyChannels) == 0 {
		return
	}
	due := weeklyDue(s, now)
	if a.weeklySent() >= due.UnixMilli() || now.Sub(due) > weeklyLate {
		return
	}
	a.setWeeklySent(now.UnixMilli())
	title, body := a.weeklySummary(ctx, now)
	if body == "" {
		return
	}
	log.Printf("sending the weekly summary")
	a.sendSummary(s.WeeklyChannels, title, body, now)
}

// sendSummary sends a summary to the channels with these IDs; it returns
// how many it was sent to.
func (a *App) sendSummary(ids []string, title, body string, now time.Time) int {
	n := 0
	for _, id := range ids {
		ch, ok := a.cfg.channel(id)
		if !ok || !ch.Enabled {
			continue
		}
		n++
		a.notifier.Send(ch, Notice{Kind: "summary", Title: title, Body: body, Time: now.UnixMilli()}, func(err error) {
			if err != nil {
				log.Printf("weekly summary via %q failed: %v", ch.Name, err)
			}
		}, nil)
	}
	return n
}

// weeklySummary writes the summary of the 7 days before now: each together
// rule's outages (the internet being down, usually), or every host's when
// there is no such rule, the lowest uptime, the most packet loss and the
// alerts. body is empty when ctx ended first.
func (a *App) weeklySummary(ctx context.Context, now time.Time) (title, body string) {
	title = "OmniPing: the last 7 days"
	from, to := now.AddDate(0, 0, -7).UnixMilli(), now.UnixMilli()
	rep, ok := a.buildReport(ctx, from, to, nil, weeklyMinLost, time.Local)
	if !ok {
		return title, ""
	}
	measured := slices.DeleteFunc(slices.Clone(rep.Targets), func(t TargetReport) bool { return t.Samples == 0 })
	if len(measured) == 0 {
		return title, "No pings were recorded in the last 7 days."
	}
	var lines []string

	// Outages: per together rule, else of single hosts.
	var rules []Rule
	a.cfg.read(func(c *Config) {
		for _, r := range c.Rules {
			if r.Enabled && r.Together {
				rules = append(rules, *r)
			}
		}
	})
	targets := a.cfg.targets()
	for _, r := range rules {
		ids := map[string]bool{}
		for _, t := range targets {
			if r.Scope.covers(&t) {
				ids[t.ID] = true
			}
		}
		if len(ids) == 0 {
			continue
		}
		sub, ok := a.buildReport(ctx, from, to, ids, weeklyMinLost, time.Local)
		if !ok {
			return title, ""
		}
		outs, count, total := sub.Together, sub.TogetherCount, sub.TogetherDowntime
		if len(sub.Targets) == 1 {
			t := sub.Targets[0]
			outs, count, total = t.Outages, t.OutageCount, t.Downtime
		}
		lines = append(lines, outageLine(r.Name, outs, count, total, ""))
	}
	if len(rules) == 0 {
		var outs []Outage
		count, total, worst := 0, int64(0), ""
		var longest int64
		for _, t := range measured {
			count += t.OutageCount
			total += t.Downtime
			outs = append(outs, t.Outages...)
			if t.Longest > longest {
				longest, worst = t.Longest, t.Name
			}
		}
		lines = append(lines, outageLine("Outages", outs, count, total, worst))
	}

	low := slices.MinFunc(measured, func(x, y TargetReport) int { return cmpFloat(x.Uptime, y.Uptime) })
	if low.Uptime < 99.995 {
		lines = append(lines, fmt.Sprintf("Lowest uptime: %s, %s", low.Name, uptimeText(low.Uptime)))
	} else {
		lines = append(lines, "Every host was up the whole time.")
	}
	lossy := slices.MaxFunc(measured, func(x, y TargetReport) int { return cmpFloat(x.Loss, y.Loss) })
	if lossy.Loss >= 0.005 {
		lines = append(lines, fmt.Sprintf("Most packet loss: %s, %s", lossy.Name, lossText(lossy.Loss)))
	}

	// Alerts that started in the period, per rule.
	perRule := map[string]int{}
	var order []string
	total := 0
	for _, e := range a.alerts.Events("", maxEvents) {
		if e.Kind != "start" || e.T < from || e.T > to || e.Held != "" {
			continue
		}
		if perRule[e.RuleName] == 0 {
			order = append(order, e.RuleName)
		}
		perRule[e.RuleName]++
		total++
	}
	if total == 0 {
		lines = append(lines, "No alerts.")
	} else {
		slices.SortStableFunc(order, func(x, y string) int { return perRule[y] - perRule[x] })
		parts := make([]string, len(order))
		for i, r := range order {
			parts[i] = fmt.Sprintf("%s %d", r, perRule[r])
		}
		lines = append(lines, fmt.Sprintf("Alerts: %d (%s)", total, strings.Join(parts, ", ")))
	}
	return title, strings.Join(lines, "\n")
}

// outageLine is "Internet down: 2 times, 6 min in total, longest 4 min (Thu
// 21:12)". worst names the host of the longest outage, when it's not obvious.
func outageLine(name string, outs []Outage, count int, total int64, worst string) string {
	switch count {
	case 0:
		return name + ": none"
	case 1:
		o := outs[0]
		return fmt.Sprintf("%s: once, %s (%s)", name, fmtDuration(o.End-o.Start), when(o.Start, worst))
	}
	longest := slices.MaxFunc(outs, func(x, y Outage) int { return int((x.End - x.Start) - (y.End - y.Start)) })
	return fmt.Sprintf("%s: %d times, %s in total, longest %s (%s)", name, count, fmtDuration(total),
		fmtDuration(longest.End-longest.Start), when(longest.Start, worst))
}

func when(t int64, host string) string {
	s := time.UnixMilli(t).In(time.Local).Format("Mon 15:04")
	if host != "" {
		s = host + ", " + s
	}
	return s
}

// uptimeText rounds down, so 99.996 shows as 99.99% rather than 100%.
func uptimeText(p float64) string { return fmt.Sprintf("%.2f%%", math.Floor(p*100)/100) }

func lossText(p float64) string {
	if p < 10 {
		return fmt.Sprintf("%.2f%%", p)
	}
	return fmt.Sprintf("%.1f%%", p)
}

func cmpFloat(x, y float64) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

// getSummary shows the summary as it would go out now.
func (a *App) getSummary(w http.ResponseWriter, r *http.Request) {
	title, body := a.weeklySummary(r.Context(), time.Now())
	if body == "" {
		return
	}
	writeJSON(w, 200, map[string]string{"title": title, "body": body})
}

// sendSummaryNow sends the summary to the chosen channels at once, to try
// it out. It doesn't count as this week's summary.
func (a *App) sendSummaryNow(w http.ResponseWriter, r *http.Request) {
	ids := a.cfg.settings().WeeklyChannels
	if len(ids) == 0 {
		fail(w, 400, "Choose at least one channel for the summary first.")
		return
	}
	now := time.Now()
	title, body := a.weeklySummary(r.Context(), now)
	if body == "" {
		return
	}
	n := a.sendSummary(ids, title, body, now)
	if n == 0 {
		fail(w, 400, "The chosen channels are all turned off.")
		return
	}
	writeJSON(w, 200, map[string]int{"sent": n})
}
