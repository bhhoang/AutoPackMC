package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bhhoang/AutoPackMC/pkg/logger"
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

	batJava := filepath.FromSlash("%~dp0" + jdkHome + "/bin/java.exe")
	bat := []string{
		"REM " + portableJavaMarker + " (falls back to java on PATH)",
		`set "JAVA=java"`,
		fmt.Sprintf(`if exist "%s" set "JAVA=%s"`, batJava, batJava),
	}
	if err := patchRunScript(filepath.Join(serverDir, "run.bat"), bat, `"%JAVA%"`, 1); err != nil {
		return err
	}

	sh := []string{
		"# " + portableJavaMarker + " (falls back to java on PATH)",
		"JAVA=java",
		fmt.Sprintf(`if [ -x "$(dirname "$0")/%s/bin/java" ]; then JAVA="$(dirname "$0")/%s/bin/java"; fi`, jdkHome, jdkHome),
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
