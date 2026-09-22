package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bhhoang/AutoPackMC/internal/downloader"
)

func sampleFailures() []downloader.FailedMod {
	return []downloader.FailedMod{
		{
			ProjectID: 1001614,
			FileID:    5922047,
			Name:      "Lionfish API",
			PageURL:   "https://www.curseforge.com/minecraft/mc-mods/lionfish-api/download/5922047",
			Err:       errors.New("HTTP 404"),
		},
		{
			ProjectID: 238222,
			FileID:    1,
			PageURL:   "https://www.curseforge.com/projects/238222",
			Err:       errors.New("boom"),
		},
	}
}

func TestTerminalHyperlink(t *testing.T) {
	got := terminalHyperlink("file:///C:/tmp/failed-mods.html", "Open all the mods")
	want := "\x1b]8;;file:///C:/tmp/failed-mods.html\x1b\\Open all the mods\x1b]8;;\x1b\\"
	if got != want {
		t.Errorf("terminalHyperlink = %q, want %q", got, want)
	}
}

func TestFileURL(t *testing.T) {
	got := fileURL(filepath.FromSlash("/tmp/my server/failed-mods.html"))
	if !strings.HasPrefix(got, "file:///") {
		t.Fatalf("fileURL = %q, want a file:// URL", got)
	}
	if strings.Contains(got, " ") {
		t.Errorf("fileURL = %q, spaces must be escaped", got)
	}
	if !strings.HasSuffix(got, "failed-mods.html") {
		t.Errorf("fileURL = %q", got)
	}
}

func TestBuildFailedModsPage(t *testing.T) {
	html := buildFailedModsPage(sampleFailures(), filepath.FromSlash("/srv/mods"))

	for _, want := range []string{
		"https://www.curseforge.com/minecraft/mc-mods/lionfish-api/download/5922047",
		"https://www.curseforge.com/projects/238222",
		"Lionfish API",
		"Open all the mods",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	// Every failed mod must be reachable from the "open all" action.
	if strings.Count(html, "urls = [") != 1 {
		t.Error("page has no url list for the open-all action")
	}
}

func TestWriteFailedModsPage(t *testing.T) {
	dir := t.TempDir()
	path, err := writeFailedModsPage(sampleFailures(), dir, filepath.Join(dir, "mods"))
	if err != nil {
		t.Fatalf("writeFailedModsPage: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("page written to %q, want inside %q", path, dir)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read page: %v", err)
	}
	if !strings.Contains(string(data), "lionfish-api/download/5922047") {
		t.Error("written page is missing the mod links")
	}
}

func TestReportFailedModsNoFailures(t *testing.T) {
	dir := t.TempDir()
	reportFailedMods(nil, dir, dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("report wrote %d files for an empty failure list, want 0", len(entries))
	}
}
