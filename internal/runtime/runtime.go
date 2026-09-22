package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/bhhoang/AutoPackMC/pkg/logger"
	"github.com/bhhoang/AutoPackMC/pkg/utils"
)

// defaultRAM is the max heap used when neither --ram nor the server's
// user_jvm_args.txt sets one.
const defaultRAM = "2G"

// Start launches the Minecraft server located in serverDir.
// ram is a JVM heap size string such as "4G" or "2048M"; empty means not
// specified, so a -Xmx in user_jvm_args.txt or defaultRAM applies.
// javaPath may be "java" to use PATH.
func Start(serverDir, ram, javaPath string) error {
	log := logger.Get()

	if javaPath == "" {
		javaPath = "java"
	}

	serverJAR, args, err := buildLaunchArgs(serverDir, ram)
	if err != nil {
		return err
	}

	log.Info().
		Str("serverDir", serverDir).
		Str("jar", serverJAR).
		Strs("args", args).
		Msg("starting Minecraft server")

	cmd := exec.Command(javaPath, args...) // #nosec G204
	cmd.Dir = serverDir
	// Forward stdin so console commands such as "stop" reach the server.
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start server process: %w", err)
	}

	// Handle graceful shutdown on SIGINT / SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case sig := <-sigCh:
		log.Info().Str("signal", sig.String()).Msg("received signal, stopping server")
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		return <-done
	case err := <-done:
		return err
	}
}

// buildLaunchArgs inspects serverDir and returns the server JAR (or args file)
// being launched and the full slice of arguments to pass to java.
func buildLaunchArgs(serverDir, ram string) (string, []string, error) {
	// Forge 1.17+ and NeoForge: the installer writes the classpath and main
	// class into libraries/.../{unix,win}_args.txt, which its run.sh/run.bat
	// pass to java as @-files. Launch java with them directly so this works on
	// every OS without a shell.
	if argsFile := findLoaderArgsFile(serverDir); argsFile != "" {
		args := []string{"-Xms512M"}
		userArgs := filepath.Join(serverDir, "user_jvm_args.txt")
		hasUserArgs := utils.FileExists(userArgs)
		if hasUserArgs {
			args = append(args, "@user_jvm_args.txt")
		}
		// The JVM uses the last -Xmx it is given, so an explicit ram goes after
		// user_jvm_args.txt to override it; otherwise the file's value stands.
		switch {
		case ram != "":
			args = append(args, "-Xmx"+ram)
		case hasUserArgs && setsMaxHeap(userArgs):
			logger.Get().Info().Msg("using max heap (-Xmx) from user_jvm_args.txt")
		default:
			args = append(args, "-Xmx"+defaultRAM)
		}
		args = append(args, "@"+filepath.ToSlash(argsFile), "nogui")
		return argsFile, args, nil
	}

	// Fabric
	fabricJAR := filepath.Join(serverDir, "fabric-server-launch.jar")
	if utils.FileExists(fabricJAR) {
		args := jvmArgs(ram, "fabric-server-launch.jar")
		return fabricJAR, args, nil
	}

	// Forge legacy (<1.17) — forge-*.jar in the server root (not the installer).
	// Only the root is searched: mods/ and libraries/ hold unrelated forge-*.jar files.
	forgeJARs, err := filepath.Glob(filepath.Join(serverDir, "forge-*.jar"))
	if err != nil {
		return "", nil, err
	}
	for _, jar := range forgeJARs {
		base := strings.ToLower(filepath.Base(jar))
		if strings.Contains(base, "installer") {
			continue
		}
		return jar, jvmArgs(ram, filepath.Base(jar)), nil
	}

	// Generic fallback: any server.jar
	serverJAR := filepath.Join(serverDir, "server.jar")
	if utils.FileExists(serverJAR) {
		return serverJAR, jvmArgs(ram, "server.jar"), nil
	}

	return "", nil, fmt.Errorf("no server JAR found in %q", serverDir)
}

// loaderArgsDirs are where Forge and NeoForge installers put their launch
// args files, relative to the server directory. NeoForge for 1.20.1 still
// used the net/neoforged/forge coordinates.
var loaderArgsDirs = []string{
	"libraries/net/minecraftforge/forge",
	"libraries/net/neoforged/neoforge",
	"libraries/net/neoforged/forge",
}

// findLoaderArgsFile returns the args file for the current OS, relative to
// serverDir, or "" when the server was not installed by a modern Forge or
// NeoForge installer. The other OS's file is used if the preferred one is missing.
func findLoaderArgsFile(serverDir string) string {
	names := []string{"unix_args.txt", "win_args.txt"}
	if goruntime.GOOS == "windows" {
		names[0], names[1] = names[1], names[0]
	}
	for _, name := range names {
		for _, dir := range loaderArgsDirs {
			matches, _ := filepath.Glob(filepath.Join(serverDir, filepath.FromSlash(dir), "*", name))
			if len(matches) == 0 {
				continue
			}
			// Several versions may be installed; use the most recently installed.
			sort.Slice(matches, func(i, j int) bool { return modTime(matches[i]).After(modTime(matches[j])) })
			rel, err := filepath.Rel(serverDir, matches[0])
			if err != nil {
				continue
			}
			return rel
		}
	}
	return ""
}

func modTime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// setsMaxHeap reports whether a JVM @-file sets -Xmx outside a comment.
func setsMaxHeap(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "-Xmx") {
				return true
			}
		}
	}
	return false
}

func jvmArgs(ram, jarName string) []string {
	if ram == "" {
		ram = defaultRAM
	}
	return []string{
		"-Xms512M",
		fmt.Sprintf("-Xmx%s", ram),
		"-jar",
		jarName,
		"nogui",
	}
}
