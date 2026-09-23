package downloader

import (
	"os"
	"testing"

	"github.com/bhhoang/IDISMAM/internal/parser"
)

// TestLiveFailureReport talks to the real CurseForge API. Enabled with
// MCPACKCTL_LIVE_TEST=1.
func TestLiveFailureReport(t *testing.T) {
	if os.Getenv("MCPACKCTL_LIVE_TEST") != "1" {
		t.Skip("set MCPACKCTL_LIVE_TEST=1 to run")
	}
	// Same public key the CLI defaults to (see internal/cmd.initConfig).
	key := os.Getenv("MCPACKCTL_CURSEFORGE_API_KEY")
	if key == "" {
		key = "$2a$10$bL4bIL5pUWqfcO7KQtnMReakwtfHbNKh6v1uTpKlzhwoueEJQnPnm"
	}
	d := New(t.TempDir(), key, 2, false)
	m := &parser.Manifest{Files: []parser.ModFile{
		{ProjectID: 238222, FileID: 8880075, Required: true},  // real, downloads
		{ProjectID: 1001614, FileID: 5922047, Required: true}, // delisted, recovered via official API
		{ProjectID: 238222, FileID: 1, Required: true},        // no such file
		{ProjectID: 99999999, FileID: 1, Required: false},     // no such project
	}}
	dest := t.TempDir()
	if err := d.DownloadMods(m, dest); err != nil {
		t.Fatalf("DownloadMods: %v", err)
	}
	entries, _ := os.ReadDir(dest)
	for _, e := range entries {
		t.Logf("downloaded: %s", e.Name())
	}
	for _, f := range d.FailedMods() {
		t.Logf("FAILED %d/%d name=%q url=%s err=%v", f.ProjectID, f.FileID, f.Name, f.PageURL, f.Err)
	}
}
