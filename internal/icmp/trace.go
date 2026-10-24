package icmp

import (
	"context"
	"errors"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// HopResult is one step of a traceroute: the router that answered when a
// probe ran out of hops (or the destination itself), or nobody.
type HopResult struct {
	TTL     int
	Addr    netip.Addr // invalid when nothing answered
	RTT     time.Duration
	Reached bool // the destination answered
}

// ErrNoTrace is returned where traceroute isn't available.
var ErrNoTrace = errors.New("traceroute is not available on this operating system")

// Trace probes every hop count from 1 to maxHops at the same time, so it
// takes about one timeout even when most hops don't answer. The result ends
// at the first hop that reached dst.
func Trace(ctx context.Context, dst netip.Addr, maxHops int, timeout time.Duration) ([]HopResult, error) {
	dst = dst.Unmap()
	out := make([]HopResult, maxHops)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var fatal error
	for i := range out {
		wg.Add(1)
		go func(ttl int) {
			defer wg.Done()
			from, rtt, reached, err := Hop(ctx, dst, ttl, timeout)
			out[ttl-1] = HopResult{TTL: ttl}
			switch {
			case errors.Is(err, ErrNoTrace):
				mu.Lock()
				fatal = err
				mu.Unlock()
			case from.IsValid():
				out[ttl-1] = HopResult{TTL: ttl, Addr: from.Unmap(), RTT: rtt, Reached: reached}
			}
		}(i + 1)
	}
	wg.Wait()
	if fatal != nil {
		return nil, fatal
	}
	// Routers limit how many of these answers they send, and all probes
	// went out at once: ask once more for the silent hops before the last
	// one that answered (or the first few, when none did).
	last := 3
	for i, h := range out {
		if h.Addr.IsValid() {
			last = i + 1
		}
	}
	for i := range out[:min(last, len(out))] {
		if out[i].Addr.IsValid() || ctx.Err() != nil {
			continue
		}
		wg.Add(1)
		go func(ttl int) {
			defer wg.Done()
			if from, rtt, reached, err := Hop(ctx, dst, ttl, timeout); err == nil && from.IsValid() {
				out[ttl-1] = HopResult{TTL: ttl, Addr: from.Unmap(), RTT: rtt, Reached: reached}
			}
		}(i + 1)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].TTL < out[j].TTL })
	for i, h := range out {
		if h.Reached {
			return out[:i+1], nil
		}
	}
	// Trailing hops that didn't answer say nothing more than the last one.
	end := len(out)
	for end > 1 && !out[end-1].Addr.IsValid() && !out[end-2].Addr.IsValid() {
		end--
	}
	return out[:end], nil
}
