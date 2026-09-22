package downloader

import (
	"bytes"
	"crypto/sha1" // #nosec G505 -- CurseForge publishes SHA-1; used for integrity, not security
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/bhhoang/AutoPackMC/internal/parser"
	"github.com/bhhoang/AutoPackMC/pkg/logger"
	"github.com/bhhoang/AutoPackMC/pkg/utils"
)

// ErrChecksumMismatch means a downloaded file does not match the size or
// hash CurseForge publishes for it.
var ErrChecksumMismatch = errors.New("downloaded file does not match its published checksum")

const (
	cfHashAlgoSHA1 = 1 // "algo" value of SHA-1 entries in CurseForge file hashes
	fileBatchSize  = 500
)

// officialFile is the file payload of the official CurseForge API.
type officialFile struct {
	ID           int      `json:"id"`
	FileName     string   `json:"fileName"`
	DownloadURL  string   `json:"downloadUrl"`
	GameVersions []string `json:"gameVersions"`
	FileLength   int64    `json:"fileLength"`
	Hashes       []struct {
		Value string `json:"value"`
		Algo  int    `json:"algo"`
	} `json:"hashes"`
}

func (f *officialFile) sha1() string {
	for _, h := range f.Hashes {
		if h.Algo == cfHashAlgoSHA1 {
			return h.Value
		}
	}
	return ""
}

// prefetchHashes looks up the SHA-1 of every file in manifest with one batch
// request per 500 files, so downloads can be verified. The website API used
// for most lookups only reports sizes. Without an API key, or if the lookup
// fails, downloads are checked against their size alone.
func (d *Downloader) prefetchHashes(manifest *parser.Manifest) {
	if d.APIKey == "" || len(manifest.Files) == 0 {
		return
	}
	ids := make([]int, 0, len(manifest.Files))
	for _, f := range manifest.Files {
		ids = append(ids, f.FileID)
	}

	hashes := make(map[int]string, len(ids))
	for start := 0; start < len(ids); start += fileBatchSize {
		end := min(start+fileBatchSize, len(ids))
		files, err := d.fetchOfficialFiles(ids[start:end])
		if err != nil {
			logger.Get().Warn().Err(err).Msg("cannot look up mod checksums; verifying file sizes only")
			return
		}
		for _, f := range files {
			if sum := f.sha1(); sum != "" {
				hashes[f.ID] = sum
			}
		}
	}

	d.hashMu.Lock()
	d.fileHashes = hashes
	d.hashMu.Unlock()
	logger.Get().Debug().Int("files", len(hashes)).Msg("fetched mod checksums")
}

func (d *Downloader) fetchOfficialFiles(fileIDs []int) ([]officialFile, error) {
	body, err := json.Marshal(map[string][]int{"fileIds": fileIDs})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, d.officialAPIBase+"/mods/files", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", d.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &utils.HTTPStatusError{StatusCode: resp.StatusCode, URL: req.URL.String()}
	}
	var result struct {
		Data []officialFile `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse file batch: %w", err)
	}
	return result.Data, nil
}

// expectedChecksum returns the size and SHA-1 a download of the file should
// have, as far as they are known.
func (d *Downloader) expectedChecksum(projectID, fileID int) (size int64, sum string) {
	d.fileInfoMu.RLock()
	if fi, ok := d.fileInfoCache[fileInfoCacheKey(projectID, fileID)]; ok {
		size, sum = fi.Size, fi.SHA1
	}
	d.fileInfoMu.RUnlock()

	if sum == "" {
		d.hashMu.RLock()
		sum = d.fileHashes[fileID]
		d.hashMu.RUnlock()
	}
	return size, sum
}

// verifyFile checks path against the expected size and SHA-1; zero values
// are not checked.
func verifyFile(path string, size int64, sum string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha1.New() // #nosec G401
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if size > 0 && n != size {
		return fmt.Errorf("%w: got %d bytes, want %d", ErrChecksumMismatch, n, size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); sum != "" && got != sum {
		return fmt.Errorf("%w: SHA-1 %s, want %s", ErrChecksumMismatch, got, sum)
	}
	return nil
}
