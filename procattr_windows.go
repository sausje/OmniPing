package main

import (
	"context"
	"os/exec"
	"syscall"
)

// shellCommand runs cmdline with cmd.exe without flashing a console window,
// which the Windows app (a program without a console) would otherwise get.
// The command line is handed over as typed: Go's own quoting of arguments
// would escape quotes the way cmd.exe doesn't understand, and a command like
// "C:\My Scripts\alert.bat" hello would fail.
func shellCommand(ctx context.Context, cmdline string) *exec.Cmd {
	const createNoWindow = 0x08000000
	cmd := exec.CommandContext(ctx, "cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
		CmdLine:       `cmd.exe /S /C "` + cmdline + `"`,
	}
	return cmd
}
