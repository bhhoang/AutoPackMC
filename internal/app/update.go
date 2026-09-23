package app

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UpdateInfo describes the newest IDISMAM release.
type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"` // newer than this build and downloadable here
	Dev       bool   `json:"dev"`       // this is a development build, which never updates
	PageURL   string `json:"pageUrl"`   // the release page, for updating by hand
}

// updater remembers the release found by the last check and the file
// downloaded for it.
type updater struct {
	mu      sync.Mutex
	latest  *release
	staged  string // downloaded and verified new executable
	running bool   // a download is in progress
}

type release struct {
	tag     string
	page    string
	exeURL  string
	exeName string
	exeSize int64
	sumsURL string
}

const updateUserAgent = "IDISMAM-updater"

func (s *Service) updateAPI() string {
	if s.cfg.UpdateAPI != "" {
		return s.cfg.UpdateAPI
	}
	return "https://api.github.com"
}

func (s *Service) exePath() (string, error) {
	if s.cfg.ExePath != "" {
		return s.cfg.ExePath, nil
	}
	return os.Executable()
}

// CheckForUpdate asks GitHub for the newest IDISMAM release.
func (s *Service) CheckForUpdate() (*UpdateInfo, error) {
	info := &UpdateInfo{Current: s.cfg.Version}
	cur, ok := parseVersion(s.cfg.Version)
	if !ok || s.cfg.UpdateRepo == "" {
		info.Dev = true
		return info, nil
	}

	req, err := http.NewRequest(http.MethodGet, s.updateAPI()+"/repos/"+s.cfg.UpdateRepo+"/releases/latest", nil)
	if err != nil {
		return nil, userError(err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", updateUserAgent)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, userError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return info, nil // no release published yet
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &Error{Code: "network", Detail: "GitHub answered " + resp.Status}
	}
	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, userError(err)
	}

	rel := &release{tag: body.TagName, page: body.HTMLURL}
	want := fmt.Sprintf("IDISMAM-%s-%s-%s.exe", body.TagName, goruntime.GOOS, goruntime.GOARCH)
	for _, a := range body.Assets {
		switch a.Name {
		case want:
			rel.exeURL, rel.exeName, rel.exeSize = a.URL, a.Name, a.Size
		case "SHA256SUMS.txt":
			rel.sumsURL = a.URL
		}
	}
	info.Latest, info.PageURL = rel.tag, rel.page
	latest, ok := parseVersion(rel.tag)
	info.Available = ok && newer(latest, cur) && rel.exeURL != "" && rel.sumsURL != ""

	s.update.mu.Lock()
	if info.Available {
		if s.update.latest == nil || s.update.latest.tag != rel.tag {
			s.update.staged = ""
		}
		s.update.latest = rel
	}
	s.update.mu.Unlock()
	return info, nil
}

// UpdateEvent is sent as "update" while the new version downloads.
type UpdateEvent struct {
	Done  int64 `json:"done"`
	Total int64 `json:"total"`
}

// DownloadUpdate downloads the release found by CheckForUpdate next to the
// running executable and checks it against the release's SHA256SUMS.txt.
// Progress arrives as "update" events.
func (s *Service) DownloadUpdate() error {
	s.update.mu.Lock()
	rel, staged, running := s.update.latest, s.update.staged, s.update.running
	if rel == nil || running {
		s.update.mu.Unlock()
		if running {
			return nil
		}
		return &Error{Code: "no_update"}
	}
	if staged != "" && exists(staged) {
		s.update.mu.Unlock()
		return nil
	}
	s.update.running = true
	s.update.mu.Unlock()
	defer func() {
		s.update.mu.Lock()
		s.update.running = false
		s.update.mu.Unlock()
	}()

	exe, err := s.exePath()
	if err != nil {
		return userError(err)
	}
	want, err := fetchChecksum(rel.sumsURL, rel.exeName)
	if err != nil {
		return err
	}
	target := exe + ".download"
	if err := downloadVerified(rel.exeURL, target, rel.exeSize, want, func(done, total int64) {
		s.ui.Emit("update", UpdateEvent{Done: done, Total: total})
	}); err != nil {
		_ = os.Remove(target)
		if errors.Is(err, os.ErrPermission) {
			return &Error{Code: "update_no_permission", Detail: err.Error()}
		}
		return userError(err)
	}
	s.update.mu.Lock()
	s.update.staged = target
	s.update.mu.Unlock()
	return nil
}

// ApplyUpdate puts the downloaded version in place of the running
// executable and returns its path. Windows lets a running program be renamed
// but not overwritten, so the old file is moved aside to ".old" first; the
// new version deletes it when it starts.
func (s *Service) ApplyUpdate() (string, error) {
	s.update.mu.Lock()
	staged := s.update.staged
	s.update.mu.Unlock()
	if staged == "" || !exists(staged) {
		return "", &Error{Code: "no_update"}
	}
	exe, err := s.exePath()
	if err != nil {
		return "", err
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return "", &Error{Code: "update_no_permission", Detail: err.Error()}
		}
		return "", userError(err)
	}
	if err := os.Rename(staged, exe); err != nil {
		_ = os.Rename(old, exe) // put the running version back
		return "", userError(err)
	}
	s.update.mu.Lock()
	s.update.staged = ""
	s.update.mu.Unlock()
	return exe, nil
}

// CleanUpAfterUpdate deletes what an update left next to exe: the previous
// version and any unfinished download.
func CleanUpAfterUpdate(exe string) {
	for _, leftover := range []string{exe + ".old", exe + ".download"} {
		for i := 0; i < 20 && exists(leftover); i++ {
			if os.Remove(leftover) == nil {
				break
			}
			time.Sleep(250 * time.Millisecond) // the old process may still be exiting
		}
	}
}

// fetchChecksum reads the SHA-256 listed for name in a SHA256SUMS.txt.
func fetchChecksum(url, name string) (string, error) {
	resp, err := httpGet(url)
	if err != nil {
		return "", userError(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", &Error{Code: "update_bad_file", Detail: "no checksum listed for " + name}
}

// downloadVerified downloads url to path and checks its size and SHA-256.
func downloadVerified(url, path string, size int64, sha string, progress func(done, total int64)) error {
	resp, err := httpGet(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	total := size
	if total <= 0 {
		total = resp.ContentLength
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	h := sha256.New()
	var done int64
	buf := make([]byte, 256<<10)
	last := time.Time{}
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return werr
			}
			h.Write(buf[:n])
			done += int64(n)
			if time.Since(last) > 150*time.Millisecond {
				progress(done, total)
				last = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return rerr
		}
	}
	progress(done, total)
	if err := f.Close(); err != nil {
		return err
	}
	if size > 0 && done != size {
		return &Error{Code: "update_bad_file", Detail: fmt.Sprintf("got %d bytes, want %d", done, size)}
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sha {
		return &Error{Code: "update_bad_file", Detail: "checksum does not match"}
	}
	return nil
}

func httpGet(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", updateUserAgent)
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &Error{Code: "network", Detail: url + ": " + resp.Status}
	}
	return resp, nil
}

// parseVersion reads "v1.2.3" (a "-suffix" is ignored).
func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func newer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
