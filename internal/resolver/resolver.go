// Package resolver resolves a CurseForge modpack URL to a downloadable archive
// URL using the official CurseForge API (https://api.curseforge.com/v1).
package resolver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bhhoang/AutoPackMC/pkg/logger"
)

const (
	cfAPIBase    = "https://api.curseforge.com/v1"
	cfGameID     = 432  // Minecraft
	cfClassID    = 4471 // Modpacks
	cfModClassID = 6    // Individual mods
	maxRetries   = 3
)

// cfURLRegex matches CurseForge modpack/mod URLs and captures the slug.
// Examples:
//
//	https://www.curseforge.com/minecraft/modpacks/deceasedcraft
//	https://www.curseforge.com/minecraft/mc-mods/jei
var cfURLRegex = regexp.MustCompile(`(?i)^https?://(?:www\.)?curseforge\.com/minecraft/(?:modpacks|mc-mods)/([a-zA-Z0-9_-]+)`)

// cfFileURLRegex matches CurseForge file URLs and captures the slug and file ID.
// Example: https://www.curseforge.com/minecraft/mc-mods/easy-mob-farm/files/7957341
var cfFileURLRegex = regexp.MustCompile(`(?i)^https?://(?:www\.)?curseforge\.com/minecraft/(?:modpacks|mc-mods)/([a-zA-Z0-9_-]+)/files/(\d+)`)

// IsCurseForgeURL reports whether input looks like a CurseForge modpack/mod URL.
func IsCurseForgeURL(input string) bool {
	return cfURLRegex.MatchString(input)
}

// ClassIDFromURL returns the CurseForge class ID for the content type in the URL:
// 6 for mc-mods, 4471 for modpacks.
func ClassIDFromURL(input string) int {
	if strings.Contains(strings.ToLower(input), "/mc-mods/") {
		return cfModClassID
	}
	return cfClassID
}

// IsCurseForgeFileURL reports whether input looks like a CurseForge specific-file URL.
func IsCurseForgeFileURL(input string) bool {
	return cfFileURLRegex.MatchString(input)
}

// ExtractFileURL extracts the slug and file ID from a CurseForge file URL.
func ExtractFileURL(input string) (slug string, fileID int, err error) {
	m := cfFileURLRegex.FindStringSubmatch(input)
	if len(m) < 3 {
		return "", 0, fmt.Errorf("cannot extract file info from CurseForge URL %q", input)
	}
	id, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, fmt.Errorf("invalid file ID in URL: %w", err)
	}
	return m[1], id, nil
}

// ResolveModID resolves a slug to a numeric CurseForge mod ID.
// classID filters by content type (4471=modpack, 6=mod).
func (r *Resolver) ResolveModID(slug string, classID int) (int, error) {
	return r.resolveModID(slug, classID)
}

// ExtractSlug extracts the slug segment from a CurseForge URL.
func ExtractSlug(input string) (string, error) {
	m := cfURLRegex.FindStringSubmatch(input)
	if len(m) < 2 {
		return "", fmt.Errorf("cannot extract slug from CurseForge URL %q", input)
	}
	return m[1], nil
}

// ---------------------------------------------------------------------------
// API response types
// ---------------------------------------------------------------------------

type cfSearchResponse struct {
	Data []cfMod `json:"data"`
}

type cfMod struct {
	ID            int    `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	DownloadCount int64  `json:"downloadCount"`
}

type cfFilesResponse struct {
	Data []CFFile `json:"data"`
}

// CFFile represents a single file entry returned by the CurseForge files API.
type CFFile struct {
	ID           int    `json:"id"`
	FileName     string `json:"fileName"`
	IsAvailable  bool   `json:"isAvailable"`
	ReleaseType  int    `json:"releaseType"` // 1=Release, 2=Beta, 3=Alpha
	IsServerPack bool   `json:"isServerPack"`
}

type cfDownloadURLResponse struct {
	Data string `json:"data"`
}

// ---------------------------------------------------------------------------
// Resolver
// ---------------------------------------------------------------------------

// Resolver resolves CurseForge modpack URLs to download URLs.
type Resolver struct {
	APIKey string
	client *http.Client
}

// New creates a Resolver using the given CurseForge API key.
func New(apiKey string) *Resolver {
	return &Resolver{
		APIKey: apiKey,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Resolve takes a CurseForge URL and returns the best download URL.
// It favours a server pack when one is available in the latest-files window.
func (r *Resolver) Resolve(input string) (string, error) {
	log := logger.Get()

	slug, err := ExtractSlug(input)
	if err != nil {
		return "", err
	}
	log.Info().Str("slug", slug).Msg("[resolver] Searching mod")

	modID, err := r.resolveModID(slug, ClassIDFromURL(input))
	if err != nil {
		return "", err
	}
	log.Info().Int("modId", modID).Msg("[resolver] Found modId")

	log.Info().Msg("[resolver] Fetching latest files (pageSize=20)")
	files, err := r.fetchLatestFiles(modID)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no files found for mod %d", modID)
	}

	primary, serverPack := selectFiles(files)

	selected := primary
	if serverPack != nil {
		log.Info().Int("fileId", serverPack.ID).Msg("[resolver] Found server pack")
		log.Info().Msg("[resolver] Using server pack")
		selected = serverPack
	} else {
		log.Info().Int("fileId", selected.ID).Msg("[resolver] Selected file")
	}

	downloadURL, err := r.getDownloadURL(modID, selected.ID)
	if err != nil {
		return "", err
	}

	return downloadURL, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// resolveModID searches for a mod by slug and returns its numeric ID.
// Match priority:
//  1. Exact slug match
//  2. Normalized slug/name match (lowercase, spaces and dashes removed)
//  3. Highest download count among results
func (r *Resolver) resolveModID(slug string, classID int) (int, error) {
	// Include the slug parameter for exact slug matching in addition to the
	// general searchFilter, which prevents popular unrelated packs from winning
	// the download-count fallback when the text search returns mixed results.
	apiURL := fmt.Sprintf("%s/mods/search?gameId=%d&classId=%d&slug=%s&searchFilter=%s",
		cfAPIBase, cfGameID, classID, slug, slug)

	body, err := r.apiGet(apiURL)
	if err != nil {
		return 0, fmt.Errorf("search mod %q: %w", slug, err)
	}

	var result cfSearchResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("parse search response: %w", err)
	}
	if len(result.Data) == 0 {
		return 0, fmt.Errorf("no mod found for slug %q", slug)
	}

	// 1. Exact slug match
	for _, mod := range result.Data {
		if mod.Slug == slug {
			return mod.ID, nil
		}
	}

	// 2. Normalized match
	slugNorm := normalizeSlug(slug)
	for _, mod := range result.Data {
		if normalizeSlug(mod.Slug) == slugNorm || normalizeSlug(mod.Name) == slugNorm {
			return mod.ID, nil
		}
	}

	// 3. Highest download count
	best := result.Data[0]
	for _, mod := range result.Data[1:] {
		if mod.DownloadCount > best.DownloadCount {
			best = mod
		}
	}
	return best.ID, nil
}

// normalizeSlug lowercases and strips spaces, dashes, and underscores.
func normalizeSlug(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}

// fetchLatestFiles returns up to the 20 most-recent files for a mod,
// sorted by date descending (sortField=3, sortOrder=desc).
func (r *Resolver) fetchLatestFiles(modID int) ([]CFFile, error) {
	apiURL := fmt.Sprintf("%s/mods/%d/files?sortField=3&sortOrder=desc&pageSize=20", cfAPIBase, modID)

	body, err := r.apiGet(apiURL)
	if err != nil {
		return nil, fmt.Errorf("fetch files for mod %d: %w", modID, err)
	}

	var result cfFilesResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse files response: %w", err)
	}
	return result.Data, nil
}

// selectFiles picks the primary download file and the best server pack from
// a slice of files (expected to be sorted newest-first).
//
// Primary selection:
//  1. First file where isAvailable==true and releaseType==1 (Release)
//  2. Fallback: first available file
//  3. Last resort: files[0]
//
// Server pack: first file where isServerPack==true.
func selectFiles(files []CFFile) (primary *CFFile, serverPack *CFFile) {
	// Server pack: first hit (newest due to sort order)
	for i := range files {
		if files[i].IsServerPack {
			serverPack = &files[i]
			break
		}
	}

	// Primary: first available Release file
	for i := range files {
		f := &files[i]
		if f.IsAvailable && f.ReleaseType == 1 {
			primary = f
			break
		}
	}

	// Fallback: first available file
	if primary == nil {
		for i := range files {
			if files[i].IsAvailable {
				primary = &files[i]
				break
			}
		}
	}

	// Last resort: first file
	if primary == nil {
		primary = &files[0]
	}

	return primary, serverPack
}

// getDownloadURL retrieves the download URL for a specific file via the API.
func (r *Resolver) getDownloadURL(modID, fileID int) (string, error) {
	apiURL := fmt.Sprintf("%s/mods/%d/files/%d/download-url", cfAPIBase, modID, fileID)

	body, err := r.apiGet(apiURL)
	if err != nil {
		return "", fmt.Errorf("get download URL for mod %d file %d: %w", modID, fileID, err)
	}

	var result cfDownloadURLResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse download URL response: %w", err)
	}
	if result.Data == "" {
		return "", fmt.Errorf("empty download URL returned for mod %d file %d", modID, fileID)
	}
	return result.Data, nil
}

// apiGet performs an authenticated GET request to the CurseForge API with
// exponential-backoff retries and 429 rate-limit handling.
func (r *Resolver) apiGet(apiURL string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential back-off: attempt 1 → 1s, attempt 2 → 2s, ...
			time.Sleep(time.Duration(1<<uint(attempt-1)) * time.Second)
		}

		req, err := http.NewRequest(http.MethodGet, apiURL, nil)
		if err != nil {
			return nil, err
		}
		if r.APIKey != "" {
			req.Header.Set("x-api-key", r.APIKey)
		}
		req.Header.Set("Accept", "application/json")

		resp, err := r.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			lastErr = fmt.Errorf("rate limited (HTTP 429) for %s", apiURL)
			// Extra back-off on rate limit: attempt 0 → 2s, attempt 1 → 4s, attempt 2 → 8s.
			time.Sleep(time.Duration(1<<uint(attempt+1)) * time.Second)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("CurseForge API returned HTTP %d for %s", resp.StatusCode, apiURL)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return body, nil
	}
	return nil, lastErr
}
