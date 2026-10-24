package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sausje/omniping/internal/history"
)

func TestReportFindsOutages(t *testing.T) {
	app, _ := outageApp(t)
	run(app, 0, 120, func(i int, id string) bool {
		internet := id != "d"
		return internet && i >= 30 && i < 50 || id == "a" && (i == 70 || i == 71)
	})
	url := fmt.Sprintf("/api/report?from=%d&to=%d&ids=a,b,c&minLost=3&tz=UTC", t0-1000, t0+200_000)
	w := httptest.NewRecorder()
	app.getReport(w, httptest.NewRequest("GET", url, nil))
	var rep Report
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatalf("%v: %s", err, w.Body)
	}
	if len(rep.Targets) != 3 {
		t.Fatalf("%d targets in the report, want 3", len(rep.Targets))
	}
	a := rep.Targets[0]
	if a.Samples != 120 || a.Lost != 22 {
		t.Errorf("a: %d samples, %d lost; want 120 and 22", a.Samples, a.Lost)
	}
	// The 2 lost pings at 70 s are loss, not an outage.
	if a.OutageCount != 1 || a.Outages[0].Start != t0+30_000 || a.Outages[0].End != t0+50_000 || a.Outages[0].Lost != 20 {
		t.Errorf("a: outages %+v (count %d), want one from 30 s to 50 s", a.Outages, a.OutageCount)
	}
	if a.Downtime != 20_000 || a.Longest != 20_000 {
		t.Errorf("a: downtime %d, longest %d; want 20000", a.Downtime, a.Longest)
	}
	if math.Abs(a.Uptime-100*100.0/120) > 0.01 {
		t.Errorf("a: uptime %.3f%%, want %.3f%%", a.Uptime, 100*100.0/120)
	}
	if a.Avg != 5 || a.P95 < 4.9 || a.P95 > 5.1 {
		t.Errorf("a: avg %v, p95 %v; want 5", a.Avg, a.P95)
	}
	if len(rep.Together) != 1 || rep.TogetherDowntime != 20_000 {
		t.Errorf("together: %+v, downtime %d; want one outage of 20 s", rep.Together, rep.TogetherDowntime)
	}
	if len(rep.Days) != 1 || rep.Days[0].Together != 1 || rep.Days[0].Outages != 3 {
		t.Errorf("days: %+v", rep.Days)
	}
	h := time.UnixMilli(t0 + 30_000).UTC().Hour()
	if rep.Hours[h].Lost != 62 {
		t.Errorf("hour %d: %d lost, want 62", h, rep.Hours[h].Lost)
	}
}

func TestReportFromMinuteSummaries(t *testing.T) {
	hours := make([]*PeriodStat, 24)
	for i := range hours {
		hours[i] = &PeriodStat{}
	}
	ta := &tally{interval: 1000, maxGap: 5000, minLost: 3, loc: time.UTC, hours: hours, days: map[string]*PeriodStat{}}
	m := int64(1_800_000_000_000) // a whole minute
	for i, lost := range []int{0, 60, 60, 30, 0} {
		r := float32(10)
		if lost == 60 {
			r = -1
		}
		ta.minute(history.Minute{T: m + int64(i)*60_000, N: 60, Lost: lost, Min: r, Avg: r, Max: r})
	}
	var tr TargetReport
	ta.finish(&tr)
	want := Outage{Start: m + 60_000, End: m + 3*60_000 + 30_000, Lost: 150}
	if len(ta.outages) != 1 || ta.outages[0] != want {
		t.Fatalf("outages %+v, want %+v", ta.outages, want)
	}
	if tr.Samples != 300 || tr.Lost != 150 || tr.Min != 10 || !tr.Minutes {
		t.Errorf("report %+v", tr)
	}
}

func TestIntersect(t *testing.T) {
	x := []Outage{{Start: 0, End: 10}, {Start: 20, End: 30}}
	y := []Outage{{Start: 5, End: 25}}
	got := intersect(x, y)
	if len(got) != 2 || got[0] != (Outage{Start: 5, End: 10}) || got[1] != (Outage{Start: 20, End: 25}) {
		t.Fatalf("intersect = %+v", got)
	}
}
