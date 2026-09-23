package downloader

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"

	"github.com/bhhoang/IDISMAM/pkg/logger"
)

// FailedMod describes a mod that could not be downloaded through any automatic
// route, together with the CurseForge page a human can fetch it from.
type FailedMod struct {
	ProjectID int
	FileID    int
	Name      string // mod display name, when it could be resolved
	FileName  string // jar name, when it could be resolved
	PageURL   string // CurseForge download page for this exact file
	Err       error
}

// modInfo is the subset of the official API's mod payload used for reporting.
type modInfo struct {
	Name       string
	WebsiteURL string
}

// recordFailure remembers a mod that could not be downloaded so it can be
// reported at the end of the run. Repeated failures for the same file collapse
// into a single entry.
func (d *Downloader) recordFailure(projectID, fileID int, fileName string, err error) {
	d.failedMu.Lock()
	defer d.failedMu.Unlock()

	for _, f := range d.failed {
		if f.ProjectID == projectID && f.FileID == fileID {
			return
		}
	}

	info := d.fetchModInfo(projectID)
	d.failed = append(d.failed, FailedMod{
		ProjectID: projectID,
		FileID:    fileID,
		Name:      info.Name,
		FileName:  fileName,
		PageURL:   modDownloadPageURL(info.WebsiteURL, projectID, fileID),
		Err:       err,
	})
}

// FailedMods returns the mods that could not be downloaded, ordered by project
// ID so the report is stable between runs.
func (d *Downloader) FailedMods() []FailedMod {
	d.failedMu.Lock()
	defer d.failedMu.Unlock()

	out := make([]FailedMod, len(d.failed))
	copy(out, d.failed)
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProjectID != out[j].ProjectID {
			return out[i].ProjectID < out[j].ProjectID
		}
		return out[i].FileID < out[j].FileID
	})
	return out
}

// modDownloadPageURL builds the page that offers this file for manual download,
// e.g. https://www.curseforge.com/minecraft/mc-mods/lionfish-api/download/8345326.
// When the mod's website URL is unknown it falls back to the numeric project
// URL, which CurseForge redirects to the project page.
func modDownloadPageURL(websiteURL string, projectID, fileID int) string {
	if websiteURL == "" {
		return fmt.Sprintf("https://www.curseforge.com/projects/%d", projectID)
	}
	return fmt.Sprintf("%s/download/%d", websiteURL, fileID)
}

// fetchModInfo looks up a mod's name and website URL via the official API.
// Reporting must never fail the run, so any error yields an empty modInfo and
// the caller falls back to the numeric project URL.
// Callers must hold failedMu; downloads are only blocked while a failure is
// being recorded, which is rare by definition.
func (d *Downloader) fetchModInfo(projectID int) modInfo {
	if cached, ok := d.modInfoCache[projectID]; ok {
		return cached
	}

	info := d.lookupModInfo(projectID)
	if d.modInfoCache == nil {
		d.modInfoCache = make(map[int]modInfo)
	}
	d.modInfoCache[projectID] = info
	return info
}

func (d *Downloader) lookupModInfo(projectID int) modInfo {
	if d.APIKey == "" {
		return modInfo{}
	}

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/mods/%d", d.officialAPIBase, projectID), nil)
	if err != nil {
		return modInfo{}
	}
	req.Header.Set("x-api-key", d.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Get().Debug().Err(err).Int("projectID", projectID).Msg("cannot resolve mod page URL")
		return modInfo{}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return modInfo{}
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Data *struct {
			Name  string `json:"name"`
			Slug  string `json:"slug"`
			Links struct {
				WebsiteURL string `json:"websiteUrl"`
			} `json:"links"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Data == nil {
		return modInfo{}
	}

	websiteURL := result.Data.Links.WebsiteURL
	if websiteURL == "" && result.Data.Slug != "" {
		websiteURL = "https://www.curseforge.com/minecraft/mc-mods/" + result.Data.Slug
	}
	return modInfo{Name: result.Data.Name, WebsiteURL: websiteURL}
}
