package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Settings are the app-wide choices made on the Settings screen.
type Settings struct {
	Language   string `json:"language"`   // "en", "vi", or "" to follow Windows
	Theme      string `json:"theme"`      // "system", "light" or "dark"
	ServersDir string `json:"serversDir"` // where new servers are created
	APIKey     string `json:"apiKey"`     // CurseForge key; empty uses the built-in one
	AutoJava   bool   `json:"autoJava"`   // pick and download Java automatically
}

// ServerRecord is a server the app knows about.
type ServerRecord struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Dir           string    `json:"dir"`
	Source        string    `json:"source"` // what it was set up from: a link or a file
	PackVersion   string    `json:"packVersion"`
	MC            string    `json:"mc"`
	Loader        string    `json:"loader"`
	LoaderVersion string    `json:"loaderVersion"`
	LogoURL       string    `json:"logoUrl"`
	JavaPath      string    `json:"javaPath"`
	JavaExplicit  bool      `json:"javaExplicit"` // the user chose Java; don't swap in the server folder's JDK
	RAMGB         int       `json:"ramGb"`
	ProjectIDs    []int     `json:"projectIds"` // CurseForge projects in the pack
	LeftOff       []LeftOff `json:"leftOff"`    // client-only mods setup kept off
	Added         []Added   `json:"added"`      // mods the user added
	Include       []string  `json:"include"`    // kept on updates even if client-only
	Exclude       []string  `json:"exclude"`    // always left off
	SkipClean     bool      `json:"skipClean"`
	Created       time.Time `json:"created"`
}

// LeftOff is a client-only mod that setup kept off a server.
type LeftOff struct {
	ProjectID int    `json:"projectId"`
	FileID    int    `json:"fileId"`
	Slug      string `json:"slug"`
	FileName  string `json:"fileName"`
	ByList    bool   `json:"byList"`
}

// Added is a mod the user put on a server.
type Added struct {
	ProjectID int    `json:"projectId"` // 0 for a .jar the user chose
	FileID    int    `json:"fileId"`
	Name      string `json:"name"`
	FileName  string `json:"fileName"`
}

// clone returns a copy that shares no slices with r, so callers can read it
// while the store changes the original.
func (r ServerRecord) clone() ServerRecord {
	r.ProjectIDs = append([]int(nil), r.ProjectIDs...)
	r.LeftOff = append([]LeftOff(nil), r.LeftOff...)
	r.Added = append([]Added(nil), r.Added...)
	r.Include = append([]string(nil), r.Include...)
	r.Exclude = append([]string(nil), r.Exclude...)
	return r
}

// store keeps settings and servers in JSON files in dir.
type store struct {
	dir string

	mu       sync.Mutex
	settings Settings
	servers  []ServerRecord
}

const (
	settingsFile = "settings.json"
	serversFile  = "servers.json"
)

func loadStore(dir string, defaults Settings) (*store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	s := &store{dir: dir, settings: defaults}
	if err := readJSON(filepath.Join(dir, settingsFile), &s.settings); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(dir, serversFile), &s.servers); err != nil {
		return nil, err
	}
	return s, nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

// writeJSON replaces path atomically, so a crash never leaves half a file.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

func (s *store) SaveSettings(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = v
	return writeJSON(filepath.Join(s.dir, settingsFile), v)
}

// Servers returns a copy of the server list.
func (s *store) Servers() []ServerRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ServerRecord, len(s.servers))
	for i, r := range s.servers {
		out[i] = r.clone()
	}
	return out
}

func (s *store) Server(id string) (ServerRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.servers {
		if r.ID == id {
			return r.clone(), true
		}
	}
	return ServerRecord{}, false
}

// UpdateServer applies change to the server with id (adding it when it is
// new) and saves the list.
func (s *store) UpdateServer(id string, change func(*ServerRecord)) (ServerRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := -1
	for j := range s.servers {
		if s.servers[j].ID == id {
			i = j
			break
		}
	}
	if i < 0 {
		s.servers = append(s.servers, ServerRecord{ID: id, Created: time.Now()})
		i = len(s.servers) - 1
	}
	change(&s.servers[i])
	return s.servers[i].clone(), writeJSON(filepath.Join(s.dir, serversFile), s.servers)
}

func (s *store) RemoveServer(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.servers[:0]
	for _, r := range s.servers {
		if r.ID != id {
			kept = append(kept, r)
		}
	}
	s.servers = kept
	return writeJSON(filepath.Join(s.dir, serversFile), s.servers)
}
