package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// errAlreadyRunning means the port is taken by another OmniPing; the
// platform opens that one instead of starting a second.
var errAlreadyRunning = errors.New("OmniPing is already running")

// portTries is how many ports after the chosen one the Windows app tries
// when the chosen one is taken.
const portTries = 20

// listenServer opens host:port for the page. With fallback, a port taken by
// another program makes it try the next ports, up to port+20; got is the
// port it opened. Before moving on it checks whether the program on the port
// is another OmniPing, and returns errAlreadyRunning if so.
func listenServer(host string, port int, fallback bool) (ln net.Listener, got int, err error) {
	ln, err = listenTCP(net.JoinHostPort(host, strconv.Itoa(port)))
	if err == nil {
		return ln, listenerPort(ln), nil
	}
	if !fallback || !isPortTaken(err) {
		return nil, 0, err
	}
	if isOurOmniPing("http://" + net.JoinHostPort(localHost(host), strconv.Itoa(port)) + "/") {
		return nil, 0, errAlreadyRunning
	}
	last := min(port+portTries, 65535)
	for p := port + 1; p <= last; p++ {
		ln, err = listenTCP(net.JoinHostPort(host, strconv.Itoa(p)))
		if err == nil {
			return ln, p, nil
		}
		if !isPortTaken(err) {
			return nil, 0, err
		}
	}
	// Windows reserves whole blocks of ports for Hyper-V, WSL and Docker;
	// past those, any free port is better than not starting.
	if ln, err = listenTCP(net.JoinHostPort(host, "0")); err == nil {
		return ln, listenerPort(ln), nil
	}
	return nil, 0, fmt.Errorf("Ports %d–%d are in use by other programs or reserved by Windows.", port, last)
}

// listenAgain opens host:port, the port OmniPing used before a restart. The
// copy before may still be letting go of it, so it tries for a few seconds.
func listenAgain(host string, port int) net.Listener {
	for range 10 {
		ln, err := listenTCP(net.JoinHostPort(host, strconv.Itoa(port)))
		if err == nil {
			return ln
		}
		if !isPortTaken(err) {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return nil
}

// nearbyPort tells whether listenServer found got among the ports right
// after chosen, rather than as any free port.
func nearbyPort(chosen, got int) bool {
	return got >= chosen && got <= min(chosen+portTries, 65535)
}

func listenerPort(ln net.Listener) int {
	if a, ok := ln.Addr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

// allInterfaces tells whether host means every network rather than one
// address.
func allInterfaces(host string) bool {
	return host == "" || host == "0.0.0.0" || host == "::"
}

// pageServer serves the page on one listener at a time. The Windows app can
// move it to another port, or between this computer only and every network,
// while it runs.
type pageServer struct {
	srv  *http.Server
	errs chan error // the current listener stopped by itself

	mu        sync.Mutex
	ln        net.Listener
	host      string
	port      int
	movedFrom int // the port OmniPing was meant to use at start, when it was taken
	conns     map[net.Conn]struct{}
}

func newPageServer(srv *http.Server) *pageServer {
	w := &pageServer{srv: srv, errs: make(chan error, 1), conns: map[net.Conn]struct{}{}}
	srv.ConnState = w.track
	return w
}

// track keeps a list of open connections, so the ones to an old address
// can be closed after a move.
func (w *pageServer) track(c net.Conn, st http.ConnState) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch st {
	case http.StateNew:
		w.conns[c] = struct{}{}
	case http.StateClosed, http.StateHijacked:
		delete(w.conns, c)
	}
}

// serve starts serving on ln. The caller holds w.mu.
func (w *pageServer) serve(ln net.Listener, host string, port int) {
	w.ln, w.host, w.port = ln, host, port
	go func() {
		err := w.srv.Serve(ln)
		w.mu.Lock()
		current := w.ln == ln
		w.mu.Unlock()
		if current && err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case w.errs <- err:
			default:
			}
		}
	}()
}

// start serves on ln, the listener runServer opened.
func (w *pageServer) start(ln net.Listener, host string, port int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.serve(ln, host, port)
}

// addr returns where the page is served now.
func (w *pageServer) addr() (host string, port, movedFrom int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.host, w.port, w.movedFrom
}

// localURL is the page's address on this computer.
func (w *pageServer) localURL() string {
	host, port, _ := w.addr()
	return "http://" + net.JoinHostPort(localHost(host), strconv.Itoa(port)) + "/"
}

// move serves on host:port instead. The new address is opened first, so
// on an error nothing changes. When only the host changes, the old listener
// has to close first (nothing can share the port on Windows); it is opened
// again if the new one fails. retire closes the old listener and the
// connections to the old address; call it once the reply went out.
func (w *pageServer) move(host string, port int) (retire func(), err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	old, oldHost, oldPort := w.ln, w.host, w.port
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	var ln net.Listener
	if port == oldPort && old != nil {
		w.ln = nil // its Serve ending is expected
		old.Close()
		ln, err = listenTCP(addr)
		if err != nil {
			back, berr := listenTCP(net.JoinHostPort(oldHost, strconv.Itoa(oldPort)))
			if berr != nil {
				log.Printf("lost port %d: %v", oldPort, berr)
				return nil, err
			}
			w.serve(back, oldHost, oldPort)
			return nil, err
		}
	} else {
		ln, err = listenTCP(addr)
		if err != nil {
			return nil, err
		}
	}
	if port != oldPort {
		w.movedFrom = 0 // the user chose this port
	}
	w.serve(ln, host, port)
	return func() {
		if old != nil {
			old.Close()
		}
		w.dropStale()
	}, nil
}

// dropStale closes the connections that came in on an address OmniPing no
// longer listens on, such as a phone's after "Other devices" was turned off.
func (w *pageServer) dropStale() {
	w.mu.Lock()
	defer w.mu.Unlock()
	want, specific := netip.Addr{}, false
	if ip, err := netip.ParseAddr(w.host); err == nil && !allInterfaces(w.host) {
		want, specific = ip.Unmap(), true
	}
	for c := range w.conns {
		ap, err := netip.ParseAddrPort(c.LocalAddr().String())
		if err != nil {
			continue
		}
		if int(ap.Port()) != w.port || (specific && ap.Addr().Unmap() != want) {
			c.Close()
		}
	}
}
