package main

import (
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// The Win32 calls the Windows app needs. Like the ICMP code they are loaded
// with syscall.NewLazyDLL, so OmniPing needs no extra modules.

var (
	modUser32   = syscall.NewLazyDLL("user32.dll")
	modShell32  = syscall.NewLazyDLL("shell32.dll")
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")
	modAdvapi32 = syscall.NewLazyDLL("advapi32.dll")
	modOle32    = syscall.NewLazyDLL("ole32.dll")

	procRegisterClassExW              = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW               = modUser32.NewProc("CreateWindowExW")
	procDestroyWindow                 = modUser32.NewProc("DestroyWindow")
	procDefWindowProcW                = modUser32.NewProc("DefWindowProcW")
	procGetMessageW                   = modUser32.NewProc("GetMessageW")
	procTranslateMessage              = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW              = modUser32.NewProc("DispatchMessageW")
	procPostMessageW                  = modUser32.NewProc("PostMessageW")
	procPostQuitMessage               = modUser32.NewProc("PostQuitMessage")
	procRegisterWindowMessageW        = modUser32.NewProc("RegisterWindowMessageW")
	procChangeWindowMessageFilterEx   = modUser32.NewProc("ChangeWindowMessageFilterEx")
	procSetForegroundWindow           = modUser32.NewProc("SetForegroundWindow")
	procCreatePopupMenu               = modUser32.NewProc("CreatePopupMenu")
	procAppendMenuW                   = modUser32.NewProc("AppendMenuW")
	procSetMenuDefaultItem            = modUser32.NewProc("SetMenuDefaultItem")
	procTrackPopupMenuEx              = modUser32.NewProc("TrackPopupMenuEx")
	procDestroyMenu                   = modUser32.NewProc("DestroyMenu")
	procSetTimer                      = modUser32.NewProc("SetTimer")
	procKillTimer                     = modUser32.NewProc("KillTimer")
	procMessageBoxW                   = modUser32.NewProc("MessageBoxW")
	procCreateIconFromResourceEx      = modUser32.NewProc("CreateIconFromResourceEx")
	procDestroyIcon                   = modUser32.NewProc("DestroyIcon")
	procGetSystemMetrics              = modUser32.NewProc("GetSystemMetrics")
	procGetSystemMetricsForDpi        = modUser32.NewProc("GetSystemMetricsForDpi")
	procGetDpiForWindow               = modUser32.NewProc("GetDpiForWindow")
	procSetProcessDpiAwarenessContext = modUser32.NewProc("SetProcessDpiAwarenessContext")

	procShellNotifyIconW     = modShell32.NewProc("Shell_NotifyIconW")
	procShellExecuteW        = modShell32.NewProc("ShellExecuteW")
	procSHGetKnownFolderPath = modShell32.NewProc("SHGetKnownFolderPath")

	procGetModuleHandleW = modKernel32.NewProc("GetModuleHandleW")
	procCreateMutexW     = modKernel32.NewProc("CreateMutexW")
	procAttachConsole    = modKernel32.NewProc("AttachConsole")
	procAllocConsole     = modKernel32.NewProc("AllocConsole")

	procRegOpenKeyExW    = modAdvapi32.NewProc("RegOpenKeyExW")
	procRegCreateKeyExW  = modAdvapi32.NewProc("RegCreateKeyExW")
	procRegQueryValueExW = modAdvapi32.NewProc("RegQueryValueExW")
	procRegSetValueExW   = modAdvapi32.NewProc("RegSetValueExW")
	procRegDeleteValueW  = modAdvapi32.NewProc("RegDeleteValueW")
	procRegCloseKey      = modAdvapi32.NewProc("RegCloseKey")

	procCoInitializeEx = modOle32.NewProc("CoInitializeEx")
	procCoUninitialize = modOle32.NewProc("CoUninitialize")
	procCoTaskMemFree  = modOle32.NewProc("CoTaskMemFree")
)

const (
	wmNull              = 0x0000
	wmDestroy           = 0x0002
	wmClose             = 0x0010
	wmQueryEndSession   = 0x0011
	wmEndSession        = 0x0016
	wmContextMenu       = 0x007B
	wmTimer             = 0x0113
	wmLButtonDblClk     = 0x0203
	wmUser              = 0x0400
	wmApp               = 0x8000
	ninSelect           = wmUser + 0
	ninKeySelect        = wmUser + 1
	ninBalloonShow      = wmUser + 2
	ninBalloonHide      = wmUser + 3
	ninBalloonTimeout   = wmUser + 4
	ninBalloonUserClick = wmUser + 5

	nimAdd        = 0
	nimModify     = 1
	nimDelete     = 2
	nimSetVersion = 4

	nifMessage         = 0x01
	nifIcon            = 0x02
	nifTip             = 0x04
	nifInfo            = 0x10
	nifShowTip         = 0x80
	niifUser           = 0x04
	niifLargeIcon      = 0x20
	notifyIconVersion4 = 4

	mfString    = 0x0000
	mfGrayed    = 0x0001
	mfChecked   = 0x0008
	mfSeparator = 0x0800

	tpmRightButton = 0x0002
	tpmBottomAlign = 0x0020
	tpmNoNotify    = 0x0080
	tpmReturnCmd   = 0x0100

	mbIconError       = 0x00000010
	mbIconInformation = 0x00000040
	mbSetForeground   = 0x00010000
	mbTopmost         = 0x00040000

	wsPopup            = 0x80000000
	wsExToolWindow     = 0x00000080
	smCxIcon           = 11
	smCxSmIcon         = 49
	msgfltAllow        = 1
	swShowNormal       = 1
	errorAlreadyExists = 183

	coinitApartmentThreaded = 0x2
	coinitDisableOLE1DDE    = 0x4
)

// notifyIconData is NOTIFYICONDATAW, all of it (Vista and later), so the
// balloon can have its own icon. 976 bytes on 64-bit Windows.
type notifyIconData struct {
	Size            uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32 // also uTimeout
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUIDItem        [16]byte
	BalloonIcon     uintptr
}

// wndClassEx is WNDCLASSEXW.
type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

// winMsg is MSG, with room for the private field newer headers add.
type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	X, Y    int32
	_       uint32
}

// wstr makes a string ready for a W function.
func wstr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(strings.ReplaceAll(s, "\x00", ""))
	return p
}

// putUTF16 copies s into a fixed text field of a Win32 struct, ending it
// with a zero. Text that doesn't fit is cut short with "…".
func putUTF16(dst []uint16, s string) {
	u := utf16.Encode([]rune(strings.ReplaceAll(s, "\x00", "")))
	if len(u) > len(dst)-1 {
		u = u[:len(dst)-2]
		if n := len(u); n > 0 && u[n-1] >= 0xD800 && u[n-1] < 0xDC00 {
			u = u[:n-1] // don't split a character in two
		}
		u = append(u, '…')
	}
	n := copy(dst, u)
	clear(dst[n:])
}

// utf16PtrToString reads a zero-terminated UTF-16 string.
func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	n := 0
	for ptr := unsafe.Pointer(p); *(*uint16)(ptr) != 0; n++ {
		ptr = unsafe.Add(ptr, 2)
	}
	return syscall.UTF16ToString(unsafe.Slice(p, n))
}

// messageBox shows a message and waits until it is closed. It works on any
// thread and needs no window.
func messageBox(text string, flags uint32) {
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(wstr(text))), uintptr(unsafe.Pointer(wstr("OmniPing"))),
		uintptr(flags|mbSetForeground|mbTopmost))
}

func errorBox(text string) { messageBox(text, mbIconError) }
