package app

import (
	"os"
	"path/filepath"
)

// DataDir returns the folder under configDir that holds Maple's settings,
// server list and log. The app used to be called AutoPack; the first time
// Maple starts, it moves the old AutoPack folder over. If the old folder
// cannot be moved yet (a file in it is still in use), it is used as it is
// and the move is tried again next time.
func DataDir(configDir string) string {
	dir := filepath.Join(configDir, "Maple")
	old := filepath.Join(configDir, "AutoPack")
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	if info, err := os.Stat(old); err != nil || !info.IsDir() {
		return dir
	}
	if err := os.Rename(old, dir); err != nil {
		return old
	}
	return dir
}
