package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestAppFolder(t *testing.T) {
	exeDir := t.TempDir()
	exe := filepath.Join(exeDir, "OmniPing.exe")
	local := filepath.Join(t.TempDir(), "Local")

	dir, portable := appFolder(exe, local)
	if portable || dir != filepath.Join(local, "OmniPing") {
		t.Errorf("installed: got %s, portable %v", dir, portable)
	}

	// A data folder next to the program makes it portable.
	if err := os.Mkdir(filepath.Join(exeDir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir, portable = appFolder(exe, local)
	if !portable || dir != filepath.Join(exeDir, "data") {
		t.Errorf("portable: got %s, portable %v", dir, portable)
	}

	// A file called data doesn't count.
	fileDir := t.TempDir()
	os.WriteFile(filepath.Join(fileDir, "data"), nil, 0o644)
	if _, portable := appFolder(filepath.Join(fileDir, "OmniPing.exe"), local); portable {
		t.Error("a file named data made OmniPing portable")
	}
}

func TestDataFolder(t *testing.T) {
	app := `C:\Users\Test\AppData\Local\OmniPing`
	cwd, _ := os.Getwd()
	tests := []struct {
		name     string
		portable bool
		s        AppSettings
		cli      cliOptions
		want     string
	}{
		{"default", false, defaultAppSettings(), cliOptions{DataDir: "data"}, app},
		{"moved", false, AppSettings{DataDir: `D:\OmniPing`}, cliOptions{DataDir: "data"}, `D:\OmniPing`},
		{"moved, relative", false, AppSettings{DataDir: `elsewhere`}, cliOptions{}, filepath.Join(app, "elsewhere")},
		{"portable ignores a move", true, AppSettings{DataDir: `D:\OmniPing`}, cliOptions{}, app},
		{"-data", false, AppSettings{DataDir: `D:\OmniPing`}, cliOptions{DataDir: `E:\Test`, DataSet: true}, `E:\Test`},
		{"-data, relative", true, AppSettings{}, cliOptions{DataDir: `mydata`, DataSet: true}, filepath.Join(cwd, "mydata")},
	}
	for _, tc := range tests {
		got, err := dataFolder(app, tc.portable, tc.s, tc.cli)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestIsDataItem(t *testing.T) {
	for _, name := range []string{"config.json", "config.json.bak", "config.json.tmp", "history", "events.jsonl", "logs", "config.before-restore.json"} {
		if !isDataItem(name) {
			t.Errorf("%s should move with the data", name)
		}
	}
	for _, name := range []string{"app.json", "omniping.log", "omniping.log.1", "crash.log", "instance.json", "README.txt"} {
		if isDataItem(name) {
			t.Errorf("%s should stay", name)
		}
	}
}

// writeFiles creates files (and their folders) under dir.
func writeFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("content of "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// listFiles returns all files under dir, with forward slashes.
func listFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	slices.Sort(out)
	return out
}

var (
	dataFiles = []string{"config.before-restore.json", "config.json", "config.json.bak", "events.jsonl", "history/t1/2026-10-16.raw", "logs/alerts.log"}
	appFiles  = []string{"app.json", "crash.log", "instance.json", "omniping.log", "omniping.log.1"}
)

func TestMoveDataFolderAtStart(t *testing.T) {
	app := t.TempDir()
	writeFiles(t, app, dataFiles...)
	writeFiles(t, app, appFiles...)
	appFile := filepath.Join(app, "app.json")
	to := filepath.Join(t.TempDir(), "new place")
	s := defaultAppSettings()
	s.MoveTo = to

	got, err := moveDataFolder(appFile, app, app, &s)
	if err != nil {
		t.Fatal(err)
	}
	if got != to {
		t.Errorf("data folder is %s, want %s", got, to)
	}
	if files := listFiles(t, to); !slices.Equal(files, dataFiles) {
		t.Errorf("new folder has %v, want %v", files, dataFiles)
	}
	if files := listFiles(t, app); !slices.Equal(files, appFiles) {
		t.Errorf("old folder has %v, want only %v", files, appFiles)
	}
	b, _ := os.ReadFile(filepath.Join(to, "history", "t1", "2026-10-16.raw"))
	if string(b) != "content of history/t1/2026-10-16.raw" {
		t.Errorf("history copied wrong: %q", b)
	}
	saved, err := loadAppSettings(appFile)
	if err != nil || saved.DataDir != to || saved.MoveTo != "" {
		t.Errorf("app.json has DataDir %q, MoveTo %q (%v)", saved.DataDir, saved.MoveTo, err)
	}

	// And back to the app folder: that is the default again, and the
	// folder it came from goes away once it's empty.
	s.MoveTo = app
	got, err = moveDataFolder(appFile, app, to, &s)
	if err != nil || got != app {
		t.Fatalf("moving back: %s, %v", got, err)
	}
	saved, _ = loadAppSettings(appFile)
	if saved.DataDir != "" || saved.MoveTo != "" {
		t.Errorf("after moving back app.json has DataDir %q, MoveTo %q", saved.DataDir, saved.MoveTo)
	}
	if _, err := os.Stat(to); !os.IsNotExist(err) {
		t.Errorf("the empty old folder is still there: %v", err)
	}
	if files := listFiles(t, app); len(files) != len(dataFiles)+len(appFiles) {
		t.Errorf("app folder has %v", files)
	}
}

func TestMoveDataFolderFails(t *testing.T) {
	app := t.TempDir()
	writeFiles(t, app, dataFiles...)
	writeFiles(t, app, "app.json")
	appFile := filepath.Join(app, "app.json")
	// The new place is a file, so it can't become a folder.
	blocked := filepath.Join(t.TempDir(), "blocked")
	os.WriteFile(blocked, nil, 0o644)

	for _, to := range []string{blocked, filepath.Join(app, "inside"), "relative"} {
		s := defaultAppSettings()
		s.MoveTo = to
		got, err := moveDataFolder(appFile, app, app, &s)
		if err == nil {
			t.Errorf("moving to %s worked", to)
		}
		if got != app {
			t.Errorf("after a failed move to %s the data folder is %s", to, got)
		}
		saved, _ := loadAppSettings(appFile)
		if saved.MoveTo != "" || saved.DataDir != "" {
			t.Errorf("after a failed move to %s app.json has DataDir %q, MoveTo %q", to, saved.DataDir, saved.MoveTo)
		}
		if files := listFiles(t, app); len(files) != len(dataFiles)+1 {
			t.Errorf("after a failed move to %s the old folder has %v", to, files)
		}
	}
}

func TestMoveDataFolderCleansUp(t *testing.T) {
	app := t.TempDir()
	writeFiles(t, app, dataFiles...)
	to := filepath.Join(t.TempDir(), "new")
	// A file named history makes the copy fail halfway, after the
	// config and events files.
	writeFiles(t, to, "history")

	if _, err := copyDataItems(app, to); err == nil {
		t.Fatal("copy worked")
	}
	for _, name := range []string{"config.json", "config.before-restore.json", "events.jsonl"} {
		if _, err := os.Stat(filepath.Join(to, name)); !os.IsNotExist(err) {
			t.Errorf("%s was left behind after the failed copy", name)
		}
	}
	if files := listFiles(t, app); !slices.Equal(files, dataFiles) {
		t.Errorf("source changed: %v", files)
	}
}
