package resolver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// cfModsClassID is CurseForge's class for mods (modpacks are cfClassID).
const cfModsClassID = 6

// ErrNoCompatibleFile means a mod has no file for the requested Minecraft
// version and loader.
var ErrNoCompatibleFile = errors.New("no file for this Minecraft version and loader")

// cfModURLRegex matches a CurseForge mod page and captures its slug.
var cfModURLRegex = regexp.MustCompile(`(?i)^https?://(?:www\.)?curseforge\.com/minecraft/mc-mods/([a-zA-Z0-9_-]+)`)

// Project is a CurseForge mod or modpack as shown to a user.
type Project struct {
	ID            int    `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Summary       string `json:"summary"`
	Author        string `json:"author"`
	DownloadCount int64  `json:"downloadCount"`
	LogoURL       string `json:"logoUrl"`
	PageURL       string `json:"pageUrl"`
	// ClientOnly is set by SearchMods when the newest file for the searched
	// version and loader is tagged Client without Server.
	ClientOnly bool `json:"clientOnly"`
}

// ModFile is a downloadable file of a mod.
type ModFile struct {
	ID           int          `json:"id"`
	FileName     string       `json:"fileName"`
	GameVersions []string     `json:"gameVersions"`
	ReleaseType  int          `json:"releaseType"` // 1=Release, 2=Beta, 3=Alpha
	Dependencies []Dependency `json:"dependencies"`
}

// Dependency is another mod a file relies on.
type Dependency struct {
	ModID        int `json:"modId"`
	RelationType int `json:"relationType"` // see RequiredDependency
}

// RequiredDependency is CurseForge's relationType for a mod that must be
// installed too (the others are embedded, optional, tool, incompatible and
// include).
const RequiredDependency = 3

// RequiredMods returns the project IDs of the mods this file cannot run
// without.
func (f ModFile) RequiredMods() []int {
	var ids []int
	for _, d := range f.Dependencies {
		if d.RelationType == RequiredDependency {
			ids = append(ids, d.ModID)
		}
	}
	return ids
}

// ClientOnly reports whether CurseForge tags the file Client without Server.
func (f ModFile) ClientOnly() bool {
	client, server := false, false
	for _, v := range f.GameVersions {
		switch v {
		case "Client":
			client = true
		case "Server":
			server = true
		}
	}
	return client && !server
}

// cfProject is the part of the API's mod payload that Project uses.
type cfProject struct {
	ID            int    `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Summary       string `json:"summary"`
	DownloadCount int64  `json:"downloadCount"`
	Authors       []struct {
		Name string `json:"name"`
	} `json:"authors"`
	Logo *struct {
		ThumbnailURL string `json:"thumbnailUrl"`
	} `json:"logo"`
	Links struct {
		WebsiteURL string `json:"websiteUrl"`
	} `json:"links"`
	LatestFiles []struct {
		GameVersions []string `json:"gameVersions"`
		FileDate     string   `json:"fileDate"`
	} `json:"latestFiles"`
}

// clientOnlyFor reports whether the newest of the project's latest files made
// for mcVersion and loader is client-only.
func (p cfProject) clientOnlyFor(mcVersion, loader string) bool {
	tag, date, found := loaderTag(loader), "", ModFile{}
	for _, f := range p.LatestFiles {
		if hasTag(f.GameVersions, mcVersion) && (tag == "" || hasTag(f.GameVersions, tag)) && f.FileDate >= date {
			found, date = ModFile{GameVersions: f.GameVersions}, f.FileDate
		}
	}
	return found.ClientOnly()
}

func (p cfProject) project() Project {
	out := Project{
		ID:            p.ID,
		Slug:          p.Slug,
		Name:          p.Name,
		Summary:       p.Summary,
		DownloadCount: p.DownloadCount,
		PageURL:       p.Links.WebsiteURL,
	}
	if len(p.Authors) > 0 {
		out.Author = p.Authors[0].Name
	}
	if p.Logo != nil {
		out.LogoURL = p.Logo.ThumbnailURL
	}
	return out
}

// IsCurseForgeModURL reports whether input is a CurseForge mod page URL.
func IsCurseForgeModURL(input string) bool {
	return cfModURLRegex.MatchString(input)
}

// LoaderTypeID maps a loader name to CurseForge's modLoaderType, or 0 when
// the loader is unknown.
func LoaderTypeID(loader string) int {
	switch strings.ToLower(loader) {
	case "forge":
		return 1
	case "fabric":
		return 4
	case "quilt":
		return 5
	case "neoforge":
		return 6
	}
	return 0
}

func (r *Resolver) api() string {
	if r.apiBase != "" {
		return r.apiBase
	}
	return cfAPIBase
}

// SearchMods finds mods made for mcVersion and loader, most popular first.
// An empty query lists the most popular ones.
func (r *Resolver) SearchMods(query, mcVersion, loader string, limit int) ([]Project, error) {
	return r.search(cfModsClassID, query, mcVersion, loader, limit)
}

func (r *Resolver) search(classID int, query, mcVersion, loader string, limit int) ([]Project, error) {
	q := url.Values{}
	q.Set("gameId", strconv.Itoa(cfGameID))
	q.Set("classId", strconv.Itoa(classID))
	q.Set("sortField", "2") // popularity
	q.Set("sortOrder", "desc")
	q.Set("pageSize", strconv.Itoa(max(1, min(limit, 50))))
	if query != "" {
		q.Set("searchFilter", query)
	}
	if mcVersion != "" {
		q.Set("gameVersion", mcVersion)
	}
	if id := LoaderTypeID(loader); id != 0 {
		q.Set("modLoaderType", strconv.Itoa(id))
	}

	body, err := r.apiGet(r.api() + "/mods/search?" + q.Encode())
	if err != nil {
		return nil, fmt.Errorf("search CurseForge: %w", err)
	}
	var result struct {
		Data []cfProject `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse search response: %w", err)
	}
	out := make([]Project, 0, len(result.Data))
	for _, p := range result.Data {
		proj := p.project()
		if mcVersion != "" {
			proj.ClientOnly = p.clientOnlyFor(mcVersion, loader)
		}
		out = append(out, proj)
	}
	return out, nil
}

// Mod looks up a mod or modpack by its project ID.
func (r *Resolver) Mod(id int) (*Project, error) {
	body, err := r.apiGet(fmt.Sprintf("%s/mods/%d", r.api(), id))
	if err != nil {
		return nil, fmt.Errorf("look up mod %d: %w", id, err)
	}
	var result struct {
		Data cfProject `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse mod response: %w", err)
	}
	p := result.Data.project()
	return &p, nil
}

// WithBaseURL points the resolver at another CurseForge API address, for
// tests outside this package.
func (r *Resolver) WithBaseURL(base string) *Resolver {
	r.apiBase = base
	return r
}

// ModFromURL looks up the mod a CurseForge mod page URL points at.
func (r *Resolver) ModFromURL(pageURL string) (*Project, error) {
	m := cfModURLRegex.FindStringSubmatch(pageURL)
	if m == nil {
		return nil, fmt.Errorf("%q is not a CurseForge mod page", pageURL)
	}
	return r.projectBySlug(cfModsClassID, strings.ToLower(m[1]))
}

// PackFromURL looks up the modpack a CurseForge modpack URL points at.
func (r *Resolver) PackFromURL(pageURL string) (*Project, error) {
	slug, err := ExtractSlug(pageURL)
	if err != nil {
		return nil, err
	}
	return r.projectBySlug(cfClassID, strings.ToLower(slug))
}

func (r *Resolver) projectBySlug(classID int, slug string) (*Project, error) {
	q := url.Values{}
	q.Set("gameId", strconv.Itoa(cfGameID))
	q.Set("classId", strconv.Itoa(classID))
	q.Set("slug", slug)
	body, err := r.apiGet(r.api() + "/mods/search?" + q.Encode())
	if err != nil {
		return nil, fmt.Errorf("look up %q: %w", slug, err)
	}
	var result struct {
		Data []cfProject `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse search response: %w", err)
	}
	for _, p := range result.Data {
		if strings.EqualFold(p.Slug, slug) {
			out := p.project()
			return &out, nil
		}
	}
	return nil, fmt.Errorf("nothing on CurseForge is called %q", slug)
}

// ModFileFor returns the newest file of a mod made for mcVersion and loader,
// preferring releases over betas and alphas. It returns ErrNoCompatibleFile
// when there is none.
func (r *Resolver) ModFileFor(modID int, mcVersion, loader string) (*ModFile, error) {
	q := url.Values{}
	q.Set("pageSize", "50")
	if mcVersion != "" {
		q.Set("gameVersion", mcVersion)
	}
	if id := LoaderTypeID(loader); id != 0 {
		q.Set("modLoaderType", strconv.Itoa(id))
	}
	body, err := r.apiGet(fmt.Sprintf("%s/mods/%d/files?%s", r.api(), modID, q.Encode()))
	if err != nil {
		return nil, fmt.Errorf("list files of mod %d: %w", modID, err)
	}
	var result struct {
		Data []struct {
			ModFile
			IsAvailable bool   `json:"isAvailable"`
			FileDate    string `json:"fileDate"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse files response: %w", err)
	}

	// The API filters loosely, so check the version and loader tags too.
	loaderTag := loaderTag(loader)
	var best *ModFile
	bestDate := ""
	for i := range result.Data {
		f := &result.Data[i]
		if !f.IsAvailable || !hasTag(f.GameVersions, mcVersion) || (loaderTag != "" && !hasTag(f.GameVersions, loaderTag)) {
			continue
		}
		better := best == nil ||
			releaseRank(f.ReleaseType) < releaseRank(best.ReleaseType) ||
			(releaseRank(f.ReleaseType) == releaseRank(best.ReleaseType) && f.FileDate > bestDate)
		if better {
			file := f.ModFile
			best, bestDate = &file, f.FileDate
		}
	}
	if best == nil {
		return nil, ErrNoCompatibleFile
	}
	return best, nil
}

// releaseRank orders release types from most to least stable.
func releaseRank(t int) int {
	if t < 1 || t > 3 {
		return 4
	}
	return t
}

// loaderTag is how CurseForge lists a loader among a file's game versions.
func loaderTag(loader string) string {
	switch strings.ToLower(loader) {
	case "forge":
		return "Forge"
	case "neoforge":
		return "NeoForge"
	case "fabric":
		return "Fabric"
	case "quilt":
		return "Quilt"
	}
	return ""
}

func hasTag(tags []string, want string) bool {
	if want == "" {
		return true
	}
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}
