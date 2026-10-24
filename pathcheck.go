package main

import (
	"os"
	"path/filepath"
	"strings"
)

// insideOrSame tells whether dir is root or a folder inside it. It compares
// the folders themselves rather than how their paths are spelled: on Windows
// "OmniPing.", "OMNIPING" and a junction can all lead to the same folder.
// dir doesn't have to exist; then its nearest existing parent counts.
func insideOrSame(dir, root string) bool {
	rootInfo, err := os.Stat(root)
	if err != nil {
		rel, err := filepath.Rel(root, dir)
		return err == nil && filepath.IsLocal(rel)
	}
	for d := filepath.Clean(dir); ; {
		if fi, err := os.Stat(d); err == nil && os.SameFile(fi, rootInfo) {
			return true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
}

// portableData tells whether dir is the "data" folder next to OmniPing.exe
// in programDir, or a folder inside it. Such a folder makes the next start
// the portable version. Like insideOrSame, it compares folders rather than
// spellings, and dir doesn't have to exist.
func portableData(dir, programDir string) bool {
	for d := filepath.Clean(dir); ; {
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		// Windows ignores dots and spaces at the end of a name.
		if strings.EqualFold(strings.TrimRight(filepath.Base(d), ". "), "data") && sameFolder(parent, programDir) {
			return true
		}
		d = parent
	}
}

// sameFolder tells whether a and b are the same existing folder.
func sameFolder(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && os.SameFile(fa, fb)
}
