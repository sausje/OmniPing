package main

import (
	"context"
	"errors"
	"net"
	"syscall"
)

// soExclusiveAddrUse (SO_EXCLUSIVEADDRUSE) stops other programs from
// opening the same port next to OmniPing. Without it Windows lets a program
// listen on 127.0.0.1:8080 while OmniPing listens on every network on
// port 8080, and the browser on this computer would end up there.
const soExclusiveAddrUse = ^syscall.SO_REUSEADDR

// Windows socket errors for a port that can't be had: in use, or reserved
// (Hyper-V and WSL keep ranges of ports) or held exclusively by another
// program.
const (
	wsaeAddrInUse = syscall.Errno(10048)
	wsaeAccess    = syscall.Errno(10013)
)

func listenTCP(addr string) (net.Listener, error) {
	lc := net.ListenConfig{Control: func(network, address string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, soExclusiveAddrUse, 1)
		})
		if err != nil {
			return err
		}
		return serr
	}}
	return lc.Listen(context.Background(), "tcp", addr)
}

func isPortTaken(err error) bool {
	var e syscall.Errno
	return errors.As(err, &e) && (e == wsaeAddrInUse || e == wsaeAccess)
}
