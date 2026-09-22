package java

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeJDKArchive builds a JDK archive in the format Download expects for the
// current OS: one top-level directory containing bin/java.
func fakeJDKArchive(t *testing.T) []byte {
	t.Helper()
	name := "jdk-17.0.99+1/bin/" + JavaBinaryName()
	var buf bytes.Buffer
	if runtime.GOOS == "windows" {
		zw := zip.NewWriter(&buf)
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(w, "fake java")
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, dir := range []string{"jdk-17.0.99+1/", "jdk-17.0.99+1/bin/"} {
		if err := tw.WriteHeader(&tar.Header{Name: dir, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: 9}); err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(tw, "fake java")
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func fakeAdoptium(t *testing.T, archive []byte, checksum string) {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/latest/17/hotspot") {
			fmt.Fprintf(w, `[{"binary":{"package":{"name":"jdk.archive","link":"%s/archive","checksum":"%s"}}}]`, srv.URL, checksum)
			return
		}
		if r.URL.Path == "/archive" {
			_, _ = w.Write(archive)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	old := adoptiumAPIBase
	adoptiumAPIBase = srv.URL
	t.Cleanup(func() { adoptiumAPIBase = old })
}

func TestDownloadVerifiesChecksum(t *testing.T) {
	if _, ok := adoptiumOS[runtime.GOOS]; !ok {
		t.Skip("unsupported OS")
	}
	archive := fakeJDKArchive(t)
	sum := sha256.Sum256(archive)
	fakeAdoptium(t, archive, hex.EncodeToString(sum[:]))

	dir := t.TempDir()
	javaPath, err := Download(17, dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "jdk-17", "bin", JavaBinaryName()); javaPath != want {
		t.Errorf("java path = %q, want %q", javaPath, want)
	}
}

func TestDownloadRejectsCorruptArchive(t *testing.T) {
	if _, ok := adoptiumOS[runtime.GOOS]; !ok {
		t.Skip("unsupported OS")
	}
	fakeAdoptium(t, fakeJDKArchive(t), strings.Repeat("0", 64))

	dir := t.TempDir()
	_, err := Download(17, dir)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Download = %v, want a checksum mismatch", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		t.Errorf("%q left behind after a rejected download", e.Name())
	}
}
