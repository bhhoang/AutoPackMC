package setup

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bhhoang/Maple/internal/downloader"
)

// writeRawPack creates a raw pack (a mods/ folder, no loader metadata) with
// one server mod and one mod the filename cleaner treats as client-only.
func writeRawPack(t *testing.T) string {
	t.Helper()
	pack := t.TempDir()
	mods := filepath.Join(pack, "mods")
	if err := os.MkdirAll(mods, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"create-0.5.jar", "journeymap-5.9.jar"} {
		if err := os.WriteFile(filepath.Join(mods, name), []byte("jar"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return pack
}

func TestRunRawPackReportsStagesAndLeftOffMods(t *testing.T) {
	pack := writeRawPack(t)
	out := t.TempDir()

	var stages []Stage
	res, err := Run(context.Background(), Options{
		Input:   pack,
		Output:  out,
		OnStage: func(s Stage, _ Info) { stages = append(stages, s) },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// No loader is known for this pack, so Java and loader stages are skipped.
	want := []Stage{StageFindPack, StageMods, StageClean, StageFinish}
	if !slices.Equal(stages, want) {
		t.Errorf("stages = %v, want %v", stages, want)
	}
	if want := []downloader.LeftOffMod{{FileName: "journeymap-5.9.jar"}}; !slices.Equal(res.LeftOff, want) {
		t.Errorf("LeftOff = %+v, want %+v", res.LeftOff, want)
	}
	if _, err := os.Stat(filepath.Join(out, "mods", "create-0.5.jar")); err != nil {
		t.Errorf("server mod missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "mods", "journeymap-5.9.jar")); !os.IsNotExist(err) {
		t.Errorf("client-only mod still on the server (stat err %v)", err)
	}
}

func TestRunStopsWhenCancelled(t *testing.T) {
	pack := writeRawPack(t)
	ctx, cancel := context.WithCancel(context.Background())

	var stages []Stage
	_, err := Run(ctx, Options{
		Input:  pack,
		Output: t.TempDir(),
		OnStage: func(s Stage, _ Info) {
			stages = append(stages, s)
			if s == StageMods {
				cancel()
			}
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if !slices.Equal(stages, []Stage{StageFindPack, StageMods}) {
		t.Errorf("stages = %v, want the setup to stop after mods", stages)
	}
}

func TestRunRejectsBadMemory(t *testing.T) {
	if _, err := Run(context.Background(), Options{Input: "x", RAM: "lots"}); err == nil {
		t.Fatal("Run accepted an invalid memory size")
	}
}

// zipDir packs dir into a new zip file.
func zipDir(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pack.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunFromAnArchiveLeavesNoScratchFiles(t *testing.T) {
	archive := zipDir(t, writeRawPack(t))
	out := t.TempDir()
	// Leftovers of an older setup: a stale mod must not reach the server.
	stale := filepath.Join(out, "_pack_extracted", "mods", "old-removed-mod.jar")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "_pack_download.zip"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(context.Background(), Options{Input: archive, Output: out}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, name := range []string{"_pack_extracted", "_pack_download.zip"} {
		if _, err := os.Stat(filepath.Join(out, name)); !os.IsNotExist(err) {
			t.Errorf("%s is still in the server folder (stat err %v)", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "mods", "old-removed-mod.jar")); !os.IsNotExist(err) {
		t.Errorf("a mod from the old unpacked pack reached the server (stat err %v)", err)
	}
	if _, err := os.Stat(filepath.Join(out, "mods", "create-0.5.jar")); err != nil {
		t.Errorf("server mod missing: %v", err)
	}
}

func TestRunRemovesScratchFilesWhenItFails(t *testing.T) {
	out := t.TempDir()
	notAPack := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(notAPack, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notAPack, "readme.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Options{Input: zipDir(t, notAPack), Output: out}); err == nil {
		t.Fatal("a folder with no mods was set up")
	}
	if _, err := os.Stat(filepath.Join(out, "_pack_extracted")); !os.IsNotExist(err) {
		t.Errorf("unpacked pack left behind after a failed setup (stat err %v)", err)
	}
}
