package detector

import (
	"fmt"
	"os"
	"path/filepath"
)

// PackType represents the detected modpack format.
type PackType int

const (
	PackTypeUnknown  PackType = iota
	PackTypeCurseForge        // contains manifest.json
	PackTypeRaw               // contains /mods directory
)

// String returns a human-readable name for the PackType.
func (p PackType) String() string {
	switch p {
	case PackTypeCurseForge:
		return "CurseForge"
	case PackTypeRaw:
		return "Raw"
	default:
		return "Unknown"
	}
}

// Detect inspects dir and returns the PackType.
// dir may be the root of an already-extracted pack or a directory.
func Detect(dir string) (PackType, error) {
	manifestPath := filepath.Join(dir, "manifest.json")
	if _, err := os.Stat(manifestPath); err == nil {
		return PackTypeCurseForge, nil
	}

	modsPath := filepath.Join(dir, "mods")
	if info, err := os.Stat(modsPath); err == nil && info.IsDir() {
		return PackTypeRaw, nil
	}

	return PackTypeUnknown, fmt.Errorf("cannot detect pack type in %q: no manifest.json or mods/ directory found", dir)
}
