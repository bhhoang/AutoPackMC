package downloader

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// newTestDownloader returns a Downloader whose CurseForge endpoints point at
// the supplied test servers.
func newTestDownloader(t *testing.T, apiKey string) *Downloader {
	t.Helper()
	d := New(t.TempDir(), apiKey, 1, false)
	return d
}

// TestCDNURL pins the forgecdn path layout: files/<id/1000>/<id%1000>/<name>,
// with no zero padding on the second segment.
func TestCDNURL(t *testing.T) {
	got := cdnURL(cfCDNBase, 8880075, "jei-26.2-neoforge-30.32.0.220.jar")
	want := "https://mediafilez.forgecdn.net/files/8880/75/jei-26.2-neoforge-30.32.0.220.jar"
	if got != want {
		t.Fatalf("cdnURL = %q, want %q", got, want)
	}
	if got := cdnURL(cfCDNBase, 5922047, ""); got != "" {
		t.Fatalf("cdnURL with empty filename = %q, want empty", got)
	}
}

// TestFetchFileInfoDelistedWithoutAPIKey covers the reported failure: the site
// API answers HTTP 200 with {"data":null} for a delisted file. That must be
// reported as unavailable instead of yielding a fabricated download URL.
func TestFetchFileInfoDelistedWithoutAPIKey(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":null}`)
	}))
	defer site.Close()

	d := newTestDownloader(t, "")
	d.siteAPIBase = site.URL

	_, err := d.fetchFileInfo(1001614, 5922047)
	if !errors.Is(err, ErrFileUnavailable) {
		t.Fatalf("fetchFileInfo error = %v, want ErrFileUnavailable", err)
	}
}

// TestFetchFileInfoDelistedFallsBackToOfficialAPI verifies that when the public
// site API has no metadata and an API key is configured, the official API is
// used to recover the real filename and download URL.
func TestFetchFileInfoDelistedFallsBackToOfficialAPI(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":null}`)
	}))
	defer site.Close()

	var gotKey string
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		if r.URL.Path != "/mods/1001614/files/5922047" {
			t.Errorf("official API path = %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":{"id":5922047,"fileName":"lionfishapi-2.0.jar",
			"downloadUrl":"https://mediafilez.forgecdn.net/files/5922/47/lionfishapi-2.0.jar",
			"gameVersions":["1.21.1","NeoForge"]}}`)
	}))
	defer official.Close()

	d := newTestDownloader(t, "test-key")
	d.siteAPIBase = site.URL
	d.officialAPIBase = official.URL

	fi, err := d.fetchFileInfo(1001614, 5922047)
	if err != nil {
		t.Fatalf("fetchFileInfo: %v", err)
	}
	if gotKey != "test-key" {
		t.Errorf("x-api-key header = %q, want %q", gotKey, "test-key")
	}
	if fi.FileName != "lionfishapi-2.0.jar" {
		t.Errorf("FileName = %q", fi.FileName)
	}
	if fi.DownloadURL != "https://mediafilez.forgecdn.net/files/5922/47/lionfishapi-2.0.jar" {
		t.Errorf("DownloadURL = %q", fi.DownloadURL)
	}
}

// TestFetchFileInfoOfficialWithoutDownloadURL covers files whose author disabled
// third-party downloads: the official API returns null downloadUrl, so the CDN
// path has to be reconstructed from the file ID and name.
func TestFetchFileInfoOfficialWithoutDownloadURL(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":null}`)
	}))
	defer site.Close()
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"id":5922047,"fileName":"lionfishapi-2.0.jar","downloadUrl":null}}`)
	}))
	defer official.Close()

	d := newTestDownloader(t, "test-key")
	d.siteAPIBase = site.URL
	d.officialAPIBase = official.URL

	fi, err := d.fetchFileInfo(1001614, 5922047)
	if err != nil {
		t.Fatalf("fetchFileInfo: %v", err)
	}
	want := cdnURL(cfCDNBase, 5922047, "lionfishapi-2.0.jar")
	if fi.DownloadURL != want {
		t.Errorf("DownloadURL = %q, want %q", fi.DownloadURL, want)
	}
}

// TestDownloadModFallsBackToCDN verifies that a 404 on the site download
// endpoint is retried against the CDN when the filename is known.
func TestDownloadModFallsBackToCDN(t *testing.T) {
	var siteHits int
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		siteHits++
		http.Error(w, "Not Found", http.StatusNotFound)
	}))
	defer site.Close()

	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/files/5922/47/lionfishapi-2.0.jar" {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		fmt.Fprint(w, "JAR-BYTES")
	}))
	defer cdn.Close()

	d := newTestDownloader(t, "")
	d.siteAPIBase = site.URL
	d.cdnBase = cdn.URL + "/files"

	destDir := t.TempDir()
	err := d.downloadMod(Task{
		ProjectID:        1001614,
		FileID:           5922047,
		DestDir:          destDir,
		ResolvedFilename: "lionfishapi-2.0.jar",
	})
	if err != nil {
		t.Fatalf("downloadMod: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(destDir, "lionfishapi-2.0.jar"))
	if err != nil {
		t.Fatalf("read downloaded mod: %v", err)
	}
	if string(got) != "JAR-BYTES" {
		t.Errorf("downloaded content = %q", got)
	}
	// A 404 is terminal; it must not be retried.
	if siteHits != 1 {
		t.Errorf("site download attempts = %d, want 1", siteHits)
	}
}
