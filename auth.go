package main

// The optional password. Off by default: OmniPing is meant for a home
// network. When one is set (on the page, or with OMNIPING_PASSWORD), every
// page and API call needs a signed-in browser; /healthz stays open for
// Docker, /metrics also takes the password as basic auth, and in the
// Windows app this PC itself never needs it.
//
// The password is kept as a slow PBKDF2 hash in auth.json in the data
// folder, not in config.json, so settings backups don't carry it. Signed-in
// browsers hold a random token for 30 days; auth.json keeps only hashes of
// those tokens, and changing the password signs every browser out.

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	authFile       = "auth.json"
	sessionCookie  = "omniping_session"
	sessionDays    = 30
	pbkdf2Rounds   = 600_000
	minPasswordLen = 8
	// After failedMax wrong passwords from one address within failedWindow,
	// that address waits until the window is over.
	failedMax    = 10
	failedWindow = 15 * time.Minute
)

type authStore struct {
	mu      sync.Mutex
	file    string
	fromEnv bool
	saved   authFileData
	failed  map[string][]time.Time // wrong passwords per address
}

type authFileData struct {
	Salt     string           `json:"salt,omitempty"` // base64
	Hash     string           `json:"hash,omitempty"` // base64; empty: no password
	Rounds   int              `json:"rounds,omitempty"`
	Sessions map[string]int64 `json:"sessions,omitempty"` // sha256 of the token (hex) → expiry, ms
}

// loadAuth reads auth.json, and applies OMNIPING_PASSWORD when it is set.
func loadAuth(dataDir string) (*authStore, error) {
	s := &authStore{file: filepath.Join(dataDir, authFile), failed: map[string][]time.Time{}}
	if b, err := os.ReadFile(s.file); err == nil {
		if err := json.Unmarshal(b, &s.saved); err != nil {
			return nil, fmt.Errorf("%s: %v", authFile, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if pw := os.Getenv("OMNIPING_PASSWORD"); pw != "" {
		s.fromEnv = true
		// Keep the sessions while the password is the same as before.
		if !s.matches(pw) {
			if err := s.set(pw); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

func (s *authStore) enabled() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saved.Hash != ""
}

func hashPassword(pw string, salt []byte, rounds int) []byte {
	key, _ := pbkdf2.Key(sha256.New, pw, salt, rounds, 32)
	return key
}

// matches checks a password against the saved hash.
func (s *authStore) matches(pw string) bool {
	s.mu.Lock()
	saved := s.saved
	s.mu.Unlock()
	if saved.Hash == "" {
		return false
	}
	salt, err1 := base64.StdEncoding.DecodeString(saved.Salt)
	want, err2 := base64.StdEncoding.DecodeString(saved.Hash)
	if err1 != nil || err2 != nil || saved.Rounds < 1 {
		return false
	}
	return hmac.Equal(hashPassword(pw, salt, saved.Rounds), want)
}

// set changes the password; an empty one turns it off. Every browser is
// signed out.
func (s *authStore) set(pw string) error {
	data := authFileData{}
	if pw != "" {
		salt := make([]byte, 16)
		rand.Read(salt)
		data = authFileData{Salt: base64.StdEncoding.EncodeToString(salt),
			Hash: base64.StdEncoding.EncodeToString(hashPassword(pw, salt, pbkdf2Rounds)), Rounds: pbkdf2Rounds}
	}
	s.mu.Lock()
	s.saved = data
	s.mu.Unlock()
	return s.write()
}

func (s *authStore) write() error {
	s.mu.Lock()
	b, _ := json.MarshalIndent(s.saved, "", "  ")
	s.mu.Unlock()
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.file)
}

func tokenKey(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// newSession signs a browser in; the token goes into its cookie.
func (s *authStore) newSession() (string, error) {
	b := make([]byte, 32)
	rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	now := time.Now().UnixMilli()
	s.mu.Lock()
	if s.saved.Sessions == nil {
		s.saved.Sessions = map[string]int64{}
	}
	for k, exp := range s.saved.Sessions {
		if exp < now {
			delete(s.saved.Sessions, k)
		}
	}
	s.saved.Sessions[tokenKey(token)] = now + sessionDays*86_400_000
	s.mu.Unlock()
	return token, s.write()
}

func (s *authStore) validSession(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.saved.Sessions[tokenKey(token)]
	return ok && exp > time.Now().UnixMilli()
}

func (s *authStore) endSession(token string) {
	s.mu.Lock()
	delete(s.saved.Sessions, tokenKey(token))
	s.mu.Unlock()
	s.write()
}

// tooMany tells whether addr made too many wrong guesses lately.
func (s *authStore) tooMany(addr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := s.failed[addr][:0]
	for _, t := range s.failed[addr] {
		if time.Since(t) < failedWindow {
			keep = append(keep, t)
		}
	}
	s.failed[addr] = keep
	return len(keep) >= failedMax
}

func (s *authStore) failure(addr string) {
	s.mu.Lock()
	s.failed[addr] = append(s.failed[addr], time.Now())
	s.mu.Unlock()
	time.Sleep(failDelay) // slows guessing down further
}

// failDelay is the wait after a wrong password; a variable for tests.
var failDelay = time.Second

// remoteHost is the address a request came from, for counting wrong
// guesses. Behind a reverse proxy that is the proxy.
func remoteHost(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// authorized tells whether r may use OmniPing.
func (a *App) authorized(r *http.Request) bool {
	if a.auth == nil || !a.auth.enabled() {
		return true
	}
	if a.desktop() && isLocal(r) {
		return true
	}
	if c, err := r.Cookie(sessionCookie); err == nil && a.auth.validSession(c.Value) {
		return true
	}
	if r.URL.Path == "/metrics" {
		if _, pw, ok := r.BasicAuth(); ok && !a.auth.tooMany(remoteHost(r)) {
			if a.auth.matches(pw) {
				return true
			}
			a.auth.failure(remoteHost(r))
		}
	}
	return false
}

// openPath is what a signed-out browser may load: the sign-in page and the
// icons a browser asks for by itself.
func openPath(p string) bool {
	switch p {
	case "/healthz", "/login", "/favicon.svg", "/manifest.webmanifest", "/apple-touch-icon.png",
		"/apple-touch-icon-precomposed.png", "/icon-192.png", "/icon-512.png", "/icon-maskable-192.png", "/icon-maskable-512.png":
		return true
	}
	return false
}

// requireAuth answers a request that isn't signed in.
func requireAuth(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/"):
		fail(w, http.StatusUnauthorized, "Sign in first.")
	case r.URL.Path == "/metrics":
		w.Header().Set("WWW-Authenticate", `Basic realm="OmniPing", charset="UTF-8"`)
		http.Error(w, "OmniPing needs its password (as basic auth, any user name).", http.StatusUnauthorized)
	case r.URL.Path == "/" || r.URL.Path == "/index.html":
		seeOther(w, "login")
	default:
		http.Error(w, "Sign in first.", http.StatusUnauthorized)
	}
}

// seeOther redirects to a relative address as it is, so OmniPing also
// works behind a reverse proxy under a path of its own.
func seeOther(w http.ResponseWriter, to string) {
	w.Header().Set("Location", to)
	w.WriteHeader(http.StatusSeeOther)
}

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (a *App) setSessionCookie(w http.ResponseWriter, r *http.Request) error {
	token, err := a.auth.newSession()
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: sessionDays * 86_400,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureRequest(r)})
	return nil
}

// login shows the sign-in page (GET) or signs in (POST, a form).
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if !a.auth.enabled() {
		seeOther(w, "./")
		return
	}
	if r.Method == http.MethodGet {
		if a.authorized(r) {
			seeOther(w, "./")
			return
		}
		loginPage(w, "", http.StatusOK)
		return
	}
	addr := remoteHost(r)
	if a.auth.tooMany(addr) {
		loginPage(w, "Too many wrong passwords. Wait 15 minutes, then try again.", http.StatusTooManyRequests)
		return
	}
	if !a.auth.matches(r.FormValue("password")) {
		a.auth.failure(addr)
		log.Printf("wrong password from %s", addr)
		loginPage(w, "That's not the password.", http.StatusUnauthorized)
		return
	}
	if err := a.setSessionCookie(w, r); err != nil {
		loginPage(w, "Could not sign in: "+err.Error(), http.StatusInternalServerError)
		return
	}
	seeOther(w, "./")
}

// getAuth is GET /api/auth: is there a password, and where from.
func (a *App) getAuth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"enabled": a.auth.enabled(), "fromEnv": a.auth != nil && a.auth.fromEnv})
}

// putAuth sets, changes or (with an empty one) removes the password. This
// browser stays signed in.
func (a *App) putAuth(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if a.auth == nil {
		fail(w, 404, "passwords aren't available here")
		return
	}
	if a.auth.fromEnv {
		fail(w, 400, "The password is set with OMNIPING_PASSWORD; change it there.")
		return
	}
	if req.Password != "" && len([]rune(req.Password)) < minPasswordLen {
		fail(w, 400, fmt.Sprintf("Use at least %d characters.", minPasswordLen))
		return
	}
	if err := a.auth.set(req.Password); err != nil {
		fail(w, 500, "Could not save the password: "+err.Error())
		return
	}
	if req.Password != "" {
		if err := a.setSessionCookie(w, r); err != nil {
			fail(w, 500, err.Error())
			return
		}
		log.Printf("a password was set")
	} else {
		log.Printf("the password was removed")
	}
	writeJSON(w, 200, map[string]bool{"enabled": req.Password != ""})
}

// logout signs this browser out.
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && a.auth != nil {
		a.auth.endSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// loginPage is the sign-in page: one file with its own styles, so it needs
// nothing that requires being signed in.
func loginPage(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	errHTML := ""
	if msg != "" {
		errHTML = `<p class="err" role="alert">` + html.EscapeString(msg) + `</p>`
	}
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in · OmniPing</title><link rel="icon" href="favicon.svg"><meta name="color-scheme" content="light dark">
<style>
:root{--bg:#f2f4f7;--panel:#fff;--line:#dbe1e8;--fg:#16202b;--muted:#586575;--accent:#1f6feb;--ink:#111922;--loss:#e0363b;--bad:#e0363b}
@media (prefers-color-scheme:dark){:root{--bg:#0d1217;--panel:#121920;--line:#233039;--fg:#dce4ed;--muted:#8a98a8;--accent:#4c8dff;--ink:#e4ebf3;--loss:#ff5257;--bad:#ff5a5f}}
*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--fg);font:15px/1.5 system-ui,"Segoe UI",sans-serif;padding:16px}
form{width:100%%;max-width:320px;background:var(--panel);border:1px solid var(--line);border-radius:14px;padding:28px 24px;display:grid;gap:14px;justify-items:center;text-align:center}
h1{margin:0;font-size:20px}p{margin:0;color:var(--muted);font-size:13.5px}.err{color:var(--bad);font-weight:500}
input{width:100%%;height:40px;border:1px solid var(--line);border-radius:9px;padding:0 12px;background:var(--panel);color:var(--fg);font:inherit}
input:focus{outline:2px solid var(--accent);outline-offset:1px}
button{width:100%%;height:40px;border:0;border-radius:9px;background:var(--accent);color:#fff;font:600 15px system-ui,sans-serif;cursor:pointer}
</style></head><body><form method="post" action="login">
<svg width="44" height="44" viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M2 17H9.7M14.7 17H22" stroke="var(--ink)" stroke-width="2"/><circle cx="2" cy="17" r="1" fill="var(--ink)"/><circle cx="22" cy="17" r="1" fill="var(--ink)"/><path d="M2.9 17L4.6 3.5L6.3 17ZM6.8 17L7.9 10.4L9 17Z" fill="var(--ink)"/><rect x="11.5" y="4" width="1.4" height="16.5" rx=".3" fill="var(--loss)"/></svg>
<h1>OmniPing</h1><p>This OmniPing has a password.</p>%s
<input type="password" name="password" autocomplete="current-password" placeholder="Password" aria-label="Password" required autofocus>
<button type="submit">Sign in</button></form></body></html>`, errHTML)
}
