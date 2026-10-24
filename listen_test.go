package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

func TestListenMovesToTheNextFreePort(t *testing.T) {
	busy := httptest.NewServer(http.NotFoundHandler()) // another program
	defer busy.Close()
	p := serverPort(t, busy)

	if _, _, err := listenServer("127.0.0.1", p, false); err == nil || !isPortTaken(err) {
		t.Fatalf("without fallback: err = %v, want a port-taken error", err)
	}
	ln, got, err := listenServer("127.0.0.1", p, true)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if got <= p || got > p+portTries || listenerPort(ln) != got {
		t.Errorf("port %d is taken; got port %d (listener on %v)", p, got, ln.Addr())
	}
}

func TestListenFindsAnotherOmniPing(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.Header().Set(omniPingHeader, "test")
			w.Header().Set(ownerHeader, ownerID())
		}
	}))
	defer other.Close()
	if _, _, err := listenServer("127.0.0.1", serverPort(t, other), true); !errors.Is(err, errAlreadyRunning) {
		t.Fatalf("err = %v, want errAlreadyRunning", err)
	}
}

// Another Windows user's OmniPing on the port is like any other program.
func TestListenPassesAnotherUsersOmniPing(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.Header().Set(omniPingHeader, "test")
			w.Header().Set(ownerHeader, "someone-else")
		}
	}))
	defer other.Close()
	p := serverPort(t, other)
	ln, got, err := listenServer("127.0.0.1", p, true)
	if err != nil {
		t.Fatalf("err = %v, want another port", err)
	}
	ln.Close()
	if got == p {
		t.Errorf("got the taken port %d", p)
	}
}

// When the ports after the chosen one are taken too (Windows reserves whole
// blocks of them), any free port is better than not starting.
func TestListenFallsBackToAnyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:65535")
	if err != nil {
		t.Skip("port 65535 is in use here")
	}
	go http.Serve(ln, http.NotFoundHandler())
	defer ln.Close()
	got, p, err := listenServer("127.0.0.1", 65535, true)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	got.Close()
	if p == 65535 || p == 0 {
		t.Errorf("port %d", p)
	}
}

// Only a port found right after the chosen one is kept for the next start.
func TestNearbyPort(t *testing.T) {
	for _, c := range []struct {
		chosen, got int
		want        bool
	}{
		{8080, 8080, true}, {8080, 8081, true}, {8080, 8100, true},
		{8080, 8101, false}, {8080, 51955, false}, {8080, 8079, false},
		{65530, 65535, true}, {65530, 50000, false},
	} {
		if got := nearbyPort(c.chosen, c.got); got != c.want {
			t.Errorf("nearbyPort(%d, %d) = %v", c.chosen, c.got, got)
		}
	}
}

// keepAlive opens a connection to addr and makes one request on it, which
// the server keeps open for the next.
func keepAlive(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	fmt.Fprintf(c, "GET /healthz HTTP/1.1\r\nHost: %s\r\n\r\n", addr)
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return c
}

// closedSoon tells whether the server closes c within a few seconds.
func closedSoon(c net.Conn) bool {
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err := c.Read(make([]byte, 1))
	var ne net.Error
	return err != nil && !(errors.As(err, &ne) && ne.Timeout())
}

func answers(addr string) bool {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func TestPageServerMoves(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	srv := &http.Server{Handler: mux}
	defer srv.Close()
	ln, port, err := listenServer("127.0.0.1", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	ps := newPageServer(srv)
	ps.start(ln, "127.0.0.1", port)
	old := net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
	conn := keepAlive(t, old)

	// A port that is taken changes nothing.
	busy := httptest.NewServer(http.NotFoundHandler())
	defer busy.Close()
	if _, err := ps.move("127.0.0.1", serverPort(t, busy)); err == nil || !isPortTaken(err) {
		t.Fatalf("moving to a taken port: err = %v", err)
	}
	if !answers(old) {
		t.Fatal("the page is gone after a failed move")
	}

	// Another port: the old address stops, with its open connections.
	free, _ := net.Listen("tcp", "127.0.0.1:0")
	newPort := free.Addr().(*net.TCPAddr).Port
	free.Close()
	retire, err := ps.move("127.0.0.1", newPort)
	if err != nil {
		t.Fatal(err)
	}
	now := net.JoinHostPort("127.0.0.1", fmt.Sprint(newPort))
	if !answers(now) {
		t.Fatal("no answer on the new port")
	}
	if !answers(old) {
		t.Error("the old port should keep working until retire")
	}
	retire()
	if answers(old) {
		t.Error("the old port still answers after retire")
	}
	if !closedSoon(conn) {
		t.Error("a connection to the old port stayed open")
	}
	if _, p, _ := ps.addr(); p != newPort {
		t.Errorf("addr() port = %d, want %d", p, newPort)
	}

	// An address this computer doesn't have: the old listener comes back.
	if _, err := ps.move("192.0.2.1", newPort); err == nil {
		t.Fatal("listening on 192.0.2.1 worked")
	}
	if !answers(now) {
		t.Fatal("the page is gone after a failed change of address")
	}

	// Same port, other address (127.0.0.2 stands in for every network).
	if runtime.GOOS == "darwin" {
		return // macOS only has 127.0.0.1
	}
	conn = keepAlive(t, now)
	retire, err = ps.move("127.0.0.2", newPort)
	if err != nil {
		t.Fatal(err)
	}
	retire()
	if !answers(net.JoinHostPort("127.0.0.2", fmt.Sprint(newPort))) {
		t.Error("no answer on 127.0.0.2")
	}
	if !closedSoon(conn) {
		t.Error("a connection to 127.0.0.1 stayed open after moving to 127.0.0.2")
	}
	if h, _, _ := ps.addr(); h != "127.0.0.2" {
		t.Errorf("addr() host = %q", h)
	}
	if !strings.HasPrefix(ps.localURL(), "http://127.0.0.2:") {
		t.Errorf("localURL() = %s", ps.localURL())
	}
}
