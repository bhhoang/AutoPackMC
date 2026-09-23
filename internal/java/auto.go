package java

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/bhhoang/Maple/pkg/logger"
	"github.com/bhhoang/Maple/pkg/utils"
)

// fallbackVersion is downloaded when the Minecraft version is unknown and no
// java is available on PATH.
const fallbackVersion = 21

var javaVersionRe = regexp.MustCompile(`version "([^"]+)"`)

// RequiredVersion returns the Java major version a Minecraft server of the
// given version runs on, following Mojang's version manifests. It returns 0
// when mcVersion cannot be parsed.
func RequiredVersion(mcVersion string) int {
	parts := strings.Split(strings.TrimSpace(mcVersion), ".")
	if len(parts) < 2 {
		return 0
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0
	}
	patch := 0
	if len(parts) > 2 {
		patch, _ = strconv.Atoi(parts[2])
	}

	// Year-based versioning (26.1, ...) replaced 1.x in 2026.
	if major >= 26 {
		return 25
	}
	if major != 1 {
		return 0
	}
	switch {
	case minor <= 16:
		return 8
	case minor <= 19, minor == 20 && patch <= 4:
		// 1.17 needs 16+, but 17 is the LTS that Forge 1.17.1 also supports.
		return 17
	default:
		return 21
	}
}

// InstalledVersion runs `javaPath -version` and returns the Java major version.
func InstalledVersion(javaPath string) (int, error) {
	cmd := exec.Command(javaPath, "-version") // #nosec G204
	utils.HideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, err
	}
	m := javaVersionRe.FindSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("unrecognised `java -version` output: %q", strings.TrimSpace(string(out)))
	}
	return parseMajor(string(m[1]))
}

// parseMajor turns "1.8.0_401" into 8 and "21.0.4" into 21.
func parseMajor(v string) (int, error) {
	v = strings.TrimPrefix(v, "1.")
	end := strings.IndexFunc(v, func(r rune) bool { return r < '0' || r > '9' })
	if end == -1 {
		end = len(v)
	}
	n, err := strconv.Atoi(v[:end])
	if err != nil {
		return 0, fmt.Errorf("parse java version %q: %w", v, err)
	}
	return n, nil
}

// Ensure returns a java executable suitable for mcVersion. It uses javaPath
// (typically "java" from PATH) when that reports exactly the required major
// version, otherwise it downloads a portable Temurin JDK for the current OS
// and architecture into destDir (reused on later runs).
func Ensure(javaPath, mcVersion, destDir string) (string, error) {
	log := logger.Get()

	required := RequiredVersion(mcVersion)
	installed, err := InstalledVersion(javaPath)
	switch {
	case err != nil:
		log.Info().Str("java", javaPath).Err(err).Msg("no usable java found")
	case required == 0:
		log.Info().Int("version", installed).Str("mc", mcVersion).Msg("unknown Minecraft version, using installed java")
		return javaPath, nil
	case installed == required:
		log.Info().Int("version", installed).Str("java", javaPath).Msg("installed java matches Minecraft version")
		return javaPath, nil
	default:
		log.Info().Int("installed", installed).Int("required", required).Str("mc", mcVersion).
			Msg("installed java does not match Minecraft version")
	}

	if required == 0 {
		required = fallbackVersion
		log.Warn().Str("mc", mcVersion).Int("version", required).Msg("unknown Minecraft version, downloading default Java")
	}
	log.Info().Int("version", required).Str("mc", mcVersion).Msg("using portable JDK")
	return Download(required, destDir)
}

// FindLocal returns the java executable of the newest jdk-<N> directory that
// Download placed in dir, or "" if there is none.
func FindLocal(dir string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "jdk-*", "bin", JavaBinaryName()))
	type jdk struct {
		version int
		path    string
	}
	var found []jdk
	for _, m := range matches {
		name := filepath.Base(filepath.Dir(filepath.Dir(m)))
		v, err := strconv.Atoi(strings.TrimPrefix(name, "jdk-"))
		if err != nil {
			continue
		}
		if fi, err := os.Stat(m); err == nil && !fi.IsDir() {
			found = append(found, jdk{v, m})
		}
	}
	if len(found) == 0 {
		return ""
	}
	sort.Slice(found, func(i, j int) bool { return found[i].version > found[j].version })
	return found[0].path
}
