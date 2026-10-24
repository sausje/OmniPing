package main

import (
	"fmt"
	"os"
	"testing"
	"unsafe"
)

func TestCommandExe(t *testing.T) {
	tests := map[string]string{
		`"C:\Program Files\OmniPing\OmniPing.exe" --autostart`: `C:\Program Files\OmniPing\OmniPing.exe`,
		`"C:\Tools\OmniPing.exe"`:                              `C:\Tools\OmniPing.exe`,
		`C:\Tools\OmniPing.exe --autostart`:                    `C:\Tools\OmniPing.exe`,
		`  C:\Tools\OmniPing.exe`:                              `C:\Tools\OmniPing.exe`,
	}
	for cmd, want := range tests {
		if got := commandExe(cmd); got != want {
			t.Errorf("commandExe(%q) = %q, want %q", cmd, got, want)
		}
	}
	if got := autostartCommand(`C:\A B\OmniPing.exe`); got != `"C:\A B\OmniPing.exe" --autostart` {
		t.Errorf("autostartCommand: %s", got)
	}
}

func TestApprovedEnabled(t *testing.T) {
	tests := []struct {
		data []byte
		want bool
	}{
		{nil, true},
		{[]byte{}, true},
		{[]byte{2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, true},
		{[]byte{6, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, true},
		{[]byte{3, 0, 0, 0, 0x5c, 0x1d, 0x8e, 0x41, 0x2b, 0x3f, 0xdb, 0x01}, false},
		{[]byte{7, 0, 0, 0}, false},
		{[]byte{1}, false},
	}
	for _, tc := range tests {
		if got := approvedEnabled(tc.data); got != tc.want {
			t.Errorf("approvedEnabled(% x) = %v, want %v", tc.data, got, tc.want)
		}
	}
}

// The real Run key is left alone: these keys live under a test key that is
// removed afterwards.
func TestAutostartRegistry(t *testing.T) {
	base := fmt.Sprintf(`Software\OmniPing-test-%d`, os.Getpid())
	k := autostartKeys{run: base + `\Run`, approved: base + `\StartupApproved`, name: "OmniPing"}
	t.Cleanup(func() {
		procDeleteTree := modAdvapi32.NewProc("RegDeleteTreeW")
		procDeleteTree.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(wstr(base))))
		if _, err := regOpen(base, keyQueryValue, false); err == nil {
			t.Errorf("could not remove %s", base)
		}
	})
	exe := `C:\Program Files\OmniPing\OmniPing.exe`

	if k.on(exe) {
		t.Error("on before it was set")
	}
	if err := k.set(false, exe); err != nil {
		t.Errorf("turning off what isn't there: %v", err)
	}
	if err := k.set(true, exe); err != nil {
		t.Fatal(err)
	}
	if cmd, err := regGetString(k.run, k.name); err != nil || cmd != `"C:\Program Files\OmniPing\OmniPing.exe" --autostart` {
		t.Errorf("Run value is %q (%v)", cmd, err)
	}
	if !k.on(exe) {
		t.Error("off after turning it on")
	}
	if !k.on(`c:\program files\omniping\OMNIPING.EXE`) {
		t.Error("the path should not depend on case")
	}
	if k.on(`D:\Other\OmniPing.exe`) {
		t.Error("on for another copy of OmniPing")
	}

	// Disabled in Task Manager: off, until it is turned on here again.
	disabled := []byte{3, 0, 0, 0, 0x5c, 0x1d, 0x8e, 0x41, 0x2b, 0x3f, 0xdb, 0x01}
	setBinary(t, k.approved, k.name, disabled)
	if k.on(exe) {
		t.Error("on while disabled in Task Manager")
	}
	if err := k.set(true, exe); err != nil {
		t.Fatal(err)
	}
	if !k.on(exe) {
		t.Error("still off after turning it on again")
	}
	if _, err := regGetBinary(k.approved, k.name); err == nil {
		t.Error("the disabled entry is still there")
	}
	setBinary(t, k.approved, k.name, []byte{2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	if !k.on(exe) {
		t.Error("off while enabled in Task Manager")
	}

	if err := k.set(false, exe); err != nil {
		t.Fatal(err)
	}
	if k.on(exe) {
		t.Error("on after turning it off")
	}
	if _, err := regGetString(k.run, k.name); err == nil {
		t.Error("the Run value is still there")
	}
}

func setBinary(t *testing.T, path, name string, data []byte) {
	t.Helper()
	h, err := regOpen(path, keySetValue, true)
	if err != nil {
		t.Fatal(err)
	}
	defer procRegCloseKey.Call(uintptr(h))
	if r, _, _ := procRegSetValueExW.Call(uintptr(h), uintptr(unsafe.Pointer(wstr(name))), 0, regBinary,
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data))); r != 0 {
		t.Fatalf("writing %s: error %d", name, r)
	}
}
