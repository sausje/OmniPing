//go:build windows

package icmp

import (
	"context"
	"encoding/binary"
	"net/netip"
	"syscall"
	"time"
	"unsafe"
)

// ipOptionInformation is IP_OPTION_INFORMATION: the TTL (IPv4) or hop limit
// (IPv6) for one echo request.
type ipOptionInformation struct {
	TTL         uint8
	TOS         uint8
	Flags       uint8
	OptionsSize uint8
	OptionsData *byte
}

const statusTTLExpired = 11013 // IP_TTL_EXPIRED_TRANSIT, also IP_HOP_LIMIT_EXCEEDED

// Hop sends one echo request to dst that may cross ttl routers. It returns
// who answered: a router (the TTL ran out there) or dst itself (reached).
func Hop(ctx context.Context, dst netip.Addr, ttl int, timeout time.Duration) (netip.Addr, time.Duration, bool, error) {
	dst = dst.Unmap()
	ms := uintptr(max(timeout/time.Millisecond, 1))
	opt := ipOptionInformation{TTL: uint8(ttl)}
	req := payload(32)
	reply := make([]byte, 256)
	var n uintptr
	var callErr error
	start := counter()
	if dst.Is4() {
		h, _, err := procCreate.Call()
		if h == invalidHandle {
			return netip.Addr{}, 0, false, err
		}
		defer procCloseHandle.Call(h)
		a := dst.As4()
		addr := *(*uint32)(unsafe.Pointer(&a[0]))
		n, _, callErr = procSendEcho.Call(h, uintptr(addr), uintptr(unsafe.Pointer(&req[0])), uintptr(len(req)),
			uintptr(unsafe.Pointer(&opt)), uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)), ms)
	} else {
		h, _, err := procCreate6.Call()
		if h == invalidHandle {
			return netip.Addr{}, 0, false, err
		}
		defer procCloseHandle.Call(h)
		src := sockaddrIn6{Family: syscall.AF_INET6}
		to := sockaddrIn6{Family: syscall.AF_INET6, Addr: dst.As16()}
		n, _, callErr = procSendEcho6.Call(h, 0, 0, 0, uintptr(unsafe.Pointer(&src)), uintptr(unsafe.Pointer(&to)),
			uintptr(unsafe.Pointer(&req[0])), uintptr(len(req)), uintptr(unsafe.Pointer(&opt)),
			uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)), ms)
	}
	rtt := since(start)
	if n == 0 {
		// Windows may report the expired TTL as the error, with the reply
		// still filled in; anything else means nobody answered.
		errno, _ := callErr.(syscall.Errno)
		if uint32(errno) != statusTTLExpired {
			if errno != 0 {
				return netip.Addr{}, 0, false, statusErr(uint32(errno))
			}
			return netip.Addr{}, 0, false, ErrTimeout
		}
	}
	var from netip.Addr
	var st uint32
	if dst.Is4() {
		// ICMP_ECHO_REPLY: Address (4 bytes), Status (4).
		from = netip.AddrFrom4([4]byte(reply[0:4]))
		st = binary.LittleEndian.Uint32(reply[4:8])
	} else {
		// ICMPV6_ECHO_REPLY: a packed IPV6_ADDRESS_EX (port 2, flow 4,
		// address 16, scope 4), then Status at offset 28.
		from = netip.AddrFrom16([16]byte(reply[6:22]))
		st = binary.LittleEndian.Uint32(reply[28:32])
	}
	switch st {
	case 0:
		return from, rtt, true, nil
	case statusTTLExpired:
		return from, rtt, false, nil
	}
	return netip.Addr{}, 0, false, statusErr(st)
}
