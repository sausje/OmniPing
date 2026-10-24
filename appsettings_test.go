package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppSettingsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	s, err := loadAppSettings(path)
	if err != nil || s != defaultAppSettings() {
		t.Fatalf("no file: %+v, %v; want the defaults", s, err)
	}
	if s.Port != 8080 || !s.AllDevices || !s.OpenOnStart {
		t.Errorf("defaults: %+v", s)
	}

	want := AppSettings{Port: 8090, DataDir: `D:\OmniPing`, MoveTo: `E:\Other`, FirstRunDone: true}
	if err := saveAppSettings(path, want); err != nil {
		t.Fatal(err)
	}
	if got, err := loadAppSettings(path); err != nil || got != want {
		t.Errorf("round trip: %+v, %v; want %+v", got, err, want)
	}

	// Settings missing from the file keep their defaults.
	os.WriteFile(path, []byte(`{"port": 70000}`), 0o644)
	if got, err := loadAppSettings(path); err != nil || got != defaultAppSettings() {
		t.Errorf("port out of range and the rest missing: %+v, %v", got, err)
	}

	os.WriteFile(path, []byte(`{"port": 80`), 0o644)
	if got, err := loadAppSettings(path); err == nil || got != defaultAppSettings() {
		t.Errorf("damaged file: %+v, %v; want the defaults and an error", got, err)
	}
	// Changing a damaged file starts again from the defaults.
	if err := updateAppSettings(path, func(s *AppSettings) { s.OpenOnStart = false }); err != nil {
		t.Fatal(err)
	}
	if got, _ := loadAppSettings(path); got.OpenOnStart || got.Port != 8080 || !got.AllDevices {
		t.Errorf("after the update: %+v", got)
	}
}
