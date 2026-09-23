package main

import (
	"context"
	"os"
	"os/exec"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/bhhoang/IDISMAM/internal/app"
)

// windowUI gives the service access to the window: events, dialogs and
// opening folders or links.
type windowUI struct {
	ctx context.Context
}

func (u *windowUI) Emit(event string, data any) {
	if u.ctx != nil {
		runtime.EventsEmit(u.ctx, event, data)
	}
}

func (u *windowUI) PickFolder(title, start string) (string, error) {
	return runtime.OpenDirectoryDialog(u.ctx, runtime.OpenDialogOptions{
		Title:                title,
		DefaultDirectory:     start,
		CanCreateDirectories: true,
	})
}

func (u *windowUI) PickFiles(title, pattern string, multiple bool) ([]string, error) {
	opts := runtime.OpenDialogOptions{
		Title:   title,
		Filters: []runtime.FileFilter{{DisplayName: pattern, Pattern: pattern}},
	}
	if multiple {
		return runtime.OpenMultipleFilesDialog(u.ctx, opts)
	}
	file, err := runtime.OpenFileDialog(u.ctx, opts)
	if err != nil || file == "" {
		return nil, err
	}
	return []string{file}, nil
}

func (u *windowUI) Open(target string) {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		runtime.BrowserOpenURL(u.ctx, target)
		return
	}
	if goruntime.GOOS == "windows" {
		_ = exec.Command("explorer.exe", target).Start() // #nosec G204
		return
	}
	runtime.BrowserOpenURL(u.ctx, "file://"+target)
}

// Window is bound to the page for the title bar buttons and closing.
type Window struct {
	ui       *windowUI
	svc      *app.Service
	ctx      context.Context
	quitting atomic.Bool
}

func (w *Window) startup(ctx context.Context) {
	w.ctx = ctx
	w.ui.ctx = ctx
	runtime.OnFileDrop(ctx, func(_, _ int, paths []string) {
		runtime.EventsEmit(ctx, "files-dropped", paths)
	})
}

// beforeClose keeps the window open while servers run or a setup is in
// progress, and asks the page to confirm stopping them.
func (w *Window) beforeClose(ctx context.Context) bool {
	if w.quitting.Load() {
		return false
	}
	servers, setup := w.svc.RunningCount(), w.svc.SetupRunning()
	if servers == 0 && !setup {
		return false
	}
	runtime.EventsEmit(ctx, "close-requested", map[string]any{"servers": servers, "setup": setup})
	return true
}

func (w *Window) secondInstance(options.SecondInstanceData) {
	runtime.WindowUnminimise(w.ctx)
	runtime.WindowShow(w.ctx)
}

// Minimise minimises the window.
func (w *Window) Minimise() { runtime.WindowMinimise(w.ctx) }

// ToggleMaximise maximises the window or restores its size.
func (w *Window) ToggleMaximise() { runtime.WindowToggleMaximise(w.ctx) }

// IsMaximised reports whether the window fills the screen.
func (w *Window) IsMaximised() bool { return runtime.WindowIsMaximised(w.ctx) }

// Close closes the window, first asking about running servers.
func (w *Window) Close() { runtime.Quit(w.ctx) }

// RestartIntoUpdate installs the downloaded new version and restarts into
// it: running servers are saved and stopped first, then the new version
// starts and this one closes. A setup in progress blocks the update.
func (w *Window) RestartIntoUpdate() error {
	if w.svc.SetupRunning() {
		return &app.Error{Code: "setup_running"}
	}
	w.svc.StopAll()
	exe, err := w.svc.ApplyUpdate()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "--after-update", strconv.Itoa(os.Getpid())) // #nosec G204
	if err := cmd.Start(); err != nil {
		return err
	}
	w.quitting.Store(true)
	runtime.Quit(w.ctx)
	return nil
}

// StopServersAndQuit cancels a running setup (letting it put the server
// folder back as it was), saves and stops every running server, then closes.
func (w *Window) StopServersAndQuit() {
	w.svc.CancelSetupAndWait()
	w.svc.StopAll()
	w.quitting.Store(true)
	runtime.Quit(w.ctx)
}
