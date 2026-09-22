// Package setup turns a modpack (a CurseForge URL, a Google Drive link, a
// ZIP/RAR archive or an extracted directory) into a ready-to-start Minecraft
// server. It is shared by the mcpackctl CLI and the AutoPack desktop app.
package setup

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bhhoang/AutoPackMC/internal/cleaner"
	"github.com/bhhoang/AutoPackMC/internal/detector"
	"github.com/bhhoang/AutoPackMC/internal/downloader"
	"github.com/bhhoang/AutoPackMC/internal/installer"
	"github.com/bhhoang/AutoPackMC/internal/java"
	"github.com/bhhoang/AutoPackMC/internal/modstate"
	"github.com/bhhoang/AutoPackMC/internal/parser"
	"github.com/bhhoang/AutoPackMC/internal/resolver"
	"github.com/bhhoang/AutoPackMC/pkg/logger"
	"github.com/bhhoang/AutoPackMC/pkg/utils"
)

// Stage is a step of a setup, reported through Options.OnStage.
type Stage string

const (
	StageFindPack Stage = "find-pack" // resolving and downloading the pack archive
	StageMods     Stage = "mods"      // downloading the pack's mods
	StageClean    Stage = "clean"     // removing client-only mods
	StageJava     Stage = "java"      // finding or downloading Java
	StageLoader   Stage = "loader"    // installing Forge, NeoForge or Fabric
	StageFinish   Stage = "finish"    // writing the memory setting
)

// Options configures a setup. Only Input and Output are required.
type Options struct {
	Input  string // CurseForge URL, Google Drive URL, archive or directory
	Output string // server directory

	RAM              string // max heap such as "6G"; empty leaves it as is
	JavaPath         string // java executable; "java" (or empty) means from PATH
	JavaPathExplicit bool   // JavaPath was chosen by the user
	JavaVersion      int    // download this Java major version instead of choosing one

	ForceLoader   string // override the pack's loader: forge, neoforge or fabric
	LoaderVersion string // override the pack's loader version
	SkipClean     bool   // keep client-only mods

	ExcludeMods []string // CurseForge slugs or IDs to leave off the server
	IncludeMods []string // CurseForge slugs or IDs to keep even if client-only

	APIKey            string // CurseForge API key
	CacheDir          string // mod download cache
	Workers           int    // parallel downloads
	ExcludeListSource string // URL or path of the client-only exclude list; empty disables it

	// OnStage, when set, is called as the setup enters each stage, with what
	// is known about the pack so far.
	OnStage func(Stage, Info)
	// OnMods, when set, reports mod download progress. It may be called from
	// several goroutines at once.
	OnMods func(done, total int)
}

// Info describes the pack being set up.
type Info struct {
	Name             string
	Version          string
	MinecraftVersion string
	Loader           string // forge, neoforge or fabric
	LoaderVersion    string
	JavaVersion      int // Java major version the Minecraft version needs, when known
	ModCount         int // mods the pack lists
}

// Result is what a finished setup produced.
type Result struct {
	Info
	JavaPath   string                  // java the server should be started with
	ProjectIDs []int                   // CurseForge projects the pack lists
	LeftOff    []downloader.LeftOffMod // client-only mods kept off the server
	Failed     []downloader.FailedMod  // mods that could not be downloaded
}

// Run sets up the server described by opts. Cancelling ctx stops the setup
// at the next stage; a stage already running finishes first.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Input == "" {
		return nil, fmt.Errorf("no modpack given")
	}
	if opts.Output == "" {
		opts.Output = "./server"
	}
	if opts.JavaPath == "" {
		opts.JavaPath = "java"
	}
	if opts.RAM != "" && !installer.ValidHeapSize(opts.RAM) {
		return nil, fmt.Errorf("invalid memory size %q (expected e.g. 4G or 2048M)", opts.RAM)
	}
	r := &run{ctx: ctx, opts: opts, res: &Result{}}
	if err := r.start(); err != nil {
		return nil, err
	}
	return r.res, nil
}

type run struct {
	ctx  context.Context
	opts Options
	res  *Result
}

// enter reports a new stage, or the cancellation that stops the setup.
func (r *run) enter(s Stage) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if r.opts.OnStage != nil {
		r.opts.OnStage(s, r.res.Info)
	}
	return nil
}

func (r *run) start() error {
	log := logger.Get()
	input := r.opts.Input
	r.opts.Output = absPath(r.opts.Output)
	output := r.opts.Output

	if err := r.enter(StageFindPack); err != nil {
		return err
	}

	var workDir, packSlug string
	switch {
	case resolver.IsCurseForgeURL(input):
		log.Info().Str("url", input).Msg("resolving modpack from CurseForge URL")
		downloadURL, err := resolver.New(r.opts.APIKey).Resolve(input)
		if err != nil {
			return fmt.Errorf("resolve CurseForge URL: %w", err)
		}
		if err := r.prepareOutput(); err != nil {
			return err
		}
		zipPath := filepath.Join(output, "_pack_download.zip")
		headers := map[string]string{}
		if r.opts.APIKey != "" {
			headers["x-api-key"] = r.opts.APIKey
		}
		if err := utils.DownloadFile(downloadURL, zipPath, headers); err != nil {
			return fmt.Errorf("download modpack: %w", err)
		}
		if workDir, err = extract(zipPath, output); err != nil {
			return err
		}
		// The pack slug selects per-modpack entries in the exclude list.
		packSlug, _ = resolver.ExtractSlug(input)

	case detector.IsGoogleDriveURL(input):
		log.Info().Str("url", input).Msg("downloading from Google Drive")
		fileID, err := utils.ExtractGoogleDriveFileID(input)
		if err != nil {
			return fmt.Errorf("extract Google Drive file ID: %w", err)
		}
		if err := r.prepareOutput(); err != nil {
			return err
		}
		zipPath := filepath.Join(output, "_pack_download.zip")
		if err := utils.DownloadGoogleDriveFile(fileID, zipPath); err != nil {
			return fmt.Errorf("download Google Drive file: %w", err)
		}
		if workDir, err = extract(zipPath, output); err != nil {
			return err
		}

	default:
		input = absPath(input)
		if err := r.resolveJava(); err != nil {
			return err
		}
		workDir = input
		lower := strings.ToLower(input)
		if strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".rar") {
			var err error
			if workDir, err = extract(input, output); err != nil {
				return err
			}
		}
	}

	packType, err := detector.Detect(workDir)
	if err != nil {
		return fmt.Errorf("detect pack type: %w", err)
	}
	log.Info().Str("type", packType.String()).Msg("detected pack type")

	switch packType {
	case detector.PackTypeCurseForge:
		return r.curseForge(workDir, packSlug)
	case detector.PackTypeRaw:
		return r.raw(workDir)
	default:
		return fmt.Errorf("unsupported pack type: %s", packType)
	}
}

// prepareOutput creates the server directory and, when a Java version was
// requested, downloads that Java into it.
func (r *run) prepareOutput() error {
	if err := utils.EnsureDir(r.opts.Output); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	return r.resolveJava()
}

func (r *run) resolveJava() error {
	javaPath, err := ResolveJavaPath(r.opts.JavaPath, r.opts.JavaPathExplicit, r.opts.JavaVersion, r.opts.Output)
	if err != nil {
		return fmt.Errorf("resolve java path: %w", err)
	}
	r.res.JavaPath = javaPath
	return nil
}

func extract(archive, output string) (string, error) {
	workDir := filepath.Join(output, "_pack_extracted")
	logger.Get().Info().Str("archive", archive).Str("dest", workDir).Msg("extracting pack archive")
	if err := utils.ExtractArchive(archive, workDir); err != nil {
		return "", fmt.Errorf("extract archive: %w", err)
	}
	return workDir, nil
}

func (r *run) autoJava() bool {
	return IsAutoJava(r.opts.JavaPath, r.opts.JavaPathExplicit, r.opts.JavaVersion)
}

func (r *run) curseForge(workDir, packSlug string) (err error) {
	log := logger.Get()
	output, opts := r.opts.Output, r.opts

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

	loaderType, loaderVersion := manifest.LoaderType, manifest.LoaderVersion
	if opts.ForceLoader != "" {
		loaderType = opts.ForceLoader
	}
	if opts.LoaderVersion != "" {
		loaderVersion = opts.LoaderVersion
	}
	r.res.Info = Info{
		Name:             manifest.Name,
		Version:          manifest.Version,
		MinecraftVersion: manifest.Minecraft.Version,
		Loader:           strings.ToLower(loaderType),
		LoaderVersion:    loaderVersion,
		JavaVersion:      java.RequiredVersion(manifest.Minecraft.Version),
		ModCount:         len(manifest.Files),
	}
	for _, f := range manifest.Files {
		r.res.ProjectIDs = append(r.res.ProjectIDs, f.ProjectID)
	}

	// Jars installed by the previous setup of this server are set aside, and
	// deleted if this version no longer installs them (restored on failure).
	update, err := modstate.Begin(output)
	if err != nil {
		return fmt.Errorf("prepare mods update: %w", err)
	}
	defer func() { update.Finish(err) }()

	modsDir := filepath.Join(output, "mods")
	dl := downloader.New(opts.CacheDir, opts.APIKey, opts.Workers, !opts.SkipClean)
	dl.OnModDone = opts.OnMods
	if !opts.SkipClean {
		ApplyExcludeList(dl, manifest, packSlug, opts)
	}

	// Mods that no download route could fetch are listed once the setup is
	// over, so the summary is the last thing on screen whether the run
	// succeeded or failed.
	defer func() {
		r.res.Failed = dl.FailedMods()
		r.res.LeftOff = dl.LeftOff()
		reportFailedMods(r.res.Failed, output, modsDir)
	}()

	if err := r.enter(StageMods); err != nil {
		return err
	}
	// A pack that ships a mods/ directory (e.g. a pre-downloaded Google Drive
	// archive) is copied as is, and only the mods it lacks are downloaded.
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

	overridesDir := filepath.Join(workDir, manifest.Overrides)
	if utils.DirExists(overridesDir) {
		log.Info().Str("src", overridesDir).Str("dst", output).Msg("copying overrides")
		if err := utils.CopyDir(overridesDir, output); err != nil {
			return fmt.Errorf("copy overrides: %w", err)
		}
	}

	if !opts.SkipClean {
		if err := r.enter(StageClean); err != nil {
			return err
		}
		removed, cleanErr := dl.CleanMods(manifest, modsDir)
		if cleanErr != nil {
			log.Warn().Err(cleanErr).Msg("API-based cleaner encountered an error")
		} else {
			log.Info().Int("removed", len(removed)).Msg("client-only mods removed")
		}
	}

	return r.finish(loaderType, manifest.Minecraft.Version, loaderVersion, "")
}

func (r *run) raw(workDir string) (err error) {
	log := logger.Get()
	output, opts := r.opts.Output, r.opts

	rp, err := parser.ParseRaw(workDir)
	if err != nil {
		return fmt.Errorf("parse raw pack: %w", err)
	}

	update, err := modstate.Begin(output)
	if err != nil {
		return fmt.Errorf("prepare mods update: %w", err)
	}
	defer func() { update.Finish(err) }()

	loaderType, loaderVersion := rp.LoaderType, rp.LoaderVersion
	if opts.ForceLoader != "" {
		loaderType = opts.ForceLoader
	}
	if opts.LoaderVersion != "" {
		loaderVersion = opts.LoaderVersion
	}
	r.res.Info = Info{
		Name:             filepath.Base(workDir),
		MinecraftVersion: rp.MCVersion,
		Loader:           strings.ToLower(loaderType),
		LoaderVersion:    loaderVersion,
		JavaVersion:      java.RequiredVersion(rp.MCVersion),
	}

	if err := r.enter(StageMods); err != nil {
		return err
	}
	log.Info().Str("src", workDir).Str("dst", output).Msg("copying raw pack")
	if err := utils.CopyDir(workDir, output); err != nil {
		return fmt.Errorf("copy pack: %w", err)
	}

	if !opts.SkipClean {
		if err := r.enter(StageClean); err != nil {
			return err
		}
		removed, cleanErr := cleaner.Clean(filepath.Join(output, "mods"))
		if cleanErr != nil {
			log.Warn().Err(cleanErr).Msg("cleaner encountered an error")
		} else {
			log.Info().Int("removed", len(removed)).Msg("client-only mods removed")
		}
		for _, name := range removed {
			r.res.LeftOff = append(r.res.LeftOff, downloader.LeftOffMod{FileName: filepath.Base(name)})
		}
	}

	if loaderType == "" {
		log.Warn().Msg("loader type unknown for raw pack; skipping loader installation")
		if err := r.enter(StageFinish); err != nil {
			return err
		}
		return applyMaxHeap(output, opts.RAM)
	}
	return r.finish(loaderType, rp.MCVersion, loaderVersion, "install loader: ")
}

// finish gets Java, installs the loader and records the memory setting.
// installPrefix is put before a loader installation error.
func (r *run) finish(loaderType, mcVersion, loaderVersion, installPrefix string) error {
	output := r.opts.Output
	if r.autoJava() {
		if err := r.enter(StageJava); err != nil {
			return err
		}
		javaPath, err := java.Ensure(r.res.JavaPath, mcVersion, output)
		if err != nil {
			return fmt.Errorf("prepare java: %w", err)
		}
		r.res.JavaPath = javaPath
	}

	if err := r.enter(StageLoader); err != nil {
		return err
	}
	if err := installer.Install(output, loaderType, mcVersion, loaderVersion, r.res.JavaPath); err != nil {
		if installPrefix != "" {
			return fmt.Errorf("%s%w", installPrefix, err)
		}
		return err
	}

	if err := r.enter(StageFinish); err != nil {
		return err
	}
	return applyMaxHeap(output, r.opts.RAM)
}

// applyMaxHeap records the memory setting in the server's user_jvm_args.txt
// so the run scripts and a plain `mcpackctl start` use it.
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

// ApplyExcludeList loads the client-only exclude list named by
// opts.ExcludeListSource, adds opts.ExcludeMods and opts.IncludeMods, and
// applies them to dl. The list only refines
// client-only detection, so any failure is logged and setup continues
// without it.
func ApplyExcludeList(dl *downloader.Downloader, manifest *parser.Manifest, packSlug string, opts Options) {
	log := logger.Get()

	list := &downloader.ExcludeList{}
	if source := opts.ExcludeListSource; source != "" {
		loaded, err := downloader.LoadExcludeList(source, opts.CacheDir)
		if err != nil {
			log.Warn().Err(err).Str("source", source).Msg("exclude list unavailable, relying on CurseForge Client/Server tags only")
		} else {
			list = loaded
		}
	}
	list.UserExcludes = opts.ExcludeMods
	list.UserIncludes = opts.IncludeMods

	n, err := dl.ApplyExcludeList(list, manifest, packSlug)
	if err != nil {
		log.Warn().Err(err).Msg("cannot look up mod slugs; only entries given as project IDs apply")
	}
	log.Info().Int("excluded", n).Str("pack", packSlug).Msg("client-only mods matched by the exclude list and --exclude-mods")
}

// ResolveJavaPath returns the java executable to use. When javaVersion is set
// and javaPath was not chosen explicitly, that Temurin JDK is downloaded into
// serverDir and its java is returned.
func ResolveJavaPath(javaPath string, javaPathExplicit bool, javaVersion int, serverDir string) (string, error) {
	if javaVersion <= 0 || javaPathExplicit {
		return javaPath, nil
	}
	if err := utils.EnsureDir(serverDir); err != nil {
		return "", fmt.Errorf("create server dir: %w", err)
	}
	return java.Download(javaVersion, serverDir)
}

// IsAutoJava reports whether Java selection was left to mcpackctl: no
// explicit java path, no Java version and no custom java_path in the config.
// A JDK matching the pack's Minecraft version is then used, downloaded if the
// java on PATH is missing or the wrong version.
func IsAutoJava(javaPath string, javaPathExplicit bool, javaVersion int) bool {
	return !javaPathExplicit && javaVersion <= 0 && javaPath == "java"
}

func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
