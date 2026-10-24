package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// Where the Windows app keeps its files. The app folder holds app.json, the
// log and crash.log: %LOCALAPPDATA%\OmniPing, or the data folder next to
// OmniPing.exe for the portable version. The data folder (settings, history,
// events) is the app folder too, unless the user moved it or -data says
// otherwise.

// appFolder returns the app folder for the program at exe. A folder named
// data next to it makes OmniPing portable: everything stays in there.
func appFolder(exe, localAppData string) (dir string, portable bool) {
	data := filepath.Join(filepath.Dir(exe), "data")
	if fi, err := os.Stat(data); err == nil && fi.IsDir() {
		return data, true
	}
	return filepath.Join(localAppData, "OmniPing"), false
}

// dataFolder picks the data folder: -data or OMNIPING_DATA first, then the
// portable folder, then where the user moved it to, else the app folder.
func dataFolder(appDir string, portable bool, s AppSettings, cli cliOptions) (string, error) {
	switch {
	case cli.DataSet:
		return filepath.Abs(cli.DataDir)
	case portable || s.DataDir == "":
		return appDir, nil
	case filepath.IsAbs(s.DataDir):
		return filepath.Clean(s.DataDir), nil
	}
	return filepath.Join(appDir, s.DataDir), nil
}

// localAppDataDir is %LOCALAPPDATA%, like C:\Users\Me\AppData\Local.
func localAppDataDir() (string, error) {
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return d, nil
	}
	// FOLDERID_LocalAppData
	id := [16]byte{0x85, 0x27, 0xB3, 0xF1, 0xBA, 0x6F, 0xCF, 0x4F, 0x9D, 0x55, 0x7B, 0x8E, 0x7F, 0x15, 0x70, 0x91}
	var p *uint16
	r, _, _ := procSHGetKnownFolderPath.Call(uintptr(unsafe.Pointer(&id)), 0, 0, uintptr(unsafe.Pointer(&p)))
	if p != nil {
		defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	}
	if r != 0 {
		return "", fmt.Errorf("cannot find the AppData\\Local folder: %v", syscall.Errno(r))
	}
	return utf16PtrToString(p), nil
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// isDataItem tells whether a file or folder in the data folder moves with
// it. The app's own files (app.json, the log, crash.log) and instance.json
// stay.
func isDataItem(name string) bool {
	switch name {
	case "history", "logs", "config.before-restore.json":
		return true
	}
	return strings.HasPrefix(name, "config.json") || strings.HasPrefix(name, "events.jsonl")
}

// moveDataFolder does a move the user asked for in the settings
// (AppSettings.MoveTo): it copies the data from the folder in use to the new
// one, then saves the new place in appFile and only then removes the old
// copy. It returns the data folder to use. When the copy fails nothing
// changes except that the move is forgotten, so OmniPing doesn't try again
// at every start.
func moveDataFolder(appFile, appDir, from string, s *AppSettings) (string, error) {
	to := filepath.Clean(s.MoveTo)
	s.MoveTo = ""
	if samePath(to, from) {
		return from, saveAppSettings(appFile, *s)
	}
	moved, err := copyDataItems(from, to)
	if err == nil {
		old := s.DataDir
		s.DataDir = to
		if samePath(to, appDir) {
			s.DataDir = ""
		}
		if err = saveAppSettings(appFile, *s); err != nil {
			s.DataDir = old
			removeItems(to, moved)
		}
	}
	if err != nil {
		if serr := saveAppSettings(appFile, *s); serr != nil {
			log.Printf("saving %s: %v", appFile, serr)
		}
		return from, err
	}
	removeItems(from, moved)
	if !samePath(from, appDir) {
		os.Remove(from) // only when nothing else is left in it
	}
	return to, nil
}

// copyDataItems copies the data in from to the folder to and returns the
// names it copied. On failure it removes what it copied.
func copyDataItems(from, to string) ([]string, error) {
	if !filepath.IsAbs(to) {
		return nil, fmt.Errorf("%s is not a full path", to)
	}
	if insideOrSame(to, from) {
		return nil, errors.New("the new folder is inside the current one")
	}
	entries, err := os.ReadDir(from)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	_, statErr := os.Stat(to)
	created := errors.Is(statErr, fs.ErrNotExist)
	if err := os.MkdirAll(to, 0o755); err != nil {
		return nil, err
	}
	var moved []string
	for _, e := range entries {
		if !isDataItem(e.Name()) {
			continue
		}
		moved = append(moved, e.Name())
		if err := copyTree(filepath.Join(from, e.Name()), filepath.Join(to, e.Name())); err != nil {
			removeItems(to, moved)
			if created {
				os.Remove(to)
			}
			return nil, err
		}
	}
	return moved, nil
}

// removeItems deletes the named files and folders from dir.
func removeItems(dir string, names []string) {
	for _, name := range names {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			log.Printf("could not remove %s: %v", filepath.Join(dir, name), err)
		}
	}
}

// copyTree copies a file, or a folder with everything in it, keeping the
// files' times.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
}
