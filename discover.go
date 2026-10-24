package main

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

// Found is a device that answered during a scan.
type Found struct {
	IP    string  `json:"ip"`
	RTT   float32 `json:"rtt"`
	Name  string  `json:"name"`
	Known string  `json:"known"` // id of the target that already pings it
}

// ScanState is the progress of the current or last address-range scan.
type ScanState struct {
	Running bool    `json:"running"`
	Start   string  `json:"start"`
	Total   int     `json:"total"`
	Done    int     `json:"done"`
	Current string  `json:"current"`
	Found   []Found `json:"found"`
	Error   string  `json:"error,omitempty"`
}

// Discovery scans an address range for hosts that answer.
type Discovery struct {
	app    *App
	mu     sync.Mutex
	state  ScanState
	cancel context.CancelFunc
}

func (d *Discovery) State() ScanState {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.state
	s.Found = append([]Found{}, d.state.Found...)
	return s
}

func (d *Discovery) Stop() {
	d.mu.Lock()
	if d.cancel != nil {
		d.cancel()
	}
	d.mu.Unlock()
}

// Start scans count IPv4 addresses beginning at start.
func (d *Discovery) Start(start string, count int, timeout time.Duration, names bool) error {
	addr, err := netip.ParseAddr(strings.TrimSpace(start))
	if err != nil || !addr.Unmap().Is4() {
		return errors.New("enter an IPv4 address to start from, like 192.168.1.1")
	}
	addr = addr.Unmap()
	if count < 1 || count > 4096 {
		return errors.New("scan between 1 and 4096 addresses")
	}
	if d.app.pinger == nil {
		return errors.New("pinging is not available on this host")
	}
	d.mu.Lock()
	if d.state.Running {
		d.mu.Unlock()
		return errors.New("a scan is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.state = ScanState{Running: true, Start: addr.String(), Total: count, Found: []Found{}}
	d.mu.Unlock()

	known := map[string]string{}
	for _, t := range d.app.cfg.targets() {
		known[t.IP] = t.ID
	}
	base := binary.BigEndian.Uint32(addr.AsSlice())
	go func() {
		defer cancel()
		sem := make(chan struct{}, 32)
		var wg sync.WaitGroup
		last := time.Now()
		for i := range count {
			if ctx.Err() != nil {
				break
			}
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], base+uint32(i))
			ip := netip.AddrFrom4(b)
			sem <- struct{}{}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				rtt, err := d.app.pinger.Ping(ctx, ip, 32, timeout)
				var f *Found
				if err == nil {
					f = &Found{IP: ip.String(), RTT: float32(rtt.Microseconds()) / 1000, Known: known[ip.String()]}
					if names {
						lctx, lcancel := context.WithTimeout(ctx, 3*time.Second)
						if n, err := net.DefaultResolver.LookupAddr(lctx, ip.String()); err == nil && len(n) > 0 {
							f.Name = strings.TrimSuffix(n[0], ".")
						}
						lcancel()
					}
				}
				d.mu.Lock()
				d.state.Done++
				d.state.Current = ip.String()
				if f != nil {
					d.state.Found = append(d.state.Found, *f)
					slices.SortFunc(d.state.Found, func(a, b Found) int {
						return netip.MustParseAddr(a.IP).Compare(netip.MustParseAddr(b.IP))
					})
				}
				d.mu.Unlock()
			}()
			if time.Since(last) > 250*time.Millisecond {
				last = time.Now()
				d.app.hub.Broadcast("discover", d.State())
			}
		}
		wg.Wait()
		d.mu.Lock()
		d.state.Running = false
		if ctx.Err() != nil && d.state.Done < d.state.Total {
			d.state.Error = "stopped"
		}
		d.mu.Unlock()
		d.app.hub.Broadcast("discover", d.State())
	}()
	return nil
}
