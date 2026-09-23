// Command idismam is the IDISMAM desktop app: a window for setting up and
// running Minecraft modpack servers without the command line.
//
// Build it on Windows with:
//
//	go build -tags desktop,production -ldflags "-H windowsgui" -o IDISMAM.exe ./cmd/idismam
package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"github.com/bhhoang/IDISMAM/internal/app"
	"github.com/bhhoang/IDISMAM/internal/downloader"
	"github.com/bhhoang/IDISMAM/pkg/logger"
	"github.com/bhhoang/IDISMAM/pkg/utils"
)

//go:embed all:frontend
var frontend embed.FS

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// publicAPIKey is the CurseForge key PolyMC provides for launchers
// (https://cf.polymc.org/api), the same default mcpackctl uses.
const publicAPIKey = "$2a$10$bL4bIL5pUWqfcO7KQtnMReakwtfHbNKh6v1uTpKlzhwoueEJQnPnm"

func main() {
	// Java runs behind the window, so it must not open console windows.
	utils.HideChildWindows = true
	finishUpdate()

	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	// The app used to be called AutoPack; its settings move over on first start.
	dataDir := app.DataDir(configDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatal(err)
	}
	logFile := openLog(filepath.Join(dataDir, "idismam.log"))
	defer logFile.Close()
	logger.SetOutput(logFile, "info")

	ui := &windowUI{}
	svc, err := app.New(app.Config{
		DataDir:           dataDir,
		CacheDir:          filepath.Join(home, ".cache", "mcpackctl"),
		DefaultServersDir: filepath.Join(home, "Documents", "Minecraft Servers"),
		DefaultAPIKey:     publicAPIKey,
		ExcludeListSource: downloader.DefaultExcludeListURL,
		Version:           version,
		UpdateRepo:        "bhhoang/IDISMAM",
	}, ui)
	if err != nil {
		logger.Get().Fatal().Err(err).Msg("cannot load saved servers")
	}
	win := &Window{ui: ui, svc: svc}

	assets, err := fs.Sub(frontend, "frontend")
	if err != nil {
		log.Fatal(err)
	}
	err = wails.Run(&options.App{
		Title:            "IDISMAM",
		Width:            1180,
		Height:           780,
		MinWidth:         960,
		MinHeight:        640,
		Frameless:        true,
		BackgroundColour: &options.RGBA{R: 14, G: 26, B: 51, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        win.startup,
		OnBeforeClose:    win.beforeClose,
		OnShutdown:       func(_ context.Context) { svc.StopAll() },
		Bind:             []interface{}{svc, win},
		DragAndDrop:      &options.DragAndDrop{EnableFileDrop: true, DisableWebViewDrop: true},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "idismam-8f4d2c1e-desktop",
			OnSecondInstanceLaunch: win.secondInstance,
		},
		Windows: &windows.Options{
			Theme:               windows.SystemDefault,
			WebviewUserDataPath: filepath.Join(dataDir, "webview"),
		},
	})
	if err != nil {
		logger.Get().Fatal().Err(err).Msg("window failed")
	}
}

// finishUpdate runs when this is a new version started by the updater
// (--after-update <pid>): it waits for the old version to close, so the
// single-instance lock goes to this process, then deletes the old file.
func finishUpdate() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	for i, arg := range os.Args {
		if arg != "--after-update" || i+1 >= len(os.Args) {
			continue
		}
		if pid, err := strconv.Atoi(os.Args[i+1]); err == nil {
			waitForExit(pid, 30*time.Second)
		}
	}
	app.CleanUpAfterUpdate(exe)
}

// waitForExit waits up to timeout for process pid to end.
func waitForExit(pid int, timeout time.Duration) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_, _ = proc.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// openLog opens the app's log file, starting a new one when it has grown
// past a few megabytes.
func openLog(path string) *os.File {
	if info, err := os.Stat(path); err == nil && info.Size() > 5<<20 {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	return f
}
