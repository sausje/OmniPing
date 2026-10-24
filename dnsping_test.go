package main

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/sausje/omniping/internal/icmp"
)

// fakeDNS answers every query with rcode, after delay; with drop it never
// answers. It returns its port and the last name asked for.
func fakeDNS(t *testing.T, rcode byte, delay time.Duration, drop bool) (int, func() string) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	asked := make(chan string, 10)
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			q := buf[:n]
			var labels []string
			for i := 12; i < n && q[i] != 0; i += int(q[i]) + 1 {
				labels = append(labels, string(q[i+1:i+1+int(q[i])]))
			}
			asked <- strings.Join(labels, ".")
			if drop {
				continue
			}
			time.Sleep(delay)
			// First a stray answer with another ID, which must be ignored.
			stray := append([]byte(nil), q...)
			binary.BigEndian.PutUint16(stray, binary.BigEndian.Uint16(q)+1)
			stray[2] |= 0x80
			pc.WriteTo(stray, from)
			a := append([]byte(nil), q...)
			a[2] |= 0x80 // an answer
			a[3] = 0x80 | rcode
			pc.WriteTo(a, from)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).Port, func() string {
		select {
		case s := <-asked:
			return s
		case <-time.After(time.Second):
			return ""
		}
	}
}

func TestDNSPing(t *testing.T) {
	lo := netip.MustParseAddr("127.0.0.1")
	ctx := context.Background()

	port, asked := fakeDNS(t, 0, 20*time.Millisecond, false)
	rtt, err := dnsPing(ctx, lo, port, "example.com.", time.Second)
	if err != nil || rtt < 20*time.Millisecond || rtt > 900*time.Millisecond {
		t.Errorf("an answer: rtt %v, err %v", rtt, err)
	}
	if got := asked(); got != "example.com" {
		t.Errorf("asked for %q, want example.com", got)
	}

	port, _ = fakeDNS(t, 3, 0, false) // no such name: the server works
	if _, err := dnsPing(ctx, lo, port, "nothing.invalid", time.Second); err != nil {
		t.Errorf("no such name: %v, want an answer", err)
	}

	port, _ = fakeDNS(t, 2, 0, false) // SERVFAIL
	if _, err := dnsPing(ctx, lo, port, "example.com", time.Second); err == nil || !strings.Contains(err.Error(), "SERVFAIL") {
		t.Errorf("SERVFAIL: %v", err)
	}

	port, _ = fakeDNS(t, 0, 0, true) // no answer
	start := time.Now()
	if _, err := dnsPing(ctx, lo, port, "example.com", 200*time.Millisecond); !errors.Is(err, icmp.ErrTimeout) {
		t.Errorf("no answer: %v, want a timeout", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("the timeout took %v", d)
	}

	if _, err := dnsPing(ctx, lo, port, "bad name", time.Second); err == nil {
		t.Error("a name with a space was asked for")
	}
}

func TestValidLookup(t *testing.T) {
	for name, want := range map[string]bool{
		"example.com": true, "example.com.": true, "a.b.c.d": true, "xn--bcher-kva.example": true,
		"": false, "a..b": false, "bad name": false, strings.Repeat("a", 64) + ".com": false,
	} {
		if got := validLookup(name); got != want {
			t.Errorf("validLookup(%q) = %v", name, got)
		}
	}
}
