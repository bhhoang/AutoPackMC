package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var heapSizeRe = regexp.MustCompile(`^[1-9][0-9]*[kKmMgG]$`)

// ValidHeapSize reports whether s is a JVM heap size such as "4G" or "2048M".
func ValidHeapSize(s string) bool {
	return heapSizeRe.MatchString(s)
}

// SetMaxHeap sets -Xmx<ram> in serverDir/user_jvm_args.txt, which the Forge
// and NeoForge run scripts and `mcpackctl start` read. An existing -Xmx is
// replaced in place; otherwise the flag is appended. It reports false when the
// server has no user_jvm_args.txt (Fabric and Forge before 1.17), whose
// launchers do not read the file.
func SetMaxHeap(serverDir, ram string) (bool, error) {
	if !ValidHeapSize(ram) {
		return false, fmt.Errorf("invalid heap size %q (expected e.g. 4G or 2048M)", ram)
	}
	path := filepath.Join(serverDir, "user_jvm_args.txt")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read user_jvm_args.txt: %w", err)
	}

	content := string(data)
	newline := "\n"
	if strings.Contains(content, "\r\n") {
		newline = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")

	replaced := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		fields := strings.Fields(line)
		changed := false
		for j, f := range fields {
			if strings.HasPrefix(f, "-Xmx") {
				fields[j] = "-Xmx" + ram
				changed = true
			}
		}
		if changed {
			lines[i] = strings.Join(fields, " ")
			replaced = true
		}
	}
	if !replaced {
		// Append after the last non-empty line, keeping a trailing newline.
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, "-Xmx"+ram, "")
	}

	if err := os.WriteFile(path, []byte(strings.Join(lines, newline)), 0o644); err != nil {
		return false, fmt.Errorf("write user_jvm_args.txt: %w", err)
	}
	return true, nil
}
