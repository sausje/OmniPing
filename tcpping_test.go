package main

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sausje/omniping/internal/icmp"
)

func TestTCPPing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	lo := netip.MustParseAddr("127.0.0.1")
	// The round trip on this computer can be shorter than one tick of the
	// Windows clock, so 0 is a fine answer here.
	if rtt, err := tcpPing(context.Background(), lo, port, time.Second); err != nil || rtt < 0 {
		t.Fatalf("open port: rtt %v, err %v", rtt, err)
	}
	ln.Close()
	// Closed now: the refusal still counts as an answer. (Windows retries
	// for about 2 seconds before it reports the refusal.)
	if _, err := tcpPing(context.Background(), lo, port, 5*time.Second); err != nil {
		t.Fatalf("closed port: %v, want an answer", err)
	}
	// 192.0.2.1 is reserved for documentation and never answers.
	start := time.Now()
	_, err = tcpPing(context.Background(), netip.MustParseAddr("192.0.2.1"), 80, 300*time.Millisecond)
	if err != nil && !errors.Is(err, icmp.ErrTimeout) {
		t.Logf("no route here instead of a timeout: %v", err)
	} else if err == nil {
		t.Fatal("192.0.2.1 answered")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}
