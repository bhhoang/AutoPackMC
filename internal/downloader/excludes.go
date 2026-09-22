package downloader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bhhoang/AutoPackMC/internal/parser"
	"github.com/bhhoang/AutoPackMC/pkg/logger"
	"github.com/bhhoang/AutoPackMC/pkg/utils"
)

// DefaultExcludeListURL is the community-maintained list of client-only
// CurseForge projects used by itzg/docker-minecraft-server (Apache-2.0).
// It catches mods whose files carry no Client/Server tag, which IsClientOnly
// cannot classify.
const DefaultExcludeListURL = "https://raw.githubusercontent.com/itzg/docker-minecraft-server/master/files/cf-exclude-include.json"

const (
	excludeListCacheFile = "cf-exclude-include.json"
	modSlugBatchSize     = 500
)

// ExcludeList follows the cf-exclude-include.json schema: entries are
// CurseForge project slugs, globally and per modpack slug.
type ExcludeList struct {
	GlobalExcludes      []string                      `json:"globalExcludes"`
	GlobalForceIncludes []string                      `json:"globalForceIncludes"`
	Modpacks            map[string]ModpackExcludeList `json:"modpacks"`
}

// ModpackExcludeList holds the adjustments for a single modpack.
type ModpackExcludeList struct {
	Excludes      []string `json:"excludes"`
	ForceIncludes []string `json:"forceIncludes"`
}

// excludedSlugs returns the slugs to skip for the given modpack: global and
// pack excludes, minus anything force-included.
func (l *ExcludeList) excludedSlugs(packSlug string) map[string]bool {
	excluded := make(map[string]bool)
	for _, s := range l.GlobalExcludes {
		excluded[s] = true
	}
	pack := l.Modpacks[packSlug]
	for _, s := range pack.Excludes {
		excluded[s] = true
	}
	for _, s := range l.GlobalForceIncludes {
		delete(excluded, s)
	}
	for _, s := range pack.ForceIncludes {
		delete(excluded, s)
	}
	return excluded
}

// LoadExcludeList reads the exclude list from source, which is an http(s) URL
// or a local file path. A downloaded list is cached in cacheDir, and the
// cached copy is used when the download fails.
func LoadExcludeList(source, cacheDir string) (*ExcludeList, error) {
	if !strings.HasPrefix(source, "http://") && !strings.HasPrefix(source, "https://") {
		data, err := os.ReadFile(source)
		if err != nil {
			return nil, fmt.Errorf("read exclude list: %w", err)
		}
		return parseExcludeList(data)
	}

	cachePath := filepath.Join(cacheDir, excludeListCacheFile)
	data, err := fetchExcludeList(source)
	if err == nil {
		if list, parseErr := parseExcludeList(data); parseErr == nil {
			if utils.EnsureDir(cacheDir) == nil {
				_ = os.WriteFile(cachePath, data, 0o644)
			}
			return list, nil
		} else {
			err = parseErr
		}
	}

	cached, readErr := os.ReadFile(cachePath)
	if readErr != nil {
		return nil, fmt.Errorf("fetch exclude list: %w", err)
	}
	logger.Get().Warn().Err(err).Str("cache", cachePath).Msg("cannot fetch exclude list, using cached copy")
	return parseExcludeList(cached)
}

func fetchExcludeList(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url) // #nosec G107
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &utils.HTTPStatusError{StatusCode: resp.StatusCode, URL: url}
	}
	return io.ReadAll(resp.Body)
}

func parseExcludeList(data []byte) (*ExcludeList, error) {
	var list ExcludeList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse exclude list: %w", err)
	}
	return &list, nil
}

// ApplyExcludeList marks the manifest's projects that the list excludes for
// packSlug (which may be empty when unknown), so downloads skip them and
// CleanMods removes them. Project slugs are looked up through the official
// API; it returns the number of excluded projects in the manifest.
func (d *Downloader) ApplyExcludeList(list *ExcludeList, manifest *parser.Manifest, packSlug string) (int, error) {
	excludedSlugs := list.excludedSlugs(packSlug)

	ids := make([]int, 0, len(manifest.Files))
	for _, f := range manifest.Files {
		ids = append(ids, f.ProjectID)
	}
	slugs, err := d.fetchModSlugs(ids)
	if err != nil {
		return 0, err
	}

	excluded := make(map[int]string)
	for id, slug := range slugs {
		if excludedSlugs[slug] {
			excluded[id] = slug
		}
	}
	d.excludedMu.Lock()
	d.excludedProjects = excluded
	d.excludedMu.Unlock()
	return len(excluded), nil
}

// excludedSlug returns the slug of projectID when the exclude list skips it.
func (d *Downloader) excludedSlug(projectID int) (string, bool) {
	d.excludedMu.RLock()
	defer d.excludedMu.RUnlock()
	slug, ok := d.excludedProjects[projectID]
	return slug, ok
}

func logExcludedSkip(projectID, fileID int, slug string) {
	logger.Get().Info().
		Int("projectID", projectID).
		Int("fileID", fileID).
		Str("slug", slug).
		Msg("skipping client-only mod (exclude list)")
}

// fetchModSlugs maps project IDs to slugs using the official API's batch
// endpoint.
func (d *Downloader) fetchModSlugs(ids []int) (map[int]string, error) {
	if d.APIKey == "" {
		return nil, fmt.Errorf("a CurseForge API key is required to look up mod slugs")
	}

	slugs := make(map[int]string, len(ids))
	for start := 0; start < len(ids); start += modSlugBatchSize {
		end := min(start+modSlugBatchSize, len(ids))
		body, err := json.Marshal(map[string][]int{"modIds": ids[start:end]})
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequest(http.MethodPost, d.officialAPIBase+"/mods", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("x-api-key", d.APIKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("look up mod slugs: %w", err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("look up mod slugs: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("look up mod slugs: %w", &utils.HTTPStatusError{StatusCode: resp.StatusCode, URL: req.URL.String()})
		}

		var result struct {
			Data []struct {
				ID   int    `json:"id"`
				Slug string `json:"slug"`
			} `json:"data"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("parse mod slugs: %w", err)
		}
		for _, m := range result.Data {
			slugs[m.ID] = m.Slug
		}
	}
	return slugs, nil
}
