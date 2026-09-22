package downloader

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestDownloadModReusesCache is the regression test for the cache never being
// hit: lookups used <projectID>-<fileID>.jar while entries were stored under
// the real filename, so every setup downloaded every mod again.
func TestDownloadModReusesCache(t *testing.T) {
	var downloads atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			downloads.Add(1)
			fmt.Fprint(w, "jar-bytes")
			return
		}
		fmt.Fprint(w, `{"data":{"fileName":"jei-1.20.1-forge-15.20.0.106.jar","gameVersions":["1.20.1","Forge"]}}`)
	}))
	defer site.Close()

	cacheDir := t.TempDir()
	for run := 1; run <= 2; run++ {
		// A fresh Downloader per run, as in separate setup invocations.
		d := New(cacheDir, "", 1, true)
		d.siteAPIBase = site.URL
		d.cdnBase = site.URL

		modsDir := t.TempDir()
		if err := d.DownloadOne(238222, 5846880, modsDir); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		got, err := os.ReadFile(filepath.Join(modsDir, "jei-1.20.1-forge-15.20.0.106.jar"))
		if err != nil || string(got) != "jar-bytes" {
			t.Fatalf("run %d: mod not installed under its real name: %q, %v", run, got, err)
		}
	}

	if n := downloads.Load(); n != 1 {
		t.Errorf("mod downloaded %d times, want 1 (second run should hit the cache)", n)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "238222-5846880.jar")); err != nil {
		t.Errorf("cache entry not keyed by project and file ID: %v", err)
	}
}

// An interrupted download must not leave a file that later runs would treat
// as a complete cache entry.
func TestDownloadModInterruptedLeavesNoCacheEntry(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mods/238222/files/5846880" {
			fmt.Fprint(w, `{"data":{"fileName":"jei.jar","gameVersions":["1.20.1","Forge"]}}`)
			return
		}
		// Every download route (site and CDN fallback): promise more bytes
		// than are sent, then drop the connection.
		w.Header().Set("Content-Length", "1000")
		fmt.Fprint(w, "partial")
		panic(http.ErrAbortHandler)
	}))
	defer site.Close()

	cacheDir := t.TempDir()
	d := New(cacheDir, "", 1, true)
	d.siteAPIBase = site.URL
	d.cdnBase = site.URL

	if err := d.DownloadOne(238222, 5846880, t.TempDir()); err == nil {
		t.Fatal("expected the interrupted download to fail")
	}
	entries, _ := os.ReadDir(cacheDir)
	for _, e := range entries {
		t.Errorf("cache contains %q after a failed download", e.Name())
	}
}
