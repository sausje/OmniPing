package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.5.0", "0.4.2", true},
		{"v0.5.0", "0.4.2", true},
		{"0.4.10", "0.4.9", true},
		{"1.0.0", "0.99.99", true},
		{"0.4.2", "0.4.2", false},
		{"0.4.1", "0.4.2", false},
		{"0.5.0", "0.5.0-rc.1", true}, // the release after its candidate
		{"0.5.0-rc.2", "0.5.0", false},
		{"0.5.0", "dev", false}, // a development build never asks
		{"garbage", "0.4.2", false},
		{"0.5", "0.4.2", false},
	} {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// fakeGitHub answers like GitHub's latest-release endpoint.
func fakeGitHub(t *testing.T, tag string, prerelease bool, status int) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("no User-Agent: GitHub refuses those")
		}
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"tag_name": %q, "html_url": "https://github.com/sausje/OmniPing/releases/tag/%s", "prerelease": %v}`, tag, tag, prerelease)
	}))
	t.Cleanup(srv.Close)
	old := updateURL
	updateURL = srv.URL
	t.Cleanup(func() { updateURL = old })
}

func TestCheckUpdate(t *testing.T) {
	oldVersion := version
	version = "0.4.2"
	t.Cleanup(func() { version = oldVersion })
	app, sh, _ := desktopApp(t)
	ctx := context.Background()

	fakeGitHub(t, "v0.5.0", false, 200)
	app.checkUpdate(ctx)
	if u := app.info().Update; u == nil || u.Version != "0.5.0" || u.URL == "" {
		t.Fatalf("info().Update = %+v, want 0.5.0", u)
	}
	// The Windows notification comes once, also after a restart.
	app.checkUpdate(ctx)
	app.update.Store(nil)
	app.checkUpdate(ctx)
	if n := len(sh.notes); n != 1 || sh.notes[0].title != "OmniPing 0.5.0 is available" || sh.notes[0].path != "#/settings/about" {
		t.Errorf("notifications: %+v, want one", sh.notes)
	}

	// The check turned off: nothing to show.
	app.cfg.update(func(c *Config) error { c.Settings.CheckUpdates = false; return nil })
	app.checkUpdate(ctx)
	if app.info().Update != nil {
		t.Error("an update shows while the check is off")
	}
	app.cfg.update(func(c *Config) error { c.Settings.CheckUpdates = true; return nil })

	// Not newer, a prerelease, or GitHub hiding a private repo: nothing.
	for _, c := range []struct {
		tag    string
		pre    bool
		status int
	}{{"v0.4.2", false, 200}, {"v0.6.0-rc.1", true, 200}, {"v0.6.0", false, 404}} {
		app.update.Store(&UpdateInfo{Version: "0.5.0"})
		fakeGitHub(t, c.tag, c.pre, c.status)
		app.checkUpdate(ctx)
		if u := app.update.Load(); c.status == 200 && u != nil {
			t.Errorf("%s (prerelease %v): update %+v, want none", c.tag, c.pre, u)
		}
	}
}
