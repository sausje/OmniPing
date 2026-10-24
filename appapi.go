package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The "This computer" settings of the Windows app: the port, whether other
// devices can open OmniPing, starting with Windows and the data folder. They
// are served at /api/app, only by the Windows app, and only the computer
// OmniPing runs on can change them.

// desktop tells whether OmniPing runs as the Windows app.
func (a *App) desktop() bool { return a.shell != nil && a.shell.Desktop() }

// announce tells the shell and instance.json where the page is now.
func (a *App) announce() {
	url := a.web.localURL()
	if err := writeInstance(a.dataDir, instanceInfo{URL: url, PID: os.Getpid(), Version: version}); err != nil {
		log.Printf("writing %s: %v", instanceFile, err)
	}
	a.shell.Attach(ShellControls{URL: url, DataDir: absDir(a.dataDir), SetPaused: a.setPaused})
}

func absDir(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// shownDataDir is the data folder as every page sees it. The Windows app
// writes it with %LOCALAPPDATA% or %USERPROFILE%, so other devices on the
// network don't learn the account name; the settings on this computer show
// the full path (GET /api/app).
func (a *App) shownDataDir() string {
	if !a.desktop() {
		return a.dataDir
	}
	d := absDir(a.dataDir)
	for _, v := range []string{"LOCALAPPDATA", "USERPROFILE"} {
		if p := os.Getenv(v); p != "" {
			if rel, err := filepath.Rel(p, d); err == nil && filepath.IsLocal(rel) {
				return filepath.Join("%"+v+"%", rel)
			}
		}
	}
	return d
}

// statusLoop keeps the tray icon up to date.
func (a *App) statusLoop(ctx context.Context) {
	a.pushStatus()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			a.pushStatus()
		}
	}
}

// pushStatus gives the shell the current summary, when it changed.
func (a *App) pushStatus() {
	if !a.desktop() {
		return
	}
	a.statusMu.Lock()
	defer a.statusMu.Unlock()
	st := ShellStatus{Targets: len(a.cfg.targets()), Alerts: len(a.alerts.Active()), Paused: !a.cfg.settings().Running}
	if a.statusSent && st == a.status {
		return
	}
	a.status, a.statusSent = st, true
	a.shell.SetStatus(st)
}

const errNotLocal = "These settings can only be changed on the computer OmniPing runs on."

// isLocal tells whether r comes from the computer OmniPing runs on. The
// address the browser asked for must be this computer too, so a web page
// whose name points at 127.0.0.1 (DNS rebinding) doesn't count.
func isLocal(r *http.Request) bool {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || !ap.Addr().Unmap().IsLoopback() {
		return false
	}
	host := requestHost(r)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Unmap().IsLoopback()
}

// requestHost is the host name or address in the page's address.
func requestHost(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.Trim(host, "[]"))
}

// appInfo is GET /api/app. Other devices only see where to open the page.
type appInfo struct {
	Desktop    bool     `json:"desktop"`
	Local      bool     `json:"local"`
	Port       int      `json:"port"`
	AllDevices bool     `json:"allDevices"`
	Addresses  []string `json:"addresses"`
	*appLocalInfo
}

type appLocalInfo struct {
	URL                string `json:"url"`
	MovedFrom          int    `json:"movedFrom"`
	OpenOnStart        bool   `json:"openOnStart"`
	Autostart          bool   `json:"autostart"`
	AutostartSupported bool   `json:"autostartSupported"`
	DataDir            string `json:"dataDir"`
	DataBytes          int64  `json:"dataBytes"`
	Portable           bool   `json:"portable"`
	CanMove            bool   `json:"canMove"`
	LogFile            string `json:"logFile"`
	// RestartError says why OmniPing could not restart to move the data.
	RestartError string `json:"restartError,omitempty"`
}

func (a *App) appSettings() AppSettings {
	if a.opts.AppFile == "" {
		return defaultAppSettings()
	}
	s, err := readAppSettings(a.opts.AppFile)
	if err != nil {
		log.Printf("reading %s: %v", a.opts.AppFile, err)
	}
	return s
}

func (a *App) appInfo(r *http.Request) appInfo {
	host, port, movedFrom := a.web.addr()
	all := allInterfaces(host)
	in := appInfo{Desktop: true, Port: port, AllDevices: all, Addresses: []string{}}
	if all {
		in.Addresses = lanAddresses(port)
	}
	if !isLocal(r) {
		return in
	}
	in.Local = true
	on, supported := a.shell.Autostart()
	in.appLocalInfo = &appLocalInfo{
		URL: a.web.localURL(), MovedFrom: movedFrom, OpenOnStart: a.appSettings().OpenOnStart,
		Autostart: on, AutostartSupported: supported,
		DataDir: absDir(a.dataDir), DataBytes: dirSize(a.dataDir),
		Portable: a.opts.Portable, CanMove: a.canMove(), LogFile: a.opts.LogFile,
	}
	if msg := a.restartErr.Load(); msg != nil {
		in.RestartError = *msg
	}
	return in
}

func (a *App) canMove() bool { return !a.opts.Portable && a.opts.Restart != nil }

// lanAddresses are the page's addresses for phones and other computers:
// the private IPv4 addresses of this computer's networks. Those of virtual
// networks (WSL, Hyper-V, VirtualBox and the like) come last.
func lanAddresses(port int) []string {
	ifs, err := net.Interfaces()
	if err != nil {
		return []string{}
	}
	var real, virtual []string
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, ad := range addrs {
			ipn, ok := ad.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || !ipn.IP.IsPrivate() {
				continue
			}
			u := "http://" + net.JoinHostPort(ipn.IP.To4().String(), strconv.Itoa(port)) + "/"
			if looksVirtual(ifc.Name) {
				virtual = append(virtual, u)
			} else {
				real = append(real, u)
			}
		}
	}
	return append(append([]string{}, real...), virtual...)
}

func looksVirtual(name string) bool {
	name = strings.ToLower(name)
	for _, s := range []string{"vethernet", "virtual", "vmware", "vbox", "wsl", "docker", "hyper-v"} {
		if strings.Contains(name, s) {
			return true
		}
	}
	return false
}

// dirSize adds up the sizes of the files in dir.
func dirSize(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

func (a *App) getApp(w http.ResponseWriter, r *http.Request) {
	if !a.desktop() {
		fail(w, 404, "only the Windows app has these settings")
		return
	}
	writeJSON(w, 200, a.appInfo(r))
}

// putApp changes the port, access from other devices, opening the page at
// start and starting with Windows. A new port or access takes effect at
// once; the reply's url is where the page has to go then ("" if it can
// stay).
func (a *App) putApp(w http.ResponseWriter, r *http.Request) {
	if !a.desktop() {
		fail(w, 404, "only the Windows app has these settings")
		return
	}
	if !isLocal(r) {
		fail(w, 403, errNotLocal)
		return
	}
	var req struct {
		Port        *int  `json:"port"`
		AllDevices  *bool `json:"allDevices"`
		OpenOnStart *bool `json:"openOnStart"`
		Autostart   *bool `json:"autostart"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.appMu.Lock()
	defer a.appMu.Unlock()
	host, port, _ := a.web.addr()
	newHost, newPort := host, port
	if req.Port != nil {
		if *req.Port < 1 || *req.Port > 65535 {
			fail(w, 400, "The port must be a number from 1 to 65535.")
			return
		}
		newPort = *req.Port
	}
	if req.AllDevices != nil && *req.AllDevices != allInterfaces(host) {
		newHost = "127.0.0.1"
		if *req.AllDevices {
			newHost = ""
		}
	}

	if req.Autostart != nil {
		if err := a.shell.SetAutostart(*req.Autostart); err != nil {
			fail(w, 500, "Could not change Start with Windows: "+err.Error())
			return
		}
	}
	url := ""
	var retire func()
	if newHost != host || newPort != port {
		var err error
		retire, err = a.web.move(newHost, newPort)
		if err != nil {
			if isPortTaken(err) {
				fail(w, 400, fmt.Sprintf("Port %d is in use by another program.", newPort))
			} else {
				fail(w, 400, fmt.Sprintf("Cannot use port %d: %v", newPort, err))
			}
			return
		}
		log.Printf("now listening on %s", net.JoinHostPort(newHost, strconv.Itoa(newPort)))
		// The page has to move when its address no longer works.
		if newPort != port || (!allInterfaces(newHost) && requestHost(r) != newHost) {
			url = a.web.localURL()
		}
		a.announce()
	}
	if a.opts.AppFile != "" && (req.Port != nil || req.AllDevices != nil || req.OpenOnStart != nil) {
		err := updateAppSettings(a.opts.AppFile, func(s *AppSettings) {
			if req.Port != nil {
				s.Port = newPort
			}
			if req.AllDevices != nil {
				s.AllDevices = *req.AllDevices
			}
			if req.OpenOnStart != nil {
				s.OpenOnStart = *req.OpenOnStart
			}
		})
		if err != nil {
			log.Printf("saving %s: %v", a.opts.AppFile, err)
			// A new port or access works now, so the page has to follow it;
			// it just won't be kept after a restart.
			if retire == nil {
				fail(w, 500, "Could not save the settings: "+err.Error())
				return
			}
		}
	}
	writeJSON(w, 200, map[string]any{"app": a.appInfo(r), "url": url})
	if retire != nil {
		http.NewResponseController(w).Flush()
		time.AfterFunc(time.Second, retire)
	}
}

// openAppFile opens the data folder or the log on this computer.
func (a *App) openAppFile(w http.ResponseWriter, r *http.Request) {
	if !a.desktop() {
		fail(w, 404, "only the Windows app has these settings")
		return
	}
	if !isLocal(r) {
		fail(w, 403, errNotLocal)
		return
	}
	var req struct {
		What string `json:"what"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var target string
	switch req.What {
	case "data":
		target = absDir(a.dataDir)
	case "log":
		if a.opts.LogFile == "" {
			fail(w, 400, "OmniPing has no log file.")
			return
		}
		target = a.opts.LogFile
	default:
		fail(w, 400, `what must be "data" or "log"`)
		return
	}
	if err := a.shell.Open(target); err != nil {
		fail(w, 500, fmt.Sprintf("Could not open %s: %v", target, err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Files that belong to the app rather than to the data; a new data folder
// may already hold these (the app folder does).
var appOnlyFiles = []string{"app.json", "app.json.tmp", "omniping.log", "omniping.log.1", "omniping.log.1.tmp", "crash.log", instanceFile}

// moveData asks the next start to move the data folder to another place,
// and restarts OmniPing. The copying happens at that start, before anything
// has the files open.
func (a *App) moveData(w http.ResponseWriter, r *http.Request) {
	if !a.desktop() {
		fail(w, 404, "only the Windows app has these settings")
		return
	}
	if !isLocal(r) {
		fail(w, 403, errNotLocal)
		return
	}
	var req struct {
		Dir string `json:"dir"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	switch {
	case a.opts.Portable:
		fail(w, 400, "Portable: the data stays next to OmniPing.exe.")
		return
	case a.opts.Restart == nil || a.opts.AppFile == "":
		fail(w, 400, "The data folder can't be moved when OmniPing is started this way.")
		return
	}
	a.appMu.Lock()
	defer a.appMu.Unlock()
	dir, err := checkNewDataDir(absDir(a.dataDir), a.opts.ProgramDir, unquote(req.Dir))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := updateAppSettings(a.opts.AppFile, func(s *AppSettings) { s.MoveTo = dir }); err != nil {
		fail(w, 500, "Could not save the settings: "+err.Error())
		return
	}
	log.Printf("moving the data folder to %s at the restart", dir)
	a.restartErr.Store(nil)
	writeJSON(w, 200, map[string]bool{"restarting": true})
	http.NewResponseController(w).Flush()
	time.AfterFunc(300*time.Millisecond, func() {
		if err := a.opts.Restart(); err != nil {
			msg := err.Error()
			a.restartErr.Store(&msg)
		}
	})
}

// unquote removes the quotes Explorer's "Copy as path" puts around a path.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// checkNewDataDir checks dir as a new data folder for the one in cur: a full
// path, not inside cur or the "data" folder next to the program, and new or
// empty (files of the app itself aside). It creates the folder, which also shows it can be
// written to.
func checkNewDataDir(cur, programDir, dir string) (string, error) {
	if dir == "" || !filepath.IsAbs(dir) {
		return "", errors.New(`Enter a full folder path, like D:\OmniPing.`)
	}
	dir = filepath.Clean(dir)
	// A Windows folder name can't hold a colon; data::$INDEX_ALLOCATION
	// would still be a folder called data.
	if runtime.GOOS == "windows" && strings.Contains(dir[len(filepath.VolumeName(dir)):], ":") {
		return "", errors.New("A folder name can't contain a colon (:).")
	}
	if sameFolder(dir, cur) {
		return "", errors.New("That is the data folder already.")
	}
	if insideOrSame(dir, cur) {
		return "", errors.New("The new folder can't be inside the current data folder.")
	}
	if programDir != "" && portableData(dir, programDir) {
		return "", errors.New("A folder called data next to OmniPing.exe would make it the portable version. Choose another folder.")
	}
	fi, err := os.Stat(dir)
	switch {
	case err == nil && !fi.IsDir():
		return "", fmt.Errorf("%s is a file, not a folder.", dir)
	case err == nil:
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", fmt.Errorf("Cannot read %s: %v", dir, err)
		}
		for _, e := range entries {
			if !slices.Contains(appOnlyFiles, strings.ToLower(e.Name())) {
				return "", fmt.Errorf("%s is not empty. Choose an empty or a new folder.", dir)
			}
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("Cannot create %s: %v", dir, err)
	}
	f, err := os.CreateTemp(dir, ".omniping-check-*")
	if err != nil {
		return "", fmt.Errorf("Cannot write to %s: %v", dir, err)
	}
	f.Close()
	os.Remove(f.Name())
	return dir, nil
}
