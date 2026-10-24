package main

// The report: uptime, packet loss and outages over a period, the evidence an
// internet provider asks for (or the reassurance that all was well).

import (
	"cmp"
	"context"
	"math"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // time zones for the report's hours and days, also on Windows

	"github.com/sausje/omniping/internal/history"
)

// Outage is a run of lost pings: from the first lost ping to the first
// reply after it.
type Outage struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
	Lost  int   `json:"lost"` // pings lost (for one target)
}

// TargetReport sums up one target over the period.
type TargetReport struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Address     string   `json:"address"`
	Group       string   `json:"group"`
	Interval    int64    `json:"interval"`
	Samples     int64    `json:"samples"`
	Lost        int64    `json:"lost"`
	Loss        float64  `json:"loss"`      // % of pings lost
	Uptime      float64  `json:"uptime"`    // % of monitored time not in an outage
	Monitored   int64    `json:"monitored"` // ms with measurements
	Outages     []Outage `json:"outages"`   // the first 1000
	OutageCount int      `json:"outageCount"`
	Downtime    int64    `json:"downtime"` // ms
	Longest     int64    `json:"longest"`  // ms
	Avg         float64  `json:"avg"`      // ms, -1 without replies
	Min         float64  `json:"min"`
	Max         float64  `json:"max"`
	P50         float64  `json:"p50"`
	P95         float64  `json:"p95"`
	P99         float64  `json:"p99"`
	First       int64    `json:"first"` // first sample in the period, 0 if none
	Last        int64    `json:"last"`
	Minutes     bool     `json:"minutes"`  // part of the period only had one-minute summaries
	OffTimes    bool     `json:"offTimes"` // its expected-off times are left out
}

// PeriodStat sums up all selected targets over an hour of the day or a day.
type PeriodStat struct {
	Key       string  `json:"key"` // "0".."23" or "2026-10-13"
	Samples   int64   `json:"samples"`
	Lost      int64   `json:"lost"`
	Loss      float64 `json:"loss"`
	Avg       float64 `json:"avg"`       // -1 without replies
	Outages   int     `json:"outages"`   // outages of single targets
	Downtime  int64   `json:"downtime"`  // of single targets, added up
	Together  int     `json:"together"`  // outages of all selected targets at once
	TogetherD int64   `json:"togetherD"` // their downtime
	got       int64
	sum       float64
}

// Report is the answer to /api/report.
type Report struct {
	From      int64          `json:"from"`
	To        int64          `json:"to"`
	Generated int64          `json:"generated"`
	Host      string         `json:"host"`
	Version   string         `json:"version"`
	Zone      string         `json:"zone"`
	MinLost   int            `json:"minLost"`
	Samples   int64          `json:"samples"`
	Lost      int64          `json:"lost"`
	Loss      float64        `json:"loss"`
	Targets   []TargetReport `json:"targets"`
	// Together lists the times every selected target was down at once (with
	// two or more targets): the internet itself, if they are internet hosts.
	Together         []Outage `json:"together"` // the first 1000
	TogetherCount    int      `json:"togetherCount"`
	TogetherDowntime int64    `json:"togetherDowntime"`
	// Traces are the traceroutes taken when the internet dropped.
	Traces []Event      `json:"traces"`
	Hours  []PeriodStat `json:"hours"`
	Days   []PeriodStat `json:"days"`
}

const (
	histN      = 1000
	histLow    = 0.01 // ms
	maxOutages = 1000
)

var histStep = math.Log(1e7) / histN // 0.01 ms to 100 s

// tally walks one target's samples in time order.
type tally struct {
	interval, maxGap int64
	minLost          int
	loc              *time.Location
	hours            []*PeriodStat
	days             map[string]*PeriodStat

	samples, lost, got int64
	sum                float64
	min, max           float32
	seen               bool
	hist               [histN]int64
	monitored          int64
	prevT, first       int64
	minutes            bool

	runStart int64
	runLost  int
	outages  []Outage

	hourFrom, hourTo int64 // local hour the cached keys belong to
	hour             *PeriodStat
	day              *PeriodStat
}

func (a *tally) when(t int64) {
	if t >= a.hourFrom && t < a.hourTo {
		return
	}
	lt := time.UnixMilli(t).In(a.loc)
	start := time.Date(lt.Year(), lt.Month(), lt.Day(), lt.Hour(), 0, 0, 0, a.loc)
	a.hourFrom, a.hourTo = start.UnixMilli(), start.Add(time.Hour).UnixMilli()
	a.hour = a.hours[lt.Hour()]
	key := lt.Format("2006-01-02")
	if a.days[key] == nil {
		a.days[key] = &PeriodStat{Key: key, Avg: -1}
	}
	a.day = a.days[key]
}

func (a *tally) gap(t int64) {
	if a.prevT == 0 {
		a.first = t
		return
	}
	if g := t - a.prevT; g > a.maxGap {
		// No measurements in between (stopped, paused): not an outage.
		a.endRun(a.prevT + a.interval)
		a.monitored += a.interval
	} else {
		a.monitored += g
	}
}

func (a *tally) count(n, lost int64, sum float64, got int64) {
	a.samples += n
	a.lost += lost
	a.got += got
	a.sum += sum
	for _, p := range []*PeriodStat{a.hour, a.day} {
		p.Samples += n
		p.Lost += lost
		p.got += got
		p.sum += sum
	}
}

// reply notes a round trip for the minimum, maximum and, with a weight, the
// percentiles.
func (a *tally) reply(r float32, weight int64) {
	if !a.seen || r < a.min {
		a.min = r
	}
	if !a.seen || r > a.max {
		a.max = r
	}
	a.seen = true
	if weight > 0 {
		i := int(math.Log(float64(max(r, histLow))/histLow) / histStep)
		a.hist[clamp(i, 0, histN-1)] += weight
	}
}

func (a *tally) sample(x history.Sample) {
	a.gap(x.T)
	a.prevT = x.T
	a.when(x.T)
	if x.R < 0 {
		a.count(1, 1, 0, 0)
		if a.runLost == 0 {
			a.runStart = x.T
		}
		a.runLost++
		return
	}
	a.count(1, 0, float64(x.R), 1)
	a.reply(x.R, 1)
	a.endRun(x.T)
}

// minute adds a one-minute summary, for days without raw samples. Outages
// are then known to about a minute.
func (a *tally) minute(m history.Minute) {
	if m.N == 0 {
		return
	}
	a.minutes = true
	a.gap(m.T)
	a.prevT = m.T + 60_000 - a.interval
	a.monitored += 60_000 - a.interval
	a.when(m.T)
	got := int64(m.N - m.Lost)
	if got > 0 && m.Avg >= 0 {
		a.count(int64(m.N), int64(m.Lost), float64(m.Avg)*float64(got), got)
		a.reply(m.Min, 0)
		a.reply(m.Max, 0)
		a.reply(m.Avg, got)
	} else {
		a.count(int64(m.N), int64(m.Lost), 0, 0)
	}
	frac := float64(m.Lost) / float64(m.N)
	switch {
	case a.runLost > 0 && m.Lost == m.N:
		a.runLost += m.N
	case a.runLost > 0:
		a.runLost += m.Lost
		a.endRun(m.T + int64(frac*60_000))
	case m.Lost == m.N:
		a.runStart, a.runLost = m.T, m.N
	case frac >= 0.5: // most of the minute lost: likely the start of an outage
		a.runStart, a.runLost = m.T+int64((1-frac)*60_000), m.Lost
	}
}

func (a *tally) endRun(end int64) {
	if a.runLost >= a.minLost && end > a.runStart {
		a.outages = append(a.outages, Outage{Start: a.runStart, End: end, Lost: a.runLost})
	}
	a.runLost = 0
}

func (a *tally) finish(t *TargetReport) {
	if a.prevT != 0 {
		a.monitored += a.interval
		a.endRun(a.prevT + a.interval)
	}
	t.Samples, t.Lost, t.Monitored, t.First, t.Last, t.Minutes = a.samples, a.lost, a.monitored, a.first, a.prevT, a.minutes
	t.Avg, t.Min, t.Max, t.P50, t.P95, t.P99 = -1, -1, -1, -1, -1, -1
	if a.samples > 0 {
		t.Loss = float64(a.lost) * 100 / float64(a.samples)
	}
	if a.got > 0 {
		t.Avg, t.Min, t.Max = a.sum/float64(a.got), float64(a.min), float64(a.max)
		t.P50, t.P95, t.P99 = a.percentile(0.50), a.percentile(0.95), a.percentile(0.99)
	}
	for _, o := range a.outages {
		d := o.End - o.Start
		t.Downtime += d
		t.Longest = max(t.Longest, d)
	}
	t.Uptime = 100
	if a.monitored > 0 {
		t.Uptime = max(0, 100*float64(a.monitored-t.Downtime)/float64(a.monitored))
	}
}

func (a *tally) percentile(p float64) float64 {
	var total int64
	for _, n := range a.hist {
		total += n
	}
	want := int64(math.Ceil(p * float64(total)))
	var run int64
	for i, n := range a.hist {
		run += n
		if run >= want && n > 0 {
			return histLow * math.Exp((float64(i)+0.5)*histStep)
		}
	}
	return float64(a.max)
}

// intersect returns the times covered by both lists of outages.
func intersect(x, y []Outage) []Outage {
	var out []Outage
	for i, j := 0, 0; i < len(x) && j < len(y); {
		s, e := max(x[i].Start, y[j].Start), min(x[i].End, y[j].End)
		if s < e {
			out = append(out, Outage{Start: s, End: e})
		}
		if x[i].End < y[j].End {
			i++
		} else {
			j++
		}
	}
	return out
}

func (a *App) getReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := time.Now().UnixMilli()
	to, err := strconv.ParseInt(q.Get("to"), 10, 64)
	if err != nil || to <= 0 || to > now {
		to = now
	}
	from, err := strconv.ParseInt(q.Get("from"), 10, 64)
	if err != nil || from <= 0 || from >= to {
		from = to - 7*86_400_000
	}
	from = max(from, to-400*86_400_000)
	minLost, err := strconv.Atoi(q.Get("minLost"))
	if err != nil || minLost < 1 {
		minLost = 3
	}
	loc := time.Local
	if z := q.Get("tz"); z != "" {
		if l, err := time.LoadLocation(z); err == nil {
			loc = l
		}
	}
	want := map[string]bool{}
	for _, id := range strings.Split(q.Get("ids"), ",") {
		if id != "" {
			want[id] = true
		}
	}
	rep, ok := a.buildReport(r.Context(), from, to, want, minLost, loc)
	if ok {
		writeJSON(w, 200, rep)
	}
}

// buildReport sums up the targets in want (every target when want is empty)
// from from to to. ok is false when ctx ended first.
func (a *App) buildReport(ctx context.Context, from, to int64, want map[string]bool, minLost int, loc *time.Location) (rep Report, ok bool) {
	now := time.Now().UnixMilli()
	s := a.cfg.settings()
	host, _ := os.Hostname()
	rep = Report{From: from, To: to, Generated: now, Host: host, Version: version, Zone: loc.String(), MinLost: minLost,
		Targets: []TargetReport{}, Together: []Outage{}}
	hours := make([]*PeriodStat, 24)
	for h := range hours {
		hours[h] = &PeriodStat{Key: strconv.Itoa(h), Avg: -1}
	}
	days := map[string]*PeriodStat{}
	var all [][]Outage
	for _, t := range a.cfg.targets() {
		if len(want) > 0 && !want[t.ID] {
			continue
		}
		iv := int64(s.IntervalMs)
		if t.IntervalMs > 0 {
			iv = int64(t.IntervalMs)
		}
		ta := &tally{interval: iv, maxGap: 3*iv + int64(s.TimeoutMs), minLost: minLost, loc: loc, hours: hours, days: days}
		// Samples in its expected-off times are left out, like time it
		// wasn't pinged: neither up nor down.
		off := len(t.Off) > 0
		a.store.Scan(t.ID, from, to, func(c history.Chunk) bool {
			for _, x := range c.Raw {
				if !off || !t.offAt(x.T) {
					ta.sample(x)
				}
			}
			for _, m := range c.Minutes {
				if !off || !t.offAt(m.T) {
					ta.minute(m)
				}
			}
			return ctx.Err() == nil
		})
		tr := TargetReport{ID: t.ID, Name: t.Name, Address: t.address(true), Group: t.Group, Interval: iv, OffTimes: len(t.Off) > 0}
		ta.finish(&tr)
		tr.OutageCount = len(ta.outages)
		tr.Outages = ta.outages[:min(len(ta.outages), maxOutages)]
		if tr.Outages == nil {
			tr.Outages = []Outage{}
		}
		for _, o := range ta.outages {
			ta.when(o.Start)
			ta.day.Outages++
			ta.day.Downtime += o.End - o.Start
		}
		rep.Samples += tr.Samples
		rep.Lost += tr.Lost
		rep.Targets = append(rep.Targets, tr)
		if tr.Samples > 0 {
			all = append(all, ta.outages)
		}
	}
	if ctx.Err() != nil {
		return rep, false
	}
	if rep.Samples > 0 {
		rep.Loss = float64(rep.Lost) * 100 / float64(rep.Samples)
	}
	if len(all) >= 2 {
		together := all[0]
		for _, o := range all[1:] {
			together = intersect(together, o)
		}
		ta := &tally{loc: loc, hours: hours, days: days}
		rep.TogetherCount = len(together)
		for _, o := range together {
			rep.TogetherDowntime += o.End - o.Start
			ta.when(o.Start)
			ta.day.Together++
			ta.day.TogetherD += o.End - o.Start
		}
		rep.Together = together[:min(len(together), maxOutages)]
		if rep.Together == nil {
			rep.Together = []Outage{}
		}
	}
	finishStat := func(p *PeriodStat) PeriodStat {
		if p.Samples > 0 {
			p.Loss = float64(p.Lost) * 100 / float64(p.Samples)
		}
		if p.got > 0 {
			p.Avg = p.sum / float64(p.got)
		}
		return *p
	}
	for _, h := range hours {
		rep.Hours = append(rep.Hours, finishStat(h))
	}
	for _, d := range days {
		rep.Days = append(rep.Days, finishStat(d))
	}
	slices.SortFunc(rep.Days, func(x, y PeriodStat) int { return cmp.Compare(x.Key, y.Key) })
	if rep.Days == nil {
		rep.Days = []PeriodStat{}
	}
	rep.Traces = []Event{}
	if a.alerts != nil {
		evs := a.alerts.Events("", maxEvents)
		for i := len(evs) - 1; i >= 0; i-- { // oldest first
			if e := evs[i]; e.Kind == "trace" && e.T >= from && e.T <= to {
				rep.Traces = append(rep.Traces, e)
			}
		}
	}
	return rep, true
}
