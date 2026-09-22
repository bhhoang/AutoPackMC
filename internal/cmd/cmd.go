package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/bhhoang/AutoPackMC/internal/cleaner"
	"github.com/bhhoang/AutoPackMC/internal/detector"
	"github.com/bhhoang/AutoPackMC/internal/downloader"
	"github.com/bhhoang/AutoPackMC/internal/installer"
	"github.com/bhhoang/AutoPackMC/internal/java"
	"github.com/bhhoang/AutoPackMC/internal/modstate"
	"github.com/bhhoang/AutoPackMC/internal/parser"
	"github.com/bhhoang/AutoPackMC/internal/resolver"
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
	rootCmd.AddCommand(newCleanCmd())
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
	viper.SetDefault("java_path", "java")
	home, _ := os.UserHomeDir()
	viper.SetDefault("cache_dir", filepath.Join(home, ".cache", "mcpackctl"))
	viper.SetDefault("workers", 4)
	// List of client-only CurseForge projects to leave off servers; a URL or a
	// local path in the cf-exclude-include.json format. Empty disables it.
	viper.SetDefault("cf_exclude_include_file", downloader.DefaultExcludeListURL)
	// Public CurseForge API key provided by PolyMC: https://cf.polymc.org/api
	viper.SetDefault("curseforge_api_key", "$2a$10$bL4bIL5pUWqfcO7KQtnMReakwtfHbNKh6v1uTpKlzhwoueEJQnPnm")

	_ = viper.ReadInConfig()

	logger.Init(viper.GetString("log_level"))
}

// ---------------------------------------------------------------------------
// setup command
// ---------------------------------------------------------------------------

func newSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup [<curseforge-url>] --input <pack.zip|dir> --output <serverDir>",
		Short: "Download and configure a modpack server",
		Long: `Download and configure a Minecraft modpack server.

Input may be:
  - A local modpack ZIP or extracted directory (--input flag)
  - A CurseForge modpack URL (as --input or positional argument)
    e.g. https://www.curseforge.com/minecraft/modpacks/deceasedcraft`,
		RunE: runSetup,
	}

	cmd.Flags().String("input", "", "path to the modpack ZIP/dir, or a CurseForge URL")
	cmd.Flags().String("output", "./server", "destination directory for the server")
	cmd.Flags().String("ram", "", "JVM max heap size (e.g. 4G)")
	cmd.Flags().String("java-path", "", "path to java executable")
	cmd.Flags().Int("java-version", 0, "automatically download this Java major version (e.g. 21) into the server directory")
	cmd.Flags().String("force-loader", "", "override loader type: forge or fabric")
	cmd.Flags().String("loader-version", "", "override loader version (e.g. 47.4.0)")
	cmd.Flags().Bool("skip-clean", false, "skip removal of client-only mods")
	addModOverrideFlags(cmd)

	return cmd
}

func runSetup(cmd *cobra.Command, args []string) error {
	log := logger.Get()

	input, _ := cmd.Flags().GetString("input")
	// Accept a CurseForge URL (or any input) as a positional argument when
	// --input is not provided.
	if input == "" && len(args) > 0 {
		input = args[0]
	}
	if input == "" {
		return fmt.Errorf("provide --input <pack.zip|dir|curseforge-url> or pass the URL as a positional argument")
	}

	output, _ := cmd.Flags().GetString("output")
	ram, _ := cmd.Flags().GetString("ram")
	javaPath, _ := cmd.Flags().GetString("java-path")
	javaPathExplicit := cmd.Flags().Changed("java-path")
	javaVersion, _ := cmd.Flags().GetInt("java-version")
	forceLoader, _ := cmd.Flags().GetString("force-loader")
	forceLoaderVersion, _ := cmd.Flags().GetString("loader-version")
	skipClean, _ := cmd.Flags().GetBool("skip-clean")
	excludeMods, includeMods := modOverrides(cmd)

	if ram == "" {
		ram = viper.GetString("ram")
	}
	if ram != "" && !installer.ValidHeapSize(ram) {
		return fmt.Errorf("invalid --ram %q (expected e.g. 4G or 2048M)", ram)
	}
	if javaPath == "" {
		javaPath = viper.GetString("java_path")
	}
	autoJava := isAutoJava(javaPath, javaPathExplicit, javaVersion)

	// opts captures output and javaPath as resolved by the time a branch
	// below hands over to the pack-specific setup.
	opts := func(packSlug string) setupOptions {
		return setupOptions{
			output:             output,
			packSlug:           packSlug,
			javaPath:           javaPath,
			autoJava:           autoJava,
			forceLoader:        forceLoader,
			forceLoaderVersion: forceLoaderVersion,
			skipClean:          skipClean,
			ram:                ram,
			excludeMods:        excludeMods,
			includeMods:        includeMods,
		}
	}

	// -----------------------------------------------------------------------
	// CurseForge URL input
	// -----------------------------------------------------------------------
	if resolver.IsCurseForgeURL(input) {
		log.Info().Str("url", input).Msg("resolving modpack from CurseForge URL")

		apiKey := viper.GetString("curseforge_api_key")
		res := resolver.New(apiKey)

		downloadURL, err := res.Resolve(input)
		if err != nil {
			return fmt.Errorf("resolve CurseForge URL: %w", err)
		}

		output = absPath(output)
		if err := utils.EnsureDir(output); err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}

		javaPath, err = resolveJavaPath(javaPath, javaPathExplicit, javaVersion, output)
		if err != nil {
			return fmt.Errorf("resolve java path: %w", err)
		}

		zipPath := filepath.Join(output, "_pack_download.zip")
		headers := map[string]string{}
		if apiKey != "" {
			headers["x-api-key"] = apiKey
		}
		if err := utils.DownloadFile(downloadURL, zipPath, headers); err != nil {
			return fmt.Errorf("download modpack: %w", err)
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

		// The pack slug selects per-modpack entries in the exclude list.
		packSlug, _ := resolver.ExtractSlug(input)

		switch packType {
		case detector.PackTypeCurseForge:
			return setupCurseForge(workDir, opts(packSlug))
		case detector.PackTypeRaw:
			return setupRaw(workDir, opts(""))
		default:
			return fmt.Errorf("unsupported pack type: %s", packType)
		}
	}

	// -----------------------------------------------------------------------
	// Google Drive URL input
	// -----------------------------------------------------------------------
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

		javaPath, err = resolveJavaPath(javaPath, javaPathExplicit, javaVersion, output)
		if err != nil {
			return fmt.Errorf("resolve java path: %w", err)
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
			return setupCurseForge(workDir, opts(""))
		case detector.PackTypeRaw:
			return setupRaw(workDir, opts(""))
		default:
			return fmt.Errorf("unsupported pack type: %s", packType)
		}
	}

	// -----------------------------------------------------------------------
	// Local file / directory input
	// -----------------------------------------------------------------------
	input = absPath(input)
	output = absPath(output)

	if resolvedJava, resolveErr := resolveJavaPath(javaPath, javaPathExplicit, javaVersion, output); resolveErr != nil {
		return fmt.Errorf("resolve java path: %w", resolveErr)
	} else {
		javaPath = resolvedJava
	}

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
		return setupCurseForge(workDir, opts(""))
	case detector.PackTypeRaw:
		return setupRaw(workDir, opts(""))
	default:
		return fmt.Errorf("unsupported pack type: %s", packType)
	}
}

// setupOptions carries the setup flags into the pack-specific setup.
type setupOptions struct {
	output             string
	packSlug           string // CurseForge modpack slug, when known
	javaPath           string
	autoJava           bool
	forceLoader        string
	forceLoaderVersion string
	skipClean          bool
	ram                string // max heap from --ram or config; empty to leave as is
	excludeMods        []string
	includeMods        []string
}

func setupCurseForge(workDir string, opts setupOptions) (err error) {
	log := logger.Get()
	output, javaPath := opts.output, opts.javaPath
	forceLoader, forceLoaderVersion, skipClean := opts.forceLoader, opts.forceLoaderVersion, opts.skipClean

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
	if forceLoaderVersion != "" {
		loaderVersion = forceLoaderVersion
	}

	// Jars installed by the previous setup of this server are set aside, and
	// deleted if this version no longer installs them (restored on failure).
	update, err := modstate.Begin(output)
	if err != nil {
		return fmt.Errorf("prepare mods update: %w", err)
	}
	defer func() { update.Finish(err) }()

	modsDir := filepath.Join(output, "mods")

	apiKey := viper.GetString("curseforge_api_key")
	cacheDir := viper.GetString("cache_dir")
	workers := viper.GetInt("workers")
	dl := downloader.New(cacheDir, apiKey, workers, !skipClean)
	if !skipClean {
		applyExcludeList(dl, manifest, opts.packSlug, cacheDir, opts.excludeMods, opts.includeMods)
	}

	// Mods that no download route could fetch are listed once the setup is
	// over, so the summary is the last thing on screen whether the run
	// succeeded or failed.
	defer func() { reportFailedMods(dl.FailedMods(), output, modsDir) }()

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

		log.Info().Int("count", len(manifest.Files)).Msg("checking manifest mods against pre-existing mods folder")
		if err := dl.DownloadMissingMods(manifest, modsDir); err != nil {
			return fmt.Errorf("download missing mods: %w", err)
		}
	} else {
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
		removed, cleanErr := dl.CleanMods(manifest, modsDir)
		if cleanErr != nil {
			log.Warn().Err(cleanErr).Msg("API-based cleaner encountered an error")
		} else {
			log.Info().Int("removed", len(removed)).Msg("client-only mods removed")
		}
	}

	if opts.autoJava {
		if javaPath, err = java.Ensure(javaPath, manifest.Minecraft.Version, output); err != nil {
			return fmt.Errorf("prepare java: %w", err)
		}
	}

	if err := installer.Install(output, loaderType, manifest.Minecraft.Version, loaderVersion, javaPath); err != nil {
		return err
	}
	return applyMaxHeap(output, opts.ram)
}

func setupRaw(workDir string, opts setupOptions) (err error) {
	log := logger.Get()
	output, javaPath := opts.output, opts.javaPath
	forceLoader, forceLoaderVersion, skipClean := opts.forceLoader, opts.forceLoaderVersion, opts.skipClean

	rp, err := parser.ParseRaw(workDir)
	if err != nil {
		return fmt.Errorf("parse raw pack: %w", err)
	}

	// Jars installed by the previous setup of this server are set aside, and
	// deleted if this version no longer installs them (restored on failure).
	update, err := modstate.Begin(output)
	if err != nil {
		return fmt.Errorf("prepare mods update: %w", err)
	}
	defer func() { update.Finish(err) }()

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
	if forceLoaderVersion != "" {
		loaderVersion = forceLoaderVersion
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

	if loaderType == "" {
		log.Warn().Msg("loader type unknown for raw pack; skipping loader installation")
	} else {
		if opts.autoJava {
			if javaPath, err = java.Ensure(javaPath, mcVersion, output); err != nil {
				return fmt.Errorf("prepare java: %w", err)
			}
		}
		if err := installer.Install(output, loaderType, mcVersion, loaderVersion, javaPath); err != nil {
			return fmt.Errorf("install loader: %w", err)
		}
	}

	return applyMaxHeap(output, opts.ram)
}

// applyMaxHeap records --ram in the server's user_jvm_args.txt so the run
// scripts and a plain `mcpackctl start` use it.
func applyMaxHeap(serverDir, ram string) error {
	log := logger.Get()
	if ram == "" {
		return nil
	}
	written, err := installer.SetMaxHeap(serverDir, ram)
	if err != nil {
		return fmt.Errorf("set max heap: %w", err)
	}
	if !written {
		log.Warn().Str("ram", ram).Msg("this loader does not read user_jvm_args.txt; pass --ram to `mcpackctl start` to set the max heap")
		return nil
	}
	log.Info().Str("ram", ram).Msg("max heap set in user_jvm_args.txt")
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
	cmd.Flags().Int("java-version", 0, "automatically download this Java major version (e.g. 21) into the server directory")
	return cmd
}

func runStart(cmd *cobra.Command, args []string) error {
	serverDir := absPath(args[0])
	ram, _ := cmd.Flags().GetString("ram")
	javaPath, _ := cmd.Flags().GetString("java-path")
	javaPathExplicit := cmd.Flags().Changed("java-path")
	javaVersion, _ := cmd.Flags().GetInt("java-version")

	if ram == "" {
		ram = viper.GetString("ram")
	}
	if javaPath == "" {
		javaPath = viper.GetString("java_path")
	}

	if ram != "" && !installer.ValidHeapSize(ram) {
		return fmt.Errorf("invalid --ram %q (expected e.g. 4G or 2048M)", ram)
	}

	resolvedJava, err := resolveJavaPath(javaPath, javaPathExplicit, javaVersion, serverDir)
	if err != nil {
		return fmt.Errorf("resolve java path: %w", err)
	}
	// Prefer the portable JDK that setup downloaded for this server.
	if isAutoJava(javaPath, javaPathExplicit, javaVersion) {
		if local := java.FindLocal(serverDir); local != "" {
			resolvedJava = local
		}
	}

	return runtime.Start(serverDir, ram, resolvedJava)
}

// ---------------------------------------------------------------------------
// clean command
// ---------------------------------------------------------------------------

func newCleanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clean --mods-dir <path>",
		Short: "Remove client-only mods from a mods directory",
		Long: `Scans the given mods directory and removes any JAR files that are identified
as client-only.

When --manifest is provided, the CurseForge API is queried for each mod's
gameVersions field, which gives an authoritative client/server classification
and avoids false positives from filename pattern matching.

Without --manifest, a built-in list of known client-only filename patterns is
used as a fallback.`,
		RunE: runClean,
	}

	cmd.Flags().String("mods-dir", "", "path to the mods directory to clean (required)")
	cmd.Flags().String("manifest", "", "path to a CurseForge manifest.json for API-based detection (recommended)")
	cmd.Flags().String("api-key", "", "CurseForge API key (falls back to MCPACKCTL_CURSEFORGE_API_KEY / config)")
	addModOverrideFlags(cmd)
	_ = cmd.MarkFlagRequired("mods-dir")

	return cmd
}

func runClean(cmd *cobra.Command, _ []string) error {
	log := logger.Get()

	modsDir, _ := cmd.Flags().GetString("mods-dir")
	modsDir = absPath(modsDir)

	manifestPath, _ := cmd.Flags().GetString("manifest")
	apiKey, _ := cmd.Flags().GetString("api-key")
	if apiKey == "" {
		apiKey = viper.GetString("curseforge_api_key")
	}

	if manifestPath != "" {
		manifestPath = absPath(manifestPath)
		manifestDir := filepath.Dir(manifestPath)

		manifest, err := parser.ParseCurseForge(manifestDir)
		if err != nil {
			return fmt.Errorf("parse manifest: %w", err)
		}

		cacheDir := viper.GetString("cache_dir")
		workers := viper.GetInt("workers")
		dl := downloader.New(cacheDir, apiKey, workers, true)
		excludeMods, includeMods := modOverrides(cmd)
		applyExcludeList(dl, manifest, "", cacheDir, excludeMods, includeMods)

		log.Info().Str("dir", modsDir).Msg("cleaning client-only mods using CurseForge API")
		removed, err := dl.CleanMods(manifest, modsDir)
		if err != nil {
			return fmt.Errorf("clean: %w", err)
		}

		if len(removed) == 0 {
			log.Info().Msg("no client-only mods found")
		} else {
			log.Info().Int("removed", len(removed)).Msg("client-only mods removed")
		}
		return nil
	}

	log.Warn().Msg("no --manifest provided; falling back to filename pattern matching (may have false positives)")
	log.Info().Str("dir", modsDir).Msg("cleaning client-only mods using filename patterns")

	removed, err := cleaner.Clean(modsDir)
	if err != nil {
		return fmt.Errorf("clean: %w", err)
	}

	if len(removed) == 0 {
		log.Info().Msg("no client-only mods found")
	} else {
		log.Info().Int("removed", len(removed)).Msg("client-only mods removed")
	}
	return nil
}

func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// resolveJavaPath returns the java executable path to use.
// If javaVersion > 0 and javaPath was not explicitly provided by the user,
// it downloads the specified Eclipse Temurin JDK into serverDir and returns
// the path to its java binary.
func resolveJavaPath(javaPath string, javaPathExplicit bool, javaVersion int, serverDir string) (string, error) {
	if javaVersion <= 0 || javaPathExplicit {
		return javaPath, nil
	}
	if err := utils.EnsureDir(serverDir); err != nil {
		return "", fmt.Errorf("create server dir: %w", err)
	}
	return java.Download(javaVersion, serverDir)
}

// isAutoJava reports whether the user left java selection to mcpackctl: no
// --java-path, no --java-version and no custom java_path in the config.
// In that case a JDK matching the pack's Minecraft version is used, downloaded
// if the java on PATH is missing or the wrong version.
func isAutoJava(javaPath string, javaPathExplicit bool, javaVersion int) bool {
	return !javaPathExplicit && javaVersion <= 0 && javaPath == "java"
}

// applyExcludeList loads the configured client-only exclude list, adds the
// user's --exclude-mods/--include-mods, and applies them to dl. The list
// only refines client-only detection, so any failure is logged and setup
// continues without it.
func applyExcludeList(dl *downloader.Downloader, manifest *parser.Manifest, packSlug, cacheDir string, excludeMods, includeMods []string) {
	log := logger.Get()

	list := &downloader.ExcludeList{}
	if source := viper.GetString("cf_exclude_include_file"); source != "" {
		loaded, err := downloader.LoadExcludeList(source, cacheDir)
		if err != nil {
			log.Warn().Err(err).Str("source", source).Msg("exclude list unavailable, relying on CurseForge Client/Server tags only")
		} else {
			list = loaded
		}
	}
	list.UserExcludes = excludeMods
	list.UserIncludes = includeMods

	n, err := dl.ApplyExcludeList(list, manifest, packSlug)
	if err != nil {
		log.Warn().Err(err).Msg("cannot look up mod slugs; only entries given as project IDs apply")
	}
	log.Info().Int("excluded", n).Str("pack", packSlug).Msg("client-only mods matched by the exclude list and --exclude-mods")
}

func addModOverrideFlags(cmd *cobra.Command) {
	cmd.Flags().String("exclude-mods", "", "CurseForge project slugs or IDs to leave off the server, separated by commas or spaces (config: exclude_mods)")
	cmd.Flags().String("include-mods", "", "CurseForge project slugs or IDs to keep even if treated as client-only (config: include_mods)")
}

// modOverrides returns the --exclude-mods and --include-mods entries, falling
// back to the exclude_mods and include_mods config keys (or MCPACKCTL_
// environment variables).
func modOverrides(cmd *cobra.Command) (excludes, includes []string) {
	get := func(flag, key string) []string {
		value, _ := cmd.Flags().GetString(flag)
		if !cmd.Flags().Changed(flag) {
			value = strings.Join(viper.GetStringSlice(key), " ")
		}
		return strings.FieldsFunc(value, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	}
	return get("exclude-mods", "exclude_mods"), get("include-mods", "include_mods")
}
