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
