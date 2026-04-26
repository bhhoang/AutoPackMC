package parser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ModFile represents a single mod entry in a CurseForge manifest.
type ModFile struct {
	ProjectID int  `json:"projectID"`
	FileID    int  `json:"fileID"`
	Required  bool `json:"required"`
}

// MinecraftInfo holds the Minecraft version and mod loader details.
type MinecraftInfo struct {
	Version    string      `json:"version"`
	ModLoaders []ModLoader `json:"modLoaders"`
}

// ModLoader represents a single mod loader entry.
type ModLoader struct {
	ID      string `json:"id"`
	Primary bool   `json:"primary"`
}

// Manifest represents a parsed CurseForge manifest.json.
type Manifest struct {
	Minecraft       MinecraftInfo `json:"minecraft"`
	ManifestType    string        `json:"manifestType"`
	ManifestVersion int           `json:"manifestVersion"`
	Name            string        `json:"name"`
	Version         string        `json:"version"`
	Files           []ModFile     `json:"files"`
	Overrides       string        `json:"overrides"`

	// Derived fields (not in JSON)
	LoaderType    string // "forge" or "fabric"
	LoaderVersion string // e.g. "47.2.0"
}

// ParseCurseForge reads and parses manifest.json from dir.
func ParseCurseForge(dir string) (*Manifest, error) {
	manifestPath := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest.json: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest.json: %w", err)
	}

	// Derive loader type and version from the primary mod loader
	for _, loader := range m.Minecraft.ModLoaders {
		if loader.Primary {
			parts := strings.SplitN(loader.ID, "-", 2)
			if len(parts) == 2 {
				m.LoaderType = strings.ToLower(parts[0])
				m.LoaderVersion = parts[1]
			} else {
				m.LoaderType = strings.ToLower(loader.ID)
			}
			break
		}
	}

	if m.LoaderType == "" {
		return nil, fmt.Errorf("no primary mod loader found in manifest.json")
	}

	return &m, nil
}

// RawPack holds metadata derived from scanning a raw pack directory.
type RawPack struct {
	ModsDir       string
	LoaderType    string // detected from jar names if possible, otherwise ""
	LoaderVersion string
	MCVersion     string
}

// ParseRaw inspects dir for a raw pack layout and returns a RawPack.
func ParseRaw(dir string) (*RawPack, error) {
	modsDir := filepath.Join(dir, "mods")
	if _, err := os.Stat(modsDir); err != nil {
		return nil, fmt.Errorf("mods directory not found in %q", dir)
	}

	rp := &RawPack{ModsDir: modsDir}

	// Attempt to read a version.json or pack.json for metadata
	for _, candidate := range []string{"version.json", "pack.json"} {
		data, err := os.ReadFile(filepath.Join(dir, candidate))
		if err != nil {
			continue
		}
		var meta map[string]string
		if json.Unmarshal(data, &meta) == nil {
			rp.MCVersion = meta["minecraft"]
			rp.LoaderType = strings.ToLower(meta["loader"])
			rp.LoaderVersion = meta["loaderVersion"]
		}
		break
	}

	return rp, nil
}
