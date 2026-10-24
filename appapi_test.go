package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeShell records what OmniPing asks of the Windows tray.
type fakeShell struct {
	mu       sync.Mutex
	attached []ShellControls
	statuses []ShellStatus
	notes    []fakeNote
	auto     bool
	opened   []string
}

type fakeNote struct {
	title, body, path string
	alert             bool
}

func (s *fakeShell) Desktop() bool { return true }
func (s *fakeShell) Attach(c ShellControls) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attached = append(s.attached, c)
}
func (s *fakeShell) SetStatus(st ShellStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses = append(s.statuses, st)
}
func (s *fakeShell) Notify(title, body, path string, alert bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notes = append(s.notes, fakeNote{title, body, path, alert})
}
func (s *fakeShell) Autostart() (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.auto, true
}
func (s *fakeShell) SetAutostart(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auto = on
	return nil
}
func (s *fakeShell) Open(target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opened = append(s.opened, target)
	return nil
}
func (s *fakeShell) Restart() error { return nil }

func (s *fakeShell) lastAttached() ShellControls {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.attached) == 0 {
		return ShellControls{}
	}
	return s.attached[len(s.attached)-1]
}

func (s *fakeShell) lastStatus() (ShellStatus, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.statuses) == 0 {
		return ShellStatus{}, 0
	}
	return s.statuses[len(s.statuses)-1], len(s.statuses)
}

// desktopApp is outageApp as the Windows app, serving on 127.0.0.1 at a
// free port. restarts receives a value each time OmniPing asks to restart.
func desktopApp(t *testing.T) (app *App, sh *fakeShell, restarts chan struct{}) {
	app, _ = outageApp(t)
	app.engine = newEngine(app)
	sh = &fakeShell{}
	restarts = make(chan struct{}, 4)
	appDir := t.TempDir()
	app.shell = sh
	app.opts = serverOptions{
		DataDir: app.dataDir, Host: "127.0.0.1",
		AppFile: filepath.Join(appDir, "app.json"), LogFile: filepath.Join(appDir, "omniping.log"),
		Restart: func() error { restarts <- struct{}{}; return nil },
	}
	ln, port, err := listenServer("127.0.0.1", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	app.opts.Port = port
	srv := &http.Server{Handler: app.handler(http.NotFoundHandler())}
	t.Cleanup(func() { srv.Close() })
	app.web = newPageServer(srv)
	app.web.start(ln, "127.0.0.1", port)
	return app, sh, restarts
}

const (
	fromHere  = "127.0.0.1:50123"
	fromPhone = "192.168.1.30:50123"
)

// call sends a request to app's handler as if from remote, to the page at
// host (like 127.0.0.1:8080).
func call(app *App, method, path, body, remote, host string) (int, map[string]any) {
	r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
	r.RemoteAddr = remote
	w := httptest.NewRecorder()
	app.handler(http.NotFoundHandler()).ServeHTTP(w, r)
	var m map[string]any
	json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

func pageHost(app *App) string {
	_, port, _ := app.web.addr()
	return fmt.Sprintf("127.0.0.1:%d", port)
}

func TestAppSettingsOnlyInTheWindowsApp(t *testing.T) {
	app, _ := outageApp(t)
	app.shell = noShell{}
	if code, _ := call(app, "GET", "/api/app", "", fromHere, "127.0.0.1:8080"); code != 404 {
		t.Errorf("GET /api/app without the Windows app: %d, want 404", code)
	}
	if app.info().Desktop {
		t.Error("info says desktop without the Windows app")
	}
}

func TestAppSettingsLocalAndFromOtherDevices(t *testing.T) {
	app, _, _ := desktopApp(t)
	if !app.info().Desktop {
		t.Error("info doesn't say desktop")
	}
	host := pageHost(app)
	code, m := call(app, "GET", "/api/app", "", fromHere, host)
	if code != 200 || m["local"] != true || m["desktop"] != true {
		t.Fatalf("local GET: %d %v", code, m)
	}
	if m["url"] != "http://"+host+"/" || m["dataDir"] != absDir(app.dataDir) || m["canMove"] != true || m["movedFrom"] != 0.0 ||
		m["allDevices"] != false || m["logFile"] != app.opts.LogFile || m["openOnStart"] != true {
		t.Errorf("local GET: %v", m)
	}
	if a, ok := m["addresses"].([]any); !ok || len(a) != 0 {
		t.Errorf("addresses while only this computer may connect: %v", m["addresses"])
	}

	code, m = call(app, "GET", "/api/app", "", fromPhone, "192.168.1.20:8080")
	if code != 200 || m["local"] != false {
		t.Fatalf("GET from a phone: %d %v", code, m)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"addresses", "allDevices", "desktop", "local", "port"}; !slices.Equal(keys, want) {
		t.Errorf("a phone sees %v, want only %v", keys, want)
	}

	for _, c := range []struct {
		remote, host string
		local        bool
	}{
		{fromHere, "localhost:8080", true},
		{"[::1]:50123", "[::1]:8080", true},
		{"[::ffff:127.0.0.1]:50123", "127.0.0.1:8080", true},
		{fromPhone, "127.0.0.1:8080", false},
	} {
		if _, m := call(app, "GET", "/api/app", "", c.remote, c.host); m["local"] != c.local {
			t.Errorf("from %s to %s: local = %v, want %v", c.remote, c.host, m["local"], c.local)
		}
	}
	// A web page whose name was made to point at this computer (DNS
	// rebinding) gets nothing at all from the Windows app.
	for _, path := range []string{"/api/app", "/api/export/config", "/api/state"} {
		if code, _ := call(app, "GET", path, "", fromHere, "evil.example:8080"); code != http.StatusMisdirectedRequest {
			t.Errorf("GET %s for evil.example: %d, want 421", path, code)
		}
	}
	// This computer's name works on its own. With other devices on, also
	// with .local and the network's DNS suffix; with any other domain it is a
	// rebinding trick. The network chooses its suffix, so with other devices
	// off (as on a network you don't trust) that doesn't count either.
	dnsSuffixes = func() []string { return []string{"fritz.box"} }
	t.Cleanup(func() { dnsSuffixes = localDNSSuffixes })
	t.Setenv("OMNIPING_ALLOWED_HOSTS", "omniping, Monitor.Example.")
	// The computer's own name, without a domain: macOS gives it as
	// name.local.
	name, _ := os.Hostname()
	name = strings.ToLower(strings.SplitN(name, ".", 2)[0])
	for _, c := range []struct {
		host           string
		local, network int // answer with other devices off and on
	}{
		{name + ":8080", 200, 200},
		{strings.ToUpper(name) + ".:8080", 200, 200},
		{"localhost.:8080", 200, 200},
		{"omniping:8080", 200, 200},
		{"monitor.example", 200, 200},
		{name + ".local:8080", 421, 200},
		{name + ".fritz.box:8080", 421, 200},
		{name + ".fritz.box.:8080", 421, 200},
		{name + ".evil.example:8080", 421, 421},
		{name + ".fritz.box.evil.example", 421, 421},
		{"fritz.box:8080", 421, 421},
		{"omniping.evil.example", 421, 421},
		{"x" + name + ".local", 421, 421},
	} {
		for _, all := range []bool{false, true} {
			app.web.mu.Lock()
			app.web.host = map[bool]string{false: "127.0.0.1", true: ""}[all]
			app.web.mu.Unlock()
			want := map[bool]int{false: c.local, true: c.network}[all]
			if code, _ := call(app, "GET", "/api/app", "", fromHere, c.host); code != want {
				t.Errorf("GET for %s (other devices %v): %d, want %d", c.host, all, code, want)
			}
		}
	}
	app.web.mu.Lock()
	app.web.host = "127.0.0.1"
	app.web.mu.Unlock()

	for _, req := range [][2]string{
		{"PUT", "/api/app"}, {"POST", "/api/app/open"}, {"POST", "/api/app/move"},
	} {
		code, m := call(app, req[0], req[1], `{"port": 8090, "what": "data", "dir": "/tmp/x"}`, fromPhone, "192.168.1.20:8080")
		if code != 403 || m["error"] != errNotLocal {
			t.Errorf("%s %s from a phone: %d %v", req[0], req[1], code, m)
		}
	}
}

func TestAppPortChange(t *testing.T) {
	app, sh, _ := desktopApp(t)
	_, oldPort, _ := app.web.addr()

	for _, p := range []int{0, -1, 65536} {
		if code, m := call(app, "PUT", "/api/app", fmt.Sprintf(`{"port": %d}`, p), fromHere, pageHost(app)); code != 400 {
			t.Errorf("port %d: %d %v", p, code, m)
		}
	}
	busy := httptest.NewServer(http.NotFoundHandler())
	defer busy.Close()
	bp := serverPort(t, busy)
	code, m := call(app, "PUT", "/api/app", fmt.Sprintf(`{"port": %d}`, bp), fromHere, pageHost(app))
	if code != 400 || m["error"] != fmt.Sprintf("Port %d is in use by another program.", bp) {
		t.Errorf("taken port: %d %v", code, m)
	}

	// The page asks over its own connection, which has to get the answer.
	free, _ := net.Listen("tcp", "127.0.0.1:0")
	newPort := free.Addr().(*net.TCPAddr).Port
	free.Close()
	req, _ := http.NewRequest("PUT", fmt.Sprintf("http://127.0.0.1:%d/api/app", oldPort), strings.NewReader(fmt.Sprintf(`{"port": %d}`, newPort)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var reply struct {
		App appInfo `json:"app"`
		URL string  `json:"url"`
	}
	json.NewDecoder(resp.Body).Decode(&reply)
	resp.Body.Close()
	want := fmt.Sprintf("http://127.0.0.1:%d/", newPort)
	if resp.StatusCode != 200 || reply.URL != want || reply.App.Port != newPort {
		t.Fatalf("moving to port %d: %d %+v", newPort, resp.StatusCode, reply)
	}
	if !answers(fmt.Sprintf("127.0.0.1:%d", newPort)) {
		t.Error("nothing answers on the new port")
	}
	deadline := time.Now().Add(5 * time.Second)
	for answers(fmt.Sprintf("127.0.0.1:%d", oldPort)) {
		if time.Now().After(deadline) {
			t.Fatal("the old port still answers")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if s, _ := loadAppSettings(app.opts.AppFile); s.Port != newPort {
		t.Errorf("saved port %d, want %d", s.Port, newPort)
	}
	if in, err := readInstance(app.dataDir); err != nil || in.URL != want {
		t.Errorf("instance.json: %+v, %v", in, err)
	}
	if c := sh.lastAttached(); c.URL != want || c.DataDir != absDir(app.dataDir) || c.SetPaused == nil {
		t.Errorf("the tray got %+v", c)
	}
}

func TestAppOtherSettings(t *testing.T) {
	app, sh, _ := desktopApp(t)
	code, m := call(app, "PUT", "/api/app", `{"openOnStart": false, "autostart": true}`, fromHere, pageHost(app))
	if code != 200 || m["url"] != "" {
		t.Fatalf("%d %v", code, m)
	}
	if got := m["app"].(map[string]any); got["openOnStart"] != false || got["autostart"] != true {
		t.Errorf("reply: %v", got)
	}
	if on, _ := sh.Autostart(); !on {
		t.Error("autostart was not turned on")
	}
	s, _ := loadAppSettings(app.opts.AppFile)
	if s.OpenOnStart || s.Port != defaultPort || !s.AllDevices {
		t.Errorf("saved %+v; only openOnStart should have changed", s)
	}
}

func TestAppAccessChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("listening on every network brings up the Windows firewall question")
	}
	app, _, _ := desktopApp(t)
	_, port, _ := app.web.addr()
	code, m := call(app, "PUT", "/api/app", `{"allDevices": true}`, fromHere, pageHost(app))
	if code != 200 || m["url"] != "" || m["app"].(map[string]any)["allDevices"] != true {
		t.Fatalf("all devices on: %d %v", code, m)
	}
	if h, p, _ := app.web.addr(); h != "" || p != port {
		t.Errorf("listening on %q:%d", h, p)
	}
	// From [::1] the page has to move to 127.0.0.1 when only that is served.
	code, m = call(app, "PUT", "/api/app", `{"allDevices": false}`, "[::1]:50123", fmt.Sprintf("[::1]:%d", port))
	if code != 200 || m["url"] != fmt.Sprintf("http://127.0.0.1:%d/", port) {
		t.Fatalf("all devices off: %d %v", code, m)
	}
	if s, _ := loadAppSettings(app.opts.AppFile); s.AllDevices {
		t.Error("allDevices was not saved")
	}
	if !answers(fmt.Sprintf("127.0.0.1:%d", port)) {
		t.Error("nothing answers on 127.0.0.1")
	}
}

func TestAppOpenFiles(t *testing.T) {
	app, sh, _ := desktopApp(t)
	for what, want := range map[string]string{"data": absDir(app.dataDir), "log": app.opts.LogFile} {
		sh.opened = nil
		if code, m := call(app, "POST", "/api/app/open", `{"what": "`+what+`"}`, fromHere, pageHost(app)); code != 204 {
			t.Errorf("open %s: %d %v", what, code, m)
		}
		if !slices.Equal(sh.opened, []string{want}) {
			t.Errorf("open %s opened %v, want %s", what, sh.opened, want)
		}
	}
	if code, _ := call(app, "POST", "/api/app/open", `{"what": "C:\\Windows"}`, fromHere, pageHost(app)); code != 400 {
		t.Errorf("opening something else: %d", code)
	}
}

func TestMoveDataFolder(t *testing.T) {
	app, _, restarts := desktopApp(t)
	move := func(dir string) (int, map[string]any) {
		b, _ := json.Marshal(map[string]string{"dir": dir})
		return call(app, "POST", "/api/app/move", string(b), fromHere, pageHost(app))
	}
	elsewhere := t.TempDir()
	full := filepath.Join(elsewhere, "full")
	os.MkdirAll(full, 0o755)
	os.WriteFile(filepath.Join(full, "notes.txt"), []byte("mine"), 0o644)
	file := filepath.Join(elsewhere, "file.txt")
	os.WriteFile(file, []byte("x"), 0o644)
	bad := []string{"", "relative", app.dataDir, filepath.Join(app.dataDir, "inside"), full, file}
	if runtime.GOOS == "windows" {
		bad = append(bad, strings.ToUpper(app.dataDir)+`\inside`, `\no-drive`)
	}
	for _, dir := range bad {
		if code, m := move(dir); code != 400 {
			t.Errorf("moving to %q: %d %v", dir, code, m)
		}
	}
	select {
	case <-restarts:
		t.Fatal("restarted after a refused move")
	default:
	}

	// A folder with only the app's own files is fine: the app folder.
	appDir := filepath.Join(elsewhere, "appdir")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "app.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(appDir, "omniping.log"), nil, 0o644)
	os.WriteFile(filepath.Join(appDir, "omniping.log.1.tmp"), nil, 0o644)
	if _, err := checkNewDataDir(absDir(app.dataDir), "", appDir); err != nil {
		t.Errorf("the app folder: %v", err)
	}

	dir := filepath.Join(elsewhere, "new", "OmniPing")
	code, m := move(dir)
	if code != 200 || m["restarting"] != true {
		t.Fatalf("moving to %s: %d %v", dir, code, m)
	}
	select {
	case <-restarts:
	case <-time.After(3 * time.Second):
		t.Fatal("no restart")
	}
	if s, _ := loadAppSettings(app.opts.AppFile); s.MoveTo != dir {
		t.Errorf("MoveTo = %q, want %q", s.MoveTo, dir)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("the new folder should exist and be empty: %v %v", entries, err)
	}

	// When the restart fails, the waiting page learns why.
	app.opts.Restart = func() error { return errors.New("no new copy") }
	if code, m := move(filepath.Join(elsewhere, "r")); code != 200 {
		t.Fatalf("moving to r: %d %v", code, m)
	}
	var restartErr any
	for i := 0; i < 30 && restartErr == nil; i++ {
		time.Sleep(100 * time.Millisecond)
		_, m := call(app, "GET", "/api/app", "", fromHere, pageHost(app))
		restartErr = m["restartError"]
	}
	if restartErr != "no new copy" {
		t.Errorf("restartError = %v", restartErr)
	}

	app.opts.Portable = true
	if code, m := move(filepath.Join(elsewhere, "p")); code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "Portable") {
		t.Errorf("portable: %d %v", code, m)
	}
	app.opts.Portable, app.opts.Restart = false, nil
	if code, _ := move(filepath.Join(elsewhere, "q")); code != 400 {
		t.Errorf("without a way to restart: %d", code)
	}
}

func TestTrayPauseAndStatus(t *testing.T) {
	app, sh, _ := desktopApp(t)
	app.pushStatus()
	app.pushStatus()
	st, n := sh.lastStatus()
	if n != 1 || st != (ShellStatus{Targets: 4}) {
		t.Fatalf("status %+v after %d calls; want one call with 4 targets", st, n)
	}
	if err := app.setPaused(true); err != nil {
		t.Fatal(err)
	}
	if app.cfg.settings().Running {
		t.Error("still running after pausing")
	}
	deadline := time.Now().Add(3 * time.Second)
	for st, _ := sh.lastStatus(); !st.Paused; st, _ = sh.lastStatus() {
		if time.Now().After(deadline) {
			t.Fatal("the tray was not told about the pause")
		}
		time.Sleep(20 * time.Millisecond)
	}
	app.setPaused(false)
	if !app.cfg.settings().Running {
		t.Error("not running after starting again")
	}
}

func TestLANAddresses(t *testing.T) {
	for _, u := range lanAddresses(8080) {
		if !strings.HasPrefix(u, "http://") || !strings.HasSuffix(u, ":8080/") || strings.Contains(u, "127.0.0.1") {
			t.Errorf("odd address %s", u)
		}
	}
	if !looksVirtual("vEthernet (WSL)") || looksVirtual("Wi-Fi") {
		t.Error("looksVirtual is wrong")
	}
}

// The page asks for a move over a real connection and gets its answer.
func TestMoveOverTheNetwork(t *testing.T) {
	app, _, restarts := desktopApp(t)
	b, _ := json.Marshal(map[string]string{"dir": filepath.Join(t.TempDir(), "x")})
	resp, err := http.Post("http://"+pageHost(app)+"/api/app/move", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"restarting":true`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	select {
	case <-restarts:
	case <-time.After(3 * time.Second):
		t.Fatal("no restart")
	}
}
