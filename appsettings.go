package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AppSettings are the Windows app's own settings. They live outside the
// data folder, because they say where the data folder is and which port to
// open, which OmniPing needs before it can read anything else.
type AppSettings struct {
	// Port for the page; 0 means the default, 8080.
	Port int `json:"port"`
	// AllDevices listens on every network, so phones can open OmniPing too.
	// Off listens on this computer only.
	AllDevices bool `json:"allDevices"`
	// OpenOnStart opens the page when OmniPing is started by hand (never
	// when Windows starts it at sign-in).
	OpenOnStart bool `json:"openOnStart"`
	// DataDir is the data folder after the user moved it; empty means the
	// default.
	DataDir string `json:"dataDir,omitempty"`
	// MoveTo asks the next start to move the data folder there first.
	MoveTo string `json:"moveTo,omitempty"`
	// FirstRunDone is set once the "OmniPing is running" notice was shown.
	FirstRunDone bool `json:"firstRunDone,omitempty"`
}

const defaultPort = 8080

func defaultAppSettings() AppSettings {
	return AppSettings{Port: defaultPort, AllDevices: true, OpenOnStart: true}
}

// loadAppSettings reads path. A missing file gives the defaults; a damaged
// one gives the defaults and an error to log.
func loadAppSettings(path string) (AppSettings, error) {
	s := defaultAppSettings()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return defaultAppSettings(), err
	}
	if s.Port <= 0 || s.Port > 65535 {
		s.Port = defaultPort
	}
	return s, nil
}

// appSettingsMu keeps changes to the app settings file from overwriting
// each other, and reads from catching a save halfway (on Windows a file that
// is open for reading can't be replaced).
var appSettingsMu sync.Mutex

// readAppSettings is loadAppSettings for while OmniPing runs.
func readAppSettings(path string) (AppSettings, error) {
	appSettingsMu.Lock()
	defer appSettingsMu.Unlock()
	return loadAppSettings(path)
}

// updateAppSettings reads path, lets change edit the settings and saves
// them. It is safe to call from any goroutine. A damaged file is replaced;
// a file that can't be read is left alone.
func updateAppSettings(path string, change func(s *AppSettings)) error {
	appSettingsMu.Lock()
	defer appSettingsMu.Unlock()
	s, err := loadAppSettings(path)
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return err
	}
	if err != nil {
		log.Printf("%s is damaged (%v); starting it again from the defaults", path, err)
	}
	change(&s)
	return saveAppSettings(path, s)
}

// saveAppSettings writes path safely: a power cut leaves the old or the new
// file, never half of one.
func saveAppSettings(path string, s AppSettings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Virus scanners and indexers briefly open new files on Windows, which
	// makes the rename fail; try again for a moment.
	for i := 0; ; i++ {
		err := os.Rename(tmp, path)
		if err == nil || i == 10 {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}
