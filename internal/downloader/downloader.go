package downloader

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bhhoang/AutoPackMC/internal/parser"
	"github.com/bhhoang/AutoPackMC/pkg/logger"
	"github.com/bhhoang/AutoPackMC/pkg/utils"
)

const (
	defaultWorkers = 4
	maxRetries     = 3
	cacheKeyFormat = "%d-%d.jar" // <projectID>-<fileID>.jar — used for cache entries without a resolved filename

	// cfSiteAPIBase is the unauthenticated API behind the CurseForge website.
	// It only exposes files that are publicly listed on the project page.
	cfSiteAPIBase = "https://www.curseforge.com/api/v1"
	// cfOfficialAPIBase is the official API (requires an API key). It still
	// serves metadata for files the website no longer lists, which is what
	// modpack manifests routinely reference.
	cfOfficialAPIBase = "https://api.curseforge.com/v1"
	// cfCDNBase is the CDN that actually serves the JAR files.
	cfCDNBase = "https://mediafilez.forgecdn.net/files"
)

// ErrFileUnavailable means CurseForge published no metadata for the requested
// file: the site API answered with a null payload (delisted/removed file) or
// with 404. Fabricating a download URL in that situation only produces a
// confusing HTTP 404 later on, so resolution stops here instead.
var ErrFileUnavailable = errors.New("file not available on CurseForge")

// FileInfo holds metadata about a mod file returned by the CurseForge API.
type FileInfo struct {
	FileName     string
	DownloadURL  string
	GameVersions []string
	Size         int64  // expected size in bytes; 0 when unknown
	SHA1         string // expected SHA-1 (hex); empty when unknown
}

// IsClientOnly returns true when the file is tagged for the Client side but
// not for the Server side, indicating it should not be installed on a server.
func (fi *FileInfo) IsClientOnly() bool {
	var hasClient, hasServer bool
	for _, v := range fi.GameVersions {
		switch v {
		case "Client":
			hasClient = true
		case "Server":
			hasServer = true
		}
	}
	return hasClient && !hasServer
}

// Task represents a single mod download request.
type Task struct {
	ProjectID        int
	FileID           int
	Required         bool
	DestDir          string
	ResolvedFilename string // optional: pre-resolved filename; if set, skips the API filename lookup
}

// Downloader manages the worker pool for parallel mod downloads.
type Downloader struct {
	Workers          int
	CacheDir         string
	APIKey           string
	FilterClientOnly bool // when true, mods tagged Client-only (no Server tag) are skipped

	fileInfoMu    sync.RWMutex
	fileInfoCache map[string]*FileInfo

	failedMu     sync.Mutex
	failed       []FailedMod
	modInfoCache map[int]modInfo // guarded by failedMu

	// Projects skipped because the exclude list marks them client-only,
	// mapped to their slugs. Set by ApplyExcludeList.
	excludedMu       sync.RWMutex
	excludedProjects map[int]string

	// SHA-1 of manifest files by file ID, from prefetchHashes.
	hashMu     sync.RWMutex
	fileHashes map[int]string

	// Endpoints, overridable in tests.
	siteAPIBase     string
	officialAPIBase string
	cdnBase         string
}

// New creates a Downloader with sensible defaults.
// filterClientOnly controls whether client-only mods are skipped during download.
func New(cacheDir, apiKey string, workers int, filterClientOnly bool) *Downloader {
	if workers <= 0 {
		workers = defaultWorkers
	}
	return &Downloader{
		Workers:          workers,
		CacheDir:         cacheDir,
		APIKey:           apiKey,
		FilterClientOnly: filterClientOnly,
		fileInfoCache:    make(map[string]*FileInfo),
		modInfoCache:     make(map[int]modInfo),
		siteAPIBase:      cfSiteAPIBase,
		officialAPIBase:  cfOfficialAPIBase,
		cdnBase:          cfCDNBase,
	}
}

// DownloadMods downloads all files specified in manifest into destDir.
func (d *Downloader) DownloadMods(manifest *parser.Manifest, destDir string) error {
	log := logger.Get()

	if err := utils.EnsureDir(destDir); err != nil {
		return fmt.Errorf("create mods dir: %w", err)
	}
	d.prefetchHashes(manifest)

	tasks := make(chan Task, len(manifest.Files))
	for _, f := range manifest.Files {
		tasks <- Task{
			ProjectID: f.ProjectID,
			FileID:    f.FileID,
			Required:  f.Required,
			DestDir:   destDir,
		}
	}
	close(tasks)

	var wg sync.WaitGroup
	errs := make(chan error, len(manifest.Files))

	for i := 0; i < d.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range tasks {
				if err := d.downloadMod(t); err != nil {
					d.recordFailure(t.ProjectID, t.FileID, t.ResolvedFilename, err)
					if t.Required && !isMissingFileErr(err) {
						errs <- err
					} else {
						log.Warn().Err(err).
							Int("projectID", t.ProjectID).
							Int("fileID", t.FileID).
							Msg("mod download failed, skipping")
					}
				}
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// logClientOnlySkip emits an info-level log indicating a client-only mod was skipped.
func logClientOnlySkip(projectID, fileID int, fi *FileInfo) {
	logger.Get().Info().
		Int("projectID", projectID).
		Int("fileID", fileID).
		Str("filename", fi.FileName).
		Strs("gameVersions", fi.GameVersions).
		Msg("skipping client-only mod")
}

func (d *Downloader) downloadMod(t Task) error {
	log := logger.Get()

	var downloadURL, filename string
	if t.ResolvedFilename != "" {
		// Filename already resolved upstream; build the download URL directly to
		// avoid a redundant API round-trip.
		filename = t.ResolvedFilename
		downloadURL = fmt.Sprintf("%s/mods/%d/files/%d/download", d.siteAPIBase, t.ProjectID, t.FileID)
	} else {
		if slug, ok := d.excludedSlug(t.ProjectID); ok && d.FilterClientOnly {
			logExcludedSkip(t.ProjectID, t.FileID, slug)
			return nil
		}
		fi, err := d.fetchFileInfo(t.ProjectID, t.FileID)
		if err != nil {
			return err
		}
		if d.FilterClientOnly && fi.IsClientOnly() {
			logClientOnlySkip(t.ProjectID, t.FileID, fi)
			return nil
		}
		downloadURL = fi.DownloadURL
		filename = fi.FileName
	}

	destFilename := filename
	if destFilename == "" {
		destFilename = fmt.Sprintf(cacheKeyFormat, t.ProjectID, t.FileID)
	}
	destFile := filepath.Join(t.DestDir, destFilename)

	// Cache entries are keyed by project and file ID, which unlike filenames
	// are unique. Downloads are written atomically, so an entry is complete.
	cacheFile := filepath.Join(d.CacheDir, fmt.Sprintf(cacheKeyFormat, t.ProjectID, t.FileID))
	if utils.FileExists(cacheFile) {
		log.Debug().
			Int("projectID", t.ProjectID).
			Int("fileID", t.FileID).
			Str("filename", destFilename).
			Msg("cache hit, copying from cache")
		return copyFileSimple(cacheFile, destFile)
	}

	if err := utils.EnsureDir(d.CacheDir); err != nil {
		return err
	}

	log.Info().
		Int("projectID", t.ProjectID).
		Int("fileID", t.FileID).
		Str("url", downloadURL).
		Msg("downloading mod")

	headers := map[string]string{}
	if d.APIKey != "" {
		headers["X-Api-Key"] = d.APIKey
	}

	// The website /download endpoint 404s for files it no longer lists, while
	// the CDN keeps serving them; try it as a fallback whenever the filename is
	// known.
	candidates := []string{downloadURL}
	if fallback := cdnURL(d.cdnBase, t.FileID, filename); fallback != "" && fallback != downloadURL {
		candidates = append(candidates, fallback)
	}

	// Downloads land in a temporary file and only enter the cache once they
	// match the size and SHA-1 CurseForge publishes, so the cache never serves
	// a corrupt jar.
	size, sum := d.expectedChecksum(t.ProjectID, t.FileID)
	tmpFile := cacheFile + ".verify"
	var downloadErr error
	for i, candidate := range candidates {
		if i > 0 {
			log.Debug().
				Int("projectID", t.ProjectID).
				Int("fileID", t.FileID).
				Str("url", candidate).
				Err(downloadErr).
				Msg("retrying download from the CurseForge CDN")
		}
		downloadErr = downloadWithRetry(candidate, tmpFile, headers)
		if downloadErr == nil {
			downloadErr = verifyFile(tmpFile, size, sum)
		}
		if downloadErr == nil {
			downloadErr = os.Rename(tmpFile, cacheFile)
		}
		if downloadErr == nil {
			break
		}
		_ = os.Remove(tmpFile)
	}
	if downloadErr != nil {
		return fmt.Errorf("download mod %d/%d: %w", t.ProjectID, t.FileID, downloadErr)
	}

	return copyFileSimple(cacheFile, destFile)
}

// DownloadOne downloads a single mod file into destDir, resolving its filename
// and download URL from CurseForge.
func (d *Downloader) DownloadOne(projectID, fileID int, destDir string) error {
	return d.downloadMod(Task{ProjectID: projectID, FileID: fileID, Required: true, DestDir: destDir})
}

// fetchFileInfo fetches file metadata (filename, download URL, and game versions)
// from the CurseForge public API.
func fileInfoCacheKey(projectID, fileID int) string {
	return fmt.Sprintf("%d/%d", projectID, fileID)
}

func (d *Downloader) fetchFileInfo(projectID, fileID int) (*FileInfo, error) {
	log := logger.Get()

	cacheKey := fileInfoCacheKey(projectID, fileID)
	d.fileInfoMu.RLock()
	if cached, ok := d.fileInfoCache[cacheKey]; ok {
		d.fileInfoMu.RUnlock()
		return cached, nil
	}
	d.fileInfoMu.RUnlock()

	fi, err := d.fetchFileInfoFromSite(projectID, fileID)
	if errors.Is(err, ErrFileUnavailable) {
		// Files pulled from the public project page (deleted, archived or
		// hidden releases) are still resolvable through the official API.
		if d.APIKey == "" {
			return nil, fmt.Errorf("%w: project %d file %d is not published on the website; "+
				"set a CurseForge API key (--api-key or MCPACKCTL_CURSEFORGE_API_KEY) to resolve it via the official API",
				ErrFileUnavailable, projectID, fileID)
		}
		log.Debug().
			Int("projectID", projectID).
			Int("fileID", fileID).
			Msg("file not listed on the website, falling back to the official CurseForge API")
		fi, err = d.fetchFileInfoFromOfficialAPI(projectID, fileID)
	}
	if err != nil {
		return nil, err
	}

	d.fileInfoMu.Lock()
	d.fileInfoCache[cacheKey] = fi
	d.fileInfoMu.Unlock()

	log.Debug().
		Int("projectID", projectID).
		Int("fileID", fileID).
		Str("filename", fi.FileName).
		Str("url", fi.DownloadURL).
		Strs("gameVersions", fi.GameVersions).
		Msg("resolved file info from CurseForge")

	return fi, nil
}

// fetchFileInfoFromSite queries the unauthenticated website API. A missing file
// is reported there as HTTP 200 with a null data payload, which must be treated
// as an error rather than as an empty-but-valid file.
func (d *Downloader) fetchFileInfoFromSite(projectID, fileID int) (*FileInfo, error) {
	fileInfoURL := fmt.Sprintf("%s/mods/%d/files/%d", d.siteAPIBase, projectID, fileID)
	resp, err := http.Get(fileInfoURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrFileUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CurseForge API returned HTTP %d for project %d file %d", resp.StatusCode, projectID, fileID)
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Data *struct {
			FileName     string   `json:"fileName"`
			GameVersions []string `json:"gameVersions"`
			FileLength   int64    `json:"fileLength"`
		} `json:"data"`
	}
	if jsonErr := json.Unmarshal(body, &result); jsonErr != nil {
		return nil, fmt.Errorf("parse CurseForge API response: %w", jsonErr)
	}
	if result.Data == nil || result.Data.FileName == "" {
		return nil, ErrFileUnavailable
	}

	return &FileInfo{
		FileName: result.Data.FileName,
		// The website API exposes no download URL; its /download endpoint
		// redirects to the CDN for every file it lists.
		DownloadURL:  fmt.Sprintf("%s/mods/%d/files/%d/download", d.siteAPIBase, projectID, fileID),
		GameVersions: result.Data.GameVersions,
		Size:         result.Data.FileLength,
	}, nil
}

// fetchFileInfoFromOfficialAPI queries api.curseforge.com using the configured
// API key. Note that it reports game versions without the website's
// Client/Server tags, so IsClientOnly reports false for files resolved here and
// they are downloaded rather than filtered out.
func (d *Downloader) fetchFileInfoFromOfficialAPI(projectID, fileID int) (*FileInfo, error) {
	apiURL := fmt.Sprintf("%s/mods/%d/files/%d", d.officialAPIBase, projectID, fileID)
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", d.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: project %d file %d is unknown to the official CurseForge API", ErrFileUnavailable, projectID, fileID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("official CurseForge API returned HTTP %d for project %d file %d", resp.StatusCode, projectID, fileID)
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Data *officialFile `json:"data"`
	}
	if jsonErr := json.Unmarshal(body, &result); jsonErr != nil {
		return nil, fmt.Errorf("parse official CurseForge API response: %w", jsonErr)
	}
	if result.Data == nil || result.Data.FileName == "" {
		return nil, fmt.Errorf("%w: project %d file %d returned no metadata", ErrFileUnavailable, projectID, fileID)
	}

	downloadURL := result.Data.DownloadURL
	if downloadURL == "" {
		// Authors can opt out of third-party downloads, which nulls out
		// downloadUrl; the CDN still serves the file at its canonical path.
		downloadURL = cdnURL(d.cdnBase, fileID, result.Data.FileName)
	}

	return &FileInfo{
		FileName:     result.Data.FileName,
		DownloadURL:  downloadURL,
		GameVersions: result.Data.GameVersions,
		Size:         result.Data.FileLength,
		SHA1:         result.Data.sha1(),
	}, nil
}

// cdnURL builds the canonical forgecdn path for a file: the ID is split into
// its thousands and remainder parts (5922047 -> 5922/47, no zero padding).
// It returns an empty string when the filename is unknown.
func cdnURL(base string, fileID int, fileName string) string {
	if fileName == "" {
		return ""
	}
	return fmt.Sprintf("%s/%d/%d/%s", base, fileID/1000, fileID%1000, url.PathEscape(fileName))
}

// downloadWithRetry retries transient failures with exponential back-off.
// Permanent failures (404 and friends) return immediately: retrying a missing
// file nine times only delays the fallback to the CDN.
func downloadWithRetry(downloadURL, dest string, headers map[string]string) error {
	var lastErr error
	attempts := 0
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(1<<(attempt-1)) * time.Second)
		}
		attempts++
		lastErr = utils.DownloadFileOnce(downloadURL, dest, headers)
		if lastErr == nil {
			return nil
		}
		if !utils.IsRetryable(lastErr) {
			break
		}
	}
	return fmt.Errorf("download %q after %d attempts: %w", downloadURL, attempts, lastErr)
}

// isMissingFileErr reports whether err means the file simply is not available
// on CurseForge, in which case the pack build continues without it instead of
// aborting.
func isMissingFileErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrFileUnavailable) {
		return true
	}
	var statusErr *utils.HTTPStatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode == http.StatusNotFound
	}
	return strings.Contains(err.Error(), "HTTP 404")
}

func copyFileSimple(src, dst string) error {
	if err := utils.EnsureDir(filepath.Dir(dst)); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// DownloadMissingMods checks which mods listed in manifest are absent from destDir
// and downloads only those. It resolves each mod's filename from the CurseForge API
// so it can match against whatever naming convention the pre-existing files use.
func (d *Downloader) DownloadMissingMods(manifest *parser.Manifest, destDir string) error {
	log := logger.Get()

	if err := utils.EnsureDir(destDir); err != nil {
		return fmt.Errorf("create mods dir: %w", err)
	}
	d.prefetchHashes(manifest)

	existingFiles, err := listDirFiles(destDir)
	if err != nil {
		return fmt.Errorf("list existing mods: %w", err)
	}

	var tasks []Task
	for _, f := range manifest.Files {
		if slug, ok := d.excludedSlug(f.ProjectID); ok && d.FilterClientOnly {
			logExcludedSkip(f.ProjectID, f.FileID, slug)
			continue
		}

		// Fast-path: check for the cache-style name (<projectID>-<fileID>.jar)
		// which is used when the real filename was not yet known at download time.
		if existingFiles[fmt.Sprintf(cacheKeyFormat, f.ProjectID, f.FileID)] {
			log.Debug().
				Int("projectID", f.ProjectID).
				Int("fileID", f.FileID).
				Msg("mod already present (cache-key match), skipping")
			continue
		}

		// Resolve the real filename from the CurseForge API to match against
		// packs that ship jar files under their actual names.
		fi, err := d.fetchFileInfo(f.ProjectID, f.FileID)
		if err != nil {
			d.recordFailure(f.ProjectID, f.FileID, "", err)
			// A file CurseForge no longer publishes cannot be downloaded by
			// anyone, so skip it rather than failing the whole pack.
			if f.Required && !isMissingFileErr(err) {
				return fmt.Errorf("resolve mod %d/%d: %w", f.ProjectID, f.FileID, err)
			}
			log.Warn().Err(err).
				Int("projectID", f.ProjectID).
				Int("fileID", f.FileID).
				Msg("cannot resolve mod filename, skipping")
			continue
		}

		if d.FilterClientOnly && fi.IsClientOnly() {
			logClientOnlySkip(f.ProjectID, f.FileID, fi)
			continue
		}

		filename := fi.FileName
		if filename != "" && existingFiles[filename] {
			log.Debug().
				Int("projectID", f.ProjectID).
				Int("fileID", f.FileID).
				Str("filename", filename).
				Msg("mod already present, skipping")
			continue
		}

		tasks = append(tasks, Task{
			ProjectID:        f.ProjectID,
			FileID:           f.FileID,
			Required:         f.Required,
			DestDir:          destDir,
			ResolvedFilename: filename,
		})
	}

	if len(tasks) == 0 {
		log.Info().Msg("all manifest mods already present, nothing to download")
		return nil
	}

	log.Info().Int("count", len(tasks)).Msg("downloading missing mods")

	taskCh := make(chan Task, len(tasks))
	for _, t := range tasks {
		taskCh <- t
	}
	close(taskCh)

	var wg sync.WaitGroup
	errs := make(chan error, len(tasks))

	for i := 0; i < d.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range taskCh {
				if err := d.downloadMod(t); err != nil {
					d.recordFailure(t.ProjectID, t.FileID, t.ResolvedFilename, err)
					if t.Required && !isMissingFileErr(err) {
						errs <- err
					} else {
						log.Warn().Err(err).
							Int("projectID", t.ProjectID).
							Int("fileID", t.FileID).
							Msg("mod download failed, skipping")
					}
				}
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// CleanMods removes client-only mod JARs from modsDir using the CurseForge API
// to determine each mod's server/client classification. It cross-references every
// file listed in manifest against the mods already present in modsDir and deletes
// those whose gameVersions indicate client-only or that the exclude list names.
// Returns the list of removed filenames.
func (d *Downloader) CleanMods(manifest *parser.Manifest, modsDir string) ([]string, error) {
	log := logger.Get()

	presentFiles, err := listDirFiles(modsDir)
	if err != nil {
		return nil, fmt.Errorf("list mods dir: %w", err)
	}

	var removed []string
	for _, f := range manifest.Files {
		fi, apiErr := d.fetchFileInfo(f.ProjectID, f.FileID)
		if apiErr != nil {
			log.Warn().Err(apiErr).
				Int("projectID", f.ProjectID).
				Int("fileID", f.FileID).
				Msg("could not fetch file info for clean check, skipping")
			continue
		}

		excludedSlug, excluded := d.excludedSlug(f.ProjectID)
		if !fi.IsClientOnly() && !excluded {
			continue
		}

		if fi.FileName == "" || !presentFiles[fi.FileName] {
			continue
		}

		fullPath := filepath.Join(modsDir, fi.FileName)
		if removeErr := os.Remove(fullPath); removeErr != nil {
			log.Warn().Err(removeErr).Str("file", fi.FileName).Msg("failed to remove client-only mod")
			continue
		}
		log.Info().
			Int("projectID", f.ProjectID).
			Int("fileID", f.FileID).
			Str("file", fi.FileName).
			Strs("gameVersions", fi.GameVersions).
			Str("excludeListSlug", excludedSlug).
			Msg("removed client-only mod")
		removed = append(removed, fi.FileName)
	}
	return removed, nil
}

// listDirFiles returns a set of filenames (not full paths) present directly inside dir.
func listDirFiles(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	files := make(map[string]bool, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			files[e.Name()] = true
		}
	}
	return files, nil
}
