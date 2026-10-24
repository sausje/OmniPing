package main

import (
	"fmt"
	"math"
	"net/http"
	"strings"
)

// serveMetrics answers /metrics in the Prometheus text format, so Grafana
// and friends can graph OmniPing's targets. The statistics cover the last
// "samples to include" pings of each target, like the table.
func (a *App) serveMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	s := a.cfg.settings()
	type row struct {
		labels                  string
		up, rtt, loss, avg, jit float64
		hasRTT, hasAvg, hasJit  bool
		count                   int64
	}
	var rows []row
	for _, t := range a.cfg.targets() {
		if t.Paused {
			continue
		}
		smp := a.store.Last(t.ID, s.SamplesToInclude)
		if len(smp) == 0 {
			continue
		}
		rw := row{count: a.store.Count(t.ID)}
		rw.labels = fmt.Sprintf(`id=%q,target=%q,address=%q,group=%q`, t.ID, label(t.Name), label(t.address(true)), label(t.Group))
		last := smp[len(smp)-1]
		if last.R >= 0 {
			rw.up, rw.rtt, rw.hasRTT = 1, float64(last.R), true
		}
		lost, got, sum, jsum, jn := 0, 0, 0.0, 0.0, 0
		prev := float32(-1)
		for _, x := range smp {
			if x.R < 0 {
				lost++
				continue
			}
			got++
			sum += float64(x.R)
			if prev >= 0 {
				d := float64(x.R - prev)
				if d < 0 {
					d = -d
				}
				jsum += d
				jn++
			}
			prev = x.R
		}
		rw.loss = float64(lost) / float64(len(smp))
		if got > 0 {
			rw.avg, rw.hasAvg = sum/float64(got), true
		}
		if jn > 0 {
			rw.jit, rw.hasJit = jsum/float64(jn), true
		}
		rows = append(rows, rw)
	}

	var b strings.Builder
	metric := func(name, help, kind string, value func(row) (float64, bool)) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
		for _, rw := range rows {
			if v, ok := value(rw); ok {
				fmt.Fprintf(&b, "%s{%s} %g\n", name, rw.labels, math.Round(v*1000)/1000)
			}
		}
	}
	metric("omniping_up", "1 if the last ping got a reply, 0 if it was lost.", "gauge",
		func(rw row) (float64, bool) { return rw.up, true })
	metric("omniping_rtt_milliseconds", "Round-trip time of the last ping.", "gauge",
		func(rw row) (float64, bool) { return rw.rtt, rw.hasRTT })
	metric("omniping_rtt_avg_milliseconds", "Average round-trip time over the last samples to include.", "gauge",
		func(rw row) (float64, bool) { return rw.avg, rw.hasAvg })
	metric("omniping_jitter_milliseconds", "Average change between consecutive round-trip times over the last samples to include.", "gauge",
		func(rw row) (float64, bool) { return rw.jit, rw.hasJit })
	metric("omniping_loss_ratio", "Share of lost pings over the last samples to include (0 to 1).", "gauge",
		func(rw row) (float64, bool) { return rw.loss, true })
	metric("omniping_samples", "Pings stored for the target.", "gauge",
		func(rw row) (float64, bool) { return float64(rw.count), true })
	fmt.Fprintf(&b, "# HELP omniping_alerts_firing Alerts firing right now.\n# TYPE omniping_alerts_firing gauge\nomniping_alerts_firing %d\n", len(a.alerts.Active()))
	fmt.Fprintf(&b, "# HELP omniping_info Version of OmniPing.\n# TYPE omniping_info gauge\nomniping_info{version=%q} 1\n", version)
	w.Write([]byte(b.String()))
}

// label keeps a Prometheus label value on one line; %q then escapes the
// backslashes and quotes the format asks for.
func label(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)
}
