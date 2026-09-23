//go:build windows

package app

import (
	"os"
	"path/filepath"
	"testing"
)

// This moves a small folder to the real Recycle Bin, so it only runs when
// MAPLE_TRY_RECYCLE_BIN is set.
func TestMoveToRecycleBin(t *testing.T) {
	if os.Getenv("MAPLE_TRY_RECYCLE_BIN") == "" {
		t.Skip("set MAPLE_TRY_RECYCLE_BIN=1 to move a test folder to the Recycle Bin")
	}
	dir := filepath.Join(t.TempDir(), "maple-recycle-test")
	writeFile(t, filepath.Join(dir, "server.properties"), "motd=test\n")
	if err := moveToRecycleBin(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("folder still there: %v", err)
	}
}

// This copies a small folder between drives with File Explorer's move, so it
// only runs when MAPLE_TRY_MOVE_TO is set to a folder on another drive.
func TestMoveAcrossDrives(t *testing.T) {
	target := os.Getenv("MAPLE_TRY_MOVE_TO")
	if target == "" {
		t.Skip("set MAPLE_TRY_MOVE_TO to a folder on another drive")
	}
	from := filepath.Join(t.TempDir(), "maple-move-test")
	writeFile(t, filepath.Join(from, "mods", "a.jar"), "jar")
	to := filepath.Join(target, "maple-move-test")
	t.Cleanup(func() { os.RemoveAll(to) })
	if err := moveAcrossDrives(from, to); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(to, "mods", "a.jar")); err != nil {
		t.Fatalf("not moved: %v", err)
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Fatalf("source still there: %v", err)
	}
}
