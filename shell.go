package main

// The app shell is what the operating system adds around the server: on
// Windows, the icon next to the clock with its menu and notifications.
// Elsewhere there is no shell and OmniPing runs as a plain server, as it
// always has.

// Shell is implemented by the Windows tray (tray_windows.go) and by noShell.
// All methods may be called from any goroutine.
type Shell interface {
	// Desktop reports whether there is a shell at all. Without one the page
	// hides the "This computer" settings and the Windows notification channel.
	Desktop() bool
	// Attach gives the shell what it needs from the running server. runServer
	// calls it once the server is up; the menu works only after that.
	Attach(c ShellControls)
	// SetStatus updates the tray icon, its tooltip and the menu's status line.
	SetStatus(st ShellStatus)
	// Notify shows a desktop notification from the tray icon. alert shows the
	// red icon. Clicking the notification opens OmniPing at path, a page
	// address relative to the server such as "#/alerts" ("" for the start page).
	Notify(title, body, path string, alert bool)
	// Autostart reports whether OmniPing starts when the user signs in.
	// supported is false where the shell can't do that.
	Autostart() (on, supported bool)
	SetAutostart(on bool) error
	// Open opens a web address in the default browser, or a file or folder.
	Open(target string) error
	// Restart starts a new copy of OmniPing, which waits until this one has
	// exited (after the data folder moved). The caller then shuts down.
	Restart() error
}

// ShellControls is what the shell's menu can do with the running server.
type ShellControls struct {
	// URL is the page's address on this computer, like http://127.0.0.1:8080/.
	URL string
	// DataDir is the absolute data folder.
	DataDir string
	// SetPaused stops or restarts pinging, like the Stop button.
	SetPaused func(paused bool) error
}

// ShellStatus is the summary shown on the tray icon and in its menu.
type ShellStatus struct {
	Targets int  // targets being watched
	Alerts  int  // alerts firing now
	Paused  bool // pinging is stopped
}

// noShell is used when OmniPing runs as a plain server.
type noShell struct{}

func (noShell) Desktop() bool                 { return false }
func (noShell) Attach(ShellControls)          {}
func (noShell) SetStatus(ShellStatus)         {}
func (noShell) Notify(_, _, _ string, _ bool) {}
func (noShell) Autostart() (bool, bool)       { return false, false }
func (noShell) SetAutostart(bool) error       { return errNoShell }
func (noShell) Open(string) error             { return errNoShell }
func (noShell) Restart() error                { return errNoShell }

type shellError string

func (e shellError) Error() string { return string(e) }

// errNoShell is returned by noShell for things only the desktop app can do.
const errNoShell = shellError("only the Windows app can do this")
