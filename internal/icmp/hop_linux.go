//go:build linux

package icmp

import (
	"context"
	"net/netip"
	"syscall"
	"time"
)

// Linux: a UDP probe to a high port, like the classic traceroute. With
// IP_RECVERR the kernel hands the ICMP errors it gets back (time exceeded
// from a router, port unreachable from dst) to the socket's error queue,
// with who sent them. That needs no raw socket and no root.

const (
	ipRecvErr       = 11 // IP_RECVERR
	ipv6RecvErr     = 25 // IPV6_RECVERR
	ipv6UnicastHops = 16 // IPV6_UNICAST_HOPS
	originICMP      = 2  // SO_EE_ORIGIN_ICMP
	originICMP6     = 3  // SO_EE_ORIGIN_ICMP6
	traceBasePort   = 33434
)

// Hop sends one probe to dst that may cross ttl routers. It returns who
// answered: a router (the TTL ran out there) or dst itself (reached).
func Hop(ctx context.Context, dst netip.Addr, ttl int, timeout time.Duration) (netip.Addr, time.Duration, bool, error) {
	dst = dst.Unmap()
	v6 := !dst.Is4()
	family, level, ttlOpt, recvErr := syscall.AF_INET, syscall.IPPROTO_IP, syscall.IP_TTL, ipRecvErr
	if v6 {
		family, level, ttlOpt, recvErr = syscall.AF_INET6, syscall.IPPROTO_IPV6, ipv6UnicastHops, ipv6RecvErr
	}
	fd, err := syscall.Socket(family, syscall.SOCK_DGRAM|syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return netip.Addr{}, 0, false, err
	}
	defer syscall.Close(fd)
	if err := syscall.SetsockoptInt(fd, level, recvErr, 1); err != nil {
		return netip.Addr{}, 0, false, err
	}
	if err := syscall.SetsockoptInt(fd, level, ttlOpt, ttl); err != nil {
		return netip.Addr{}, 0, false, err
	}
	var to syscall.Sockaddr = &syscall.SockaddrInet4{Port: traceBasePort + ttl, Addr: dst.As4()}
	if v6 {
		to = &syscall.SockaddrInet6{Port: traceBasePort + ttl, Addr: dst.As16()}
	}
	start := time.Now()
	if err := syscall.Sendto(fd, []byte("OmniPing traceroute"), 0, to); err != nil {
		return netip.Addr{}, 0, false, err
	}
	deadline := start.Add(timeout)
	buf, oob := make([]byte, 512), make([]byte, 512)
	for {
		_, oobn, _, _, err := syscall.Recvmsg(fd, buf, oob, syscall.MSG_ERRQUEUE)
		if err == nil {
			if from, reached, ok := parseRecvErr(oob[:oobn], v6); ok {
				return from, time.Since(start), reached, nil
			}
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return netip.Addr{}, 0, false, ErrTimeout
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// parseRecvErr reads a sock_extended_err and the address after it.
func parseRecvErr(oob []byte, v6 bool) (netip.Addr, bool, bool) {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return netip.Addr{}, false, false
	}
	for _, m := range msgs {
		d := m.Data
		// struct sock_extended_err: errno (4), origin, type, code, pad,
		// info (4), data (4); then the sender's sockaddr.
		if len(d) < 16 {
			continue
		}
		origin, typ, code := d[4], d[5], d[6]
		var from netip.Addr
		switch {
		case !v6 && origin == originICMP && len(d) >= 16+8:
			from = netip.AddrFrom4([4]byte(d[16+4 : 16+8]))
		case v6 && origin == originICMP6 && len(d) >= 16+24:
			from = netip.AddrFrom16([16]byte(d[16+8 : 16+24]))
		default:
			continue
		}
		switch {
		case !v6 && typ == 11, v6 && typ == 3: // time exceeded on the way
			return from, false, true
		case !v6 && typ == 3, v6 && typ == 1: // unreachable: dst answered (port), or a router gave up
			return from, !v6 && code == 3 || v6 && code == 4, true
		}
	}
	return netip.Addr{}, false, false
}
