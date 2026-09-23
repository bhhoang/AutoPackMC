package runtime

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bhhoang/IDISMAM/pkg/logger"
)

// ClientOnlyMod is a mod that crashed a dedicated server by loading
// client-only game code.
type ClientOnlyMod struct {
	ModID string
	Name  string // display name, when the report gives one
	File  string // jar file name, when the report gives one
}

var (
	crashModHeaderRe = regexp.MustCompile(`^-- MOD (\S+) --\s*$`)
	crashModFileRe   = regexp.MustCompile(`^\s*Mod File:\s*(.+?)\s*$`)
	crashFailureRe   = regexp.MustCompile(`^\s*Failure message:\s*(.+) \([^()]+\) has failed to load correctly`)
)

// clientOnlyCrashMarker is how Forge and NeoForge report a mod touching
// client classes on a dedicated server.
const clientOnlyCrashMarker = "invalid dist DEDICATED_SERVER"

// FindClientOnlyMods returns the mods a Forge/NeoForge crash report blames
// for loading client-only classes on a dedicated server.
func FindClientOnlyMods(report string) []ClientOnlyMod {
	var mods []ClientOnlyMod
	var cur *ClientOnlyMod
	blamed := false
	flush := func() {
		if cur != nil && blamed {
			mods = append(mods, *cur)
		}
		cur, blamed = nil, false
	}

	for _, line := range strings.Split(strings.ReplaceAll(report, "\r\n", "\n"), "\n") {
		if m := crashModHeaderRe.FindStringSubmatch(line); m != nil {
			flush()
			cur = &ClientOnlyMod{ModID: m[1]}
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(line, "-- ") { // next non-mod section
			flush()
			continue
		}
		if m := crashModFileRe.FindStringSubmatch(line); m != nil {
			// Paths look like /D:/server/mods/x.jar or /srv/mods/x.jar.
			cur.File = path.Base(filepath.ToSlash(m[1]))
		} else if m := crashFailureRe.FindStringSubmatch(line); m != nil {
			cur.Name = m[1]
		}
		if strings.Contains(line, clientOnlyCrashMarker) {
			blamed = true
		}
	}
	flush()
	return mods
}

// reportClientOnlyCrash logs the client-only mods named in crash reports
// written since the server started, with what to do about them, and returns
// them.
func reportClientOnlyCrash(serverDir string, since time.Time) []ClientOnlyMod {
	reports, _ := filepath.Glob(filepath.Join(serverDir, "crash-reports", "crash-*.txt"))
	seen := make(map[string]bool)
	var found []ClientOnlyMod
	for _, r := range reports {
		if info, err := os.Stat(r); err != nil || info.ModTime().Before(since) {
			continue
		}
		data, err := os.ReadFile(r)
		if err != nil {
			continue
		}
		for _, mod := range FindClientOnlyMods(string(data)) {
			if seen[mod.ModID] {
				continue
			}
			seen[mod.ModID] = true
			found = append(found, mod)
			ev := logger.Get().Error().Str("modId", mod.ModID).Str("report", r)
			if mod.Name != "" {
				ev = ev.Str("mod", mod.Name)
			}
			if mod.File != "" {
				ev = ev.Str("file", filepath.Join("mods", mod.File))
			}
			ev.Msg("client-only mod crashed the server; remove it from mods/ and start again (pass its CurseForge slug to setup --exclude-mods to keep it out)")
		}
	}
	return found
}
