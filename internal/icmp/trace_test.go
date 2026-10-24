package icmp

import (
	"context"
	"net/netip"
	"runtime"
	"testing"
	"time"
)

// A traceroute to this computer itself reaches it at the first hop.
func TestTraceLoopback(t *testing.T) {
	hops, err := Trace(context.Background(), netip.MustParseAddr("127.0.0.1"), 4, time.Second)
	if err == ErrNoTrace {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(hops) != 1 || !hops[0].Reached || hops[0].Addr != netip.MustParseAddr("127.0.0.1") {
		t.Errorf("hops %+v, want 127.0.0.1 reached at hop 1", hops)
	}
}

func TestTraceStopsAtFirstReached(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		if _, err := Trace(context.Background(), netip.MustParseAddr("127.0.0.1"), 2, time.Second); err != ErrNoTrace {
			t.Errorf("err = %v, want ErrNoTrace here", err)
		}
	}
}
