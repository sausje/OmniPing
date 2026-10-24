package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omniping.log")
	os.WriteFile(path, []byte(strings.Repeat("o", 60)), 0o644)
	l, err := openLog(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	l.Write([]byte(strings.Repeat("a", 30)))
	l.Write([]byte(strings.Repeat("b", 30))) // 120 bytes: the file rotates first
	l.Write([]byte(strings.Repeat("c", 30)))
	l.Close()

	old, _ := os.ReadFile(path + ".1")
	cur, _ := os.ReadFile(path)
	if string(old) != strings.Repeat("o", 60)+strings.Repeat("a", 30) {
		t.Errorf(".1 has %q", old)
	}
	if string(cur) != strings.Repeat("b", 30)+strings.Repeat("c", 30) {
		t.Errorf("the log has %q", cur)
	}

	// Another round replaces the old .1.
	l, _ = openLog(path, 100)
	l.Write([]byte(strings.Repeat("d", 50)))
	l.Close()
	old, _ = os.ReadFile(path + ".1")
	cur, _ = os.ReadFile(path)
	if string(old) != strings.Repeat("b", 30)+strings.Repeat("c", 30) || string(cur) != strings.Repeat("d", 50) {
		t.Errorf("second rotation: .1 %q, log %q", old, cur)
	}
}

func TestCrashLogRemovedWhenEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.log")
	f, err := openCrashLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := closeCrashLog(f); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("empty crash.log is still there: %v", err)
	}

	os.WriteFile(path, []byte("panic: earlier\n"), 0o644)
	f, _ = openCrashLog(path)
	closeCrashLog(f)
	if b, _ := os.ReadFile(path); string(b) != "panic: earlier\n" {
		t.Errorf("crash.log with a crash in it: %q", b)
	}
}

// When the log can't be moved aside, the older .1 stays.
func TestLogRotationKeepsOldLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omniping.log")
	if err := os.WriteFile(path+".1", []byte("older log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	other, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	l, err := openLog(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.Write([]byte(strings.Repeat("x", 90)))
	l.Write([]byte(strings.Repeat("y", 30)))
	if b, err := os.ReadFile(path + ".1"); err != nil || string(b) != "older log\n" {
		t.Errorf(".1 = %q, %v", b, err)
	}
}

// When .1 is held open (a log viewer, say), a rotation through the log
// package must not hang, and the next start tidies up.
func TestLogRotationWithOldLogOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omniping.log")
	if err := os.WriteFile(path+".1", []byte("older log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	viewer, err := os.Open(path + ".1") // no FILE_SHARE_DELETE
	if err != nil {
		t.Fatal(err)
	}
	l, err := openLog(path, 200)
	if err != nil {
		t.Fatal(err)
	}
	out := log.Writer()
	log.SetOutput(l)

	done := make(chan struct{})
	go func() {
		for i := range 10 {
			log.Printf("line %d %s", i, strings.Repeat("z", 40))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		// The log package stays stuck, so the test can't put it back.
		t.Fatal("log.Printf hangs while the log rotates")
	}
	log.SetOutput(out)
	l.Close()
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "could not keep the old log") {
		t.Errorf("the log does not say the rotation failed:\n%s", b)
	}

	viewer.Close()
	l, err = openLog(path, 200)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	if _, err := os.Stat(path + ".1.tmp"); !os.IsNotExist(err) {
		t.Errorf(".1.tmp is still there: %v", err)
	}
	if b, _ := os.ReadFile(path + ".1"); !strings.Contains(string(b), "line ") {
		t.Errorf(".1 = %q, want the last rotated log", b)
	}
}
