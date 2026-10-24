package main

import (
	"encoding/json"
	"fmt"
	"image/png"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The page's own files, as the built program serves them.
func webServer(t *testing.T) http.Handler {
	t.Helper()
	sub, err := fs.Sub(webFiles, "web")
	if err != nil {
		t.Fatal(err)
	}
	return noCache(http.FileServer(http.FS(sub)))
}

func TestPageFilesExist(t *testing.T) {
	h := webServer(t)
	page, err := fs.ReadFile(webFiles, "web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	refs := regexp.MustCompile(`(?:href|src)="([^"#:]+)"`).FindAllStringSubmatch(string(page), -1)
	if len(refs) == 0 {
		t.Fatal("found no files referred to in index.html")
	}
	for _, m := range refs {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/"+m[1], nil))
		if rec.Code != http.StatusOK {
			t.Errorf("index.html refers to %s, which gives %d", m[1], rec.Code)
		}
	}
}

// Phones take the home-screen icon and name from the manifest.
func TestManifest(t *testing.T) {
	h := webServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/manifest.webmanifest", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest: %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/manifest+json" {
		t.Errorf("manifest Content-Type = %q", ct)
	}
	var m struct {
		ShortName string `json:"short_name"`
		StartURL  string `json:"start_url"`
		Display   string
		Icons     []struct{ Src, Sizes, Type, Purpose string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if m.ShortName == "" || m.StartURL == "" || m.Display != "standalone" {
		t.Errorf("manifest misses short_name, start_url or display standalone: %+v", m)
	}
	purposes := map[string]bool{}
	for _, ic := range m.Icons {
		purposes[ic.Purpose] = true
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/"+ic.Src, nil))
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "image/png") {
			t.Errorf("icon %s: %d %s", ic.Src, rec.Code, rec.Header().Get("Content-Type"))
			continue
		}
		cfg, err := png.DecodeConfig(rec.Body)
		if err != nil {
			t.Errorf("icon %s: %v", ic.Src, err)
			continue
		}
		if got := fmt.Sprintf("%dx%d", cfg.Width, cfg.Height); got != ic.Sizes {
			t.Errorf("icon %s is %s, the manifest says %s", ic.Src, got, ic.Sizes)
		}
	}
	if !purposes["any"] || !purposes["maskable"] {
		t.Errorf("manifest needs both an \"any\" and a \"maskable\" icon, has %v", purposes)
	}
}
