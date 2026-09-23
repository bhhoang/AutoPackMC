package downloader

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bhhoang/IDISMAM/internal/parser"
)

// siteNullServer answers every request the way CurseForge's website API answers
// for a file it no longer publishes.
func siteNullServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":null}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestFailedModsRecordsPageURL verifies that a mod which cannot be downloaded by
// any route is collected with the CurseForge page a human can download it from.
func TestFailedModsRecordsPageURL(t *testing.T) {
	site := siteNullServer(t)

	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mods/1001614":
			fmt.Fprint(w, `{"data":{"id":1001614,"name":"Lionfish API","slug":"lionfish-api",
				"links":{"websiteUrl":"https://www.curseforge.com/minecraft/mc-mods/lionfish-api"}}}`)
		default: // the file itself is gone from the official API too
			http.Error(w, "Not Found", http.StatusNotFound)
		}
	}))
	defer official.Close()

	d := New(t.TempDir(), "test-key", 1, false)
	d.siteAPIBase = site.URL
	d.officialAPIBase = official.URL

	manifest := &parser.Manifest{Files: []parser.ModFile{
		{ProjectID: 1001614, FileID: 5922047, Required: true},
	}}
	if err := d.DownloadMods(manifest, t.TempDir()); err != nil {
		t.Fatalf("DownloadMods: %v", err)
	}

	failed := d.FailedMods()
	if len(failed) != 1 {
		t.Fatalf("FailedMods() = %d entries, want 1", len(failed))
	}
	got := failed[0]
	want := "https://www.curseforge.com/minecraft/mc-mods/lionfish-api/download/5922047"
	if got.PageURL != want {
		t.Errorf("PageURL = %q, want %q", got.PageURL, want)
	}
	if got.Name != "Lionfish API" {
		t.Errorf("Name = %q, want %q", got.Name, "Lionfish API")
	}
	if got.ProjectID != 1001614 || got.FileID != 5922047 {
		t.Errorf("ids = %d/%d", got.ProjectID, got.FileID)
	}
	if got.Err == nil {
		t.Error("Err = nil, want the download failure")
	}
}

// TestFailedModsFallbackPageURL covers mods whose name cannot be resolved: the
// numeric project URL still lands the user on the right page.
func TestFailedModsFallbackPageURL(t *testing.T) {
	site := siteNullServer(t)

	d := New(t.TempDir(), "", 1, false) // no API key: no official API at all
	d.siteAPIBase = site.URL

	manifest := &parser.Manifest{Files: []parser.ModFile{
		{ProjectID: 1001614, FileID: 5922047, Required: false},
	}}
	if err := d.DownloadMods(manifest, t.TempDir()); err != nil {
		t.Fatalf("DownloadMods: %v", err)
	}

	failed := d.FailedMods()
	if len(failed) != 1 {
		t.Fatalf("FailedMods() = %d entries, want 1", len(failed))
	}
	want := "https://www.curseforge.com/projects/1001614"
	if failed[0].PageURL != want {
		t.Errorf("PageURL = %q, want %q", failed[0].PageURL, want)
	}
}

// TestFailedModsDeduplicated guards against the same mod being reported twice
// when it is retried through several code paths.
func TestFailedModsDeduplicated(t *testing.T) {
	site := siteNullServer(t)
	d := New(t.TempDir(), "", 1, false)
	d.siteAPIBase = site.URL

	manifest := &parser.Manifest{Files: []parser.ModFile{
		{ProjectID: 1001614, FileID: 5922047},
		{ProjectID: 1001614, FileID: 5922047},
	}}
	if err := d.DownloadMods(manifest, t.TempDir()); err != nil {
		t.Fatalf("DownloadMods: %v", err)
	}
	if got := len(d.FailedMods()); got != 1 {
		t.Fatalf("FailedMods() = %d entries, want 1", got)
	}
}

// TestFailedModsFromMissingModsPath verifies the same reporting happens on the
// "top up an existing mods folder" path.
func TestFailedModsFromMissingModsPath(t *testing.T) {
	site := siteNullServer(t)
	d := New(t.TempDir(), "", 1, false)
	d.siteAPIBase = site.URL

	manifest := &parser.Manifest{Files: []parser.ModFile{
		{ProjectID: 1001614, FileID: 5922047, Required: true},
	}}
	if err := d.DownloadMissingMods(manifest, t.TempDir()); err != nil {
		t.Fatalf("DownloadMissingMods: %v", err)
	}
	failed := d.FailedMods()
	if len(failed) != 1 {
		t.Fatalf("FailedMods() = %d entries, want 1", len(failed))
	}
	if !strings.Contains(failed[0].PageURL, "1001614") {
		t.Errorf("PageURL = %q", failed[0].PageURL)
	}
}
