//go:build windows

package icmp

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Windows has no usable raw ICMP sockets for normal users, so this uses the
// IP Helper API (the same one ping.exe uses). It is meant for trying OmniPing
// on a PC; the Raspberry Pi build uses sockets.

var (
	iphlpapi          = syscall.NewLazyDLL("iphlpapi.dll")
	procCreate        = iphlpapi.NewProc("IcmpCreateFile")
	procCreate6       = iphlpapi.NewProc("Icmp6CreateFile")
	procSendEcho      = iphlpapi.NewProc("IcmpSendEcho")
	procSendEcho6     = iphlpapi.NewProc("Icmp6SendEcho2")
	procCloseHandle   = iphlpapi.NewProc("IcmpCloseHandle")
	invalidHandle     = ^uintptr(0)
	errStatusTimedOut = uint32(11010)

	// Go's clock on Windows only ticks every 0.5–15 ms, too coarse for LAN
	// pings, so round trips are timed with the performance counter.
	kernel32    = syscall.NewLazyDLL("kernel32.dll")
	procQPC     = kernel32.NewProc("QueryPerformanceCounter")
	procQPF     = kernel32.NewProc("QueryPerformanceFrequency")
	qpcFreq     int64
	qpcFreqOnce sync.Once
)

func counter() int64 {
	var c int64
	procQPC.Call(uintptr(unsafe.Pointer(&c)))
	return c
}

func since(start int64) time.Duration {
	qpcFreqOnce.Do(func() { procQPF.Call(uintptr(unsafe.Pointer(&qpcFreq))) })
	if qpcFreq <= 0 {
		return 0
	}
	ticks := counter() - start
	return time.Duration(ticks/qpcFreq*int64(time.Second) + ticks%qpcFreq*int64(time.Second)/qpcFreq)
}

var statusText = map[uint32]string{
	11002: "destination network unreachable",
	11003: "destination host unreachable",
	11004: "destination protocol unreachable",
	11005: "destination port unreachable",
	11013: "TTL expired in transit",
	11050: "general failure",
}

type winPinger struct{}

func New() (Pinger, error) {
	if err := iphlpapi.Load(); err != nil {
		return nil, err
	}
	return winPinger{}, nil
}

func (winPinger) Mode() string { return "Windows ICMP API" }
func (winPinger) Close() error { return nil }

func (winPinger) Ping(ctx context.Context, dst netip.Addr, size int, timeout time.Duration) (time.Duration, error) {
	dst = dst.Unmap()
	ms := uint32(timeout / time.Millisecond)
	if ms == 0 {
		ms = 1
	}
	req := payload(max(size, 1))
	reply := make([]byte, 128+len(req)+8)
	if dst.Is4() {
		return ping4(dst, req[:size:len(req)], req, reply, ms)
	}
	return ping6(dst, req[:size:len(req)], req, reply, ms)
}

func statusErr(st uint32) error {
	if st == errStatusTimedOut {
		return ErrTimeout
	}
	if s, ok := statusText[st]; ok {
		return errors.New(s)
	}
	return fmt.Errorf("ICMP status %d", st)
}

func ping4(dst netip.Addr, data, buf, reply []byte, ms uint32) (time.Duration, error) {
	h, _, err := procCreate.Call()
	if h == invalidHandle {
		return 0, err
	}
	defer procCloseHandle.Call(h)
	a := dst.As4()
	addr := *(*uint32)(unsafe.Pointer(&a[0])) // network byte order, as Windows expects
	start := counter()
	n, _, callErr := procSendEcho.Call(h, uintptr(addr),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(data)), 0,
		uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)), uintptr(ms))
	elapsed := since(start)
	if n == 0 {
		if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
			return 0, statusErr(uint32(errno))
		}
		return 0, ErrTimeout
	}
	// ICMP_ECHO_REPLY starts with Address (4 bytes), Status (4), RoundTripTime (4).
	if st := binary.LittleEndian.Uint32(reply[4:8]); st != 0 {
		return 0, statusErr(st)
	}
	return elapsed, nil
}

type sockaddrIn6 struct {
	Family   uint16
	Port     uint16
	Flowinfo uint32
	Addr     [16]byte
	ScopeID  uint32
}

func ping6(dst netip.Addr, data, buf, reply []byte, ms uint32) (time.Duration, error) {
	h, _, err := procCreate6.Call()
	if h == invalidHandle {
		return 0, err
	}
	defer procCloseHandle.Call(h)
	src := sockaddrIn6{Family: syscall.AF_INET6}
	to := sockaddrIn6{Family: syscall.AF_INET6, Addr: dst.As16()}
	start := counter()
	n, _, callErr := procSendEcho6.Call(h, 0, 0, 0,
		uintptr(unsafe.Pointer(&src)), uintptr(unsafe.Pointer(&to)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(data)), 0,
		uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)), uintptr(ms))
	elapsed := since(start)
	if n == 0 {
		if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
			return 0, statusErr(uint32(errno))
		}
		return 0, ErrTimeout
	}
	// ICMPV6_ECHO_REPLY: a packed 26-byte address, then Status at offset 28.
	if st := binary.LittleEndian.Uint32(reply[28:32]); st != 0 {
		return 0, statusErr(st)
	}
	return elapsed, nil
}
