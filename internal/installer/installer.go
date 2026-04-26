package installer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bhhoang/AutoPackMC/pkg/logger"
	"github.com/bhhoang/AutoPackMC/pkg/utils"
)

const (
	forgeInstallerURL  = "https://maven.minecraftforge.net/net/minecraftforge/forge/%s-%s/forge-%s-%s-installer.jar"
	fabricServerJarURL = "https://meta.fabricmc.net/v2/versions/loader/%s/%s/server/jar"

	defaultEULA = "eula=true\n"
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
	return writeServerProperties(serverDir)
}

func installForge(serverDir, mcVersion, forgeVersion, javaPath string) error {
	log := logger.Get()

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

func installFabric(serverDir, mcVersion, loaderVersion string) error {
	log := logger.Get()

	jarURL := fmt.Sprintf(fabricServerJarURL, mcVersion, loaderVersion)
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
