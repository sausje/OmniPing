package main

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestInstanceMutexName(t *testing.T) {
	// The name must not change between versions: an old and a new OmniPing
	// on the same data folder must find each other.
	const want = `Local\OmniPing-a9eb5cc0cef2`
	if got := instanceMutexName(`C:\Users\Test\AppData\Local\OmniPing`); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	same := []string{`c:\users\test\appdata\local\omniping`, `C:\Users\Test\AppData\Local\OmniPing\`, `C:\Users\Test\AppData\Local\.\OmniPing`}
	for _, p := range same {
		if got := instanceMutexName(p); got != want {
			t.Errorf("%s gives %s, want %s", p, got, want)
		}
	}
	if instanceMutexName(`D:\OmniPing`) == want {
		t.Error("another folder gives the same name")
	}
}

func TestLockDataDir(t *testing.T) {
	dir := fmt.Sprintf(`C:\OmniPing-test-%d`, os.Getpid()) // only its name is used
	release, ok, err := lockDataDir(dir, 0)
	if err != nil || !ok {
		t.Fatalf("first lock: %v, %v", ok, err)
	}
	if _, ok, _ := lockDataDir(dir, 0); ok {
		t.Error("a second lock on the same folder worked")
	}
	if other, ok, _ := lockDataDir(dir+"-other", 0); !ok {
		t.Error("another folder is locked too")
	} else {
		other()
	}

	// A restarted OmniPing waits for the old one to let go.
	go func() {
		time.Sleep(300 * time.Millisecond)
		release()
	}()
	start := time.Now()
	release2, ok, err := lockDataDir(dir, 5*time.Second)
	if err != nil || !ok {
		t.Fatalf("waiting lock: %v, %v", ok, err)
	}
	release2()
	if time.Since(start) > 3*time.Second {
		t.Errorf("waited %v", time.Since(start))
	}
}

func TestCreateMutex(t *testing.T) {
	name := fmt.Sprintf(`Local\OmniPing-test-%d`, os.Getpid())
	h1, existed, err := createMutex(name)
	if err != nil || existed {
		t.Fatalf("first: existed %v, %v", existed, err)
	}
	h2, existed, err := createMutex(name)
	if err != nil || !existed {
		t.Errorf("second: existed %v, %v", existed, err)
	}
	syscall.CloseHandle(h2)
	syscall.CloseHandle(h1)
	h3, existed, _ := createMutex(name)
	syscall.CloseHandle(h3)
	if existed {
		t.Error("still there after both handles were closed")
	}
}
