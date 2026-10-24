package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Windows command that quotes a path with spaces, as people write them.
func TestScriptWithQuotedPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "My Scripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bat := filepath.Join(dir, "alert.bat")
	if err := os.WriteFile(bat, []byte("@echo ran %1\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := shellCommand(context.Background(), `"`+bat+`" hello & echo two`).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if s := string(out); !strings.Contains(s, "ran hello") || !strings.Contains(s, "two") {
		t.Errorf("output %q", s)
	}
}
