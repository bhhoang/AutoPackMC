package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bhhoang/AutoPackMC/internal/cleaner"
	"github.com/bhhoang/AutoPackMC/internal/detector"
	"github.com/bhhoang/AutoPackMC/internal/downloader"
	"github.com/bhhoang/AutoPackMC/internal/installer"
	"github.com/bhhoang/AutoPackMC/internal/parser"
	"github.com/bhhoang/AutoPackMC/internal/runtime"
	"github.com/bhhoang/AutoPackMC/pkg/logger"
	"github.com/bhhoang/AutoPackMC/pkg/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

// rootCmd is the base command for mcpackctl.
var rootCmd = &cobra.Command{
	Use:   "mcpackctl",
	Short: "AutoPackMC — automated Minecraft modpack server setup",
	Long: `mcpackctl downloads, configures, and runs Minecraft modpack servers.
It supports CurseForge and raw modpack formats with Forge and Fabric loaders.`,
}

// Execute adds all child commands to the root command and runs it.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: $HOME/.config/mcpackctl/config.yaml)")
	rootCmd.PersistentFlags().String("log-level", "info", "log level (debug, info, warn, error)")
	_ = viper.BindPFlag("log_level", rootCmd.PersistentFlags().Lookup("log-level"))

	rootCmd.AddCommand(newSetupCmd())
	rootCmd.AddCommand(newStartCmd())
	rootCmd.AddCommand(newDownloadCmd())
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err == nil {
			viper.AddConfigPath(filepath.Join(home, ".config", "mcpackctl"))
		}
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	viper.SetEnvPrefix("MCPACKCTL")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Defaults
	viper.SetDefault("ram", "2G")
	viper.SetDefault("java_path", "java")
	home, _ := os.UserHomeDir()
	viper.SetDefault("cache_dir", filepath.Join(home, ".cache", "mcpackctl"))
	viper.SetDefault("workers", 4)

	_ = viper.ReadInConfig()

	logger.Init(viper.GetString("log_level"))
}

// ---------------------------------------------------------------------------
// setup command
// ---------------------------------------------------------------------------

func newSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup --input <pack.zip|dir> --output <serverDir>",
		Short: "Download and configure a modpack server",
		RunE:  runSetup,
	}

	cmd.Flags().String("input", "", "path to the modpack ZIP or extracted directory (required)")
	cmd.Flags().String("output", "./server", "destination directory for the server")
	cmd.Flags().String("ram", "", "JVM max heap size (e.g. 4G)")
	cmd.Flags().String("java-path", "", "path to java executable")
	cmd.Flags().String("force-loader", "", "override loader type: forge or fabric")
	cmd.Flags().Bool("skip-clean", false, "skip removal of client-only mods")
	_ = cmd.MarkFlagRequired("input")

	return cmd
}

func runSetup(cmd *cobra.Command, _ []string) error {
	log := logger.Get()

	input, _ := cmd.Flags().GetString("input")
	output, _ := cmd.Flags().GetString("output")
	ram, _ := cmd.Flags().GetString("ram")
	javaPath, _ := cmd.Flags().GetString("java-path")
	forceLoader, _ := cmd.Flags().GetString("force-loader")
	skipClean, _ := cmd.Flags().GetBool("skip-clean")

	if ram == "" {
		ram = viper.GetString("ram")
	}
	if javaPath == "" {
		javaPath = viper.GetString("java_path")
	}

	if detector.IsGoogleDriveURL(input) {
		log.Info().Str("url", input).Msg("downloading from Google Drive")

		fileID, err := utils.ExtractGoogleDriveFileID(input)
		if err != nil {
			return fmt.Errorf("extract Google Drive file ID: %w", err)
		}

		output = absPath(output)
		if err := utils.EnsureDir(output); err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}

		zipPath := filepath.Join(output, "_pack_download.zip")
		if err := utils.DownloadGoogleDriveFile(fileID, zipPath); err != nil {
			return fmt.Errorf("download Google Drive file: %w", err)
		}

		workDir := filepath.Join(output, "_pack_extracted")
		log.Info().Str("archive", zipPath).Str("dest", workDir).Msg("extracting pack archive")
		if err := utils.ExtractArchive(zipPath, workDir); err != nil {
			return fmt.Errorf("extract archive: %w", err)
		}

		packType, err := detector.Detect(workDir)
		if err != nil {
			return fmt.Errorf("detect pack type: %w", err)
		}
		log.Info().Str("type", packType.String()).Msg("detected pack type")

		switch packType {
		case detector.PackTypeCurseForge:
			return setupCurseForge(workDir, output, javaPath, forceLoader, skipClean)
		case detector.PackTypeRaw:
			return setupRaw(workDir, output, javaPath, forceLoader, skipClean)
		default:
			return fmt.Errorf("unsupported pack type: %s", packType)
		}
	}

	input = absPath(input)
	output = absPath(output)

	workDir := input

	if strings.HasSuffix(strings.ToLower(input), ".zip") || strings.HasSuffix(strings.ToLower(input), ".rar") {
		workDir = filepath.Join(output, "_pack_extracted")
		log.Info().Str("archive", input).Str("dest", workDir).Msg("extracting pack archive")
		if err := utils.ExtractArchive(input, workDir); err != nil {
			return fmt.Errorf("extract archive: %w", err)
		}
	}

	packType, err := detector.Detect(workDir)
	if err != nil {
		return fmt.Errorf("detect pack type: %w", err)
	}
	log.Info().Str("type", packType.String()).Msg("detected pack type")

	switch packType {
	case detector.PackTypeCurseForge:
		return setupCurseForge(workDir, output, javaPath, forceLoader, skipClean)
	case detector.PackTypeRaw:
		return setupRaw(workDir, output, javaPath, forceLoader, skipClean)
	default:
		return fmt.Errorf("unsupported pack type: %s", packType)
	}
}

func setupCurseForge(workDir, output, javaPath, forceLoader string, skipClean bool) error {
	log := logger.Get()

	manifest, err := parser.ParseCurseForge(workDir)
	if err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}

	log.Info().
		Str("name", manifest.Name).
		Str("version", manifest.Version).
		Str("mc", manifest.Minecraft.Version).
		Str("loader", manifest.LoaderType).
		Str("loaderVersion", manifest.LoaderVersion).
		Msg("parsed CurseForge manifest")

	loaderType := manifest.LoaderType
	loaderVersion := manifest.LoaderVersion
	if forceLoader != "" {
		loaderType = forceLoader
	}

	modsDir := filepath.Join(output, "mods")

	// If the pack already ships a mods/ directory (e.g. a pre-downloaded Google Drive
	// archive), copy it directly and then download any mods that are listed in the
	// manifest but absent from that folder.  This handles packs where the cloud
	// archive is complete as well as partially-populated ones.
	packModsDir := filepath.Join(workDir, "mods")
	if utils.DirExists(packModsDir) {
		log.Info().Str("src", packModsDir).Str("dst", modsDir).Msg("using pre-existing mods from pack")
		if err := utils.CopyDir(packModsDir, modsDir); err != nil {
			return fmt.Errorf("copy pre-existing mods: %w", err)
		}

		apiKey := viper.GetString("curseforge_api_key")
		cacheDir := viper.GetString("cache_dir")
		workers := viper.GetInt("workers")

		dl := downloader.New(cacheDir, apiKey, workers)
		log.Info().Int("count", len(manifest.Files)).Msg("checking manifest mods against pre-existing mods folder")
		if err := dl.DownloadMissingMods(manifest, modsDir); err != nil {
			return fmt.Errorf("download missing mods: %w", err)
		}
	} else {
		apiKey := viper.GetString("curseforge_api_key")
		cacheDir := viper.GetString("cache_dir")
		workers := viper.GetInt("workers")

		dl := downloader.New(cacheDir, apiKey, workers)
		log.Info().Int("count", len(manifest.Files)).Msg("downloading mods")
		if err := dl.DownloadMods(manifest, modsDir); err != nil {
			return fmt.Errorf("download mods: %w", err)
		}
	}

	// Copy overrides
	overridesDir := filepath.Join(workDir, manifest.Overrides)
	if utils.DirExists(overridesDir) {
		log.Info().Str("src", overridesDir).Str("dst", output).Msg("copying overrides")
		if err := utils.CopyDir(overridesDir, output); err != nil {
			return fmt.Errorf("copy overrides: %w", err)
		}
	}

	if !skipClean {
		removed, cleanErr := cleaner.Clean(modsDir)
		if cleanErr != nil {
			log.Warn().Err(cleanErr).Msg("cleaner encountered an error")
		} else {
			log.Info().Int("removed", len(removed)).Msg("client-only mods removed")
		}
	}

	return installer.Install(output, loaderType, manifest.Minecraft.Version, loaderVersion, javaPath)
}

func setupRaw(workDir, output, javaPath, forceLoader string, skipClean bool) error {
	log := logger.Get()

	rp, err := parser.ParseRaw(workDir)
	if err != nil {
		return fmt.Errorf("parse raw pack: %w", err)
	}

	log.Info().Str("src", workDir).Str("dst", output).Msg("copying raw pack")
	if err := utils.CopyDir(workDir, output); err != nil {
		return fmt.Errorf("copy pack: %w", err)
	}

	loaderType := rp.LoaderType
	loaderVersion := rp.LoaderVersion
	mcVersion := rp.MCVersion

	if forceLoader != "" {
		loaderType = forceLoader
	}

	if loaderType == "" {
		log.Warn().Msg("loader type unknown for raw pack; skipping loader installation")
	} else {
		if err := installer.Install(output, loaderType, mcVersion, loaderVersion, javaPath); err != nil {
			return fmt.Errorf("install loader: %w", err)
		}
	}

	if !skipClean {
		modsDir := filepath.Join(output, "mods")
		removed, cleanErr := cleaner.Clean(modsDir)
		if cleanErr != nil {
			log.Warn().Err(cleanErr).Msg("cleaner encountered an error")
		} else {
			log.Info().Int("removed", len(removed)).Msg("client-only mods removed")
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// start command
// ---------------------------------------------------------------------------

func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start <serverDir>",
		Short: "Start a previously set-up Minecraft server",
		Args:  cobra.ExactArgs(1),
		RunE:  runStart,
	}

	cmd.Flags().String("ram", "", "JVM max heap size (e.g. 4G)")
	cmd.Flags().String("java-path", "", "path to java executable")
	return cmd
}

func runStart(cmd *cobra.Command, args []string) error {
	serverDir := absPath(args[0])
	ram, _ := cmd.Flags().GetString("ram")
	javaPath, _ := cmd.Flags().GetString("java-path")

	if ram == "" {
		ram = viper.GetString("ram")
	}
	if javaPath == "" {
		javaPath = viper.GetString("java_path")
	}

	return runtime.Start(serverDir, ram, javaPath)
}

func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
