package downloader

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bhhoang/IDISMAM/internal/parser"
)

// TestDownloadModsReportsProgressAndLeftOff downloads a pack with one server
// mod, one mod CurseForge tags Client and one the exclude list names, and
// checks the progress calls and the left-off list.
func TestDownloadModsReportsProgressAndLeftOff(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/download"):
			fmt.Fprint(w, "JAR")
		case r.URL.Path == "/mods/1/files/10":
			fmt.Fprint(w, `{"data":{"fileName":"server-mod.jar","gameVersions":["1.20.1","Client","Server"]}}`)
		case r.URL.Path == "/mods/2/files/20":
			fmt.Fprint(w, `{"data":{"fileName":"shaders.jar","gameVersions":["1.20.1","Client"]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()

	d := New(t.TempDir(), "", 2, true)
	d.siteAPIBase = site.URL
	d.excludedProjects = map[int]string{3: "minimap"}

	var mu sync.Mutex
	var calls [][2]int
	d.OnModDone = func(done, total int) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, [2]int{done, total})
	}

	manifest := &parser.Manifest{Files: []parser.ModFile{
		{ProjectID: 1, FileID: 10, Required: true},
		{ProjectID: 2, FileID: 20, Required: true},
		{ProjectID: 3, FileID: 30, Required: true},
	}}
	modsDir := t.TempDir()
	if err := d.DownloadMods(manifest, modsDir); err != nil {
		t.Fatalf("DownloadMods: %v", err)
	}

	if _, err := os.Stat(filepath.Join(modsDir, "server-mod.jar")); err != nil {
		t.Errorf("server mod not downloaded: %v", err)
	}
	if len(calls) != 4 || calls[0] != [2]int{0, 3} || calls[3] != [2]int{3, 3} {
		t.Errorf("OnModDone calls = %v, want 0/3 then one per mod up to 3/3", calls)
	}

	got := d.LeftOff()
	want := []LeftOffMod{
		{ProjectID: 2, FileID: 20, FileName: "shaders.jar"},
		{ProjectID: 3, FileID: 30, Slug: "minimap", ByList: true},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("LeftOff() = %+v, want %+v", got, want)
	}
}

func TestNoteLeftOffMergesSightings(t *testing.T) {
	d := New(t.TempDir(), "", 1, true)
	d.noteLeftOff(LeftOffMod{ProjectID: 3, FileID: 30, Slug: "minimap", ByList: true})
	d.noteLeftOff(LeftOffMod{ProjectID: 3, FileID: 30, FileName: "minimap-1.0.jar"})

	got := d.LeftOff()
	want := []LeftOffMod{{ProjectID: 3, FileID: 30, Slug: "minimap", FileName: "minimap-1.0.jar", ByList: true}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("LeftOff() = %+v, want %+v", got, want)
	}
}
