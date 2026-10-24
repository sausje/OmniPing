package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// startServer runs runServer in the background until the test ends and
// waits until it is up.
func startServer(t *testing.T, o serverOptions, sh Shell) (url string, stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	urls := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- runServer(ctx, o, sh, func(u string) { urls <- u }) }()
	var once sync.Once
	var stopErr error
	stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case stopErr = <-done:
			case <-time.After(10 * time.Second):
				stopErr = errors.New("runServer did not return")
			}
		})
		return stopErr
	}
	select {
	case url = <-urls:
	case err := <-done:
		t.Fatalf("runServer: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("runServer did not start")
	}
	t.Cleanup(func() { stop() })
	return url, stop
}

func TestWindowsAppStartAndQuit(t *testing.T) {
	busy := httptest.NewServer(http.NotFoundHandler()) // another program on the port
	defer busy.Close()
	bp := serverPort(t, busy)
	dir := t.TempDir()
	appFile := filepath.Join(t.TempDir(), "app.json")
	sh := &fakeShell{}
	url, stop := startServer(t, serverOptions{DataDir: dir, Host: "127.0.0.1", Port: bp, PortFallback: true, AppFile: appFile}, sh)

	s, _ := loadAppSettings(appFile)
	if s.Port <= bp || url != fmt.Sprintf("http://127.0.0.1:%d/", s.Port) {
		t.Fatalf("port %d is taken; page at %s, saved port %d", bp, url, s.Port)
	}
	if len(sh.notes) != 1 || sh.notes[0].title != fmt.Sprintf("OmniPing moved to port %d", s.Port) || sh.notes[0].path != "#/settings/computer" {
		t.Errorf("notes: %+v", sh.notes)
	}
	if in, err := readInstance(dir); err != nil || in.URL != url || in.PID != os.Getpid() {
		t.Errorf("instance.json: %+v %v", in, err)
	}
	if c := sh.lastAttached(); c.URL != url || !filepath.IsAbs(c.DataDir) {
		t.Errorf("the tray got %+v", c)
	}
	resp, err := http.Get(url + "api/app")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("GET /api/app: %d", resp.StatusCode)
	}

	// An open page (its live updates) must not hold up quitting.
	stream, err := http.Get(url + "api/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	start := time.Now()
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("quitting took %v", d)
	}
	if _, err := os.Stat(filepath.Join(dir, instanceFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("instance.json is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Errorf("settings were not saved: %v", err)
	}
}

// After a restart the new copy comes back on the address the page that
// waits for it polls, without saving that port or telling again.
func TestRestartKeepsTheAddress(t *testing.T) {
	busy := httptest.NewServer(http.NotFoundHandler())
	defer busy.Close()
	free := httptest.NewServer(http.NotFoundHandler())
	fp := serverPort(t, free)
	free.Close()
	appFile := filepath.Join(t.TempDir(), "app.json")
	sh := &fakeShell{}
	url, _ := startServer(t, serverOptions{DataDir: t.TempDir(), Host: "127.0.0.1", Port: serverPort(t, busy), PortFallback: true, TryPort: fp, AppFile: appFile}, sh)
	if want := fmt.Sprintf("http://127.0.0.1:%d/", fp); url != want {
		t.Errorf("page at %s, want %s", url, want)
	}
	if s, _ := loadAppSettings(appFile); s.Port == fp {
		t.Errorf("the port from before the restart was saved")
	}
	if len(sh.notes) != 0 {
		t.Errorf("notes: %+v", sh.notes)
	}
}

func TestSecondWindowsAppFindsTheFirst(t *testing.T) {
	url, _ := startServer(t, serverOptions{DataDir: t.TempDir(), Host: "127.0.0.1", Port: 0}, &fakeShell{})
	port := 0
	fmt.Sscanf(strings.TrimPrefix(url, "http://127.0.0.1:"), "%d", &port)
	err := runServer(context.Background(), serverOptions{DataDir: t.TempDir(), Host: "127.0.0.1", Port: port, PortFallback: true}, &fakeShell{}, nil)
	if err != errAlreadyRunning {
		t.Fatalf("err = %v, want errAlreadyRunning", err)
	}
}

// As a plain server (Docker, Linux, macOS) nothing changes: no
// instance.json, no other port.
func TestPlainServer(t *testing.T) {
	dir := t.TempDir()
	url, stop := startServer(t, serverOptions{DataDir: dir, Host: "127.0.0.1", Port: 0}, noShell{})
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Errorf("url %s", url)
	}
	if _, err := os.Stat(filepath.Join(dir, instanceFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a plain server wrote instance.json: %v", err)
	}
	resp, err := http.Get(url + "api/app")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("GET /api/app on a plain server: %d, want 404", resp.StatusCode)
	}
	stop()

	busy := httptest.NewServer(http.NotFoundHandler())
	defer busy.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", serverPort(t, busy))
	err = runServer(context.Background(), serverOptions{DataDir: dir, Host: "127.0.0.1", Port: serverPort(t, busy)}, noShell{}, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "cannot listen on "+addr+": listen tcp "+addr+": bind:") {
		t.Errorf("a taken port: err = %v", err)
	}
}
