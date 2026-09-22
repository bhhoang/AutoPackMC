//go:build windows

package utils

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW from the Windows process creation flags.
const createNoWindow = 0x08000000

// HideWindow stops cmd from opening a console window when HideChildWindows
// is set.
func HideWindow(cmd *exec.Cmd) {
	if !HideChildWindows {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
