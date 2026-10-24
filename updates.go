package main

// "New version available": once a day OmniPing asks GitHub for the latest
// release. A newer one shows on the page (top bar and Settings → About), and
// the Windows app shows a notification once per version. Only the version
// number is fetched; nothing about the user or the network is sent.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// installKind tells how this OmniPing was installed, so the update notice
// can say how to update: "docker" (also Podman), "package" (the Linux .deb
// or .rpm, which puts the program in /usr/bin), or "" for a program started
// by hand. The Windows app is Info.Desktop instead.
var installKind = sync.OnceValue(func() string {
	for _, f := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(f); err == nil {
			return "docker"
		}
	}
	if exe, err := os.Executable(); err == nil && exe == "/usr/bin/omniping" {
		return "package"
	}
	return ""
})

// UpdateInfo is a release newer than this OmniPing.
type UpdateInfo struct {
	Version string `json:"version"` // like "0.5.0"
	URL     string `json:"url"`     // its release page
}

// updateURL is GitHub's latest release of OmniPing; a variable for tests.
var updateURL = "https://api.github.com/repos/sausje/OmniPing/releases/latest"

const (
	updateFirst = time.Minute    // the first check, once the server is up
	updateEvery = 24 * time.Hour // the checks after that
	// updateFile notes the last version the Windows app told about, so a
	// restart doesn't show the same notification again.
	updateFile = "update.json"
)

// updateLoop checks for a new version while the setting is on.
func (a *App) updateLoop(ctx context.Context) {
	wait := updateFirst
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-a.updateNow:
		}
		wait = updateEvery
		a.checkUpdate(ctx)
	}
}

// checkUpdate asks GitHub once and notes what it finds.
func (a *App) checkUpdate(ctx context.Context) {
	if !a.cfg.settings().CheckUpdates {
		a.update.Store(nil)
		return
	}
	u, err := latestRelease(ctx)
	if err != nil {
		// A private repo, no internet, GitHub's rate limit: try again tomorrow.
		log.Printf("checking for a new version: %v", err)
		return
	}
	if u == nil || !newerVersion(u.Version, version) {
		a.update.Store(nil)
		return
	}
	if old := a.update.Swap(u); old == nil || old.Version != u.Version {
		log.Printf("OmniPing %s is available: %s", u.Version, u.URL)
		a.hub.Broadcast("info", a.info())
		a.tellUpdate(u)
	}
}

// latestRelease asks GitHub for the latest release.
func latestRelease(ctx context.Context) (*UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", updateURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "OmniPing/"+version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var rel struct {
		Tag        string `json:"tag_name"`
		URL        string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	if rel.Draft || rel.Prerelease || rel.Tag == "" {
		return nil, nil
	}
	return &UpdateInfo{Version: strings.TrimPrefix(rel.Tag, "v"), URL: rel.URL}, nil
}

// tellUpdate shows the Windows notification, once per version.
func (a *App) tellUpdate(u *UpdateInfo) {
	if !a.desktop() {
		return
	}
	file := filepath.Join(a.dataDir, updateFile)
	var seen struct {
		Told string `json:"told"`
	}
	if b, err := os.ReadFile(file); err == nil {
		json.Unmarshal(b, &seen)
	}
	if seen.Told == u.Version {
		return
	}
	a.shell.Notify(fmt.Sprintf("OmniPing %s is available", u.Version),
		fmt.Sprintf("You have %s. Click to see what's new and download it.", version), "#/settings/about", false)
	b, _ := json.Marshal(map[string]string{"told": u.Version})
	if err := os.WriteFile(file, b, 0o644); err != nil {
		log.Printf("writing %s: %v", updateFile, err)
	}
}

// newerVersion tells whether version a is newer than b, both like "0.5.0"
// or "0.5.0-rc.1". A version with a suffix comes before the same version
// without one. A development build ("dev") never asks.
func newerVersion(a, b string) bool {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return false
	}
	for i := range 3 {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return pa[3] > pb[3]
}

// parseVersion reads "1.2.3" or "1.2.3-suffix"; the fourth number is 1
// without a suffix and 0 with one.
func parseVersion(s string) ([4]int, bool) {
	var v [4]int
	s = strings.TrimPrefix(s, "v")
	core, suffix, _ := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	if suffix == "" {
		v[3] = 1
	}
	return v, true
}
