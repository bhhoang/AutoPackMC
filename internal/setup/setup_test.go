package setup

import (
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
