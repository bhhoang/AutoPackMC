//go:build !windows

package app

import (
	"bufio"
	"errors"
	"os"
	"strconv"
	"strings"
)

// systemMemoryGB is the machine's memory in GB from /proc/meminfo, or 0 when
// it cannot be read.
func systemMemoryGB() int {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return 0
			}
			return int((kb + 1<<19) >> 20)
		}
	}
	return 0
}

// moveToRecycleBin is only available on Windows, the one system the desktop
// app is built for.
func moveToRecycleBin(dir string) error {
	return errors.New("moving a folder to the Recycle Bin needs Windows")
}
