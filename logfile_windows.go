package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"
)

// The Windows app has no console, so its log goes to omniping.log in the
// app folder. At 5 MB the file is renamed to omniping.log.1 (replacing the
// one before) and a new one is started.

const logMaxBytes = 5 << 20

type logFile struct {
	mu      sync.Mutex
	path    string
	max     int64
	f       *os.File
	written int64 // size of the file, or bytes since a rotation failed
}

func openLog(path string, max int64) (*logFile, error) {
	l := &logFile{path: path, max: max}
	// A rotation that was cut short leaves the old log as .1.tmp. If it
	// still can't become .1, the next rotation replaces it.
	if _, err := os.Stat(path + ".1.tmp"); err == nil {
		os.Remove(path + ".1")
		os.Rename(path+".1.tmp", path+".1")
	}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *logFile) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	l.f = f
	l.written = 0
	if fi, err := f.Stat(); err == nil {
		l.written = fi.Size()
	}
	return nil
}

func (l *logFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil && l.written > 0 && l.written+int64(len(p)) > l.max {
		l.rotate()
	}
	if l.f == nil {
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	n, err := l.f.Write(p)
	l.written += int64(n)
	return n, err
}

// rotate keeps the current file as .1. When that fails (another copy of
// OmniPing has the file open, say) it carries on in the same file and tries
// again after another max bytes.
//
// It must not use the log package: Write runs inside log.Printf, so logging
// from here would wait for itself forever.
func (l *logFile) rotate() {
	l.f.Close()
	l.f = nil
	// Move the current file aside first, so the older .1 is only replaced
	// when the rotation can actually happen.
	var keepErr error
	renamed := os.Rename(l.path, l.path+".1.tmp") == nil
	if renamed {
		os.Remove(l.path + ".1")
		keepErr = os.Rename(l.path+".1.tmp", l.path+".1")
	}
	if err := l.open(); err != nil {
		return
	}
	if !renamed {
		l.written = 0
	}
	if keepErr != nil {
		// Something holds .1 open. The old log stays as .1.tmp until the
		// next rotation or start.
		n, _ := fmt.Fprintf(l.f, "%s could not keep the old log: %v\n", time.Now().Format("2006/01/02 15:04:05"), keepErr)
		l.written += int64(n)
	}
}

func (l *logFile) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// openCrashLog makes the Go runtime write panics and fatal errors to path,
// since without a console they would be lost.
func openCrashLog(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// closeCrashLog stops writing crashes to f and removes the file when no
// crash was ever written to it.
func closeCrashLog(f *os.File) error {
	if f == nil {
		return nil
	}
	err := debug.SetCrashOutput(nil, debug.CrashOptions{})
	fi, serr := f.Stat()
	err = errors.Join(err, f.Close())
	if serr == nil && fi.Size() == 0 {
		os.Remove(f.Name())
	}
	return err
}
