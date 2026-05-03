// Package java provides helpers for automatically downloading a JDK from the
// Eclipse Temurin (Adoptium) distribution into a local directory.
package java

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/bhhoang/AutoPackMC/pkg/logger"
	"github.com/bhhoang/AutoPackMC/pkg/utils"
)

const (
	// adoptiumURL is the Eclipse Temurin binary download URL template.
	// Parameters: javaVersion, os, arch
	adoptiumURL = "https://api.adoptium.net/v3/binary/latest/%d/ga/%s/%s/jdk/hotspot/normal/eclipse"
)

// adoptiumOS maps GOOS values to the Adoptium API OS parameter.
var adoptiumOS = map[string]string{
	"linux":   "linux",
	"darwin":  "mac",
	"windows": "windows",
}

// adoptiumArch maps GOARCH values to the Adoptium API architecture parameter.
var adoptiumArch = map[string]string{
	"amd64": "x64",
	"arm64": "aarch64",
	"arm":   "arm",
}

// JavaBinaryName is the name of the java executable ("java" on Unix, "java.exe" on Windows).
func JavaBinaryName() string {
	if runtime.GOOS == "windows" {
		return "java.exe"
	}
	return "java"
}

// Download downloads Eclipse Temurin JDK of the given major version into
// destDir/jdk-<version>/ and returns the path to the java executable.
// If the JDK is already present, the download is skipped.
func Download(version int, destDir string) (string, error) {
	log := logger.Get()

	jdkDir := filepath.Join(destDir, fmt.Sprintf("jdk-%d", version))
	javaExe := filepath.Join(jdkDir, "bin", JavaBinaryName())

	// If already downloaded, skip.
	if utils.FileExists(javaExe) {
		log.Info().
			Int("version", version).
			Str("path", javaExe).
			Msg("JDK already present, skipping download")
		return javaExe, nil
	}

	osName, ok := adoptiumOS[runtime.GOOS]
	if !ok {
		return "", fmt.Errorf("unsupported OS for automatic Java download: %s", runtime.GOOS)
	}
	archName, ok := adoptiumArch[runtime.GOARCH]
	if !ok {
		return "", fmt.Errorf("unsupported architecture for automatic Java download: %s", runtime.GOARCH)
	}

	url := fmt.Sprintf(adoptiumURL, version, osName, archName)

	archiveExt := ".tar.gz"
	if runtime.GOOS == "windows" {
		archiveExt = ".zip"
	}
	archivePath := filepath.Join(destDir, fmt.Sprintf("jdk-%d%s", version, archiveExt))

	log.Info().
		Int("version", version).
		Str("url", url).
		Str("dest", archivePath).
		Msg("downloading JDK")

	if err := utils.DownloadFile(url, archivePath, nil); err != nil {
		return "", fmt.Errorf("download JDK %d: %w", version, err)
	}

	log.Info().
		Str("archive", archivePath).
		Str("dest", destDir).
		Msg("extracting JDK")

	extractDir := filepath.Join(destDir, fmt.Sprintf("_jdk-%d-extract", version))
	if err := utils.EnsureDir(extractDir); err != nil {
		return "", fmt.Errorf("create JDK extract dir: %w", err)
	}

	if runtime.GOOS == "windows" {
		if err := utils.ExtractZip(archivePath, extractDir); err != nil {
			return "", fmt.Errorf("extract JDK zip: %w", err)
		}
	} else {
		if err := utils.ExtractTarGz(archivePath, extractDir); err != nil {
			return "", fmt.Errorf("extract JDK tar.gz: %w", err)
		}
	}

	// The JDK archive contains a single top-level directory (e.g. jdk-21.0.3+9).
	// Move it to jdkDir for a predictable path.
	topDir, err := findSingleSubdir(extractDir)
	if err != nil {
		return "", fmt.Errorf("find extracted JDK root: %w", err)
	}

	if err := os.Rename(topDir, jdkDir); err != nil {
		return "", fmt.Errorf("rename JDK dir: %w", err)
	}
	_ = os.RemoveAll(extractDir)
	_ = os.Remove(archivePath)

	if !utils.FileExists(javaExe) {
		return "", fmt.Errorf("java binary not found at %q after extraction", javaExe)
	}

	log.Info().
		Int("version", version).
		Str("path", javaExe).
		Msg("JDK ready")

	return javaExe, nil
}

// findSingleSubdir returns the single sub-directory inside dir.
// It is used to locate the top-level directory inside a JDK archive extraction.
func findSingleSubdir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(dir, e.Name()))
		}
	}
	if len(dirs) == 1 {
		return dirs[0], nil
	}
	return "", fmt.Errorf("expected exactly one sub-directory in %q, found %d", dir, len(dirs))
}
