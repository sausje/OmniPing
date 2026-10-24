//go:build !linux && !windows

package icmp

import (
	"context"
	"net/netip"
	"time"
)

// Hop isn't available here (macOS needs raw sockets for it).
func Hop(ctx context.Context, dst netip.Addr, ttl int, timeout time.Duration) (netip.Addr, time.Duration, bool, error) {
	return netip.Addr{}, 0, false, ErrNoTrace
}
