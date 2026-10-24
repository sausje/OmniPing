package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// authApp is an app with a password store, and a way to send it requests.
func authApp(t *testing.T) (*App, func(method, path, body string, cookie *http.Cookie, mod ...func(*http.Request)) *httptest.ResponseRecorder) {
	app, _ := outageApp(t)
	var err error
	if app.auth, err = loadAuth(app.dataDir); err != nil {
		t.Fatal(err)
	}
	old := failDelay
	failDelay = 0
	t.Cleanup(func() { failDelay = old })
	h := app.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("page")) }))
	do := func(method, path, body string, cookie *http.Cookie, mod ...func(*http.Request)) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "192.168.1.20:50000"
		if strings.HasPrefix(body, "{") {
			r.Header.Set("Content-Type", "application/json")
		} else if body != "" {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		for _, m := range mod {
			m(r)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	return app, do
}

func sessionFrom(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestPassword(t *testing.T) {
	app, do := authApp(t)

	// No password: everything is open, as before.
	if w := do("GET", "/api/info", "", nil); w.Code != 200 {
		t.Fatalf("without a password: %d", w.Code)
	}
	if w := do("PUT", "/api/auth", `{"password": "short"}`, nil); w.Code != 400 {
		t.Errorf("a short password: %d, want 400", w.Code)
	}
	w := do("PUT", "/api/auth", `{"password": "correct horse"}`, nil)
	mine := sessionFrom(w)
	if w.Code != 200 || mine == nil || !mine.HttpOnly {
		t.Fatalf("setting a password: %d, cookie %+v", w.Code, mine)
	}
	b, _ := os.ReadFile(filepath.Join(app.dataDir, authFile))
	if strings.Contains(string(b), "correct horse") || strings.Contains(string(b), mine.Value) {
		t.Error("auth.json holds the password or a session token")
	}

	// Signed out: the API, the page and /metrics are closed; /healthz,
	// the sign-in page and the icons are open.
	for path, want := range map[string]int{"/api/info": 401, "/api/state": 401, "/": 303, "/js/main.js": 401, "/metrics": 401,
		"/healthz": 200, "/login": 200, "/favicon.svg": 200, "/icon-192.png": 200, "/icon-../js/main.js": 401} {
		if w := do("GET", path, "", nil); w.Code != want {
			t.Errorf("signed out, GET %s: %d, want %d", path, w.Code, want)
		}
	}
	if w := do("GET", "/", "", nil); w.Header().Get("Location") != "login" {
		t.Errorf("the page sends to %q, want login", w.Header().Get("Location"))
	}
	if w := do("GET", "/metrics", "", nil); !strings.Contains(w.Header().Get("WWW-Authenticate"), "Basic") {
		t.Error("/metrics doesn't ask for basic auth")
	}

	// Signing in.
	if w := do("POST", "/login", url.Values{"password": {"wrong one"}}.Encode(), nil); w.Code != 401 || sessionFrom(w) != nil || !strings.Contains(w.Body.String(), "not the password") {
		t.Errorf("a wrong password: %d", w.Code)
	}
	w = do("POST", "/login", url.Values{"password": {"correct horse"}}.Encode(), nil)
	phone := sessionFrom(w)
	if w.Code != 303 || phone == nil {
		t.Fatalf("signing in: %d", w.Code)
	}
	for _, c := range []*http.Cookie{mine, phone} {
		if w := do("GET", "/api/info", "", c); w.Code != 200 {
			t.Errorf("signed in: %d", w.Code)
		}
	}
	basic := func(pw string) func(*http.Request) { return func(r *http.Request) { r.SetBasicAuth("prometheus", pw) } }
	if w := do("GET", "/metrics", "", nil, basic("correct horse")); w.Code != 200 {
		t.Errorf("/metrics with the password: %d", w.Code)
	}
	if w := do("GET", "/metrics", "", nil, basic("nope")); w.Code != 401 {
		t.Errorf("/metrics with a wrong password: %d", w.Code)
	}

	// Signing out ends only that browser's session.
	do("POST", "/api/logout", "{}", phone)
	if w := do("GET", "/api/info", "", phone); w.Code != 401 {
		t.Errorf("after signing out: %d", w.Code)
	}
	if w := do("GET", "/api/info", "", mine); w.Code != 200 {
		t.Errorf("the other browser after that: %d", w.Code)
	}

	// A new password signs everyone else out.
	w = do("PUT", "/api/auth", `{"password": "battery staple"}`, mine)
	mine2 := sessionFrom(w)
	if w := do("GET", "/api/info", "", mine); w.Code != 401 {
		t.Errorf("an old session after a new password: %d", w.Code)
	}
	if w := do("GET", "/api/info", "", mine2); w.Code != 200 {
		t.Errorf("the browser that changed it: %d", w.Code)
	}

	// A restart keeps the password and the sessions.
	app.auth, _ = loadAuth(app.dataDir)
	if w := do("GET", "/api/info", "", mine2); w.Code != 200 || !app.auth.enabled() {
		t.Errorf("after a restart: %d", w.Code)
	}

	// Turning it off opens everything again.
	if w := do("PUT", "/api/auth", `{"password": ""}`, mine2); w.Code != 200 {
		t.Fatalf("turning it off: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/info", "", nil); w.Code != 200 {
		t.Errorf("without a password again: %d", w.Code)
	}
}

func TestPasswordGuessing(t *testing.T) {
	app, do := authApp(t)
	app.auth.set("correct horse")
	for range failedMax {
		do("POST", "/login", "password=guess", nil)
	}
	if w := do("POST", "/login", url.Values{"password": {"correct horse"}}.Encode(), nil); w.Code != 429 {
		t.Errorf("after %d wrong guesses, the right one: %d, want 429", failedMax, w.Code)
	}
	// Another address isn't held up.
	w := do("POST", "/login", url.Values{"password": {"correct horse"}}.Encode(), nil, func(r *http.Request) { r.RemoteAddr = "192.168.1.30:5000" })
	if w.Code != 303 {
		t.Errorf("another address: %d", w.Code)
	}
	// The wait ends.
	app.auth.mu.Lock()
	for k, ts := range app.auth.failed {
		for i := range ts {
			ts[i] = ts[i].Add(-failedWindow - time.Second)
		}
		app.auth.failed[k] = ts
	}
	app.auth.mu.Unlock()
	if w := do("POST", "/login", url.Values{"password": {"correct horse"}}.Encode(), nil); w.Code != 303 {
		t.Errorf("after the wait: %d", w.Code)
	}
}

// OMNIPING_PASSWORD wins over the page, and its sessions last across
// restarts until it changes.
func TestPasswordFromEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OMNIPING_PASSWORD", "from docker")
	s, err := loadAuth(dir)
	if err != nil || !s.enabled() || !s.fromEnv || !s.matches("from docker") {
		t.Fatalf("enabled %v, from env %v, %v", s.enabled(), s.fromEnv, err)
	}
	token, _ := s.newSession()
	s2, _ := loadAuth(dir)
	if !s2.validSession(token) {
		t.Error("a restart with the same password signed the browser out")
	}
	t.Setenv("OMNIPING_PASSWORD", "changed")
	s3, _ := loadAuth(dir)
	if s3.validSession(token) || !s3.matches("changed") {
		t.Error("a changed password kept the old session")
	}

	app, do := authApp(t)
	app.auth = s3
	if w := do("PUT", "/api/auth", `{"password": ""}`, nil, func(r *http.Request) {
		tok, _ := s3.newSession()
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	}); w.Code != 400 {
		t.Errorf("turning off an OMNIPING_PASSWORD from the page: %d, want 400", w.Code)
	}
}

// In the Windows app, this PC itself never needs the password.
func TestPasswordWindowsAppLocal(t *testing.T) {
	app, _, _ := desktopApp(t)
	var err error
	if app.auth, err = loadAuth(app.dataDir); err != nil {
		t.Fatal(err)
	}
	app.auth.set("correct horse")
	if code, _ := call(app, "GET", "/api/info", "", fromHere, pageHost(app)); code != 200 {
		t.Errorf("from this PC: %d", code)
	}
	if code, _ := call(app, "GET", "/api/info", "", fromPhone, "192.168.1.20:8080"); code != 401 {
		t.Errorf("from a phone: %d, want 401", code)
	}
}
