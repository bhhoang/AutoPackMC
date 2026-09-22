package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/bhhoang/AutoPackMC/internal/cleaner"
	"github.com/bhhoang/AutoPackMC/internal/downloader"
	"github.com/bhhoang/AutoPackMC/internal/installer"
	"github.com/bhhoang/AutoPackMC/internal/java"
	"github.com/bhhoang/AutoPackMC/internal/parser"
	"github.com/bhhoang/AutoPackMC/internal/runtime"
	"github.com/bhhoang/AutoPackMC/internal/setup"
	"github.com/bhhoang/AutoPackMC/pkg/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

// version is set at build time with
// -ldflags "-X github.com/bhhoang/AutoPackMC/internal/cmd.version=v1.2.3".
var version = "dev"

// rootCmd is the base command for mcpackctl.
var rootCmd = &cobra.Command{
	Use:     "mcpackctl",
	Version: version,
	Short:   "AutoPackMC — automated Minecraft modpack server setup",
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
	javaVersion, _ := cmd.Flags().GetInt("java-version")
	forceLoader, _ := cmd.Flags().GetString("force-loader")
	loaderVersion, _ := cmd.Flags().GetString("loader-version")
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

	_, err := setup.Run(context.Background(), setup.Options{
		Input:             input,
		Output:            output,
		RAM:               ram,
		JavaPath:          javaPath,
		JavaPathExplicit:  cmd.Flags().Changed("java-path"),
		JavaVersion:       javaVersion,
		ForceLoader:       forceLoader,
		LoaderVersion:     loaderVersion,
		SkipClean:         skipClean,
		ExcludeMods:       excludeMods,
		IncludeMods:       includeMods,
		APIKey:            viper.GetString("curseforge_api_key"),
		CacheDir:          viper.GetString("cache_dir"),
		Workers:           viper.GetInt("workers"),
		ExcludeListSource: viper.GetString("cf_exclude_include_file"),
	})
	return err
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

	resolvedJava, err := setup.ResolveJavaPath(javaPath, javaPathExplicit, javaVersion, serverDir)
	if err != nil {
		return fmt.Errorf("resolve java path: %w", err)
	}
	// Prefer the portable JDK that setup downloaded for this server.
	if setup.IsAutoJava(javaPath, javaPathExplicit, javaVersion) {
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
		setup.ApplyExcludeList(dl, manifest, "", setup.Options{
			CacheDir:          cacheDir,
			ExcludeListSource: viper.GetString("cf_exclude_include_file"),
			ExcludeMods:       excludeMods,
			IncludeMods:       includeMods,
		})

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
