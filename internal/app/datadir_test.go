package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDataDirMovesTheAutoPackFolder(t *testing.T) {
	cfg := t.TempDir()
	writeFile(t, filepath.Join(cfg, "AutoPack", "servers.json"), "[]")

	dir := DataDir(cfg)
	if dir != filepath.Join(cfg, "IDISMAM") {
		t.Fatalf("data dir = %s", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "servers.json")); err != nil {
		t.Fatalf("settings were not moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg, "AutoPack")); !os.IsNotExist(err) {
		t.Fatalf("old folder still there: %v", err)
	}
}

func TestDataDirKeepsAnExistingIDISMAMFolder(t *testing.T) {
	cfg := t.TempDir()
	writeFile(t, filepath.Join(cfg, "AutoPack", "old.txt"), "old")
	writeFile(t, filepath.Join(cfg, "IDISMAM", "new.txt"), "new")

	if dir := DataDir(cfg); dir != filepath.Join(cfg, "IDISMAM") {
		t.Fatalf("data dir = %s", dir)
	}
	if _, err := os.Stat(filepath.Join(cfg, "AutoPack", "old.txt")); err != nil {
		t.Fatalf("old folder was touched: %v", err)
	}
}

func TestDataDirOnFirstStart(t *testing.T) {
	cfg := t.TempDir()
	if dir := DataDir(cfg); dir != filepath.Join(cfg, "IDISMAM") {
		t.Fatalf("data dir = %s", dir)
	}
}
