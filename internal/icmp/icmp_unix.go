//go:build linux || darwin

package icmp

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
	"sync"
	"syscall"
	"time"
)

type waitKey struct {
	v6   bool
	seq  uint16
	addr netip.Addr
}

type sockPinger struct {
	mode  string
	dgram bool // unprivileged "ping sockets": the kernel owns the ICMP id
	id    uint16
	c4    net.PacketConn
	c6    net.PacketConn

	mu   sync.Mutex
	seq  uint16
	wait map[waitKey]chan time.Time
}

// New opens raw ICMP sockets (needs root or CAP_NET_RAW, which Docker grants
// by default) and falls back to unprivileged ping sockets.
func New() (Pinger, error) {
	p := &sockPinger{id: uint16(rand.Uint32()), wait: map[waitKey]chan time.Time{}}
	c4, rawErr := net.ListenPacket("ip4:icmp", "0.0.0.0")
	if rawErr == nil {
		p.mode, p.c4 = "raw ICMP sockets", c4
		if c6, err := net.ListenPacket("ip6:ipv6-icmp", "::"); err == nil {
			p.c6 = c6
		}
	} else {
		c4, dgErr := listenDgram(false)
		if dgErr != nil {
			return nil, fmt.Errorf("cannot open an ICMP socket: raw sockets: %v; unprivileged ping sockets: %v. Run with CAP_NET_RAW (Docker: cap_add NET_RAW) or allow ping sockets with sysctl net.ipv4.ping_group_range", rawErr, dgErr)
		}
		p.mode, p.c4, p.dgram = "unprivileged ping sockets", c4, true
		if c6, err := listenDgram(true); err == nil {
			p.c6 = c6
		}
	}
	go p.read(p.c4, false)
	if p.c6 != nil {
		go p.read(p.c6, true)
	}
	return p, nil
}

func listenDgram(v6 bool) (net.PacketConn, error) {
	family, proto := syscall.AF_INET, syscall.IPPROTO_ICMP
	var sa syscall.Sockaddr = &syscall.SockaddrInet4{}
	if v6 {
		family, proto, sa = syscall.AF_INET6, syscall.IPPROTO_ICMPV6, &syscall.SockaddrInet6{}
	}
	fd, err := syscall.Socket(family, syscall.SOCK_DGRAM, proto)
	if err != nil {
		return nil, err
	}
	if err := syscall.Bind(fd, sa); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "icmp")
	defer f.Close()
	return net.FilePacketConn(f)
}

func (p *sockPinger) Mode() string { return p.mode }

func (p *sockPinger) Close() error {
	err := p.c4.Close()
	if p.c6 != nil {
		p.c6.Close()
	}
	return err
}

func (p *sockPinger) Ping(ctx context.Context, dst netip.Addr, size int, timeout time.Duration) (time.Duration, error) {
	dst = dst.Unmap()
	v6 := dst.Is6()
	c := p.c4
	if v6 {
		c = p.c6
		if c == nil {
			return 0, errors.New("IPv6 is not available on this host")
		}
	}

	ch := make(chan time.Time, 1)
	p.mu.Lock()
	var k waitKey
	for range 1 << 16 {
		p.seq++
		k = waitKey{v6, p.seq, dst}
		if _, busy := p.wait[k]; !busy {
			break
		}
	}
	p.wait[k] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.wait, k)
		p.mu.Unlock()
	}()

	var to net.Addr = &net.IPAddr{IP: dst.AsSlice(), Zone: dst.Zone()}
	if p.dgram {
		to = &net.UDPAddr{IP: dst.AsSlice(), Zone: dst.Zone()}
	}
	pkt := echoRequest(v6, p.id, k.seq, size)
	start := time.Now()
	if _, err := c.WriteTo(pkt, to); err != nil {
		return 0, err
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case at := <-ch:
		return at.Sub(start), nil
	case <-t.C:
		return 0, ErrTimeout
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (p *sockPinger) read(c net.PacketConn, v6 bool) {
	buf := make([]byte, 64*1024)
	want := byte(0)
	if v6 {
		want = 129
	}
	for {
		n, from, err := c.ReadFrom(buf)
		now := time.Now()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(10 * time.Millisecond) // don't spin on an error that keeps coming back
			continue
		}
		b := buf[:n]
		// Some kernels hand raw IPv4 sockets the IP header too; skip it.
		if !v6 && len(b) >= 20 && b[0]>>4 == 4 {
			if ihl := int(b[0]&0x0f) * 4; len(b) >= ihl+8 {
				b = b[ihl:]
			}
		}
		if len(b) < 8 || b[0] != want || b[1] != 0 {
			continue
		}
		if !p.dgram && binary.BigEndian.Uint16(b[4:]) != p.id {
			continue
		}
		var src netip.Addr
		switch a := from.(type) {
		case *net.IPAddr:
			src, _ = netip.AddrFromSlice(a.IP)
		case *net.UDPAddr:
			src, _ = netip.AddrFromSlice(a.IP)
		}
		k := waitKey{v6, binary.BigEndian.Uint16(b[6:]), src.Unmap()}
		p.mu.Lock()
		ch := p.wait[k]
		p.mu.Unlock()
		if ch != nil {
			select {
			case ch <- now:
			default:
			}
		}
	}
}
