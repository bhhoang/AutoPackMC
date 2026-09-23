package downloader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bhhoang/IDISMAM/internal/parser"
	"github.com/bhhoang/IDISMAM/pkg/logger"
	"github.com/bhhoang/IDISMAM/pkg/utils"
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

	// The user's own --exclude-mods and --include-mods: project slugs or
	// numeric project IDs. They take precedence over the entries above, and
	// UserIncludes also over a Client-only tag on CurseForge.
	UserExcludes []string `json:"-"`
	UserIncludes []string `json:"-"`
}

// ModpackExcludeList holds the adjustments for a single modpack.
type ModpackExcludeList struct {
	Excludes      []string `json:"excludes"`
	ForceIncludes []string `json:"forceIncludes"`
}

// resolve returns, for the given modpack, the entries (slugs or project IDs)
// to skip and the ones to keep even when tagged client-only. Precedence from
// lowest to highest: list excludes, list force-includes, user excludes, user
// includes.
func (l *ExcludeList) resolve(packSlug string) (excluded, forced map[string]bool) {
	excluded = make(map[string]bool)
	forced = make(map[string]bool)
	set := func(entries []string, exclude bool) {
		for _, e := range entries {
			excluded[e] = exclude
			forced[e] = !exclude
		}
	}
	pack := l.Modpacks[packSlug]
	set(l.GlobalExcludes, true)
	set(pack.Excludes, true)
	set(l.GlobalForceIncludes, false)
	set(pack.ForceIncludes, false)
	set(l.UserExcludes, true)
	set(l.UserIncludes, false)
	return excluded, forced
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
// CleanMods removes them, and the ones it force-includes, which are kept
// even when tagged client-only. Entries are matched by slug, looked up
// through the official API, or by numeric project ID. It returns the number
// of excluded projects in the manifest. If the slug lookup fails, entries
// given as project IDs still apply and the error is returned.
func (d *Downloader) ApplyExcludeList(list *ExcludeList, manifest *parser.Manifest, packSlug string) (int, error) {
	excludedEntries, forcedEntries := list.resolve(packSlug)

	ids := make([]int, 0, len(manifest.Files))
	for _, f := range manifest.Files {
		ids = append(ids, f.ProjectID)
	}
	slugs, slugErr := d.fetchModSlugs(ids)

	excluded := make(map[int]string)
	forced := make(map[int]bool)
	for _, id := range ids {
		slug := slugs[id]
		idKey := strconv.Itoa(id)
		switch {
		case forcedEntries[idKey] || (slug != "" && forcedEntries[slug]):
			forced[id] = true
		case excludedEntries[idKey] || (slug != "" && excludedEntries[slug]):
			name := slug
			if name == "" {
				name = idKey
			}
			excluded[id] = name
		}
	}
	d.excludedMu.Lock()
	d.excludedProjects = excluded
	d.forcedProjects = forced
	d.excludedMu.Unlock()
	return len(excluded), slugErr
}

// excludedSlug returns the slug of projectID when the exclude list skips it.
func (d *Downloader) excludedSlug(projectID int) (string, bool) {
	d.excludedMu.RLock()
	defer d.excludedMu.RUnlock()
	slug, ok := d.excludedProjects[projectID]
	return slug, ok
}

// forceIncluded reports whether projectID must be kept even when CurseForge
// tags it client-only.
func (d *Downloader) forceIncluded(projectID int) bool {
	d.excludedMu.RLock()
	defer d.excludedMu.RUnlock()
	return d.forcedProjects[projectID]
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
