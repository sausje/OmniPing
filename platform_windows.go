package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

func init() {
	// The tray's window and its message loop must stay on one thread, the
	// main one.
	runtime.LockOSThread()
}

// platformMain on Windows runs OmniPing next to the clock, or with --console
// as a plain server in the terminal, the way it runs on Linux (init in
// console_windows.go has attached the terminal by then).
func platformMain(cli cliOptions) int {
	if cli.Console {
		return runClassic(cli)
	}
	return runTray(cli)
}

// runTray is the Windows app: no console window, an icon next to the clock,
// files in %LOCALAPPDATA%\OmniPing (or next to the program when portable),
// and one OmniPing per data folder.
func runTray(cli cliOptions) int {
	// A plain go build has no manifest; ask for sharp icons anyway.
	if procSetProcessDpiAwarenessContext.Find() == nil {
		procSetProcessDpiAwarenessContext.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
	}
	exe, err := os.Executable()
	if err != nil {
		errorBox("OmniPing cannot find out where it is installed.\n\n" + sentence(err))
		return 1
	}
	exe = filepath.Clean(exe)
	localAppData, err := localAppDataDir()
	if err != nil {
		errorBox(sentence(err))
		return 1
	}
	appDir, portable := appFolder(exe, localAppData)
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		errorBox(fmt.Sprintf("OmniPing cannot create its folder %s.\n\n%s", appDir, sentence(err)))
		return 1
	}

	logPath := filepath.Join(appDir, "omniping.log")
	if lf, err := openLog(logPath, logMaxBytes); err == nil {
		log.SetOutput(lf)
		defer lf.Close()
	}
	if f, err := openCrashLog(filepath.Join(appDir, "crash.log")); err != nil {
		log.Printf("cannot open crash.log: %v", err)
	} else {
		defer closeCrashLog(f)
	}

	appFile := filepath.Join(appDir, "app.json")
	s, err := loadAppSettings(appFile)
	if err != nil {
		log.Printf("%s is damaged, so OmniPing uses the default settings: %v", appFile, err)
	}
	dataDir, err := dataFolder(appDir, portable, s, cli)
	if err != nil {
		log.Print(err)
		errorBox(fmt.Sprintf("OmniPing cannot use the data folder %s.\n\n%s", cli.DataDir, sentence(err)))
		return 1
	}
	log.Printf("OmniPing %s starting (app folder %s, data folder %s)", version, appDir, dataDir)

	// After a restart the old copy may still be saving; give it time.
	wait := time.Duration(0)
	if cli.Restarted {
		wait = 15 * time.Second
	}
	unlock, ok, err := lockDataDir(dataDir, wait)
	if err != nil {
		log.Printf("cannot check for another OmniPing: %v", err)
		unlock = func() {}
	} else if !ok {
		return showRunning(dataDir, cli)
	}
	defer func() { unlock() }()

	canMove := !portable && !cli.DataSet
	if s.MoveTo != "" && canMove {
		to := s.MoveTo
		newDir, err := moveDataFolder(appFile, appDir, dataDir, &s)
		switch {
		case err != nil:
			log.Printf("moving the data to %s failed: %v", to, err)
			errorBox(fmt.Sprintf("OmniPing could not move its data to %s.\n\n%s\n\nIt keeps using %s.", to, sentence(err), dataDir))
		case !samePath(newDir, dataDir):
			log.Printf("moved the data from %s to %s", dataDir, newDir)
			newUnlock, ok, err := lockDataDir(newDir, 0)
			if err == nil && !ok {
				return showRunning(newDir, cli)
			}
			if err == nil {
				unlock()
				unlock = newUnlock
			}
			dataDir = newDir
		}
	}
	// Held until OmniPing exits; the installer looks for it.
	createMutex(runningMutex)

	o := serverOptions{
		DataDir: dataDir, Port: s.Port, PortFallback: true, WebDir: cli.WebDir,
		AppFile: appFile, LogFile: logPath, Portable: portable, ProgramDir: filepath.Dir(exe),
	}
	if !s.AllDevices {
		o.Host = "127.0.0.1"
	}
	if cli.AddrSet {
		host, port, err := splitAddr(cli.Addr)
		if err != nil {
			errorBox(fmt.Sprintf("OmniPing cannot listen on %s.\n\n%s", cli.Addr, sentence(err)))
			return 1
		}
		o.Host, o.Port, o.PortFallback = host, port, false
	} else if cli.Restarted {
		o.TryPort = cli.RestartPort
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t := newTray(exe, dataDir, logPath)
	t.stop, t.autostarted = cancel, cli.Autostart
	if canMove {
		o.Restart = func() error {
			err := t.Restart()
			if err != nil {
				log.Printf("cannot restart: %v", err)
				go errorBox("OmniPing could not restart.\n\n" + sentence(err) + "\n\nThe data folder moves the next time OmniPing starts.")
			}
			return err
		}
	}
	if err := t.create(); err != nil {
		log.Print(err)
		errorBox("OmniPing could not start.\n\n" + sentence(err))
		return 1
	}

	var started atomic.Bool
	ready := func(url string) {
		started.Store(true)
		t.serverStarted(url)
		if !s.FirstRunDone {
			t.Notify("OmniPing is running", "It keeps pinging in the background. You'll find it next to the clock; right-click it for settings.", "", false)
			markFirstRunDone(appFile)
		}
		// After a restart (the data folder moved) the page that asked for it
		// reloads itself, so don't open a second one.
		if s.OpenOnStart && !cli.Autostart && !cli.Restarted {
			t.openInBackground(url)
		}
	}
	go func() {
		err := runServer(ctx, o, t, ready)
		other := ""
		// Another OmniPing has the port: runServer says so when it may move
		// to another port; with a fixed port (-addr) the listen just fails.
		if err != nil && !started.Load() && (errors.Is(err, errAlreadyRunning) || !o.PortFallback) {
			other = otherOmniPing(o)
		}
		t.serverExited(err, other)
	}()
	code := t.run()
	log.Print("OmniPing stopped")
	return code
}

// showRunning handles a second start for the same data folder: it opens the
// page of the OmniPing that is already running.
func showRunning(dataDir string, cli cliOptions) int {
	log.Printf("OmniPing is already running with the data folder %s", dataDir)
	if cli.Autostart {
		return 0
	}
	if in, err := readInstance(dataDir); err == nil && in.URL != "" && isOurOmniPing(in.URL) {
		if err := shellOpen(in.URL); err == nil {
			return 0
		}
	}
	messageBox("OmniPing is already running.", mbIconInformation)
	return 0
}

// otherOmniPing returns the page of another OmniPing of this Windows user
// (one with a different data folder) that has o's port, or "". Another
// user's OmniPing, an older version or one in Docker counts as another
// program: the app moves to the next free port.
func otherOmniPing(o serverOptions) string {
	u := "http://" + net.JoinHostPort(localHost(o.Host), strconv.Itoa(o.Port)) + "/"
	if isOurOmniPing(u) {
		return u
	}
	return ""
}

// markFirstRunDone notes in app.json that the welcome notification was
// shown, without overwriting what the server may be saving at the same time.
func markFirstRunDone(appFile string) {
	if err := updateAppSettings(appFile, func(s *AppSettings) { s.FirstRunDone = true }); err != nil {
		log.Printf("saving %s: %v", appFile, err)
	}
}

func splitAddr(addr string) (host string, port int, err error) {
	host, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err = net.LookupPort("tcp", p)
	return host, port, err
}
