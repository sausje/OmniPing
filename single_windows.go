package main

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// One OmniPing per data folder: two would fight over the same files. Each
// running copy holds a named mutex for its data folder, so a second start
// can tell, and opens the page of the first one instead.

// runningMutex is held by every running OmniPing. The installer looks for it
// (AppMutex) to ask the user to close OmniPing first.
const runningMutex = `Local\OmniPingRunning`

// instanceMutexName is the mutex for one data folder. Windows paths ignore
// case, so the name does too.
func instanceMutexName(dataDir string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(dataDir))))
	return `Local\OmniPing-` + hex.EncodeToString(sum[:])[:12]
}

// createMutex opens the named mutex, creating it if needed. existed tells
// whether another process already had it.
func createMutex(name string) (h syscall.Handle, existed bool, err error) {
	r, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(wstr(name))))
	if r == 0 {
		return 0, false, callErr
	}
	return syscall.Handle(r), callErr == syscall.Errno(errorAlreadyExists), nil
}

// lockDataDir claims dataDir for this process. ok is false when another
// OmniPing uses it; wait gives that one time to exit first (after a
// restart). release lets go again.
func lockDataDir(dataDir string, wait time.Duration) (release func(), ok bool, err error) {
	name := instanceMutexName(dataDir)
	deadline := time.Now().Add(wait)
	for {
		h, existed, err := createMutex(name)
		if err != nil {
			return nil, false, err
		}
		if !existed {
			return func() { syscall.CloseHandle(h) }, true, nil
		}
		syscall.CloseHandle(h)
		if time.Now().After(deadline) {
			return nil, false, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
}
