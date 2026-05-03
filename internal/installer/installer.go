package installer

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bhhoang/AutoPackMC/pkg/logger"
	"github.com/bhhoang/AutoPackMC/pkg/utils"
)

const (
	forgeInstallerURL         = "https://maven.minecraftforge.net/net/minecraftforge/forge/%s-%s/forge-%s-%s-installer.jar"
	fabricServerJarURL        = "https://meta.fabricmc.net/v2/versions/loader/%s/%s/%s/server/jar"
	fabricInstallerVersionURL = "https://meta.fabricmc.net/v2/versions/installer"

	defaultEULA             = "eula=true\n"
	defaultServerProperties = `#Minecraft server properties
server-port=25565
online-mode=true
difficulty=normal
gamemode=survival
max-players=20
spawn-protection=16
view-distance=10
simulation-distance=10
level-name=world
motd=A Minecraft Server
`
)

// Install sets up the Minecraft server inside serverDir using the given loader details.
// javaPath is used to run the Forge installer; it may be "java" to use PATH.
func Install(serverDir, loaderType, mcVersion, loaderVersion, javaPath string) error {
	log := logger.Get()

	if err := utils.EnsureDir(serverDir); err != nil {
		return fmt.Errorf("create server dir: %w", err)
	}

	if javaPath == "" {
		javaPath = "java"
	}

	log.Info().
		Str("loader", loaderType).
		Str("mcVersion", mcVersion).
		Str("loaderVersion", loaderVersion).
		Str("serverDir", serverDir).
		Msg("installing server")

	switch strings.ToLower(loaderType) {
	case "forge", "neoforge":
		if err := installForge(serverDir, mcVersion, loaderVersion, javaPath); err != nil {
			return err
		}
	case "fabric":
		if err := installFabric(serverDir, mcVersion, loaderVersion); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported loader type %q", loaderType)
	}

	if err := writeEULA(serverDir); err != nil {
		return err
	}
	if err := writeServerProperties(serverDir); err != nil {
		return err
	}
	return WriteRunScript(serverDir, mcVersion, loaderVersion)
}

func installForge(serverDir, mcVersion, forgeVersion, javaPath string) error {
	log := logger.Get()

	// Normalize: if the caller passed "1.20.1-47.4.0" instead of just "47.4.0",
	// strip the "<mcVersion>-" prefix so the URL is always well-formed.
	if mcVersion != "" && strings.HasPrefix(forgeVersion, mcVersion+"-") {
		forgeVersion = strings.TrimPrefix(forgeVersion, mcVersion+"-")
	} else if mcVersion == "" {
		// When mcVersion is not provided but forgeVersion is in the combined
		// "<mcVersion>-<forgeVersion>" format (e.g. "1.20.1-47.4.0"), extract
		// mcVersion from the prefix so the installer URL is well-formed.
		if idx := strings.Index(forgeVersion, "-"); idx != -1 {
			mcVersion = forgeVersion[:idx]
			forgeVersion = forgeVersion[idx+1:]
		}
	}

	installerURL := fmt.Sprintf(forgeInstallerURL, mcVersion, forgeVersion, mcVersion, forgeVersion)
	installerJAR := filepath.Join(serverDir, fmt.Sprintf("forge-%s-%s-installer.jar", mcVersion, forgeVersion))

	log.Info().Str("url", installerURL).Msg("downloading Forge installer")
	if err := utils.DownloadFile(installerURL, installerJAR, nil); err != nil {
		return fmt.Errorf("download Forge installer: %w", err)
	}

	log.Info().Msg("running Forge installer (--installServer)")
	cmd := exec.Command(javaPath, "-jar", installerJAR, "--installServer") // #nosec G204
	cmd.Dir = serverDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Forge installer failed: %w", err)
	}

	// Clean up the installer jar and installer logs
	_ = os.Remove(installerJAR)
	_ = os.Remove(installerJAR + ".log")

	log.Info().Msg("Forge server installed")
	return nil
}

func fetchLatestFabricInstallerVersion() (string, error) {
	resp, err := http.Get(fabricInstallerVersionURL) // #nosec G107
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d fetching Fabric installer versions", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var versions []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	}
	if err := json.Unmarshal(body, &versions); err != nil {
		return "", fmt.Errorf("parse Fabric installer versions: %w", err)
	}

	for _, v := range versions {
		if v.Stable {
			return v.Version, nil
		}
	}
	if len(versions) > 0 {
		logger.Get().Warn().Str("version", versions[0].Version).Msg("no stable Fabric installer version found, using latest")
		return versions[0].Version, nil
	}
	return "", fmt.Errorf("no Fabric installer versions found")
}

func installFabric(serverDir, mcVersion, loaderVersion string) error {
	log := logger.Get()

	installerVersion, err := fetchLatestFabricInstallerVersion()
	if err != nil {
		return fmt.Errorf("fetch Fabric installer version: %w", err)
	}

	jarURL := fmt.Sprintf(fabricServerJarURL, mcVersion, loaderVersion, installerVersion)
	dest := filepath.Join(serverDir, "fabric-server-launch.jar")

	log.Info().Str("url", jarURL).Msg("downloading Fabric server jar")
	if err := utils.DownloadFile(jarURL, dest, nil); err != nil {
		return fmt.Errorf("download Fabric server jar: %w", err)
	}

	// Fabric needs a server.jar alongside it — use the vanilla launcher properties
	propsFile := filepath.Join(serverDir, "fabric-server-launcher.properties")
	if !utils.FileExists(propsFile) {
		if err := os.WriteFile(propsFile, []byte("serverJar=server.jar\n"), 0o644); err != nil {
			return fmt.Errorf("write fabric launcher properties: %w", err)
		}
	}

	log.Info().Msg("Fabric server jar downloaded")
	return nil
}

func writeEULA(serverDir string) error {
	eulaPath := filepath.Join(serverDir, "eula.txt")
	if utils.FileExists(eulaPath) {
		return nil
	}
	return os.WriteFile(eulaPath, []byte(defaultEULA), 0o644)
}

func writeServerProperties(serverDir string) error {
	propsPath := filepath.Join(serverDir, "server.properties")
	if utils.FileExists(propsPath) {
		return nil
	}
	return os.WriteFile(propsPath, []byte(defaultServerProperties), 0o644)
}

func WriteRunScript(serverDir, mcVersion, loaderVersion string) error {
	runShPath := filepath.Join(serverDir, "run.sh")
	if utils.FileExists(runShPath) {
		return nil
	}

	content := "#!/usr/bin/env sh\n" +
		"# Minecraft server startup script\n" +
		"set -eu\n" +
		"DIR=\"$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\"\n" +
		"if [ -z \"${JAVA:-}\" ]; then\n" +
		"  if [ -x \"$DIR/jdk-21/bin/java\" ]; then JAVA=\"$DIR/jdk-21/bin/java\";\n" +
		"  else JAVA=java; fi\n" +
		"fi\n" +
		"cd \"$DIR\"\n" +
		"ARGS=\"libraries/net/minecraftforge/forge/" + mcVersion + "-" + loaderVersion + "/unix_args.txt\"\n" +
		"if [ -f \"$ARGS\" ]; then\n" +
		"  [ -f user_jvm_args.txt ] || : > user_jvm_args.txt\n" +
		"  exec \"$JAVA\" @user_jvm_args.txt @\"$ARGS\" \"$@\"\n" +
		"fi\n" +
		"LEGACY=\"minecraftforge-universal-" + mcVersion + "-" + loaderVersion + "-v" + strings.ReplaceAll(mcVersion, ".", "") + "-pregradle.jar\"\n" +
		"if [ -f \"$LEGACY\" ]; then exec \"$JAVA\" -jar \"$LEGACY\" nogui \"$@\"; fi\n" +
		"echo \"No Forge startup target found.\" >&2\n" +
		"exit 1\n"
	return os.WriteFile(runShPath, []byte(content), 0o755)
}
