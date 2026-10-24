package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"runtime"
	"slices"
	"syscall"
	"testing"
	"unsafe"
)

func TestWin32StructSizes(t *testing.T) {
	nid, wc := unsafe.Sizeof(notifyIconData{}), unsafe.Sizeof(wndClassEx{})
	wantNid, wantWc := uintptr(976), uintptr(80)
	if runtime.GOARCH == "386" {
		wantNid, wantWc = 956, 48
	}
	if nid != wantNid {
		t.Errorf("NOTIFYICONDATAW is %d bytes, want %d", nid, wantNid)
	}
	if wc != wantWc {
		t.Errorf("WNDCLASSEXW is %d bytes, want %d", wc, wantWc)
	}
	// Spot checks against the offsets in the Windows headers (64-bit).
	if runtime.GOARCH != "386" {
		var d notifyIconData
		offsets := map[string][2]uintptr{
			"hWnd":         {unsafe.Offsetof(d.Wnd), 8},
			"hIcon":        {unsafe.Offsetof(d.Icon), 32},
			"szTip":        {unsafe.Offsetof(d.Tip), 40},
			"szInfo":       {unsafe.Offsetof(d.Info), 304},
			"uVersion":     {unsafe.Offsetof(d.Version), 816},
			"szInfoTitle":  {unsafe.Offsetof(d.InfoTitle), 820},
			"dwInfoFlags":  {unsafe.Offsetof(d.InfoFlags), 948},
			"guidItem":     {unsafe.Offsetof(d.GUIDItem), 952},
			"hBalloonIcon": {unsafe.Offsetof(d.BalloonIcon), 968},
		}
		for name, o := range offsets {
			if o[0] != o[1] {
				t.Errorf("%s is at %d, want %d", name, o[0], o[1])
			}
		}
	}
}

func TestIcoImage(t *testing.T) {
	pngSig := []byte("\x89PNG")
	for name, ico := range map[string][]byte{"normal": icoNormal, "alert": icoAlert, "paused": icoPaused} {
		for _, size := range []int{16, 20, 24, 32, 40, 48} {
			img, err := icoImage(ico, size)
			if err != nil {
				t.Fatalf("%s %d: %v", name, size, err)
			}
			// Either a PNG or a bitmap whose header gives the size.
			if bytes.HasPrefix(img, pngSig) {
				if w := int(binary.BigEndian.Uint32(img[16:])); w != size {
					t.Errorf("%s %d: got a %d px PNG", name, size, w)
				}
			} else if w := int(binary.LittleEndian.Uint32(img[4:])); w != size {
				t.Errorf("%s %d: got a %d px bitmap", name, size, w)
			}
		}
		// Odd sizes get the next bigger picture, which Windows scales down.
		big, _ := icoImage(ico, 36)
		want, _ := icoImage(ico, 40)
		if !bytes.Equal(big, want) {
			t.Errorf("%s: 36 px did not pick the 40 px picture", name)
		}
		huge, _ := icoImage(ico, 1000)
		biggest, _ := icoImage(ico, 256)
		if !bytes.Equal(huge, biggest) {
			t.Errorf("%s: 1000 px did not pick the biggest picture", name)
		}
	}
	if _, err := icoImage([]byte("not an icon"), 16); err == nil {
		t.Error("no error for a file that isn't an .ico")
	}
}

// The icons load into Windows in every size the tray can ask for.
func TestIconFromICO(t *testing.T) {
	for _, size := range []int{16, 20, 24, 32, 64} {
		h, err := iconFromICO(icoAlert, size)
		if err != nil || h == 0 {
			t.Errorf("%d px: %v", size, err)
			continue
		}
		procDestroyIcon.Call(h)
	}
}

func TestTrayLook(t *testing.T) {
	tests := []struct {
		started, hasStatus bool
		st                 ShellStatus
		icon               int
		tip, line          string
	}{
		{false, false, ShellStatus{}, 0, "OmniPing – starting…", ""},
		{true, false, ShellStatus{}, 0, "OmniPing", ""},
		{true, true, ShellStatus{Targets: 11}, 0, "OmniPing – 11 targets · all clear", "11 targets · all clear"},
		{true, true, ShellStatus{Targets: 1}, 0, "OmniPing – 1 target · all clear", "1 target · all clear"},
		{true, true, ShellStatus{Targets: 11, Alerts: 2}, 1, "OmniPing – 11 targets · 2 alerts", "11 targets · 2 alerts"},
		{true, true, ShellStatus{Targets: 11, Alerts: 1}, 1, "OmniPing – 11 targets · 1 alert", "11 targets · 1 alert"},
		{true, true, ShellStatus{Targets: 11, Alerts: 2, Paused: true}, 2, "OmniPing – paused", "Paused"},
		{true, true, ShellStatus{}, 0, "OmniPing – no targets yet", "No targets yet"},
	}
	for _, tc := range tests {
		icon, tip := trayLook(tc.started, tc.hasStatus, tc.st)
		if icon != tc.icon || tip != tc.tip {
			t.Errorf("%+v: got icon %d %q, want %d %q", tc.st, icon, tip, tc.icon, tc.tip)
		}
		if tc.line != "" && statusLine(tc.st) != tc.line {
			t.Errorf("%+v: status line %q, want %q", tc.st, statusLine(tc.st), tc.line)
		}
	}
}

func TestPutUTF16(t *testing.T) {
	var buf [8]uint16
	putUTF16(buf[:], "short")
	if got := syscall.UTF16ToString(buf[:]); got != "short" {
		t.Errorf("got %q", got)
	}
	putUTF16(buf[:], "much too long")
	if got := syscall.UTF16ToString(buf[:]); got != "much t…" {
		t.Errorf("got %q", got)
	}
	// An emoji is two UTF-16 units; it is not cut in half.
	putUTF16(buf[:], "abcde😀xyz")
	if got := syscall.UTF16ToString(buf[:]); got != "abcde…" {
		t.Errorf("got %q", got)
	}
	// What was in the field before is cleared.
	putUTF16(buf[:], "a")
	if buf[1] != 0 || buf[7] != 0 {
		t.Errorf("left over: %v", buf)
	}
}

func TestSentence(t *testing.T) {
	tests := map[string]string{
		"cannot read settings: bad":          "Cannot read settings: bad.",
		"bind: Only one usage is permitted.": "Bind: Only one usage is permitted.",
		"één fout":                           "Één fout.",
		"":                                   "",
	}
	for in, want := range tests {
		if got := sentence(errors.New(in)); got != want {
			t.Errorf("sentence(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRestartArgs(t *testing.T) {
	tests := []struct {
		in   []string
		port int
		want []string
	}{
		{nil, 0, []string{"--restarted"}},
		{[]string{"--autostart"}, 0, []string{"--restarted"}},
		{[]string{"-autostart=true", "-data", `D:\x`}, 0, []string{"-data", `D:\x`, "--restarted"}},
		{[]string{"--restarted", "-addr", ":9593"}, 0, []string{"--restarted", "-addr", ":9593"}},
		{[]string{"-web", "web"}, 9590, []string{"-web", "web", "--restarted", "--restart-port=9590"}},
		{[]string{"--restarted", "--restart-port=61234"}, 9590, []string{"--restarted", "--restart-port=9590"}},
		{[]string{"-restart-port", "61234", "-web", "web"}, 9591, []string{"-web", "web", "--restarted", "--restart-port=9591"}},
	}
	for _, tc := range tests {
		if got := restartArgs(tc.in, tc.port); !slices.Equal(got, tc.want) {
			t.Errorf("restartArgs(%q, %d) = %q, want %q", tc.in, tc.port, got, tc.want)
		}
	}
}

func TestNotifyQueue(t *testing.T) {
	tr := newTray(`C:\OmniPing.exe`, `C:\data`, "")
	tr.Notify("one", "", "#/target/a", true)
	tr.Notify("two", "", "#/target/b", false)
	tr.Notify("three", "", "", false)
	tr.Notify("four", "", "", false)
	tr.Notify("five", "", "#/target/c", true)
	var got []notice
	for {
		n, ok := tr.nextNotice()
		if !ok {
			break
		}
		got = append(got, n)
	}
	if len(got) != 4 {
		t.Fatalf("got %d notifications: %+v", len(got), got)
	}
	if got[0].title != "one" || got[2].title != "three" || got[0].path != "#/target/a" {
		t.Errorf("first three: %+v", got[:3])
	}
	if s := got[3]; s.title != "2 more notifications" || s.path != "#/alerts" || !s.alert {
		t.Errorf("summary: %+v", s)
	}
}

func TestShellIsDesktop(t *testing.T) {
	var sh Shell = newTray(`C:\OmniPing.exe`, `C:\data`, "")
	if !sh.Desktop() {
		t.Error("the tray is not a desktop shell")
	}
	if _, supported := sh.Autostart(); !supported {
		t.Error("autostart not supported")
	}
}
