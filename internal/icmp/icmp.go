// Package icmp sends ICMP echo requests ("pings") and measures how long the
// reply takes. Each platform has its own implementation: raw or unprivileged
// sockets on Linux and macOS, and the IP Helper API on Windows.
package icmp

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"time"
)

// ErrTimeout is returned when no reply arrived before the timeout.
var ErrTimeout = errors.New("request timed out")

// Pinger sends one echo request at a time per call. Calls may run concurrently.
type Pinger interface {
	// Ping sends an echo request with size bytes of payload to dst and waits
	// for the matching reply. It returns the round-trip time, ErrTimeout, or
	// another error (for example "network unreachable").
	Ping(ctx context.Context, dst netip.Addr, size int, timeout time.Duration) (time.Duration, error)
	// Mode describes how packets are sent, for the status page.
	Mode() string
	Close() error
}

// echoRequest builds an ICMP (v4) or ICMPv6 echo request.
func echoRequest(v6 bool, id, seq uint16, size int) []byte {
	b := make([]byte, 8+size)
	if v6 {
		b[0] = 128
	} else {
		b[0] = 8
	}
	binary.BigEndian.PutUint16(b[4:], id)
	binary.BigEndian.PutUint16(b[6:], seq)
	for i := 8; i < len(b); i++ {
		b[i] = byte(0x20 + (i-8)%64)
	}
	if !v6 {
		// The kernel fills in the ICMPv6 checksum itself; ICMPv4 is ours to do.
		binary.BigEndian.PutUint16(b[2:], checksum(b))
	}
	return b
}

func checksum(b []byte) uint16 {
	var s uint32
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	for s>>16 != 0 {
		s = s&0xffff + s>>16
	}
	return ^uint16(s)
}

func payload(size int) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = byte(0x20 + i%64)
	}
	return b
}
