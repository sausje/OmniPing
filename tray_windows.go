package main

import (
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

// The icon next to the clock, its menu and its notifications. The window
// and everything that touches it live on the main thread (see init in
// platform_windows.go); the Shell methods may be called from any goroutine,
// so they only store what changed and wake that thread up.

//go:embed winres/tray/normal.ico
var icoNormal []byte

//go:embed winres/tray/alert.ico
var icoAlert []byte

//go:embed winres/tray/paused.ico
var icoPaused []byte

const (
	trayIconID = 1

	wmTrayIcon   = wmApp + 1 // the icon's mouse and balloon events
	wmTrayWake   = wmApp + 2 // something to show changed
	wmServerDone = wmApp + 3 // runServer returned
	wmTrayExit   = wmApp + 4 // close the window and leave the message loop

	timerAddIcon = 1 // retry adding the icon (no taskbar yet)
	timerBalloon = 2 // move on when a notification never reports back
	timerQuit    = 3 // stop waiting for the server
)

// Menu items.
const (
	idOpen = 1 + iota
	idStatus
	idPause
	idAutostart
	idSettings
	idDataFolder
	idLog
	idVersion
	idQuit
)

const (
	// At most this many notifications wait; more are summed up in one.
	maxWaitingNotices = 3
	// A notification that doesn't report being closed is given up after
	// this long, so the next one can show.
	balloonTimeout = 10 * time.Second
)

type notice struct {
	title, body, path string
	alert             bool
}

type tray struct {
	exe         string // this program, for Start with Windows
	dataDir     string // known before the server is up
	logFile     string
	autostarted bool   // started by Windows at sign-in: open no pages unasked
	stop        func() // stops the server

	hwnd        uintptr
	taskbarMsg  uint32
	wakePending atomic.Bool
	serverDone  chan struct{} // closed when runServer returned

	mu        sync.Mutex
	controls  ShellControls
	status    ShellStatus
	hasStatus bool
	waiting   []notice
	more      int  // notifications left out of waiting
	moreAlert bool // one of them was an alert
	ready     bool // the server is up
	serverErr error
	other     string // page of another OmniPing that has the port

	// Main thread only.
	added     bool
	icons     [3]uintptr // tray icons: normal, alert, paused
	bigIcons  [2]uintptr // balloon icons: normal, alert
	shownIcon int
	shownTip  string
	showing   bool
	last      notice // the notification on screen, for clicks
	lastOpen  time.Time
	quitting  bool
	exitCode  int
}

// theTray is the one tray; the window procedure finds it here.
var theTray *tray

func newTray(exe, dataDir, logFile string) *tray {
	return &tray{exe: exe, dataDir: dataDir, logFile: logFile, serverDone: make(chan struct{}), shownIcon: -1, stop: func() {}}
}

// create makes the hidden window that receives the icon's events and adds
// the icon. It is a real top-level window, not a message-only one, so that
// it hears when Explorer restarts and when Windows shuts down.
func (t *tray) create() error {
	theTray = t
	inst, _, _ := procGetModuleHandleW.Call(0)
	class := wstr("OmniPingTray")
	wc := wndClassEx{WndProc: syscall.NewCallback(trayWndProc), Instance: inst, ClassName: class}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("cannot register the window class: %v", err)
	}
	hwnd, _, err := procCreateWindowExW.Call(wsExToolWindow, uintptr(unsafe.Pointer(class)),
		uintptr(unsafe.Pointer(wstr("OmniPing"))), wsPopup, 0, 0, 0, 0, 0, 0, inst, 0)
	if hwnd == 0 {
		return fmt.Errorf("cannot create the window: %v", err)
	}
	t.hwnd = hwnd
	r, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(wstr("TaskbarCreated"))))
	t.taskbarMsg = uint32(r)
	// Let the message through even when OmniPing runs as administrator.
	procChangeWindowMessageFilterEx.Call(hwnd, r, msgfltAllow, 0)
	t.loadIcons()
	t.addIcon()
	return nil
}

// run handles window messages until the tray closes, and returns the exit
// code.
func (t *tray) run() int {
	var m winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	t.freeIcons()
	return t.exitCode
}

func trayWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	if t := theTray; t != nil && t.hwnd == hwnd {
		if r, ok := t.handle(uint32(msg), wparam, lparam); ok {
			return r
		}
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

func (t *tray) handle(msg uint32, wparam, lparam uintptr) (uintptr, bool) {
	switch msg {
	case wmTrayIcon:
		t.iconEvent(uint32(lparam&0xFFFF), int32(int16(wparam&0xFFFF)), int32(int16(wparam>>16&0xFFFF)))
	case wmTrayWake:
		t.wakePending.Store(false)
		t.refresh()
	case wmServerDone:
		t.serverStopped()
	case wmTimer:
		t.timer(wparam)
	case wmClose:
		t.startQuit()
	case wmQueryEndSession:
		return 1, true
	case wmEndSession:
		if wparam != 0 {
			t.endSession()
		}
		return 0, true
	case wmTrayExit:
		procDestroyWindow.Call(t.hwnd)
	case wmDestroy:
		t.removeIcon()
		procPostQuitMessage.Call(0)
	default:
		if t.taskbarMsg != 0 && msg == t.taskbarMsg {
			// Explorer restarted (or the screen's scaling changed): the icon
			// is gone and must be added again.
			t.added = false
			t.showing = false
			t.loadIcons()
			t.addIcon()
			return 0, true
		}
		return 0, false
	}
	return 0, true
}

func (t *tray) post(msg uint32) {
	procPostMessageW.Call(t.hwnd, uintptr(msg), 0, 0)
}

// wake asks the main thread to show what changed.
func (t *tray) wake() {
	if t.hwnd != 0 && t.wakePending.CompareAndSwap(false, true) {
		t.post(wmTrayWake)
	}
}

// Shell, for the server.

func (t *tray) Desktop() bool { return true }

func (t *tray) Attach(c ShellControls) {
	t.mu.Lock()
	t.controls = c
	t.mu.Unlock()
	t.wake()
}

func (t *tray) SetStatus(st ShellStatus) {
	t.mu.Lock()
	t.status, t.hasStatus = st, true
	t.mu.Unlock()
	t.wake()
}

func (t *tray) Notify(title, body, path string, alert bool) {
	t.mu.Lock()
	if len(t.waiting) < maxWaitingNotices {
		t.waiting = append(t.waiting, notice{title, body, path, alert})
	} else {
		t.more++
		t.moreAlert = t.moreAlert || alert
	}
	t.mu.Unlock()
	t.wake()
}

func (t *tray) Autostart() (on, supported bool) { return windowsAutostart.on(t.exe), true }

func (t *tray) SetAutostart(on bool) error { return windowsAutostart.set(on, t.exe) }

func (t *tray) Open(target string) error { return shellOpen(target) }

// Restart starts a new copy of OmniPing with the same options and quits this
// one. The new copy waits until this one is gone (--restarted).
func (t *tray) Restart() error {
	// The page that waits for the restart polls the current address, so the
	// new copy tries this port first.
	t.mu.Lock()
	port := 0
	if u, err := url.Parse(t.controls.URL); err == nil {
		port, _ = strconv.Atoi(u.Port())
	}
	t.mu.Unlock()
	cmd := exec.Command(t.exe, restartArgs(os.Args[1:], port)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008} // DETACHED_PROCESS
	if err := cmd.Start(); err != nil {
		return err
	}
	cmd.Process.Release()
	t.post(wmClose)
	return nil
}

// restartArgs are the options for the new copy: the same, but not
// --autostart (that would keep the page closed), and with --restarted and
// the port in use now (0 for none).
func restartArgs(args []string, port int) []string {
	var out []string
	restarted := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, _, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		switch {
		case strings.HasPrefix(a, "-") && name == "autostart":
			continue
		case strings.HasPrefix(a, "-") && name == "restart-port":
			if !hasValue {
				i++ // the value is the next argument
			}
			continue
		case strings.HasPrefix(a, "-") && name == "restarted":
			restarted = true
		}
		out = append(out, a)
	}
	if !restarted {
		out = append(out, "--restarted")
	}
	if port != 0 {
		out = append(out, "--restart-port="+strconv.Itoa(port))
	}
	return out
}

// serverStarted is called when the page can be opened. Until the server
// attaches, the menu uses this address.
func (t *tray) serverStarted(url string) {
	t.mu.Lock()
	t.ready = true
	if t.controls.URL == "" {
		t.controls.URL = url
	}
	t.mu.Unlock()
	t.wake()
}

// serverExited is called with what runServer returned.
func (t *tray) serverExited(err error, other string) {
	t.mu.Lock()
	t.serverErr, t.other = err, other
	t.mu.Unlock()
	close(t.serverDone)
	t.post(wmServerDone)
}

// Shutting down.

// startQuit removes the icon at once, stops the server and closes the tray
// when the server is done (or after 5 seconds).
func (t *tray) startQuit() {
	if t.quitting {
		return
	}
	t.quitting = true
	t.removeIcon()
	log.Print("quitting")
	t.stop()
	procSetTimer.Call(t.hwnd, timerQuit, 5000, 0)
}

// endSession runs when Windows signs out or shuts down. Windows may end the
// process as soon as this returns, so the server must be done by then.
func (t *tray) endSession() {
	log.Print("Windows is shutting down")
	t.quitting = true
	t.stop()
	select {
	case <-t.serverDone:
	case <-time.After(5 * time.Second):
		log.Print("OmniPing did not stop within 5 seconds")
	}
	t.removeIcon()
	t.post(wmTrayExit)
}

func (t *tray) serverStopped() {
	t.mu.Lock()
	err, ready, other := t.serverErr, t.ready, t.other
	t.mu.Unlock()
	if !t.quitting {
		t.quitting = true
		t.removeIcon()
		switch {
		case other != "" && t.autostarted:
			log.Printf("another OmniPing is running at %s", other)
		case other != "":
			log.Printf("another OmniPing is running at %s; opening that one", other)
			if err := shellOpen(other); err != nil {
				log.Printf("cannot open %s: %v", other, err)
			}
		case err != nil && !ready:
			log.Print(err)
			t.exitCode = 1
			errorBox("OmniPing could not start.\n\n" + sentence(err))
		case err != nil:
			log.Print(err)
			t.exitCode = 1
			errorBox("OmniPing stopped.\n\n" + sentence(err))
		default:
			log.Print("the server stopped")
		}
	}
	procKillTimer.Call(t.hwnd, timerQuit)
	procDestroyWindow.Call(t.hwnd)
}

// sentence turns an error into a sentence for a message box: a capital
// first and a full stop at the end.
func sentence(err error) string {
	s := strings.TrimSpace(err.Error())
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	s = string(unicode.ToUpper(r)) + s[n:]
	if !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "!") && !strings.HasSuffix(s, "?") {
		s += "."
	}
	return s
}

func (t *tray) timer(id uintptr) {
	switch id {
	case timerAddIcon:
		t.addIcon()
	case timerBalloon:
		procKillTimer.Call(t.hwnd, timerBalloon)
		t.showing = false
		t.refresh()
	case timerQuit:
		procKillTimer.Call(t.hwnd, timerQuit)
		log.Print("OmniPing did not stop within 5 seconds; quitting anyway")
		procDestroyWindow.Call(t.hwnd)
	}
}

// The icon.

func (t *tray) nid(flags uint32) notifyIconData {
	d := notifyIconData{Wnd: t.hwnd, ID: trayIconID, Flags: flags, CallbackMessage: wmTrayIcon}
	d.Size = uint32(unsafe.Sizeof(d))
	return d
}

func (t *tray) shellNotifyIcon(op uintptr, d *notifyIconData) bool {
	r, _, _ := procShellNotifyIconW.Call(op, uintptr(unsafe.Pointer(d)))
	return r != 0
}

// addIcon puts the icon next to the clock. At sign-in the taskbar may not be
// there yet; then it tries again every 2 seconds (and when Explorer says the
// taskbar was created).
func (t *tray) addIcon() {
	if t.added || t.quitting {
		return
	}
	icon, tip := t.look()
	d := t.nid(nifMessage | nifIcon | nifTip | nifShowTip)
	d.Icon = t.icons[icon]
	putUTF16(d.Tip[:], tip)
	if !t.shellNotifyIcon(nimAdd, &d) && !t.shellNotifyIcon(nimModify, &d) {
		procSetTimer.Call(t.hwnd, timerAddIcon, 2000, 0)
		return
	}
	procKillTimer.Call(t.hwnd, timerAddIcon)
	v := t.nid(0)
	v.Version = notifyIconVersion4
	t.shellNotifyIcon(nimSetVersion, &v)
	t.added, t.shownIcon, t.shownTip = true, icon, tip
	t.refresh()
}

func (t *tray) removeIcon() {
	procKillTimer.Call(t.hwnd, timerAddIcon)
	if !t.added {
		return
	}
	d := t.nid(0)
	t.shellNotifyIcon(nimDelete, &d)
	t.added = false
}

// look returns the icon (0 normal, 1 alert, 2 paused) and tooltip for now.
func (t *tray) look() (int, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return trayLook(t.controls.URL != "", t.hasStatus, t.status)
}

func trayLook(started, hasStatus bool, st ShellStatus) (icon int, tip string) {
	switch {
	case !started:
		return 0, "OmniPing – starting…"
	case !hasStatus:
		return 0, "OmniPing"
	case st.Paused:
		return 2, "OmniPing – paused"
	case st.Alerts > 0:
		return 1, "OmniPing – " + statusLine(st)
	case st.Targets == 0:
		return 0, "OmniPing – no targets yet"
	}
	return 0, "OmniPing – " + statusLine(st)
}

// statusLine is the menu's status line, like "11 targets · all clear".
func statusLine(st ShellStatus) string {
	switch {
	case st.Paused:
		return "Paused"
	case st.Targets == 0 && st.Alerts == 0:
		return "No targets yet"
	case st.Alerts == 0:
		return plural(st.Targets, "target") + " · all clear"
	}
	return plural(st.Targets, "target") + " · " + plural(st.Alerts, "alert")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// refresh brings the icon and tooltip up to date and shows the next
// notification.
func (t *tray) refresh() {
	if !t.added {
		return
	}
	if icon, tip := t.look(); icon != t.shownIcon || tip != t.shownTip {
		d := t.nid(nifIcon | nifTip | nifShowTip)
		d.Icon = t.icons[icon]
		putUTF16(d.Tip[:], tip)
		if t.shellNotifyIcon(nimModify, &d) {
			t.shownIcon, t.shownTip = icon, tip
		}
	}
	if !t.showing {
		if n, ok := t.nextNotice(); ok {
			t.showBalloon(n)
		}
	}
}

// nextNotice takes the next notification to show.
func (t *tray) nextNotice() (notice, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.waiting) > 0 {
		n := t.waiting[0]
		t.waiting = t.waiting[1:]
		return n, true
	}
	if t.more > 0 {
		n := notice{
			title: plural(t.more, "more notification"),
			body:  "Open OmniPing to see what happened.",
			path:  "#/alerts",
			alert: t.moreAlert,
		}
		t.more, t.moreAlert = 0, false
		return n, true
	}
	return notice{}, false
}

func (t *tray) showBalloon(n notice) {
	d := t.nid(nifInfo)
	putUTF16(d.InfoTitle[:], n.title)
	putUTF16(d.Info[:], n.body)
	d.InfoFlags = niifUser | niifLargeIcon
	d.BalloonIcon = t.bigIcons[0]
	if n.alert {
		d.BalloonIcon = t.bigIcons[1]
	}
	if !t.shellNotifyIcon(nimModify, &d) {
		log.Printf("could not show the notification %q", n.title)
		return
	}
	t.showing, t.last = true, n
	procSetTimer.Call(t.hwnd, timerBalloon, uintptr(balloonTimeout/time.Millisecond), 0)
}

func (t *tray) iconEvent(event uint32, x, y int32) {
	switch event {
	case ninSelect, ninKeySelect, wmLButtonDblClk:
		// One click can bring several of these; open the page once.
		if time.Since(t.lastOpen) > time.Second {
			t.lastOpen = time.Now()
			t.openPage("")
		}
	case wmContextMenu:
		t.showMenu(x, y)
	case ninBalloonUserClick:
		path := t.last.path
		t.balloonDone()
		t.openPage(path)
	case ninBalloonHide, ninBalloonTimeout:
		t.balloonDone()
	}
}

func (t *tray) balloonDone() {
	procKillTimer.Call(t.hwnd, timerBalloon)
	t.showing = false
	t.refresh()
}

// The menu.

func (t *tray) showMenu(x, y int32) {
	t.mu.Lock()
	c, st, hasStatus := t.controls, t.status, t.hasStatus
	t.mu.Unlock()
	started := c.URL != ""
	status := "Starting…"
	if started {
		status = "Running"
		if hasStatus {
			status = statusLine(st)
		}
	}

	m, _, _ := procCreatePopupMenu.Call()
	if m == 0 {
		return
	}
	defer procDestroyMenu.Call(m)
	item := func(id int, text string, grayed, checked bool) {
		flags := uintptr(mfString)
		if grayed {
			flags |= mfGrayed
		}
		if checked {
			flags |= mfChecked
		}
		procAppendMenuW.Call(m, flags, uintptr(id), uintptr(unsafe.Pointer(wstr(text))))
	}
	separator := func() { procAppendMenuW.Call(m, mfSeparator, 0, 0) }

	item(idOpen, "Open OmniPing", !started, false)
	item(idStatus, status, true, false)
	separator()
	item(idPause, "Pause pinging", c.SetPaused == nil, hasStatus && st.Paused)
	item(idAutostart, "Start with Windows", false, windowsAutostart.on(t.exe))
	item(idSettings, "Settings…", !started, false)
	item(idDataFolder, "Open data folder", false, false)
	item(idLog, "View log", t.logFile == "", false)
	separator()
	item(idVersion, "OmniPing "+version, true, false)
	item(idQuit, "Quit OmniPing", false, false)
	procSetMenuDefaultItem.Call(m, idOpen, 0)

	// Without this the menu stays open when the user clicks elsewhere.
	procSetForegroundWindow.Call(t.hwnd)
	cmd, _, _ := procTrackPopupMenuEx.Call(m, tpmRightButton|tpmBottomAlign|tpmReturnCmd|tpmNoNotify,
		uintptr(x), uintptr(y), t.hwnd, 0)
	procPostMessageW.Call(t.hwnd, wmNull, 0, 0)

	switch cmd {
	case idOpen:
		t.openPage("")
	case idPause:
		paused := !(hasStatus && st.Paused)
		go func() {
			if err := c.SetPaused(paused); err != nil {
				log.Printf("pause: %v", err)
				errorBox("OmniPing could not change this.\n\n" + sentence(err))
				return
			}
			t.mu.Lock()
			t.status.Paused = paused
			t.mu.Unlock()
			t.wake()
		}()
	case idAutostart:
		on := !windowsAutostart.on(t.exe)
		if err := t.SetAutostart(on); err != nil {
			log.Printf("start with Windows: %v", err)
			go errorBox("OmniPing could not change this.\n\n" + sentence(err))
		}
	case idSettings:
		t.openPage("#/settings/computer")
	case idDataFolder:
		dir := c.DataDir
		if dir == "" {
			dir = t.dataDir
		}
		t.openInBackground(dir)
	case idLog:
		t.openInBackground(t.logFile)
	case idQuit:
		t.startQuit()
	}
}

// openPage opens OmniPing's page at path (like "#/alerts") in the browser.
func (t *tray) openPage(path string) {
	t.mu.Lock()
	url := t.controls.URL
	t.mu.Unlock()
	if url != "" {
		t.openInBackground(url + path)
	}
}

// openInBackground opens target without holding up the menu, and says so
// when that fails.
func (t *tray) openInBackground(target string) {
	go func() {
		if err := shellOpen(target); err != nil {
			log.Printf("cannot open %s: %v", target, err)
			errorBox(fmt.Sprintf("OmniPing could not open %s.\n\n%s", target, sentence(err)))
		}
	}()
}

// shellOpen opens a web address, file or folder the way Explorer would.
// ShellExecute wants COM set up on its thread, so it gets a thread of its
// own, which ends with it.
func shellOpen(target string) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // never unlocked: the thread goes when this ends
		procCoInitializeEx.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE)
		defer procCoUninitialize.Call()
		r, _, _ := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(wstr("open"))), uintptr(unsafe.Pointer(wstr(target))),
			0, 0, swShowNormal)
		if r <= 32 {
			done <- shellExecuteError(r)
			return
		}
		done <- nil
	}()
	return <-done
}

func shellExecuteError(r uintptr) error {
	switch r {
	case 2, 3:
		return errors.New("it was not found")
	case 5:
		return errors.New("access was denied")
	case 31:
		return errors.New("no program is set up to open it")
	}
	return fmt.Errorf("Windows error %d", r)
}

// Icons.

// loadIcons makes the icons in the sizes Windows wants now, which depend on
// the screen's scaling.
func (t *tray) loadIcons() {
	small, large := iconSizes(t.hwnd)
	var old []uintptr
	old = append(old, t.icons[:]...)
	old = append(old, t.bigIcons[:]...)
	for i, ico := range [][]byte{icoNormal, icoAlert, icoPaused} {
		h, err := iconFromICO(ico, small)
		if err != nil {
			log.Printf("tray icon: %v", err)
		}
		t.icons[i] = h
	}
	// Windows 10 and 11 draw the notification icon larger than a standard
	// large icon, so give it twice the size to scale down from.
	for i, ico := range [][]byte{icoNormal, icoAlert} {
		h, err := iconFromICO(ico, 2*large)
		if err != nil {
			log.Printf("notification icon: %v", err)
		}
		t.bigIcons[i] = h
	}
	t.shownIcon = -1
	for _, h := range old {
		if h != 0 {
			procDestroyIcon.Call(h)
		}
	}
}

func (t *tray) freeIcons() {
	for _, h := range t.icons {
		if h != 0 {
			procDestroyIcon.Call(h)
		}
	}
	for _, h := range t.bigIcons {
		if h != 0 {
			procDestroyIcon.Call(h)
		}
	}
	t.icons, t.bigIcons = [3]uintptr{}, [2]uintptr{}
}

// iconSizes returns the size of small (tray) and large (notification) icons
// for the screen the window is on.
func iconSizes(hwnd uintptr) (small, large int) {
	dpi := uintptr(96)
	if procGetDpiForWindow.Find() == nil {
		if d, _, _ := procGetDpiForWindow.Call(hwnd); d != 0 {
			dpi = d
		}
	}
	if procGetSystemMetricsForDpi.Find() == nil {
		s, _, _ := procGetSystemMetricsForDpi.Call(smCxSmIcon, dpi)
		l, _, _ := procGetSystemMetricsForDpi.Call(smCxIcon, dpi)
		small, large = int(s), int(l)
	} else {
		s, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
		l, _, _ := procGetSystemMetrics.Call(smCxIcon)
		small, large = int(s), int(l)
	}
	if small <= 0 {
		small = 16
	}
	if large <= 0 {
		large = 32
	}
	return small, large
}

func iconFromICO(ico []byte, size int) (uintptr, error) {
	img, err := icoImage(ico, size)
	if err != nil {
		return 0, err
	}
	h, _, callErr := procCreateIconFromResourceEx.Call(uintptr(unsafe.Pointer(&img[0])), uintptr(len(img)), 1, 0x00030000,
		uintptr(size), uintptr(size), 0)
	if h == 0 {
		return 0, callErr
	}
	return h, nil
}

// icoImage picks the picture for size from an .ico file: that size exactly,
// else the next bigger one (Windows scales it down), else the biggest.
func icoImage(ico []byte, size int) ([]byte, error) {
	if len(ico) < 6 || binary.LittleEndian.Uint16(ico[2:]) != 1 {
		return nil, errors.New("not an .ico file")
	}
	count := int(binary.LittleEndian.Uint16(ico[4:]))
	best, bestW := -1, 0
	for i := range count {
		e := 6 + 16*i
		if e+16 > len(ico) {
			break
		}
		w := int(ico[e])
		if w == 0 {
			w = 256
		}
		better := best < 0 ||
			(w >= size && (bestW < size || w < bestW)) ||
			(w < size && bestW < size && w > bestW)
		if better {
			best, bestW = e, w
		}
	}
	if best < 0 {
		return nil, errors.New("the .ico file has no pictures")
	}
	n := int(binary.LittleEndian.Uint32(ico[best+8:]))
	off := int(binary.LittleEndian.Uint32(ico[best+12:]))
	if n <= 0 || off < 0 || off+n > len(ico) {
		return nil, errors.New("damaged .ico file")
	}
	return ico[off : off+n], nil
}
