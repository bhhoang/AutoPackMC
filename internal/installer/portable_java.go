package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bhhoang/Maple/pkg/logger"
)

// portableJavaMarker tags the lines UsePortableJava adds, so re-running setup
// (for example with a different JDK) replaces them instead of stacking copies.
const portableJavaMarker = "mcpackctl: portable Java"

// UsePortableJava points the java command in the loader's run.bat and run.sh
// at the portable JDK when javaPath lies inside serverDir (where setup
// downloads it). The scripts fall back to java on PATH when that JDK is
// missing, e.g. after copying the server to another OS. Scripts without a
// plain "java ..." launch line are left untouched.
func UsePortableJava(serverDir, javaPath string) error {
	rel, err := filepath.Rel(serverDir, javaPath)
	if err != nil || !filepath.IsLocal(rel) {
		return nil
	}
	// javaPath is <jdk home>/bin/java[.exe].
	jdkHome := filepath.ToSlash(filepath.Dir(filepath.Dir(rel)))

	// Both blocks honour a JAVA variable set by the user. The block length must
	// stay the same so that blocks from earlier runs are replaced cleanly.
	batJava := filepath.FromSlash("%~dp0" + jdkHome + "/bin/java.exe")
	bat := []string{
		"REM " + portableJavaMarker + " (set JAVA to override; falls back to java on PATH)",
		fmt.Sprintf(`if not defined JAVA if exist "%s" set "JAVA=%s"`, batJava, batJava),
		`if not defined JAVA set "JAVA=java"`,
	}
	if err := patchRunScript(filepath.Join(serverDir, "run.bat"), bat, `"%JAVA%"`, 1); err != nil {
		return err
	}

	// DIR is absolute, so the path stays valid when the script changes
	// directory or is started from elsewhere (e.g. ./server/run.sh).
	sh := []string{
		"# " + portableJavaMarker + " (set JAVA to override; falls back to java on PATH)",
		`DIR="$(cd "$(dirname "$0")" && pwd)"`,
		fmt.Sprintf(`if [ -z "${JAVA:-}" ]; then JAVA=java; if [ -x "$DIR/%s/bin/java" ]; then JAVA="$DIR/%s/bin/java"; fi; fi`, jdkHome, jdkHome),
	}
	return patchRunScript(filepath.Join(serverDir, "run.sh"), sh, `"$JAVA"`, 1)
}

// patchRunScript inserts block at line index insertAt of path and replaces the
// leading "java" of every launch line with javaRef. A block from a previous
// run is replaced. Missing scripts are ignored. insertAt is 1 for both
// scripts: after the shebang in run.sh, and after "@echo off" in run.bat so
// the block is not echoed.
func patchRunScript(path string, block []string, javaRef string, insertAt int) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}

	content := string(data)
	newline := "\n"
	if strings.Contains(content, "\r\n") {
		newline = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")

	var out []string
	patched := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.Contains(line, portableJavaMarker) {
			patched = true
			i += len(block) - 1 // drop the old block
			continue
		}
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(strings.ToLower(trimmed), "java ") {
			indent := line[:len(line)-len(trimmed)]
			line = indent + javaRef + trimmed[len("java"):]
			patched = true
		}
		out = append(out, line)
	}
	if !patched {
		return nil
	}

	insertAt = min(insertAt, len(out))
	out = append(out[:insertAt], append(append([]string{}, block...), out[insertAt:]...)...)

	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, newline)), info.Mode().Perm()); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	logger.Get().Info().Str("script", path).Msg("run script uses the portable JDK")
	return nil
}
