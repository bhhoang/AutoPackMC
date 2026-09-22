package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// Excerpt of the real Forge 1.20.1 crash report caused by Mekalus, plus a
// second mod that failed for an unrelated reason and must not be blamed.
const mekalusCrashReport = `---- Minecraft Crash Report ----
Description: Mod loading error has occurred

-- Head --
Thread: main
Suspected Mods: NONE
Stacktrace:
	at net.minecraftforge.fml.loading.RuntimeDistCleaner.processClassWithFlags(RuntimeDistCleaner.java:57) ~[fmlloader-1.20.1-47.4.0.jar%2369!/:1.0] {}
-- MOD oculus --
Details:
	Mod File: /D:/TestSV/AutopackMC/server/mods/mekalus-mc1.20.1-1.8.0.1.jar
	Failure message: Mekalus (Oculus) (oculus) has failed to load correctly
		java.lang.RuntimeException: Attempted to load class net/minecraft/client/gui/screens/Screen for invalid dist DEDICATED_SERVER
	Mod Version: 1.8.0.1
	Exception message: java.lang.RuntimeException: Attempted to load class net/minecraft/client/gui/screens/Screen for invalid dist DEDICATED_SERVER
Stacktrace:
	at net.minecraftforge.fml.loading.RuntimeDistCleaner.processClassWithFlags(RuntimeDistCleaner.java:57) ~[fmlloader-1.20.1-47.4.0.jar%2369!/:1.0] {}
-- MOD somelib --
Details:
	Mod File: /srv/server/mods/somelib-1.0.jar
	Failure message: Some Lib (somelib) has failed to load correctly
		java.lang.NullPointerException: config was null
-- System Details --
Details:
	Minecraft Version: 1.20.1
`

func TestFindClientOnlyMods(t *testing.T) {
	got := FindClientOnlyMods(mekalusCrashReport)
	want := []ClientOnlyMod{{ModID: "oculus", Name: "Mekalus (Oculus)", File: "mekalus-mc1.20.1-1.8.0.1.jar"}}
	if !slices.Equal(got, want) {
		t.Errorf("FindClientOnlyMods = %+v, want %+v", got, want)
	}

	if got := FindClientOnlyMods("---- Minecraft Crash Report ----\nDescription: Watching Server\n"); len(got) != 0 {
		t.Errorf("unrelated crash blamed %+v", got)
	}
}

func TestReportClientOnlyCrashOnlyReadsNewReports(t *testing.T) {
	dir := t.TempDir()
	reports := filepath.Join(dir, "crash-reports")
	if err := os.MkdirAll(reports, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(reports, "crash-2026-09-22_22.55.51-fml.txt")
	if err := os.WriteFile(old, []byte(mekalusCrashReport), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	since := time.Now().Add(-time.Minute)
	if n := len(reportClientOnlyCrash(dir, since)); n != 0 {
		t.Errorf("a crash report from an earlier run was reported (%d mods)", n)
	}

	if err := os.WriteFile(filepath.Join(reports, "crash-2026-09-23_00.10.00-fml.txt"), []byte(mekalusCrashReport), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := len(reportClientOnlyCrash(dir, since)); n != 1 {
		t.Errorf("reported %d mods from the new crash report, want 1", n)
	}
}
