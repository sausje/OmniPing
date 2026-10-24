package main

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/sausje/omniping/internal/history"
	"github.com/sausje/omniping/internal/icmp"
)

// Engine pings every target on its interval and records the results.
//
// Each target has its own goroutine. Pings run concurrently, but results are
// recorded strictly in the order they were sent: a lost packet holds back the
// samples after it until its timeout expires, so history never has gaps or
// reordering.
type Engine struct {
	app *App

	mu      sync.Mutex
	ctx     context.Context
	runners map[string]*runner

	errMu  sync.Mutex
	errors map[string]string // last ping or DNS error per target
}

type runner struct {
	cancel context.CancelFunc
	wake   chan struct{}
}

func newEngine(app *App) *Engine {
	return &Engine{app: app, runners: map[string]*runner{}, errors: map[string]string{}}
}

// Start begins pinging and keeps names resolved until ctx ends.
func (e *Engine) Start(ctx context.Context) {
	e.mu.Lock()
	e.ctx = ctx
	e.mu.Unlock()
	e.Sync(true)
	go e.resolveLoop(ctx)
}

// Sync starts and stops target goroutines to match the config. With restart,
// every target starts over, spread out by the stagger setting.
func (e *Engine) Sync(restart bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ctx == nil {
		return
	}
	targets := e.app.cfg.targets()
	st := e.app.cfg.settings()
	keep := map[string]bool{}
	for i, t := range targets {
		keep[t.ID] = true
		r, ok := e.runners[t.ID]
		if ok && !restart {
			select {
			case r.wake <- struct{}{}:
			default:
			}
			continue
		}
		if ok {
			r.cancel()
		}
		ctx, cancel := context.WithCancel(e.ctx)
		r = &runner{cancel: cancel, wake: make(chan struct{}, 1)}
		e.runners[t.ID] = r
		delay := time.Duration(i*st.StaggerMs) * time.Millisecond
		if st.IntervalMs > 0 {
			delay %= time.Duration(st.IntervalMs) * time.Millisecond
		}
		go e.run(ctx, t.ID, delay, r.wake)
	}
	for id, r := range e.runners {
		if !keep[id] {
			r.cancel()
			delete(e.runners, id)
		}
	}
}

// Stop ends the goroutine of one target (before it is deleted).
func (e *Engine) Stop(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r, ok := e.runners[id]; ok {
		r.cancel()
		delete(e.runners, id)
	}
}

func (e *Engine) setErr(id, msg string) {
	e.errMu.Lock()
	old := e.errors[id]
	if msg == "" {
		delete(e.errors, id)
	} else {
		e.errors[id] = msg
	}
	e.errMu.Unlock()
	if old != msg {
		e.app.hub.Broadcast("targetError", map[string]string{"id": id, "error": msg})
	}
}

// Err returns the last error for a target, or "".
func (e *Engine) Err(id string) string {
	e.errMu.Lock()
	defer e.errMu.Unlock()
	return e.errors[id]
}

func (e *Engine) interval(t Target, s Settings) time.Duration {
	ms := s.IntervalMs
	if t.IntervalMs > 0 {
		ms = t.IntervalMs
	}
	return time.Duration(max(ms, 100)) * time.Millisecond
}

type pending struct {
	t    int64
	r    float32
	done bool
}

type result struct {
	p   *pending
	rtt time.Duration
	err error
}

func (e *Engine) run(ctx context.Context, id string, delay time.Duration, wake chan struct{}) {
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return
	}
	t, ok := e.app.cfg.target(id)
	if !ok {
		return
	}
	interval := e.interval(t, e.app.cfg.settings())
	tick := time.NewTicker(interval)
	defer tick.Stop()
	results := make(chan result, 16)
	var queue []*pending

	send := func() {
		t, ok := e.app.cfg.target(id)
		s := e.app.cfg.settings()
		if !ok || !s.Running || t.Paused {
			return
		}
		p := &pending{t: time.Now().UnixMilli()}
		queue = append(queue, p)
		ip, err := netip.ParseAddr(t.IP)
		if err != nil {
			msg := "waiting for the name to resolve"
			if t.IP == "" && e.Err(id) != "" {
				msg = e.Err(id)
			}
			p.done, p.r = true, history.Lost
			e.setErr(id, msg)
			return
		}
		timeout := time.Duration(s.TimeoutMs) * time.Millisecond
		if t.Port == 0 && t.Lookup == "" && e.app.pinger == nil {
			p.done, p.r = true, history.Lost
			e.setErr(id, "pinging is not available on this server (TCP targets still work)")
			return
		}
		go func() {
			var rtt time.Duration
			var err error
			switch {
			case t.Lookup != "":
				rtt, err = dnsPing(ctx, ip, t.Port, t.Lookup, timeout)
			case t.Port > 0:
				rtt, err = tcpPing(ctx, ip, t.Port, timeout)
			default:
				rtt, err = e.app.pinger.Ping(ctx, ip, s.PacketSize, timeout)
			}
			select {
			case results <- result{p, rtt, err}:
			case <-ctx.Done():
			}
		}()
	}

	send()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
			t, ok := e.app.cfg.target(id)
			if !ok {
				return
			}
			if iv := e.interval(t, e.app.cfg.settings()); iv != interval {
				interval = iv
				tick.Reset(iv)
			}
		case <-tick.C:
			send()
		case res := <-results:
			res.p.done = true
			switch {
			case res.err == nil:
				res.p.r = float32(res.rtt.Microseconds()) / 1000
				e.setErr(id, "")
			case errors.Is(res.err, icmp.ErrTimeout):
				res.p.r = history.Lost
				e.setErr(id, "")
			case errors.Is(res.err, context.Canceled):
				return
			default:
				res.p.r = history.Lost
				e.setErr(id, res.err.Error())
			}
		}
		for len(queue) > 0 && queue[0].done {
			e.record(id, queue[0].t, queue[0].r)
			queue = queue[1:]
		}
	}
}

func (e *Engine) record(id string, t int64, r float32) {
	e.app.store.Append(id, t, r)
	e.app.hub.Sample(id, t, r)
	e.app.alerts.OnSample(id, t)
}

// resolveLoop looks names up when targets are added and again every few
// minutes, so targets behind dynamic DNS follow their address.
func (e *Engine) resolveLoop(ctx context.Context) {
	last := time.Time{}
	for {
		s := e.app.cfg.settings()
		full := time.Since(last) >= time.Duration(s.DNSRefreshMin)*time.Minute
		if full {
			last = time.Now()
		}
		for _, t := range e.app.cfg.targets() {
			if full || t.IP == "" {
				e.resolve(ctx, t)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
	}
}

// ResolveNow looks up one target in the background (after add or edit).
func (e *Engine) ResolveNow(id string) {
	go func() {
		if t, ok := e.app.cfg.target(id); ok {
			e.resolve(context.Background(), t)
		}
	}()
}

func (e *Engine) resolve(ctx context.Context, t Target) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	host := strings.Trim(t.Host, "[]")
	var ip, name string
	if addr, err := netip.ParseAddr(host); err == nil {
		ip = addr.Unmap().String()
		if names, err := net.DefaultResolver.LookupAddr(ctx, ip); err == nil && len(names) > 0 {
			name = strings.TrimSuffix(names[0], ".")
		}
	} else {
		addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addrs) == 0 {
			if t.IP == "" {
				e.setErr(t.ID, "cannot resolve "+host)
			}
			return
		}
		pick := addrs[0]
		for _, a := range addrs {
			if a.Unmap().Is4() {
				pick = a
				break
			}
		}
		ip, name = pick.Unmap().String(), host
	}

	var old string
	changed, grouped := false, false
	e.app.cfg.update(func(c *Config) error {
		for _, x := range c.Targets {
			if x.ID == t.ID {
				old = x.IP
				if x.IP != ip || (name != "" && x.DNSName != name) {
					x.IP = ip
					if name != "" {
						x.DNSName = name
					}
					changed = true
				}
				if x.AutoGroup {
					assignAutoGroup(c, x)
					grouped = x.Group != ""
				}
			}
		}
		return nil
	})
	if grouped {
		e.app.changed() // a group was filled in (and maybe created)
	}
	if !changed {
		return
	}
	if e.Err(t.ID) != "" && strings.HasPrefix(e.Err(t.ID), "cannot resolve") {
		e.setErr(t.ID, "")
	}
	if nt, ok := e.app.cfg.target(t.ID); ok {
		e.app.hub.Broadcast("target", e.app.targetDTO(nt))
	}
	if old != "" && old != ip {
		e.app.alerts.OnIPChange(t.ID, old, ip)
	}
}
