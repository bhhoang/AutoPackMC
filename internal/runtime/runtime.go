package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/bhhoang/AutoPackMC/pkg/logger"
	"github.com/bhhoang/AutoPackMC/pkg/utils"
)

// Start launches the Minecraft server located in serverDir.
// ram is a JVM heap size string such as "4G" or "2048M".
// javaPath may be "java" to use PATH.
func Start(serverDir, ram, javaPath string) error {
	log := logger.Get()

	if javaPath == "" {
		javaPath = "java"
	}
	if ram == "" {
		ram = "2G"
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

// buildLaunchArgs inspects serverDir and returns the server JAR path and the full
// slice of arguments to pass to java.
func buildLaunchArgs(serverDir, ram string) (string, []string, error) {
	// Prefer a run.sh launcher (Forge 1.17+)
	runSh := filepath.Join(serverDir, "run.sh")
	if utils.FileExists(runSh) {
		// We still launch via java using the @args file that Forge generates
		argsFile := filepath.Join(serverDir, "user_jvm_args.txt")
		if !utils.FileExists(argsFile) {
			if err := os.WriteFile(argsFile, []byte(fmt.Sprintf("-Xms512M -Xmx%s\n", ram)), 0o644); err != nil {
				return "", nil, fmt.Errorf("write user_jvm_args.txt: %w", err)
			}
		}
		return "run.sh", []string{runSh}, nil
	}

	// Fabric
	fabricJAR := filepath.Join(serverDir, "fabric-server-launch.jar")
	if utils.FileExists(fabricJAR) {
		args := jvmArgs(ram, "fabric-server-launch.jar")
		return fabricJAR, args, nil
	}

	// Forge legacy — search for forge-*.jar (not installer)
	forgeJARs, err := utils.FindFiles(serverDir, "forge-*.jar")
	if err != nil {
		return "", nil, err
	}
	for _, jar := range forgeJARs {
		base := strings.ToLower(filepath.Base(jar))
		if strings.Contains(base, "installer") {
			continue
		}
		rel, _ := filepath.Rel(serverDir, jar)
		args := jvmArgs(ram, rel)
		return jar, args, nil
	}

	// Generic fallback: any server.jar
	serverJAR := filepath.Join(serverDir, "server.jar")
	if utils.FileExists(serverJAR) {
		return serverJAR, jvmArgs(ram, "server.jar"), nil
	}

	return "", nil, fmt.Errorf("no server JAR found in %q", serverDir)
}

func jvmArgs(ram, jarName string) []string {
	return []string{
		"-Xms512M",
		fmt.Sprintf("-Xmx%s", ram),
		"-jar",
		jarName,
		"nogui",
	}
}
