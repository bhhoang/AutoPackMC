package utils

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	unrar "github.com/markpendlebury/gounrar"
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

// ExtractArchive extracts a ZIP or RAR archive from src into the dest directory.
func ExtractArchive(src, dest string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	content := string(data)
	if strings.HasPrefix(content, "PK") || strings.Contains(content, "application/zip") {
		return extractZip(src, dest)
	}
	if strings.HasPrefix(content, "Rar!") || strings.Contains(content, "application/x-rar") || isRARBinary(data) {
		return extractRar(src, dest)
	}

	ext := strings.ToLower(filepath.Ext(src))
	if ext == ".zip" {
		return extractZip(src, dest)
	}
	if ext == ".rar" {
		return extractRar(src, dest)
	}

	return fmt.Errorf("unsupported archive format for file: %s", src)
}

func isRARBinary(data []byte) bool {
	return len(data) >= 6 && string(data[:6]) == "Rar!\x1a"
}

// ExtractZip extracts a ZIP archive from src into the dest directory.
func ExtractZip(src, dest string) error {
	return extractZip(src, dest)
}

func extractZip(src, dest string) error {
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

func extractRar(src, dest string) error {
	if err := EnsureDir(dest); err != nil {
		return err
	}

	if err := unrar.RarExtractor(src, dest); err != nil {
		return fmt.Errorf("extract RAR: %w", err)
	}
	return nil
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

// ExtractTarGz extracts a .tar.gz archive from src into the dest directory.
func ExtractTarGz(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open tar.gz %q: %w", src, err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader for %q: %w", src, err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	if err := EnsureDir(dest); err != nil {
		return err
	}

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		target := filepath.Join(dest, filepath.FromSlash(header.Name))

		// Guard against path traversal
		if !isSubPath(dest, target) {
			return fmt.Errorf("tar entry %q escapes destination directory", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := EnsureDir(target); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := EnsureDir(filepath.Dir(target)); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, header.FileInfo().Mode())
			if err != nil {
				return fmt.Errorf("create %q: %w", target, err)
			}
			if _, err := io.Copy(out, tr); err != nil { // #nosec G110 — controlled tar extraction
				out.Close()
				return fmt.Errorf("write %q: %w", target, err)
			}
			out.Close()
			// Symbolic links are intentionally skipped. Extracting symlinks from
			// untrusted archives carries path-traversal risk and is not required
			// for the JDK binaries this function is designed to unpack.
		}
	}
	return nil
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

var driveFileIDRegex = regexp.MustCompile(`/d/([a-zA-Z0-9_-]+)`)

func ExtractGoogleDriveFileID(link string) (string, error) {
	u, err := url.Parse(link)
	if err != nil {
		return "", err
	}

	matches := driveFileIDRegex.FindStringSubmatch(u.Path)
	if len(matches) > 1 {
		return matches[1], nil
	}

	matches = driveFileIDRegex.FindStringSubmatch(link)
	if len(matches) > 1 {
		return matches[1], nil
	}

	return "", fmt.Errorf("no Google Drive file ID found in %q", link)
}

func DownloadGoogleDriveFile(fileID, dest string) error {
	downloadURL := fmt.Sprintf("https://drive.google.com/uc?export=download&id=%s", fileID)

	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d for Google Drive file %s", resp.StatusCode, fileID)
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/zip") || strings.Contains(contentType, "application/x-zip-compressed") || strings.Contains(contentType, "application/octet-stream") || strings.Contains(contentType, "application/x-rar-compressed") {
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		defer out.Close()

		_, err = io.Copy(out, resp.Body)
		return err
	}

	body, _ := io.ReadAll(resp.Body)
	content := string(body)

	if strings.Contains(content, "attachment") || strings.Contains(content, "filename=") {
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		defer out.Close()

		_, err = out.WriteString(content)
		return err
	}

	confirmRegex := regexp.MustCompile(`name="confirm" value="([^"]+)"`)
	matches := confirmRegex.FindStringSubmatch(content)
	var confirmToken string
	if len(matches) > 1 {
		confirmToken = matches[1]
	} else {
		confirmToken = "t"
	}

	uuidRegex := regexp.MustCompile(`name="uuid" value="([^"]+)"`)
	uuidMatches := uuidRegex.FindStringSubmatch(content)
	var uuidToken string
	if len(uuidMatches) > 1 {
		uuidToken = uuidMatches[1]
	}

	cookies := resp.Cookies()
	downloadURL = fmt.Sprintf("https://drive.usercontent.google.com/download?id=%s&export=download&confirm=%s", fileID, confirmToken)
	if uuidToken != "" {
		downloadURL += "&uuid=" + uuidToken
	}

	downloadReq, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}
	for _, c := range cookies {
		downloadReq.AddCookie(c)
	}

	resp2, err := http.DefaultClient.Do(downloadReq)
	if err != nil {
		return err
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d downloading from %s", resp2.StatusCode, downloadURL)
	}

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp2.Body)
	return err
}
