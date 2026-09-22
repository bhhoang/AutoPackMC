package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func touchFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWriteRunScriptsFabric(t *testing.T) {
	dir := t.TempDir()
	touchFile(t, dir, "fabric-server-launch.jar", "")

	if err := WriteRunScripts(dir); err != nil {
		t.Fatal(err)
	}

	sh := readFile(t, filepath.Join(dir, "run.sh"))
	if !strings.Contains(sh, "\njava $(grep -v '^[[:space:]]*#' user_jvm_args.txt 2>/dev/null) -jar fabric-server-launch.jar nogui \"$@\"\n") {
		t.Errorf("run.sh does not launch the Fabric jar:\n%s", sh)
	}
	bat := readFile(t, filepath.Join(dir, "run.bat"))
	if !strings.Contains(bat, "\r\njava %JVM_ARGS% -jar fabric-server-launch.jar nogui %*\r\n") {
		t.Errorf("run.bat does not launch the Fabric jar:\n%s", bat)
	}
	if !strings.HasPrefix(bat, "@echo off\r\n") {
		t.Errorf("run.bat must start with @echo off:\n%s", bat)
	}

	// SetMaxHeap can now record --ram for Fabric too.
	written, err := SetMaxHeap(dir, "6G")
	if err != nil || !written {
		t.Fatalf("SetMaxHeap = %v, %v", written, err)
	}
	if args := readFile(t, filepath.Join(dir, "user_jvm_args.txt")); !strings.Contains(args, "\n-Xmx6G\n") {
		t.Errorf("user_jvm_args.txt = %q", args)
	}
}

func TestWriteRunScriptsLegacyForge(t *testing.T) {
	dir := t.TempDir()
	touchFile(t, dir, "forge-1.12.2-14.23.5.2860-installer.jar", "")
	touchFile(t, dir, "forge-1.12.2-14.23.5.2860.jar", "")
	touchFile(t, dir, "minecraft_server.1.12.2.jar", "")

	if err := WriteRunScripts(dir); err != nil {
		t.Fatal(err)
	}
	if sh := readFile(t, filepath.Join(dir, "run.sh")); !strings.Contains(sh, "-jar forge-1.12.2-14.23.5.2860.jar nogui") {
		t.Errorf("run.sh does not launch the Forge jar:\n%s", sh)
	}
}

func TestWriteRunScriptsKeepsForeignScriptsAndReplacesOwn(t *testing.T) {
	dir := t.TempDir()
	touchFile(t, dir, "fabric-server-launch.jar", "")
	custom := "#!/bin/sh\njava -Xmx12G -jar fabric-server-launch.jar\n"
	touchFile(t, dir, "run.sh", custom)
	// run.bat as written by an earlier mcpackctl version is regenerated.
	touchFile(t, dir, "run.bat", "@echo off\r\nREM "+runScriptMarker+" (old)\r\njava -jar old.jar\r\n")

	if err := WriteRunScripts(dir); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "run.sh")); got != custom {
		t.Errorf("custom run.sh was overwritten:\n%s", got)
	}
	if bat := readFile(t, filepath.Join(dir, "run.bat")); strings.Contains(bat, "old.jar") {
		t.Errorf("generated run.bat was not regenerated:\n%s", bat)
	}
}

// The run.sh that earlier versions wrote for every loader ended in "No
// startup target found" for Fabric; re-running setup must replace it.
func TestWriteRunScriptsReplacesBrokenLegacyScript(t *testing.T) {
	dir := t.TempDir()
	touchFile(t, dir, "fabric-server-launch.jar", "")
	touchFile(t, dir, "run.sh", "#!/usr/bin/env sh\n"+legacyRunScriptMarker+"\nset -eu\necho \"No startup target found for fabric.\" >&2\nexit 1\n")

	if err := WriteRunScripts(dir); err != nil {
		t.Fatal(err)
	}
	if sh := readFile(t, filepath.Join(dir, "run.sh")); strings.Contains(sh, "No startup target") {
		t.Errorf("broken run.sh was kept:\n%s", sh)
	}
}

func TestWriteRunScriptsLeavesModernForgeAlone(t *testing.T) {
	dir := t.TempDir()
	touchFile(t, dir, "libraries/net/minecraftforge/forge/1.20.1-47.4.0/unix_args.txt", "")
	touchFile(t, dir, "run.sh", forgeRunSh)

	if err := WriteRunScripts(dir); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "run.sh")); got != forgeRunSh {
		t.Errorf("Forge's run.sh was changed:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "run.bat")); !os.IsNotExist(err) {
		t.Error("run.bat should not be generated for modern Forge")
	}
}

// Generated scripts and the portable JDK patch work together.
func TestGeneratedScriptsUsePortableJava(t *testing.T) {
	dir := t.TempDir()
	touchFile(t, dir, "fabric-server-launch.jar", "")
	if err := WriteRunScripts(dir); err != nil {
		t.Fatal(err)
	}
	if err := UsePortableJava(dir, filepath.Join(dir, "jdk-21", "bin", "java.exe")); err != nil {
		t.Fatal(err)
	}
	if sh := readFile(t, filepath.Join(dir, "run.sh")); !strings.Contains(sh, "\n\"$JAVA\" $(grep") || !strings.Contains(sh, "jdk-21") {
		t.Errorf("run.sh not patched for the portable JDK:\n%s", sh)
	}
	if bat := readFile(t, filepath.Join(dir, "run.bat")); !strings.Contains(bat, "\r\n\"%JAVA%\" %JVM_ARGS%") || !strings.Contains(bat, "jdk-21") {
		t.Errorf("run.bat not patched for the portable JDK:\n%s", bat)
	}
}

// A block written by the previous version of UsePortableJava is replaced,
// not duplicated.
func TestUsePortableJavaUpgradesOldBlock(t *testing.T) {
	dir := t.TempDir()
	touchFile(t, dir, "run.sh", "#!/usr/bin/env sh\n"+
		"# "+portableJavaMarker+" (falls back to java on PATH)\n"+
		"JAVA=java\n"+
		"if [ -x \"$(dirname \"$0\")/jdk-17/bin/java\" ]; then JAVA=\"$(dirname \"$0\")/jdk-17/bin/java\"; fi\n"+
		"\"$JAVA\" @user_jvm_args.txt @libraries/x/unix_args.txt \"$@\"\n")

	if err := UsePortableJava(dir, filepath.Join(dir, "jdk-17", "bin", "java")); err != nil {
		t.Fatal(err)
	}
	sh := readFile(t, filepath.Join(dir, "run.sh"))
	if strings.Count(sh, portableJavaMarker) != 1 || strings.Contains(sh, `$(dirname "$0")/jdk-17`) {
		t.Errorf("old block not replaced:\n%s", sh)
	}
}
