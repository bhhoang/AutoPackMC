package downloader

import (
	"crypto/sha1" // #nosec G505
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bhhoang/AutoPackMC/internal/parser"
)

const goodJar = "the real jar bytes"

func sha1Hex(s string) string {
	h := sha1.Sum([]byte(s)) // #nosec G401
	return hex.EncodeToString(h[:])
}

// checksumServer serves file metadata, a site download that returns
// siteBody, and a CDN download that returns cdnBody.
func checksumServer(t *testing.T, siteBody, cdnBody string, withHashes bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/mods/files":
			if !withHashes {
				http.Error(w, "no", http.StatusForbidden)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{
				"id": 5846880, "fileName": "jei.jar", "fileLength": len(goodJar),
				"hashes": []map[string]any{{"value": sha1Hex(goodJar), "algo": 1}, {"value": "md5", "algo": 2}},
			}}})
		case r.URL.Path == "/mods/238222/files/5846880":
			// The website API reports the size but no hash.
			fmt.Fprintf(w, `{"data":{"fileName":"jei.jar","gameVersions":["1.20.1"],"fileLength":%d}}`, len(goodJar))
		case strings.HasSuffix(r.URL.Path, "/download"):
			fmt.Fprint(w, siteBody)
		case strings.HasPrefix(r.URL.Path, "/5846/880/"):
			fmt.Fprint(w, cdnBody)
		default:
			http.NotFound(w, r)
		}
	}))
}

func newChecksumDownloader(t *testing.T, srv *httptest.Server) *Downloader {
	t.Helper()
	d := New(t.TempDir(), "key", 1, true)
	d.siteAPIBase = srv.URL
	d.officialAPIBase = srv.URL
	d.cdnBase = srv.URL
	return d
}

var checksumManifest = &parser.Manifest{Files: []parser.ModFile{{ProjectID: 238222, FileID: 5846880, Required: true}}}

// A truncated download fails the size check; the CDN copy is used instead.
func TestDownloadRejectsWrongSizeAndFallsBack(t *testing.T) {
	srv := checksumServer(t, "trunc", goodJar, false)
	defer srv.Close()
	d := newChecksumDownloader(t, srv)

	modsDir := t.TempDir()
	if err := d.DownloadMods(checksumManifest, modsDir); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(modsDir, "jei.jar")); string(got) != goodJar {
		t.Errorf("installed %q, want the CDN copy", got)
	}
}

// Same size, different content: only the SHA-1 from the batch lookup
// catches it.
func TestDownloadRejectsWrongSHA1(t *testing.T) {
	corrupt := strings.Repeat("x", len(goodJar))
	srv := checksumServer(t, corrupt, corrupt, true)
	defer srv.Close()
	d := newChecksumDownloader(t, srv)

	err := d.DownloadOne(238222, 5846880, t.TempDir())
	if err != nil {
		t.Fatalf("DownloadOne without a manifest has no SHA-1 and should pass on size: %v", err)
	}

	d = newChecksumDownloader(t, srv)
	modsDir := t.TempDir()
	err = d.DownloadMods(checksumManifest, modsDir)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("DownloadMods = %v, want ErrChecksumMismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(modsDir, "jei.jar")); !os.IsNotExist(statErr) {
		t.Error("a corrupt jar was installed")
	}
	entries, _ := os.ReadDir(d.CacheDir)
	for _, e := range entries {
		t.Errorf("cache contains %q after a checksum mismatch", e.Name())
	}
}

func TestDownloadAcceptsMatchingChecksum(t *testing.T) {
	srv := checksumServer(t, goodJar, goodJar, true)
	defer srv.Close()
	d := newChecksumDownloader(t, srv)

	modsDir := t.TempDir()
	if err := d.DownloadMods(checksumManifest, modsDir); err != nil {
		t.Fatal(err)
	}
	if size, sum := d.expectedChecksum(238222, 5846880); size != int64(len(goodJar)) || sum != sha1Hex(goodJar) {
		t.Errorf("expected checksum = %d, %q", size, sum)
	}
}
