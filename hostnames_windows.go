package main

import (
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// The DNS suffixes of this PC: the domain it is a member of, and what each
// network it is on calls itself (a FRITZ!Box says fritz.box, many routers
// lan or home). <computer name>.<suffix> reaches this PC on that network.
// The network's own DNS server answers for such a name, so hostAllowed only
// trusts it when other devices can reach OmniPing anyway.

var (
	modIphlpapi              = syscall.NewLazyDLL("iphlpapi.dll")
	procGetAdaptersAddresses = modIphlpapi.NewProc("GetAdaptersAddresses")
	procGetComputerNameExW   = modKernel32.NewProc("GetComputerNameExW")
)

// ipAdapterAddresses is the start of IP_ADAPTER_ADDRESSES_LH; OmniPing reads
// no further than OperStatus.
type ipAdapterAddresses struct {
	Length                uint32
	IfIndex               uint32
	Next                  *ipAdapterAddresses
	AdapterName           *byte
	FirstUnicastAddress   uintptr
	FirstAnycastAddress   uintptr
	FirstMulticastAddress uintptr
	FirstDnsServerAddress uintptr
	DnsSuffix             *uint16
	Description           *uint16
	FriendlyName          *uint16
	PhysicalAddress       [8]byte
	PhysicalAddressLength uint32
	Flags                 uint32
	Mtu                   uint32
	IfType                uint32
	OperStatus            uint32
}

const (
	gaaSkipAddresses      = 0x1 | 0x2 | 0x4 | 0x8 // unicast, anycast, multicast and DNS server addresses
	errorBufferOverflow   = 111
	ifOperStatusUp        = 1
	computerNameDnsDomain = 2
)

var suffixCache struct {
	sync.Mutex
	at   time.Time
	list []string
}

// localDNSSuffixes returns the DNS suffixes of this PC, lower-case and
// without dots at the ends. They are looked up again after 30 seconds, as
// a laptop moves between networks.
func localDNSSuffixes() []string {
	suffixCache.Lock()
	defer suffixCache.Unlock()
	if !suffixCache.at.IsZero() && time.Since(suffixCache.at) < 30*time.Second {
		return suffixCache.list
	}
	var list []string
	add := func(s string) {
		s = strings.ToLower(strings.Trim(s, ". "))
		if s != "" && !strings.Contains(s, " ") && !slices.Contains(list, s) {
			list = append(list, s)
		}
	}
	var buf [256]uint16
	n := uint32(len(buf))
	if r, _, _ := procGetComputerNameExW.Call(computerNameDnsDomain, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))); r != 0 {
		add(syscall.UTF16ToString(buf[:n]))
	}
	for _, s := range adapterDNSSuffixes() {
		add(s)
	}
	suffixCache.at, suffixCache.list = time.Now(), list
	return list
}

// adapterDNSSuffixes returns the DNS suffix of each network adapter that is
// up.
func adapterDNSSuffixes() []string {
	size := uint32(16 << 10)
	for range 3 {
		buf := make([]byte, size)
		r, _, _ := procGetAdaptersAddresses.Call(syscall.AF_UNSPEC, gaaSkipAddresses, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
		switch r {
		case 0:
			var out []string
			for a := (*ipAdapterAddresses)(unsafe.Pointer(&buf[0])); a != nil; a = a.Next {
				if a.OperStatus == ifOperStatusUp {
					out = append(out, utf16PtrToString(a.DnsSuffix))
				}
			}
			return out
		case errorBufferOverflow:
			continue // size now holds what is needed
		default:
			return nil
		}
	}
	return nil
}
