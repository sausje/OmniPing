package main

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/sausje/omniping/internal/icmp"
)

func hop(ttl int, addr string, reached bool) icmp.HopResult {
	h := icmp.HopResult{TTL: ttl, Reached: reached, RTT: 2 * time.Millisecond}
	if addr != "" {
		h.Addr = netip.MustParseAddr(addr)
	}
	return h
}

func TestTraceVerdict(t *testing.T) {
	targets := []Target{{Name: "Router", IP: "192.168.1.1"}}
	for _, c := range []struct {
		hops []icmp.HopResult
		want string
	}{
		{[]icmp.HopResult{hop(1, "", false), hop(2, "", false)}, "Nothing answered, not even your router"},
		{[]icmp.HopResult{hop(1, "192.168.1.1", false), hop(2, "", false)}, "Router (192.168.1.1) answers, nothing after it does: the line to your provider is down"},
		{[]icmp.HopResult{hop(1, "192.168.2.1", false), hop(2, "", false)}, "Your router (192.168.2.1) answers"},
		{[]icmp.HopResult{hop(1, "192.168.1.1", false), hop(2, "10.254.0.1", false), hop(3, "", false)}, "Your provider's first router (10.254.0.1) answers"},
		{[]icmp.HopResult{hop(1, "192.168.1.1", false), hop(2, "10.254.0.1", false), hop(3, "213.51.0.1", false), hop(4, "", false)}, "Answers stop after hop 3 (213.51.0.1)"},
		{[]icmp.HopResult{hop(1, "192.168.1.1", false), hop(2, "1.1.1.1", true)}, "The traceroute reached Cloudflare"},
	} {
		got := traceVerdict(nameHops(c.hops, targets), "Cloudflare")
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("%+v:\n got  %s\n want %s…", c.hops, got, c.want)
		}
	}
}

func TestTraceDestination(t *testing.T) {
	members := []Target{{ID: "lan", IP: "192.168.1.20"}, {ID: "none"}, {ID: "net", IP: "1.1.1.1"}}
	if d, ok := traceDestination(members); !ok || d.ID != "net" {
		t.Errorf("picked %q, want the internet host", d.ID)
	}
	if d, ok := traceDestination(members[:2]); !ok || d.ID != "lan" {
		t.Errorf("picked %q, want the only host with an address", d.ID)
	}
	if _, ok := traceDestination(members[1:2]); ok {
		t.Error("picked a host without an address")
	}
}

// When "Internet down" starts, one traceroute goes to an internet host and
// lands in the alert history.
func TestOutageTraceroute(t *testing.T) {
	app, _ := outageApp(t)
	app.cfg.update(func(c *Config) error {
		ips := map[string]string{"a": "1.1.1.1", "b": "8.8.8.8", "c": "9.9.9.9", "d": "192.168.1.1"}
		for _, x := range c.Targets {
			x.IP = ips[x.ID]
		}
		return nil
	})
	traceMu.Lock()
	lastTrace = map[string]time.Time{}
	traceMu.Unlock()
	traced := make(chan netip.Addr, 4)
	traceFn = func(ctx context.Context, dst netip.Addr, maxHops int, timeout time.Duration) ([]icmp.HopResult, error) {
		traced <- dst
		return []icmp.HopResult{hop(1, "192.168.1.1", false), hop(2, "10.254.0.1", false), hop(3, "", false)}, nil
	}
	t.Cleanup(func() { traceFn = icmp.Trace })

	run(app, 0, 40, func(i int, id string) bool { return id != "d" && i >= 10 })
	select {
	case dst := <-traced:
		if dst.String() != "1.1.1.1" {
			t.Errorf("traced %v, want A's 1.1.1.1", dst)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no traceroute")
	}
	var ev *Event
	for range 50 {
		for _, e := range app.alerts.Events("", 50) {
			if e.Kind == "trace" {
				ev = &e
			}
		}
		if ev != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ev == nil {
		t.Fatal("no trace event")
	}
	if ev.Rule != "net" || len(ev.Trace) != 3 || ev.Trace[0].Name != "D" || ev.Trace[1].Name != "your provider" ||
		!strings.HasPrefix(ev.Text, "Your provider's first router (10.254.0.1) answers") {
		t.Errorf("trace event %+v", ev)
	}
	// Flapping doesn't start a second one right away.
	run(app, 40, 60, func(i int, id string) bool { return id != "d" && i >= 45 && i < 50 })
	run(app, 60, 80, func(i int, id string) bool { return id != "d" && i >= 65 })
	select {
	case <-traced:
		t.Error("a second traceroute within 5 minutes")
	case <-time.After(300 * time.Millisecond):
	}
}
