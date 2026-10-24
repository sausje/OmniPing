package main

import (
	"fmt"
	"net"
	"testing"
)

// Windows lets a program listen on every network next to another one on
// 127.0.0.1 with the same port; OmniPing must not, or the browser on this
// computer would reach the other program. (The test only listens on every
// network if that protection is broken, so it brings up no firewall
// question when it passes.)
func TestPortIsExclusive(t *testing.T) {
	other, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	p := other.Addr().(*net.TCPAddr).Port
	ln, _, err := listenServer("", p, false)
	if err == nil {
		ln.Close()
		t.Fatalf("OmniPing could listen on every network next to another program on 127.0.0.1:%d", p)
	}
	if !isPortTaken(err) {
		t.Errorf("err = %v, not seen as a taken port", err)
	}

	ours, p, err := listenServer("127.0.0.1", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	defer ours.Close()
	if second, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p)); err == nil {
		second.Close()
		t.Error("a second listener on the same address worked")
	} else if !isPortTaken(err) {
		t.Errorf("err = %v, not seen as a taken port", err)
	}
}
