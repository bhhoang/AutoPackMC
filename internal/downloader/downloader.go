package downloader

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bhhoang/AutoPackMC/internal/parser"
	"github.com/bhhoang/AutoPackMC/pkg/logger"
	"github.com/bhhoang/AutoPackMC/pkg/utils"
)

const (
	defaultWorkers    = 4
	maxRetries        = 3
	curseForgeBaseURL = "https://api.curseforge.com/v1/mods/%d/files/%d/download-url"
	cacheKeyFormat    = "%d-%d.jar" // <projectID>-<fileID>.jar — used for cache entries without a resolved filename
)

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
	Workers    int
	CacheDir   string
	APIKey     string
}

// New creates a Downloader with sensible defaults.
func New(cacheDir, apiKey string, workers int) *Downloader {
	if workers <= 0 {
		workers = defaultWorkers
	}
	return &Downloader{
		Workers:  workers,
		CacheDir: cacheDir,
		APIKey:   apiKey,
	}
}

// DownloadMods downloads all files specified in manifest into destDir.
func (d *Downloader) DownloadMods(manifest *parser.Manifest, destDir string) error {
	log := logger.Get()

	if err := utils.EnsureDir(destDir); err != nil {
		return fmt.Errorf("create mods dir: %w", err)
	}

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
					if t.Required {
						errs <- err
					} else {
						log.Warn().Err(err).
							Int("projectID", t.ProjectID).
							Int("fileID", t.FileID).
							Msg("optional mod download failed, skipping")
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

func (d *Downloader) downloadMod(t Task) error {
	log := logger.Get()

	cacheFile := filepath.Join(d.CacheDir, fmt.Sprintf(cacheKeyFormat, t.ProjectID, t.FileID))

	// Serve from cache if available
	if utils.FileExists(cacheFile) {
		log.Debug().
			Int("projectID", t.ProjectID).
			Int("fileID", t.FileID).
			Msg("cache hit, copying from cache")
		destFile := filepath.Join(t.DestDir, fmt.Sprintf(cacheKeyFormat, t.ProjectID, t.FileID))
		return utils.CopyDir(cacheFile, destFile) // single file copy via CopyDir is fine but use direct copy
	}

	var downloadURL, filename string
	if t.ResolvedFilename != "" {
		// Filename already resolved upstream; build the download URL directly to
		// avoid a redundant API round-trip.
		filename = t.ResolvedFilename
		downloadURL = fmt.Sprintf("https://www.curseforge.com/api/v1/mods/%d/files/%d/download", t.ProjectID, t.FileID)
	} else {
		var err error
		downloadURL, filename, err = d.resolveDownloadURL(t.ProjectID, t.FileID)
		if err != nil {
			return err
		}
	}

	if err := utils.EnsureDir(d.CacheDir); err != nil {
		return err
	}

	// Use the real filename for the cache entry when known
	if filename != "" {
		cacheFile = filepath.Join(d.CacheDir, filename)
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

	if err := downloadWithRetry(downloadURL, cacheFile, headers); err != nil {
		return fmt.Errorf("download mod %d/%d: %w", t.ProjectID, t.FileID, err)
	}

	destFilename := filename
	if destFilename == "" {
		destFilename = fmt.Sprintf(cacheKeyFormat, t.ProjectID, t.FileID)
	}
	destFile := filepath.Join(t.DestDir, destFilename)

	return copyFileSimple(cacheFile, destFile)
}

// resolveDownloadURL fetches the real download URL from the CurseForge API.
// Uses the public API v1 endpoint that doesn't require an API key.
func (d *Downloader) resolveDownloadURL(projectID, fileID int) (url, filename string, err error) {
	log := logger.Get()

	// First, get the file info to get the filename
	fileInfoURL := fmt.Sprintf("https://www.curseforge.com/api/v1/mods/%d/files/%d", projectID, fileID)
	resp, err := http.Get(fileInfoURL)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("CurseForge API returned HTTP %d for project %d file %d", resp.StatusCode, projectID, fileID)
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Data struct {
			FileName string `json:"fileName"`
		} `json:"data"`
	}
	if jsonErr := json.Unmarshal(body, &result); jsonErr != nil {
		return "", "", fmt.Errorf("parse CurseForge API response: %w", jsonErr)
	}

	filename = result.Data.FileName
	downloadURL := fmt.Sprintf("https://www.curseforge.com/api/v1/mods/%d/files/%d/download", projectID, fileID)

	log.Debug().
		Int("projectID", projectID).
		Int("fileID", fileID).
		Str("filename", filename).
		Msg("got file info from CurseForge API")

	return downloadURL, filename, nil
}

func downloadWithRetry(url, dest string, headers map[string]string) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(1<<(attempt-1)) * time.Second)
		}
		lastErr = utils.DownloadFile(url, dest, headers)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
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

	existingFiles, err := listDirFiles(destDir)
	if err != nil {
		return fmt.Errorf("list existing mods: %w", err)
	}

	var tasks []Task
	for _, f := range manifest.Files {
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
		_, filename, err := d.resolveDownloadURL(f.ProjectID, f.FileID)
		if err != nil {
			if f.Required {
				return fmt.Errorf("resolve mod %d/%d: %w", f.ProjectID, f.FileID, err)
			}
			log.Warn().Err(err).
				Int("projectID", f.ProjectID).
				Int("fileID", f.FileID).
				Msg("cannot resolve optional mod filename, skipping")
			continue
		}

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
					if t.Required {
						errs <- err
					} else {
						log.Warn().Err(err).
							Int("projectID", t.ProjectID).
							Int("fileID", t.FileID).
							Msg("optional mod download failed, skipping")
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
