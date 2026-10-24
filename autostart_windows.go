package main

import (
	"errors"
	"strings"
	"syscall"
	"unsafe"
)

// Start with Windows: a value in the user's Run key, the same one the
// installer writes. Task Manager and Settings → Apps → Startup can switch it
// off without removing it; they note that under StartupApproved.

type autostartKeys struct {
	run      string // key under HKEY_CURRENT_USER with the command
	approved string // key where Windows notes that the user disabled it
	name     string // value name in both
}

var windowsAutostart = autostartKeys{
	run:      `Software\Microsoft\Windows\CurrentVersion\Run`,
	approved: `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`,
	name:     "OmniPing",
}

// autostartCommand is what Windows runs at sign-in.
func autostartCommand(exe string) string { return `"` + exe + `" --autostart` }

// commandExe returns the program of a command line: the part in quotes, or
// up to the first space.
func commandExe(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if rest, ok := strings.CutPrefix(cmd, `"`); ok {
		exe, _, _ := strings.Cut(rest, `"`)
		return exe
	}
	exe, _, _ := strings.Cut(cmd, " ")
	return exe
}

// approvedEnabled reads a StartupApproved value. Windows writes 02 00 00 00…
// for an enabled entry and 03 00 00 00 plus the time for a disabled one; an
// odd first byte means disabled. No value means enabled.
func approvedEnabled(data []byte) bool {
	return len(data) == 0 || data[0]&1 == 0
}

// on tells whether Windows starts exe at sign-in.
func (k autostartKeys) on(exe string) bool {
	cmd, err := regGetString(k.run, k.name)
	if err != nil || !samePath(commandExe(cmd), exe) {
		return false
	}
	data, _ := regGetBinary(k.approved, k.name)
	return approvedEnabled(data)
}

// set turns starting at sign-in on or off. Turning it on also undoes a
// "disabled" from Task Manager, since the user now asked for it here.
func (k autostartKeys) set(on bool, exe string) error {
	if !on {
		return regDeleteValue(k.run, k.name)
	}
	if err := regSetString(k.run, k.name, autostartCommand(exe)); err != nil {
		return err
	}
	if data, _ := regGetBinary(k.approved, k.name); !approvedEnabled(data) {
		return regDeleteValue(k.approved, k.name)
	}
	return nil
}

// Registry access under HKEY_CURRENT_USER.

const (
	hkeyCurrentUser   = 0x80000001
	keyQueryValue     = 0x0001
	keySetValue       = 0x0002
	regSZ             = 1
	regExpandSZ       = 2
	regBinary         = 3
	errorFileNotFound = syscall.Errno(2)
)

func regOpen(path string, access uint32, create bool) (syscall.Handle, error) {
	var h syscall.Handle
	var r uintptr
	if create {
		r, _, _ = procRegCreateKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(wstr(path))), 0, 0, 0,
			uintptr(access), 0, uintptr(unsafe.Pointer(&h)), 0)
	} else {
		r, _, _ = procRegOpenKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(wstr(path))), 0,
			uintptr(access), uintptr(unsafe.Pointer(&h)))
	}
	if r != 0 {
		return 0, syscall.Errno(r)
	}
	return h, nil
}

func regGet(path, name string) (typ uint32, data []byte, err error) {
	h, err := regOpen(path, keyQueryValue, false)
	if err != nil {
		return 0, nil, err
	}
	defer procRegCloseKey.Call(uintptr(h))
	n := uint32(256)
	for {
		data = make([]byte, n)
		r, _, _ := procRegQueryValueExW.Call(uintptr(h), uintptr(unsafe.Pointer(wstr(name))), 0,
			uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&n)))
		switch syscall.Errno(r) {
		case 0:
			return typ, data[:n], nil
		case syscall.ERROR_MORE_DATA:
			continue // n now holds the size needed
		default:
			return 0, nil, syscall.Errno(r)
		}
	}
}

func regGetString(path, name string) (string, error) {
	typ, data, err := regGet(path, name)
	if err != nil {
		return "", err
	}
	if typ != regSZ && typ != regExpandSZ {
		return "", errors.New("not a text value")
	}
	u := unsafe.Slice((*uint16)(unsafe.Pointer(unsafe.SliceData(data))), len(data)/2)
	return syscall.UTF16ToString(u), nil
}

func regGetBinary(path, name string) ([]byte, error) {
	_, data, err := regGet(path, name)
	return data, err
}

func regSetString(path, name, value string) error {
	h, err := regOpen(path, keySetValue, true)
	if err != nil {
		return err
	}
	defer procRegCloseKey.Call(uintptr(h))
	u, err := syscall.UTF16FromString(value)
	if err != nil {
		return err
	}
	r, _, _ := procRegSetValueExW.Call(uintptr(h), uintptr(unsafe.Pointer(wstr(name))), 0, regSZ,
		uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)*2))
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

// regDeleteValue removes a value; one that isn't there is fine.
func regDeleteValue(path, name string) error {
	h, err := regOpen(path, keySetValue, false)
	if errors.Is(err, errorFileNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	defer procRegCloseKey.Call(uintptr(h))
	r, _, _ := procRegDeleteValueW.Call(uintptr(h), uintptr(unsafe.Pointer(wstr(name))))
	if r != 0 && syscall.Errno(r) != errorFileNotFound {
		return syscall.Errno(r)
	}
	return nil
}
