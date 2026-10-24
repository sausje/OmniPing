package main

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"time"

	"github.com/sausje/omniping/internal/icmp"
)

// tcpPing measures how long it takes to open a TCP connection, for hosts that
// do not answer pings. A refused connection still counts as an answer: the
// host replied, the port is just closed (like tcping and PingPlotter).
// Windows retries a refused connection for about two seconds before it
// reports it, so there a closed port measures that wait, not the network.
func tcpPing(ctx context.Context, ip netip.Addr, port int, timeout time.Duration) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var d net.Dialer
	start := time.Now()
	c, err := d.DialContext(ctx, "tcp", netip.AddrPortFrom(ip.Unmap(), uint16(port)).String())
	rtt := time.Since(start)
	switch {
	case err == nil:
		c.Close()
		return rtt, nil
	case errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(err.Error(), "refused"):
		return rtt, nil
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) && ctx.Err() == context.DeadlineExceeded:
		return 0, icmp.ErrTimeout
	case errors.Is(err, context.Canceled):
		return 0, err
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return 0, icmp.ErrTimeout
	}
	return 0, err
}
