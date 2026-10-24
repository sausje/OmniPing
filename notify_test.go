package main

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNotificationIsRetriedUntilItGetsThrough(t *testing.T) {
	old := retryAfter
	retryAfter = []time.Duration{10 * time.Millisecond}
	defer func() { retryAfter = old }()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			http.Error(w, "the internet is down", http.StatusBadGateway)
		}
	}))
	defer srv.Close()

	n := newNotifier(&App{hub: newHub()})
	ch := Channel{Name: "hook", Type: "webhook", Config: map[string]string{"url": srv.URL}}
	var retries atomic.Int32
	done := make(chan error, 1)
	n.Send(ch, Notice{Kind: "end", Title: "test"}, func(err error) { done <- err }, func(error) { retries.Add(1) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("delivery failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no final result")
	}
	if calls.Load() != 3 || retries.Load() != 2 {
		t.Fatalf("calls = %d, retries = %d; want 3 and 2", calls.Load(), retries.Load())
	}
}

func TestWindowsNotifications(t *testing.T) {
	ch := Channel{Name: "Windows", Type: "windows", Config: map[string]string{}}
	for _, sh := range []Shell{nil, noShell{}} {
		n := newNotifier(&App{hub: newHub(), shell: sh})
		err := n.SendNow(ch, Notice{Kind: "start", Title: "down"})
		if err == nil || err.Error() != "Windows notifications only work in the Windows app" || !isPermanent(err) {
			t.Errorf("shell %T: err = %v", sh, err)
		}
	}

	sh := &fakeShell{}
	n := newNotifier(&App{hub: newHub(), shell: sh})
	n.SendNow(ch, Notice{Kind: "start", Title: "Router is down", Body: "No replies", Target: Target{ID: "a1"}})
	n.SendNow(ch, Notice{Kind: "end", Title: "Internet is back", Target: Target{Name: "A, B"}})
	n.SendNow(ch, Notice{Kind: "test", Title: "OmniPing test notification"})
	want := []fakeNote{
		{"Router is down", "No replies", "#/target/a1", true},
		{"Internet is back", "", "#/alerts", false},
		{"OmniPing test notification", "", "#/alerts", true},
	}
	if !slices.Equal(sh.notes, want) {
		t.Errorf("notes:\n%+v\nwant\n%+v", sh.notes, want)
	}
}

func TestWindowsChannelNeedsTheWindowsApp(t *testing.T) {
	app, _ := outageApp(t)
	app.engine = newEngine(app)
	save := func(body string) int {
		w := httptest.NewRecorder()
		app.saveChannel(w, httptest.NewRequest("POST", "/api/channels", strings.NewReader(body)))
		return w.Code
	}
	if code := save(`{"type": "windows", "enabled": true}`); code != 400 {
		t.Errorf("an enabled Windows channel without the Windows app: %d, want 400", code)
	}
	if code := save(`{"type": "windows", "enabled": false}`); code != 200 {
		t.Errorf("a turned-off Windows channel: %d, want 200", code)
	}
	app.shell = &fakeShell{}
	if code := save(`{"type": "windows", "enabled": true}`); code != 200 {
		t.Errorf("a Windows channel in the Windows app: %d, want 200", code)
	}
}
