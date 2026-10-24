package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The trailing dot is the same folder on Windows.
func TestCheckNewDataDirOtherSpelling(t *testing.T) {
	cur := filepath.Join(t.TempDir(), "OmniPing")
	if err := os.MkdirAll(cur, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{cur + `.\logs`, strings.ToUpper(cur) + `\logs`} {
		if _, err := checkNewDataDir(cur, "", dir); err == nil {
			t.Errorf("%s was accepted", dir)
		}
	}
}

// "DATA." next to the program, or data through a junction to its folder, is
// the portable data folder too.
func TestPortableDataOtherSpelling(t *testing.T) {
	base := t.TempDir()
	program := filepath.Join(base, "OmniPing")
	if err := os.MkdirAll(program, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, program).CombinedOutput(); err != nil {
		t.Fatalf("mklink: %v %s", err, out)
	}
	for _, dir := range []string{
		program + `\DATA.`, program + `.\data\x`, strings.ToUpper(program) + `\Data `, link + `\data`,
	} {
		if !portableData(dir, program) {
			t.Errorf("%s is not seen as the portable data folder", dir)
		}
	}
	for _, dir := range []string{program + `\database`, base + `\data`, program} {
		if portableData(dir, program) {
			t.Errorf("%s is seen as the portable data folder", dir)
		}
	}
	// An NTFS stream name is the folder itself.
	cur := filepath.Join(base, "cur")
	os.MkdirAll(cur, 0o755)
	for _, dir := range []string{program + `\data::$INDEX_ALLOCATION`, program + `\data:$I30:$INDEX_ALLOCATION\sub`} {
		if _, err := checkNewDataDir(cur, program, dir); err == nil {
			t.Errorf("%s was accepted", dir)
		}
	}
	if _, err := os.Stat(program + `\data`); err == nil {
		t.Error("the portable data folder was created")
	}
}
