// OmniPing pings many hosts at once and shows the results in a browser. It
// is a small server that can run on a Raspberry Pi, or an app on Windows.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/sausje/omniping/internal/history"
	"github.com/sausje/omniping/internal/icmp"
)

// version is set at build time: go build -ldflags "-X main.version=1.2.3".
var version = "dev"

//go:embed web
var webFiles embed.FS

// App ties the parts together.
type App struct {
	cfg       *configStore
	dataDir   string
	started   time.Time
	pinger    icmp.Pinger
	pingerErr string
	store     *history.Store
	engine    *Engine
	alerts    *Alerts
	hub       *Hub
	notifier  *Notifier
	disc      *Discovery
	diskBytes atomic.Int64
	// allowScripts enables the "Run a script" channel. It is off unless
	// OMNIPING_ALLOW_SCRIPTS is set, because the page has no login and the
	// channel runs any command on the server.
	allowScripts bool
	// shell is the Windows tray, or noShell.
	shell Shell
	opts  serverOptions
	web   *pageServer
	// appMu keeps changes from the "This computer" settings in order.
	appMu sync.Mutex
	// update is a newer release on GitHub, nil if there's none (or the
	// check is off); updateNow asks updateLoop to check at once.
	update    atomic.Pointer[UpdateInfo]
	updateNow chan struct{}
	// auth is the optional password (auth.go).
	auth *authStore
	// restartErr says why the restart for a data move failed, for the page
	// that waits for it.
	restartErr atomic.Pointer[string]
	// statusMu guards status, the last summary given to the shell.
	statusMu   sync.Mutex
	status     ShellStatus
	statusSent bool
}

func envBool(key string) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// cliOptions are the choices made on the command line or in the environment.
type cliOptions struct {
	DataDir string // folder for settings and history
	Addr    string // address to listen on, like :8080
	WebDir  string // serve the page from this folder (development)
	// DataSet and AddrSet tell whether DataDir and Addr were given, rather
	// than being the defaults; the Windows app has its own defaults.
	DataSet, AddrSet bool
	// Console runs the Windows build as a plain server in a terminal.
	Console bool
	// Autostart: started by Windows at sign-in, so don't open the page.
	Autostart bool
	// Restarted: started by OmniPing itself, which may still be exiting.
	Restarted bool
	// RestartPort is the port the copy before the restart used.
	RestartPort int
}

// serverOptions are what runServer needs to start.
type serverOptions struct {
	DataDir string
	// Host and Port to listen on; an empty Host listens on every interface.
	Host string
	Port int
	// PortFallback lets the Windows app move to the next free port when Port
	// is taken by another program.
	PortFallback bool
	// TryPort is tried before Port, for this start only: after a restart,
	// the page that waits for it keeps its address. It is not saved.
	TryPort int
	WebDir  string
	// AppFile is the Windows app's settings file (port, access, data folder);
	// empty when OmniPing runs as a plain server.
	AppFile  string
	LogFile  string // the program's own log, shown in the settings ("" if none)
	Portable bool   // data lives next to the program; it can't be moved
	// ProgramDir is the folder of OmniPing.exe. A data folder can't move into
	// it: a "data" folder there would make the next start portable.
	ProgramDir string
	// Restart asks the platform to start a new copy and quit this one (after
	// the data folder moved). nil when that isn't possible.
	Restart func() error
}

func main() {
	cli := cliOptions{}
	flag.StringVar(&cli.DataDir, "data", env("OMNIPING_DATA", "data"), "folder for settings and history")
	flag.StringVar(&cli.Addr, "addr", env("OMNIPING_ADDR", ":"+env("OMNIPING_PORT", "8080")), "address to listen on")
	flag.StringVar(&cli.WebDir, "web", env("OMNIPING_WEB_DIR", ""), "serve the web page from this folder instead of the built-in copy (for development)")
	flag.BoolVar(&cli.Console, "console", false, "Windows: run in this terminal instead of next to the clock")
	flag.BoolVar(&cli.Autostart, "autostart", false, "Windows: started at sign-in; don't open the page")
	flag.BoolVar(&cli.Restarted, "restarted", false, "started by OmniPing itself after a restart")
	flag.IntVar(&cli.RestartPort, "restart-port", 0, "after a restart: the port to try first")
	// "omniping healthcheck" asks a running OmniPing whether it is up (for Docker).
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		flag.CommandLine.Parse(os.Args[2:])
		os.Exit(healthcheck(cli.Addr))
	}
	flag.Parse()
	cli.DataSet = os.Getenv("OMNIPING_DATA") != ""
	cli.AddrSet = os.Getenv("OMNIPING_ADDR") != "" || os.Getenv("OMNIPING_PORT") != ""
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "data":
			cli.DataSet = true
		case "addr":
			cli.AddrSet = true
		}
	})
	log.SetFlags(log.Ldate | log.Ltime)
	os.Exit(platformMain(cli))
}

// runClassic runs OmniPing as a plain server, the way it always has: data
// in cli.DataDir, listening on cli.Addr, until Ctrl+C or SIGTERM.
func runClassic(cli cliOptions) int {
	host, portStr, err := net.SplitHostPort(cli.Addr)
	if err != nil {
		log.Printf("cannot listen on %s: %v", cli.Addr, err)
		return 1
	}
	port, err := net.LookupPort("tcp", portStr)
	if err != nil {
		log.Printf("cannot listen on %s: %v", cli.Addr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	o := serverOptions{DataDir: cli.DataDir, Host: host, Port: port, WebDir: cli.WebDir}
	if err := runServer(ctx, o, noShell{}, nil); err != nil {
		log.Print(err)
		return 1
	}
	return 0
}

// runServer runs OmniPing until ctx ends, then saves everything and returns.
// ready, if not nil, is called once the page can be opened, with its address
// on this computer. Errors before that are returned for the platform to show.
func runServer(ctx context.Context, o serverOptions, sh Shell, ready func(url string)) error {
	if !waitForClock(ctx) {
		return nil
	}

	if err := os.MkdirAll(o.DataDir, 0o755); err != nil {
		return fmt.Errorf("cannot create data folder: %v", err)
	}

	// Open the ICMP sockets and the web port first: both may need root, and
	// they keep working after dropPrivileges switches to PUID/PGID.
	pinger, err := icmp.New()
	pingerErr := ""
	if err != nil {
		pingerErr = err.Error()
		log.Printf("PINGING DISABLED: %v", err)
	}
	addr := net.JoinHostPort(o.Host, strconv.Itoa(o.Port))
	var ln net.Listener
	port := 0
	if o.TryPort != 0 && o.TryPort != o.Port {
		ln = listenAgain(o.Host, o.TryPort)
		port = o.TryPort
	}
	if ln == nil {
		ln, port, err = listenServer(o.Host, o.Port, o.PortFallback)
		if err != nil {
			var opErr *net.OpError
			if errors.As(err, &opErr) {
				return fmt.Errorf("cannot listen on %s: %v", addr, err)
			}
			return err // errAlreadyRunning, or all ports taken
		}
	}
	movedFrom := 0
	// A port from the end of the fallback (any free port, picked by the
	// system) is only for now: the next start tries the chosen port again.
	keepPort := nearbyPort(o.Port, port)
	keptAddress := port == o.TryPort
	if port != o.Port && o.Port != 0 {
		movedFrom = o.Port
		if keptAddress {
			log.Printf("using port %d again after the restart", port)
		} else {
			log.Printf("port %d is in use by another program; using port %d", o.Port, port)
		}
		if o.AppFile != "" && keepPort && !keptAddress {
			if err := updateAppSettings(o.AppFile, func(s *AppSettings) { s.Port = port }); err != nil {
				log.Printf("saving the new port: %v", err)
			}
		}
	}
	addr = net.JoinHostPort(o.Host, strconv.Itoa(port))
	dropPrivileges(o.DataDir)

	cfg, err := loadConfig(filepath.Join(o.DataDir, "config.json"))
	if err != nil {
		ln.Close()
		return fmt.Errorf("cannot read settings: %v", err)
	}
	s := cfg.settings()

	app := &App{
		cfg: cfg, dataDir: o.DataDir, started: time.Now(), hub: newHub(),
		pinger: pinger, pingerErr: pingerErr, allowScripts: envBool("OMNIPING_ALLOW_SCRIPTS"),
		shell: sh, opts: o,
		updateNow: make(chan struct{}, 1),
	}
	if app.auth, err = loadAuth(o.DataDir); err != nil {
		ln.Close()
		return fmt.Errorf("cannot read the password: %v", err)
	}
	if app.auth.enabled() {
		log.Printf("a password is set: signing in is needed")
	}
	app.store, err = history.Open(filepath.Join(o.DataDir, "history"), s.MemorySamples)
	if err != nil {
		ln.Close()
		return fmt.Errorf("cannot open history: %v", err)
	}
	for _, t := range cfg.targets() {
		app.store.Ensure(t.ID)
	}
	app.notifier = newNotifier(app)
	app.alerts = newAlerts(app, filepath.Join(o.DataDir, "events.jsonl"))
	app.engine = newEngine(app)
	app.disc = &Discovery{app: app}

	done := make(chan struct{})
	go cfg.saveLoop(done)
	go app.hub.run(ctx)
	go app.housekeeping(ctx)
	go app.updateLoop(ctx)
	app.engine.Start(ctx)

	var static http.Handler
	if o.WebDir != "" {
		static = http.FileServer(http.Dir(o.WebDir))
	} else {
		sub, _ := fs.Sub(webFiles, "web")
		static = http.FileServer(http.FS(sub))
	}
	srv := &http.Server{
		Handler:           app.handler(noCache(static)),
		ReadHeaderTimeout: 10 * time.Second,
		// Requests end with ctx, so open live updates don't hold up the
		// shutdown.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	app.web = newPageServer(srv)
	app.web.movedFrom = movedFrom
	log.Printf("OmniPing %s: open http://%s in a browser (pinging with %s, data in %s)", version, displayAddr(addr), modeOf(app), o.DataDir)
	app.web.start(ln, o.Host, port)
	url := app.web.localURL()
	if app.desktop() {
		app.announce()
		go app.statusLoop(ctx)
		// After a restart the copy before has already said so.
		if movedFrom != 0 && !keptAddress {
			body := fmt.Sprintf("Another program was already using port %d. OmniPing now uses %d; you can change this in Settings.", movedFrom, port)
			if !keepPort {
				body = fmt.Sprintf("Ports %d to %d are in use or reserved by Windows. OmniPing uses %d for now and tries %d again at the next start; you can choose another port in Settings.", movedFrom, min(movedFrom+portTries, 65535), port, movedFrom)
			}
			sh.Notify(fmt.Sprintf("OmniPing moved to port %d", port), body, "#/settings/computer", false)
		}
	}
	if ready != nil {
		ready(url)
	}

	var stopErr error
	select {
	case <-ctx.Done():
	case err := <-app.web.errs:
		log.Printf("web server stopped: %v", err)
		stopErr = fmt.Errorf("the web server stopped: %v", err)
	}
	// Windows gives a few seconds at sign-out, so this has to be quick.
	log.Print("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	srv.Shutdown(sctx)
	cancel()
	srv.Close()
	close(done)
	if err := app.store.Flush(true); err != nil {
		log.Printf("saving history: %v", err)
	}
	app.alerts.persist()
	if err := cfg.save(); err != nil {
		log.Printf("saving settings: %v", err)
	}
	if app.desktop() {
		removeInstance(o.DataDir)
	}
	return stopErr
}

// localHost is the address a browser on this computer uses for host.
func localHost(host string) string {
	if host == "" || host == "0.0.0.0" || host == "::" {
		return "127.0.0.1"
	}
	return host
}

func modeOf(a *App) string {
	if a.pinger == nil {
		return "nothing (see the error above)"
	}
	return a.pinger.Mode()
}

func displayAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host, _ = os.Hostname()
		if host == "" {
			host = "localhost"
		}
	}
	return net.JoinHostPort(host, port)
}

// healthcheck returns 0 when the OmniPing listening on addr answers.
func healthcheck(addr string) int {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		log.Print(err)
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	c := http.Client{Timeout: 4 * time.Second}
	resp, err := c.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		log.Print(err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("health check: %s", resp.Status)
		return 1
	}
	return 0
}

// noCache makes browsers check for a newer page after an update.
func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		switch {
		case strings.HasSuffix(r.URL.Path, ".js"):
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		case strings.HasSuffix(r.URL.Path, ".woff2"):
			w.Header().Set("Content-Type", "font/woff2")
		case strings.HasSuffix(r.URL.Path, ".webmanifest"):
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		h.ServeHTTP(w, r)
	})
}

// waitForClock holds off on a Raspberry Pi that has just booted and has no
// real-time clock: samples stamped 1970 would end up in the wrong place.
// It returns false when ctx ends first.
func waitForClock(ctx context.Context) bool {
	if time.Now().Year() >= 2024 {
		return true
	}
	log.Print("system clock looks wrong; waiting up to 5 minutes for it to be set")
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Year() < 2024 && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
	return true
}

// housekeeping writes history to disk and removes old files.
func (a *App) housekeeping(ctx context.Context) {
	s := a.cfg.settings()
	a.store.Cleanup(s.RawDays, s.SummaryDays)
	a.diskBytes.Store(a.store.DiskUsage())
	lastFlush, lastClean, lastDisk, lastWeekly := time.Now(), time.Now(), time.Now(), time.Time{}
	var weekly atomic.Bool
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		s := a.cfg.settings()
		if time.Since(lastFlush) >= time.Duration(s.FlushSec)*time.Second {
			lastFlush = time.Now()
			if err := a.store.Flush(false); err != nil {
				log.Printf("writing history: %v", err)
			}
		}
		if time.Since(lastClean) >= time.Hour {
			lastClean = time.Now()
			a.store.Cleanup(s.RawDays, s.SummaryDays)
		}
		if time.Since(lastDisk) >= time.Minute {
			lastDisk = time.Now()
			a.diskBytes.Store(a.store.DiskUsage())
			a.hub.Broadcast("info", a.info())
		}
		// Once a minute: is the weekly summary due? Working it out reads a
		// week of history, so it doesn't hold up the rest.
		if time.Since(lastWeekly) >= time.Minute && weekly.CompareAndSwap(false, true) {
			lastWeekly = time.Now()
			go func() {
				defer weekly.Store(false)
				a.weeklyCheck(ctx, time.Now())
			}()
		}
	}
}
