//go:build !linux && !darwin && !windows

package icmp

import "errors"

func New() (Pinger, error) {
	return nil, errors.New("pinging is not supported on this operating system")
}
