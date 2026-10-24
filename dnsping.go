package main

// The DNS check: instead of pinging a DNS server, ask it to look up a name
// and time the answer. A server can answer pings while it can't look up
// names, and then the internet seems broken; only this check shows that.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/sausje/omniping/internal/icmp"
)

// dnsPing asks the DNS server at ip (port 0 means 53) for the address of
// name and times the answer. "No such name" counts as an answer, since the
// server works; a server that fails or refuses counts as lost, like no
// answer at all. It sends its own query rather than using the system's
// resolver, so no cache answers for the server.
func dnsPing(ctx context.Context, ip netip.Addr, port int, name string, timeout time.Duration) (time.Duration, error) {
	if port == 0 {
		port = 53
	}
	q, id, err := dnsQuery(name)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", netip.AddrPortFrom(ip.Unmap(), uint16(port)).String())
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) })
	defer stop()

	start := time.Now()
	if _, err := conn.Write(q); err != nil {
		return 0, err
	}
	buf := make([]byte, 1500)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				if ctx.Err() == context.Canceled {
					return 0, context.Canceled
				}
				return 0, icmp.ErrTimeout
			}
			return 0, err // nothing listening on the port, say
		}
		rtt := time.Since(start)
		// Only our own answer counts: same ID, and marked as an answer.
		if n < 12 || binary.BigEndian.Uint16(buf) != id || buf[2]&0x80 == 0 {
			continue
		}
		switch rcode := buf[3] & 0x0f; rcode {
		case 0, 3: // an answer, or "no such name"
			return rtt, nil
		case 2:
			return 0, errors.New("the DNS server could not look the name up (SERVFAIL)")
		case 5:
			return 0, errors.New("the DNS server refused to look the name up (REFUSED)")
		default:
			return 0, fmt.Errorf("the DNS server answered with error code %d", rcode)
		}
	}
}

// dnsQuery builds a query for name's IPv4 address (an A record), asking the
// server to look it up for us. It returns the query and its ID.
func dnsQuery(name string) ([]byte, uint16, error) {
	name = strings.TrimSuffix(name, ".")
	if !validLookup(name) {
		return nil, 0, fmt.Errorf("%q is not a name to look up", name)
	}
	id := uint16(rand.N(1 << 16))
	q := make([]byte, 12, 12+len(name)+6)
	binary.BigEndian.PutUint16(q[0:], id)
	q[2] = 0x01                          // recursion desired
	binary.BigEndian.PutUint16(q[4:], 1) // one question
	for _, label := range strings.Split(name, ".") {
		q = append(q, byte(len(label)))
		q = append(q, label...)
	}
	q = append(q, 0, 0, 1, 0, 1) // end of name, type A, class IN
	return q, id, nil
}

// validLookup tells whether name can be asked for: labels of 1 to 63
// characters, 253 in all.
func validLookup(name string) bool {
	name = strings.TrimSuffix(name, ".")
	if name == "" || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		for _, c := range label {
			if c <= ' ' || c > '~' {
				return false
			}
		}
	}
	return true
}
