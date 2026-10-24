package main

import (
	"log"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// OmniPing.exe is a Windows program without a console of its own, so that no
// black window shows. With --console it borrows the console of the terminal
// that started it (or opens one) and writes its log there.

func init() {
	// Before the options are read, so that -h and mistakes show too.
	if consoleWanted(os.Args[1:]) {
		attachConsole()
	}
}

// consoleWanted tells whether args ask for --console.
func consoleWanted(args []string) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if name == "console" {
			on, err := strconv.ParseBool(value)
			return !hasValue || (err == nil && on)
		}
	}
	return false
}

const attachParentProcess = ^uint32(0)

func attachConsole() {
	if r, _, _ := procAttachConsole.Call(uintptr(attachParentProcess)); r == 0 {
		procAllocConsole.Call()
	}
	// Output sent to a file or a pipe stays there; only handles that lead
	// nowhere are pointed at the console.
	if f := consoleOut(os.Stdout); f != nil {
		os.Stdout = f
	}
	if f := consoleOut(os.Stderr); f != nil {
		os.Stderr = f
	}
	log.SetOutput(os.Stderr)
}

// consoleOut returns the console for f when f leads nowhere, else nil.
func consoleOut(f *os.File) *os.File {
	if f != nil {
		h := syscall.Handle(f.Fd())
		if h != 0 && h != syscall.InvalidHandle {
			if t, err := syscall.GetFileType(h); err == nil && t != syscall.FILE_TYPE_UNKNOWN {
				return nil
			}
		}
	}
	h, err := syscall.CreateFile(wstr("CONOUT$"), syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil
	}
	return os.NewFile(uintptr(h), "CONOUT$")
}
