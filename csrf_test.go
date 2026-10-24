package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCrossSiteChangesAreRefused(t *testing.T) {
	app := &App{disc: &Discovery{}, hub: newHub()}
	h := app.handler(http.NotFoundHandler())
	try := func(site, origin string) int {
		r := httptest.NewRequest("POST", "http://192.168.1.20:8080/api/discover/stop", nil)
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if code := try("cross-site", "https://evil.example"); code != http.StatusForbidden {
		t.Errorf("cross-site POST: status %d, want 403", code)
	}
	if code := try("", "https://evil.example"); code != http.StatusForbidden {
		t.Errorf("POST from another origin without Sec-Fetch-Site: status %d, want 403", code)
	}
	if code := try("same-origin", "http://192.168.1.20:8080"); code != http.StatusOK {
		t.Errorf("same-origin POST: status %d, want 200", code)
	}
	if code := try("", ""); code != http.StatusOK {
		t.Errorf("POST from a script without browser headers: status %d, want 200", code)
	}
}
