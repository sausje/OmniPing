//go:build !windows

package main

import (
	"context"
	"os/exec"
)

// shellCommand runs cmdline with sh.
func shellCommand(ctx context.Context, cmdline string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", cmdline)
}
