package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnquote(t *testing.T) {
	for in, want := range map[string]string{
		`"D:\OmniPing"`:    `D:\OmniPing`,
		` "D:\Omni Ping" `: `D:\Omni Ping`,
		`D:\OmniPing`:      `D:\OmniPing`,
		`"`:                `"`,
	} {
		if got := unquote(in); got != want {
			t.Errorf("unquote(%q) = %q, want %q", in, got, want)
		}
	}
}

// A new data folder can't be the "data" folder next to the program (that
// makes the next start portable) or the current one under another spelling.
func TestCheckNewDataDirRefuses(t *testing.T) {
	base := t.TempDir()
	cur := filepath.Join(base, "OmniPing")
	program := filepath.Join(base, "Programs", "OmniPing")
	for _, d := range []string{cur, program} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ dir, want string }{
		{cur, "already"},
		{filepath.Join(cur, "history"), "inside the current"},
		{filepath.Join(program, "data"), "portable"},
		{filepath.Join(program, "data", "sub"), "portable"},
	} {
		if _, err := checkNewDataDir(cur, program, c.dir); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one about %q", c.dir, err, c.want)
		}
	}
	if _, err := os.Stat(filepath.Join(program, "data")); err == nil {
		t.Error("the refused folder was created")
	}
	if _, err := checkNewDataDir(cur, program, filepath.Join(base, "elsewhere")); err != nil {
		t.Errorf("a new folder elsewhere: %v", err)
	}
	// Other folders next to the program are fine (D:\OmniPing.exe with the
	// data in D:\OmniPing Data, say).
	if _, err := checkNewDataDir(cur, program, filepath.Join(program, "history")); err != nil {
		t.Errorf("another folder next to the program: %v", err)
	}
}

func TestInsideOrSame(t *testing.T) {
	root := t.TempDir()
	if !insideOrSame(filepath.Join(root, "a", "b"), root) {
		t.Error("a folder that doesn't exist yet inside root")
	}
	if insideOrSame(filepath.Dir(root), root) {
		t.Error("the parent of root")
	}
	if !insideOrSame(root, root) {
		t.Error("root itself")
	}
}
