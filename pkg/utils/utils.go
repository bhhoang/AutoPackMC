package utils

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// EnsureDir creates a directory (and any parents) if it does not exist.
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

// FileExists returns true if the path exists and is a regular file.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// DirExists returns true if the path exists and is a directory.
func DirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ExtractZip extracts a ZIP archive from src into the dest directory.
func ExtractZip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("open zip %q: %w", src, err)
	}
	defer r.Close()

	if err := EnsureDir(dest); err != nil {
		return err
	}

	for _, f := range r.File {
		target := filepath.Join(dest, filepath.FromSlash(f.Name))

		// Guard against zip-slip
		if !isSubPath(dest, target) {
			return fmt.Errorf("zip entry %q escapes destination directory", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := EnsureDir(target); err != nil {
				return err
			}
			continue
		}

		if err := EnsureDir(filepath.Dir(target)); err != nil {
			return err
		}

		if err := extractFile(f, target); err != nil {
			return fmt.Errorf("extract %q: %w", f.Name, err)
		}
	}
	return nil
}

func isSubPath(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	// Reject any path component that navigates above base
	return !strings.HasPrefix(rel, "..")
}

func extractFile(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc) // #nosec G110 — controlled ZIP extraction
	return err
}

// CopyDir recursively copies src directory to dest.
func CopyDir(src, dest string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)

		if info.IsDir() {
			return EnsureDir(target)
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := EnsureDir(filepath.Dir(dest)); err != nil {
		return err
	}

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// FindFiles returns all files under dir whose base name matches the glob pattern.
func FindFiles(dir, pattern string) ([]string, error) {
	var matches []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		matched, err := filepath.Match(pattern, filepath.Base(path))
		if err != nil {
			return err
		}
		if matched {
			matches = append(matches, path)
		}
		return nil
	})
	return matches, err
}

// DownloadFile downloads url to dest with the provided headers, retrying up to 3 times
// with exponential back-off (1s, 2s, 4s).
func DownloadFile(url, dest string, headers map[string]string) error {
	if err := EnsureDir(filepath.Dir(dest)); err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(1<<(attempt-1)) * time.Second)
		}
		lastErr = downloadOnce(url, dest, headers)
		if lastErr == nil {
			return nil
		}
	}
	return fmt.Errorf("download %q after 3 attempts: %w", url, lastErr)
}

func downloadOnce(url, dest string, headers map[string]string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}
