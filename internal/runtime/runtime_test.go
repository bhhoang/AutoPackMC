package runtime

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"testing"
)

func touch(t *testing.T, dir, rel string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func osArgsName() string {
	if goruntime.GOOS == "windows" {
		return "win_args.txt"
	}
	return "unix_args.txt"
}

func TestBuildLaunchArgsModernForge(t *testing.T) {
	dir := t.TempDir()
	// run.sh must not be what gets launched: java cannot execute a shell script.
	touch(t, dir, "run.sh")
	touch(t, dir, "user_jvm_args.txt")
	touch(t, dir, "libraries/net/minecraftforge/forge/1.20.1-47.4.0/unix_args.txt")
	touch(t, dir, "libraries/net/minecraftforge/forge/1.20.1-47.4.0/win_args.txt")

	_, args, err := buildLaunchArgs(dir, "4G")
	if err != nil {
		t.Fatal(err)
	}
	// An explicit ram comes after user_jvm_args.txt so it overrides the file.
	want := []string{
		"-Xms512M", "@user_jvm_args.txt", "-Xmx4G",
		"@libraries/net/minecraftforge/forge/1.20.1-47.4.0/" + osArgsName(), "nogui",
	}
	if !slices.Equal(args, want) {
		t.Errorf("args = %q, want %q", args, want)
	}
}

func TestBuildLaunchArgsNeoForgeWithoutUserArgs(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "libraries/net/neoforged/neoforge/21.1.77/"+osArgsName())

	_, args, err := buildLaunchArgs(dir, "6G")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-Xms512M", "-Xmx6G", "@libraries/net/neoforged/neoforge/21.1.77/" + osArgsName(), "nogui"}
	if !slices.Equal(args, want) {
		t.Errorf("args = %q, want %q", args, want)
	}
}

func TestBuildLaunchArgsLegacyForgeIgnoresModsAndInstaller(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "run.sh")
	touch(t, dir, "mods/forge-config-api-port-8.0.0.jar")
	touch(t, dir, "forge-1.12.2-14.23.5.2860-installer.jar")
	touch(t, dir, "forge-1.12.2-14.23.5.2860.jar")

	_, args, err := buildLaunchArgs(dir, "2G")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-Xms512M", "-Xmx2G", "-jar", "forge-1.12.2-14.23.5.2860.jar", "nogui"}
	if !slices.Equal(args, want) {
		t.Errorf("args = %q, want %q", args, want)
	}
}

func TestBuildLaunchArgsFabric(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "run.sh")
	touch(t, dir, "fabric-server-launch.jar")

	_, args, err := buildLaunchArgs(dir, "2G")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-Xms512M", "-Xmx2G", "-jar", "fabric-server-launch.jar", "nogui"}
	if !slices.Equal(args, want) {
		t.Errorf("args = %q, want %q", args, want)
	}
}

func TestBuildLaunchArgsNothingFound(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "run.sh")
	if _, _, err := buildLaunchArgs(dir, "2G"); err == nil {
		t.Fatal("expected an error when no server jar exists")
	}
}

func TestBuildLaunchArgsMaxHeapFromUserArgs(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "libraries/net/minecraftforge/forge/1.20.1-47.4.0/"+osArgsName())
	userArgs := "# For example, to set the maximum to 3GB: -Xmx3G\n-Xmx4G\n"
	if err := os.WriteFile(filepath.Join(dir, "user_jvm_args.txt"), []byte(userArgs), 0o644); err != nil {
		t.Fatal(err)
	}

	// No ram given: the file's -Xmx4G applies and no default is added.
	_, args, err := buildLaunchArgs(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-Xms512M", "@user_jvm_args.txt", "@libraries/net/minecraftforge/forge/1.20.1-47.4.0/" + osArgsName(), "nogui"}
	if !slices.Equal(args, want) {
		t.Errorf("args = %q, want %q", args, want)
	}
}

func TestBuildLaunchArgsDefaultMaxHeap(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "libraries/net/minecraftforge/forge/1.20.1-47.4.0/"+osArgsName())
	// Forge's stock file only mentions -Xmx in comments.
	if err := os.WriteFile(filepath.Join(dir, "user_jvm_args.txt"), []byte("# -Xmx4G\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, args, err := buildLaunchArgs(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(args, "-Xmx"+defaultRAM) {
		t.Errorf("args = %q, want the default -Xmx%s", args, defaultRAM)
	}
}
