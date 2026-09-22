package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Scripts as written by the Forge 1.20.1 installer.
const (
	forgeRunBat = "@echo off\r\n" +
		"REM Forge requires a configured set of both JVM and program arguments.\r\n" +
		"REM Add custom JVM arguments to the user_jvm_args.txt\r\n" +
		"java @user_jvm_args.txt @libraries/net/minecraftforge/forge/1.20.1-47.4.0/win_args.txt %*\r\n" +
		"pause\r\n"
	forgeRunSh = "#!/usr/bin/env sh\n" +
		"# Forge requires a configured set of both JVM and program arguments.\n" +
		"java @user_jvm_args.txt @libraries/net/minecraftforge/forge/1.20.1-47.4.0/unix_args.txt \"$@\"\n"
)

func writeScripts(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "run.bat"), []byte(forgeRunBat), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(forgeRunSh), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUsePortableJavaPatchesForgeScripts(t *testing.T) {
	dir := t.TempDir()
	writeScripts(t, dir)

	if err := UsePortableJava(dir, filepath.Join(dir, "jdk-17", "bin", "java.exe")); err != nil {
		t.Fatal(err)
	}

	bat := readFile(t, filepath.Join(dir, "run.bat"))
	jdkBat := filepath.FromSlash("%~dp0jdk-17/bin/java.exe")
	for _, want := range []string{
		"@echo off\r\nREM " + portableJavaMarker,
		`if exist "` + jdkBat + `" set "JAVA=` + jdkBat + `"`,
		"\r\n\"%JAVA%\" @user_jvm_args.txt @libraries/net/minecraftforge/forge/1.20.1-47.4.0/win_args.txt %*\r\n",
	} {
		if !strings.Contains(bat, want) {
			t.Errorf("run.bat missing %q:\n%s", want, bat)
		}
	}
	if strings.Contains(strings.ReplaceAll(bat, "\r\n", ""), "\n") {
		t.Error("run.bat line endings were changed from CRLF")
	}

	sh := readFile(t, filepath.Join(dir, "run.sh"))
	for _, want := range []string{
		"#!/usr/bin/env sh\n# " + portableJavaMarker,
		`DIR="$(cd "$(dirname "$0")" && pwd)"`,
		`if [ -z "${JAVA:-}" ]; then JAVA=java; if [ -x "$DIR/jdk-17/bin/java" ]; then JAVA="$DIR/jdk-17/bin/java"; fi; fi`,
		"\n\"$JAVA\" @user_jvm_args.txt @libraries/net/minecraftforge/forge/1.20.1-47.4.0/unix_args.txt \"$@\"\n",
	} {
		if !strings.Contains(sh, want) {
			t.Errorf("run.sh missing %q:\n%s", want, sh)
		}
	}
}

// Re-running setup with another JDK must replace the block, not add a second.
func TestUsePortableJavaRerunReplacesBlock(t *testing.T) {
	dir := t.TempDir()
	writeScripts(t, dir)

	if err := UsePortableJava(dir, filepath.Join(dir, "jdk-17", "bin", "java")); err != nil {
		t.Fatal(err)
	}
	if err := UsePortableJava(dir, filepath.Join(dir, "jdk-21", "bin", "java")); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"run.bat", "run.sh"} {
		s := readFile(t, filepath.Join(dir, name))
		if n := strings.Count(s, portableJavaMarker); n != 1 {
			t.Errorf("%s has %d portable Java blocks, want 1:\n%s", name, n, s)
		}
		if strings.Contains(s, "jdk-17") || !strings.Contains(s, "jdk-21") {
			t.Errorf("%s should reference only jdk-21:\n%s", name, s)
		}
	}
}

func TestUsePortableJavaIgnoresJavaOutsideServer(t *testing.T) {
	dir := t.TempDir()
	writeScripts(t, dir)

	for _, javaPath := range []string{"java", filepath.Join(t.TempDir(), "jdk-17", "bin", "java")} {
		if err := UsePortableJava(dir, javaPath); err != nil {
			t.Fatal(err)
		}
	}
	if got := readFile(t, filepath.Join(dir, "run.bat")); got != forgeRunBat {
		t.Errorf("run.bat changed:\n%s", got)
	}
	if got := readFile(t, filepath.Join(dir, "run.sh")); got != forgeRunSh {
		t.Errorf("run.sh changed:\n%s", got)
	}
}
