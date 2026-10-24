//go:build !windows

package main

import (
	"errors"
	"net"
	"syscall"
)

func listenTCP(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }

func isPortTaken(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }
