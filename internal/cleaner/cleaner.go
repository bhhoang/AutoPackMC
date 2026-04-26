package cleaner

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/bhhoang/AutoPackMC/pkg/logger"
)

// clientOnlyPatterns lists filename substrings/prefixes for known client-only mods.
var clientOnlyPatterns = []string{
	"optifine",
	"optifabric",
	"sodium",
	"iris",
	"rubidium",
	"oculus",
	"embeddium",
	"replaymod",
	"screenshot-to-clipboard",
	"betterf3",
	"journeymap",
	"xaeros",
	"xaero",
	"voxelmap",
	"minimap",
	"worldmap",
	"dynamic-lights",
	"dynamiclights",
	"entityculling",
	"smoothboot",
	"lazydfu",
	"modernfix-client",
	"fpsreducer",
	"legendarytooltips",
	"itemzoom",
	"blur",
	"animatica",
	"cit-resewn",
	"continuity",
	"lambdabettergrass",
	"fabric-language-kotlin", // optional, sometimes client-only in packs
}

// Clean removes client-only mod JARs from modsDir and returns the list of removed files.
func Clean(modsDir string) ([]string, error) {
	log := logger.Get()

	entries, err := os.ReadDir(modsDir)
	if err != nil {
		return nil, err
	}

	var removed []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if !strings.HasSuffix(name, ".jar") {
			continue
		}
		if isClientOnly(name) {
			fullPath := filepath.Join(modsDir, entry.Name())
			if removeErr := os.Remove(fullPath); removeErr != nil {
				log.Warn().Err(removeErr).Str("file", entry.Name()).Msg("failed to remove client-only mod")
				continue
			}
			log.Info().Str("file", entry.Name()).Msg("removed client-only mod")
			removed = append(removed, entry.Name())
		}
	}
	return removed, nil
}

func isClientOnly(name string) bool {
	for _, pattern := range clientOnlyPatterns {
		if strings.Contains(name, pattern) {
			return true
		}
	}
	return false
}
