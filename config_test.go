package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigSaveKeepsBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cs, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cs.update(func(c *Config) error { c.Targets = []*Target{{ID: "a", Name: "First", Host: "192.0.2.1"}}; return nil })
	if err := cs.save(); err != nil {
		t.Fatal(err)
	}
	cs.update(func(c *Config) error { c.Targets[0].Name = "Second"; return nil })
	if err := cs.save(); err != nil {
		t.Fatal(err)
	}

	// A power cut empties config.json: the backup from the save before is used.
	os.WriteFile(path, nil, 0o644)
	cs, err = loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if ts := cs.targets(); len(ts) != 1 || ts[0].Name != "First" {
		t.Fatalf("targets after recovery = %+v, want the backup with First", ts)
	}

	// Damaged JSON without a usable backup is an error, not a silent reset.
	os.WriteFile(path, []byte(`{"targets": [`), 0o644)
	os.Remove(path + ".bak")
	if _, err := loadConfig(path); err == nil {
		t.Fatal("damaged config without a backup loaded without an error")
	}
}
