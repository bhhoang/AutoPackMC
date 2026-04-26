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
)

// Task represents a single mod download request.
type Task struct {
	ProjectID int
	FileID    int
	Required  bool
	DestDir   string
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

	cacheFile := filepath.Join(d.CacheDir, fmt.Sprintf("%d-%d.jar", t.ProjectID, t.FileID))

	// Serve from cache if available
	if utils.FileExists(cacheFile) {
		log.Debug().
			Int("projectID", t.ProjectID).
			Int("fileID", t.FileID).
			Msg("cache hit, copying from cache")
		destFile := filepath.Join(t.DestDir, fmt.Sprintf("%d-%d.jar", t.ProjectID, t.FileID))
		return utils.CopyDir(cacheFile, destFile) // single file copy via CopyDir is fine but use direct copy
	}

	downloadURL, filename, err := d.resolveDownloadURL(t.ProjectID, t.FileID)
	if err != nil {
		return err
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
		destFilename = fmt.Sprintf("%d-%d.jar", t.ProjectID, t.FileID)
	}
	destFile := filepath.Join(t.DestDir, destFilename)

	return copyFileSimple(cacheFile, destFile)
}

// resolveDownloadURL fetches the real download URL from the CurseForge API.
// Falls back to a direct CDN pattern when no API key is available.
func (d *Downloader) resolveDownloadURL(projectID, fileID int) (url, filename string, err error) {
	log := logger.Get()

	if d.APIKey == "" {
		log.Warn().
			Int("projectID", projectID).
			Int("fileID", fileID).
			Msg("no CurseForge API key set; attempting direct CDN URL (may fail for some mods)")
		// Best-effort direct CDN pattern
		url = fmt.Sprintf("https://edge.forgecdn.net/files/%d/%d/", fileID/1000, fileID%1000)
		return url, "", nil
	}

	apiURL := fmt.Sprintf(curseForgeBaseURL, projectID, fileID)
	req, reqErr := http.NewRequest(http.MethodGet, apiURL, nil)
	if reqErr != nil {
		return "", "", reqErr
	}
	req.Header.Set("X-Api-Key", d.APIKey)

	resp, respErr := http.DefaultClient.Do(req)
	if respErr != nil {
		return "", "", respErr
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("CurseForge API returned HTTP %d for project %d file %d", resp.StatusCode, projectID, fileID)
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Data string `json:"data"`
	}
	if jsonErr := json.Unmarshal(body, &result); jsonErr != nil {
		return "", "", fmt.Errorf("parse CurseForge API response: %w", jsonErr)
	}

	// Extract filename from the URL path
	filename = filepath.Base(result.Data)
	return result.Data, filename, nil
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
