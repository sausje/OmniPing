package main

// The traceroute when the internet drops: when a "together" rule starts
// (like "Internet down"), OmniPing traces the route to one of its internet
// hosts once and keeps the result in the alert history. The last hop that
// still answered shows where the connection breaks: in the house, at the
// provider, or further out.

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/sausje/omniping/internal/icmp"
)

// TraceHop is one hop of a stored traceroute.
type TraceHop struct {
	TTL     int     `json:"ttl"`
	Addr    string  `json:"addr,omitempty"` // empty when nothing answered
	Name    string  `json:"name,omitempty"` // a target's name, "your router" or "your provider"
	RTT     float64 `json:"rtt"`            // ms, -1 without an answer
	Reached bool    `json:"reached,omitempty"`
}

const (
	traceMaxHops = 15
	traceTimeout = 1500 * time.Millisecond
	// traceEvery limits a rule that keeps flapping to one trace per period.
	traceEvery = 5 * time.Minute
)

// traceFn runs the traceroute; a variable for tests.
var traceFn = icmp.Trace

var (
	traceMu   sync.Mutex
	lastTrace = map[string]time.Time{}
)

// traceOutage traces the route to the first internet host of the rule's
// members and adds the result to the alert history.
func (a *App) traceOutage(r Rule, members []Target) {
	traceMu.Lock()
	if time.Since(lastTrace[r.ID]) < traceEvery {
		traceMu.Unlock()
		return
	}
	lastTrace[r.ID] = time.Now()
	traceMu.Unlock()

	dst, ok := traceDestination(members)
	if !ok {
		return
	}
	ip, _ := netip.ParseAddr(dst.IP)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	hops, err := traceFn(ctx, ip, traceMaxHops, traceTimeout)
	if err != nil {
		log.Printf("traceroute to %s: %v", dst.IP, err)
		return
	}
	th := nameHops(hops, a.cfg.targets())
	ev := &Event{
		ID: newID(), T: time.Now().UnixMilli(), Kind: "trace", Rule: r.ID, RuleName: r.Name,
		Target: dst.ID, TargetName: dst.Name, Title: "Traceroute to " + dst.Name,
		Text: traceVerdict(th, dst.Name) + "\n" + traceText(th), Trace: th,
	}
	log.Printf("traceroute to %s: %s", dst.IP, traceVerdict(th, dst.Name))
	a.alerts.add(ev)
}

// traceDestination picks the first member on the internet (a public
// address), or else the first with an address at all.
func traceDestination(members []Target) (Target, bool) {
	var first *Target
	for i, m := range members {
		ip, err := netip.ParseAddr(m.IP)
		if err != nil {
			continue
		}
		if !localAddr(ip) {
			return m, true
		}
		if first == nil {
			first = &members[i]
		}
	}
	if first == nil {
		return Target{}, false
	}
	return *first, true
}

// localAddr is an address that isn't on the internet: private, link-local,
// loopback or carrier-grade NAT.
func localAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}

// nameHops turns the trace into stored hops, named after targets with the
// same address, "your router" for a private first hop and "your provider"
// for the first unnamed hop after it.
func nameHops(hops []icmp.HopResult, targets []Target) []TraceHop {
	names := map[string]string{}
	for _, t := range targets {
		if t.IP != "" && t.Lookup == "" && t.Port == 0 {
			names[t.IP] = t.Name
		}
	}
	out := make([]TraceHop, len(hops))
	provider := false
	for i, h := range hops {
		th := TraceHop{TTL: h.TTL, RTT: -1, Reached: h.Reached}
		if h.Addr.IsValid() {
			th.Addr = h.Addr.String()
			th.RTT = float64(h.RTT.Microseconds()) / 1000
			switch name, known := names[th.Addr]; {
			case known:
				th.Name = name
			case h.TTL == 1 && localAddr(h.Addr):
				th.Name = "your router"
			case h.TTL > 1 && !provider && !h.Reached:
				th.Name = "your provider"
				provider = true
			}
		}
		out[i] = th
	}
	return out
}

// traceVerdict says in one sentence where the connection breaks.
func traceVerdict(hops []TraceHop, dst string) string {
	var last *TraceHop
	for i := range hops {
		if hops[i].Reached {
			return fmt.Sprintf("The traceroute reached %s: by then the connection was back, or only its pings were lost.", dst)
		}
		if hops[i].Addr != "" {
			last = &hops[i]
		}
	}
	switch {
	case last == nil:
		return "Nothing answered, not even your router: the problem is in your own network, like the router, a cable or Wi-Fi."
	case last.TTL == 1:
		return fmt.Sprintf("%s answers, nothing after it does: the line to your provider is down (the modem, the line or your provider).", hopLabel(*last, true))
	case last.Name == "your provider":
		return fmt.Sprintf("Your provider's first router (%s) answers, nothing after it does: the break is in your provider's network.", last.Addr)
	}
	return fmt.Sprintf("Answers stop after hop %d (%s): the break is in your provider's network or beyond.", last.TTL, hopLabel(*last, false))
}

func hopLabel(h TraceHop, capital bool) string {
	switch {
	case h.Name == "your router" && capital:
		return "Your router (" + h.Addr + ")"
	case h.Name != "":
		return h.Name + " (" + h.Addr + ")"
	}
	return h.Addr
}

// traceText lists the hops, one per line.
func traceText(hops []TraceHop) string {
	var b strings.Builder
	for _, h := range hops {
		switch {
		case h.Addr == "":
			fmt.Fprintf(&b, "%2d  no answer\n", h.TTL)
		case h.Name != "":
			fmt.Fprintf(&b, "%2d  %s  %s  %.1f ms\n", h.TTL, h.Addr, h.Name, h.RTT)
		default:
			fmt.Fprintf(&b, "%2d  %s  %.1f ms\n", h.TTL, h.Addr, h.RTT)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
