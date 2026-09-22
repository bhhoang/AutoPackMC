//go:build !windows

package utils

import "os/exec"

// HideWindow does nothing outside Windows, where child processes do not open
// console windows.
func HideWindow(cmd *exec.Cmd) {}
