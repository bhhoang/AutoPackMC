package runtime

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/bhhoang/IDISMAM/pkg/logger"
	"github.com/bhhoang/IDISMAM/pkg/utils"
)

// defaultRAM is the max heap used when neither --ram nor the server's
// user_jvm_args.txt sets one.
const defaultRAM = "2G"

// Start launches the Minecraft server located in serverDir and runs it in
// the terminal: what the user types goes to the server console, and Ctrl+C
// stops it cleanly. ram is a JVM heap size string such as "4G" or "2048M";
// empty means not specified, so a -Xmx in user_jvm_args.txt or defaultRAM
// applies. javaPath may be "java" to use PATH.
func Start(serverDir, ram, javaPath string) error {
	log := logger.Get()

	// Watch for signals before starting so an early Ctrl+C is not missed.
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	srv, err := Launch(serverDir, ram, javaPath, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	go func() {
		_, _ = io.Copy(&lockedWriter{s: srv}, os.Stdin)
	}()

	select {
	case sig := <-sigCh:
		log.Info().Str("signal", sig.String()).Msg("stopping server (press Ctrl+C again to kill it)")
		return stopServer(srv.cmd.Process, &lockedWriter{s: srv}, srv.exitCh(), sigCh, stopTimeout)
	case <-srv.Done():
		if n := len(srv.ClientOnlyMods()); n > 0 {
			return fmt.Errorf("server crashed: %d client-only mod(s) must be removed", n)
		}
		return srv.Wait()
	}
}

// stopTimeout is how long the server may take to save and exit after "stop".
const stopTimeout = 60 * time.Second

// stopServer asks the server to shut down cleanly by sending "stop" to its
// console, which saves the worlds; a signal-based stop is unsupported on
// Windows and would skip that. The process is killed if it has not exited
// after timeout or when another signal arrives on again.
func stopServer(proc *os.Process, console io.Writer, done <-chan error, again <-chan os.Signal, timeout time.Duration) error {
	log := logger.Get()

	// The write can block when the server stops reading its console, so it
	// must not hold up the timeout and kill below.
	go func() {
		if _, err := io.WriteString(console, "stop\n"); err != nil {
			log.Warn().Err(err).Msg("cannot send stop to the server console")
		}
	}()

	var reason string
	select {
	case <-done:
		// Stopped as requested; its exit status is not a failure here.
		log.Info().Msg("server stopped")
		return nil
	case <-again:
		reason = "second interrupt"
	case <-time.After(timeout):
		reason = fmt.Sprintf("server did not stop within %s", timeout)
	}

	log.Warn().Str("reason", reason).Msg("killing server; unsaved progress may be lost")
	if err := proc.Kill(); err != nil {
		return fmt.Errorf("kill server: %w", err)
	}
	<-done
	return fmt.Errorf("server killed: %s", reason)
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

	jar, err := ServerJar(serverDir)
	if err != nil {
		return "", nil, err
	}
	// Java 8, which older servers need, does not support @-files, so the
	// arguments in user_jvm_args.txt are expanded here instead.
	args := []string{"-Xms512M"}
	userArgs := readJVMArgs(filepath.Join(serverDir, "user_jvm_args.txt"))
	args = append(args, userArgs...)
	switch {
	case ram != "":
		args = append(args, "-Xmx"+ram)
	case hasMaxHeap(userArgs):
		logger.Get().Info().Msg("using max heap (-Xmx) from user_jvm_args.txt")
	default:
		args = append(args, "-Xmx"+defaultRAM)
	}
	args = append(args, "-jar", jar, "nogui")
	return filepath.Join(serverDir, jar), args, nil
}

// UsesArgsFiles reports whether serverDir was installed by a Forge 1.17+ or
// NeoForge installer, which launches through @-files and writes its own
// run.bat and run.sh.
func UsesArgsFiles(serverDir string) bool {
	return findLoaderArgsFile(serverDir) != ""
}

// ServerJar returns the jar, relative to serverDir, that starts a server
// launched with "java -jar": Fabric's launcher, a pre-1.17 Forge jar, or a
// plain server.jar.
func ServerJar(serverDir string) (string, error) {
	if utils.FileExists(filepath.Join(serverDir, "fabric-server-launch.jar")) {
		return "fabric-server-launch.jar", nil
	}

	// Forge legacy (<1.17) — forge-*.jar in the server root (not the installer).
	// Only the root is searched: mods/ and libraries/ hold unrelated forge-*.jar files.
	forgeJARs, err := filepath.Glob(filepath.Join(serverDir, "forge-*.jar"))
	if err != nil {
		return "", err
	}
	for _, jar := range forgeJARs {
		base := filepath.Base(jar)
		if !strings.Contains(strings.ToLower(base), "installer") {
			return base, nil
		}
	}

	if utils.FileExists(filepath.Join(serverDir, "server.jar")) {
		return "server.jar", nil
	}
	return "", fmt.Errorf("no server JAR found in %q", serverDir)
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

// readJVMArgs returns the arguments in a JVM @-file such as
// user_jvm_args.txt: whitespace-separated, with # starting a comment line.
// A missing file yields no arguments.
func readJVMArgs(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var args []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		args = append(args, strings.Fields(line)...)
	}
	return args
}

// setsMaxHeap reports whether a JVM @-file sets -Xmx outside a comment.
func setsMaxHeap(path string) bool {
	return hasMaxHeap(readJVMArgs(path))
}

func hasMaxHeap(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-Xmx") {
			return true
		}
	}
	return false
}
