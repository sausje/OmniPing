package icmp

import (
	"context"
	"net/netip"
	"testing"
	"time"
)

func TestChecksum(t *testing.T) {
	// Echo request, id 1, seq 1, no payload: known checksum 0xf7fd.
	b := []byte{8, 0, 0, 0, 0, 1, 0, 1}
	if got := checksum(b); got != 0xf7fd {
		t.Fatalf("checksum = %#04x, want 0xf7fd", got)
	}
}

func TestPingLoopback(t *testing.T) {
	p, err := New()
	if err != nil {
		t.Skipf("no ICMP access here: %v", err)
	}
	defer p.Close()
	rtt, err := p.Ping(context.Background(), netip.MustParseAddr("127.0.0.1"), 56, 2*time.Second)
	if err != nil {
		t.Fatalf("ping 127.0.0.1 via %s: %v", p.Mode(), err)
	}
	if rtt <= 0 || rtt > time.Second {
		t.Fatalf("implausible rtt %v", rtt)
	}
	t.Logf("127.0.0.1 answered in %v using %s", rtt, p.Mode())
}

func TestPingTimeout(t *testing.T) {
	p, err := New()
	if err != nil {
		t.Skipf("no ICMP access here: %v", err)
	}
	defer p.Close()
	// 192.0.2.0/24 is reserved for documentation and never answers.
	start := time.Now()
	_, err = p.Ping(context.Background(), netip.MustParseAddr("192.0.2.1"), 56, 300*time.Millisecond)
	if err == nil {
		t.Fatal("expected no reply from 192.0.2.1")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("timeout took %v", time.Since(start))
	}
}
