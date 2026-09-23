//go:build windows

package app

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// memoryStatusEx is MEMORYSTATUSEX from the Windows API.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

// systemMemoryGB is the PC's installed memory in GB, rounded to the nearest
// GB, or 0 when it cannot be read.
func systemMemoryGB() int {
	st := memoryStatusEx{length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if ok, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&st))); ok == 0 {
		return 0
	}
	return int((st.totalPhys + 1<<29) >> 30)
}

var procSHFileOperationW = windows.NewLazySystemDLL("shell32.dll").NewProc("SHFileOperationW")

// shFileOpStruct is SHFILEOPSTRUCTW from the Windows API (64-bit layout).
type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

const (
	foDelete           = 0x3
	fofNoConfirmation  = 0x10
	fofAllowUndo       = 0x40
	fofNoErrorUI       = 0x400
	fofWantNukeWarning = 0x4000
)

// moveToRecycleBin moves dir to the Recycle Bin, as deleting it in File
// Explorer does. Windows shows its own progress, and asks first when the
// folder is too big for the Recycle Bin and would be deleted for good.
func moveToRecycleBin(dir string) error {
	// pFrom is a list of paths, each ending in NUL, ended by another NUL.
	from, err := windows.UTF16FromString(dir)
	if err != nil {
		return err
	}
	from = append(from, 0)
	op := shFileOpStruct{
		wFunc:  foDelete,
		pFrom:  &from[0],
		fFlags: fofAllowUndo | fofNoConfirmation | fofNoErrorUI | fofWantNukeWarning,
	}
	r, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	if op.fAnyOperationsAborted != 0 {
		return &Error{Code: "trash_cancelled"}
	}
	if r != 0 {
		return &Error{Code: "trash_failed", Detail: fmt.Sprintf("SHFileOperation error 0x%X", r)}
	}
	if _, err := os.Stat(dir); err == nil {
		return &Error{Code: "trash_failed", Detail: "the folder is still there"}
	}
	return nil
}
